// MeetMD — M1 spike: capture system audio (all meeting participants) on macOS
// using ScreenCaptureKit, with no virtual audio device required.
//
// This proves the riskiest architectural assumption: that we can record the
// mixed system output (everyone you hear) browser-agnostically. It records to a
// WAV for N seconds, then exits.
//
// Build:  swiftc -O SystemAudioRecorder.swift -o system-audio-recorder
// Run:    ./system-audio-recorder out.wav 8
//
// NOTE: the first run prompts for Screen Recording permission (TCC). Grant it in
// System Settings ▸ Privacy & Security ▸ Screen Recording, then re-run.

import Foundation
import AVFoundation
import CoreMedia
import ScreenCaptureKit
import CoreAudio
import AudioToolbox

// WAV output format: 16kHz mono, 16-bit PCM — exactly what whisper.cpp expects,
// so the captured file feeds transcription with no resampling.
private enum Output {
    static let sampleRate = 16_000.0
    static let channels: AVAudioChannelCount = 1
    static let settings: [String: Any] = [
        AVFormatIDKey: kAudioFormatLinearPCM,
        AVSampleRateKey: sampleRate,
        AVNumberOfChannelsKey: channels,
        AVLinearPCMBitDepthKey: 16,
        AVLinearPCMIsFloatKey: false,
        AVLinearPCMIsBigEndianKey: false,
        AVLinearPCMIsNonInterleaved: false,
    ]
}

@available(macOS 13.0, *)
final class SystemAudioRecorder: NSObject, SCStreamOutput, SCStreamDelegate {
    private let outputURL: URL
    private let micURL: URL? // when set, the mic is captured to a second channel
    // When false (mic-only mode, e.g. quick voice notes) ScreenCaptureKit is not
    // started at all, so no Screen Recording permission is needed — only the mic
    // is recorded, to outputURL.
    private let captureSystem: Bool
    private var stream: SCStream?
    private var audioFile: AVAudioFile?
    private var samplesWritten: AVAudioFramePosition = 0

    // Mic capture (the user's own voice, which never reaches system output).
    private var engine: AVAudioEngine?
    private var micFile: AVAudioFile?
    private var micSamplesWritten: AVAudioFramePosition = 0
    // Observes AVAudioEngineConfigurationChange so the mic tap survives the engine
    // stopping mid-recording (device/route swap, the meeting app grabbing the mic).
    private var micConfigObserver: NSObjectProtocol?
    // The mic can bind to a silent device with no error and no config change (seen
    // with the built-in mic when the meeting app already holds it), recording
    // nothing all meeting. These pin the mic to the live default input, follow
    // default-input swaps, and watch for a dead mic while system audio flows. All
    // tap installs are wrapped so an AVFAudio exception can never crash the process.
    private var defaultInputListener: AudioObjectPropertyListenerBlock?
    private var micWatchdog: DispatchSourceTimer?
    private var lastSystemSamples: AVAudioFramePosition = 0
    private var lastMicSamples: AVAudioFramePosition = 0
    private var micSilentTicks = 0
    private var rearming = false // guards re-entrant tap re-installs
    private static let defaultInputAddress = AudioObjectPropertyAddress(
        mSelector: kAudioHardwarePropertyDefaultInputDevice,
        mScope: kAudioObjectPropertyScopeGlobal,
        mElement: kAudioObjectPropertyElementMain)

    // stopping guards the stream-death restart (so an intentional stop doesn't
    // trigger a restart); systemRestarts caps recovery attempts.
    private var stopping = false
    private var systemRestarts = 0

    // paused is read on the audio queue and written from signal handlers, so it
    // is guarded by a lock. While paused, samples are dropped, which keeps the
    // recording continuous (no gap) across pause/resume.
    // While recording we hold a power-management activity so the display/system
    // don't sleep — display sleep invalidates the SCStream and kills the system
    // capture mid-meeting.
    private var activity: NSObjectProtocol?

    private let lock = NSLock()
    private var pausedFlag = false
    var paused: Bool {
        get { lock.lock(); defer { lock.unlock() }; return pausedFlag }
        set { lock.lock(); pausedFlag = newValue; lock.unlock() }
    }

    init(outputURL: URL, micURL: URL?, captureSystem: Bool = true) {
        self.outputURL = outputURL
        self.micURL = micURL
        self.captureSystem = captureSystem
    }

    func start() async throws {
        activity = ProcessInfo.processInfo.beginActivity(
            options: [.idleDisplaySleepDisabled, .idleSystemSleepDisabled],
            reason: "MeetMD recording")
        if captureSystem {
            try await startSystem()
            // Mic failure (e.g. no permission) must not abort the system capture.
            do {
                try startMic(to: micURL)
            } catch {
                FileHandle.standardError.write("mic indisponível: \(error)\n".data(using: .utf8)!)
            }
        } else {
            // Mic-only: the mic IS the recording, so a failure here is fatal.
            try startMic(to: outputURL)
        }
    }

    private func startSystem() async throws {
        // A display is required to build a content filter even for audio-only capture.
        let content = try await SCShareableContent.excludingDesktopWindows(
            false, onScreenWindowsOnly: false)
        guard let display = content.displays.first else {
            throw RecorderError.noDisplay
        }
        let filter = SCContentFilter(display: display, excludingApplications: [], exceptingWindows: [])

        let config = SCStreamConfiguration()
        config.capturesAudio = true
        config.sampleRate = Int(Output.sampleRate)
        config.channelCount = Int(Output.channels)
        config.excludesCurrentProcessAudio = true
        // Minimal video — SCStream still produces frames, but we ignore them.
        config.width = 100
        config.height = 100

        let stream = SCStream(filter: filter, configuration: config, delegate: self)
        try stream.addStreamOutput(self, type: .audio, sampleHandlerQueue: .global(qos: .userInitiated))
        try await stream.startCapture()
        self.stream = stream
    }

    // startMic taps the default input device and writes it, resampled to 16kHz
    // mono, to `dest`. The same `paused` flag drops mic samples too.
    private func startMic(to dest: URL?) throws {
        guard let micURL = dest else { return }
        let engine = AVAudioEngine()
        self.engine = engine
        self.micFile = try AVAudioFile(forWriting: micURL, settings: Output.settings)
        try installMicTap()

        // The audio engine stops whenever the input configuration changes — the
        // meeting app grabbing the mic, a device/route swap, AirPods (dis)connecting.
        // Without re-arming, the tap goes silent for the rest of the meeting
        // (observed: 3.5s of mic vs 58min of system audio). Re-install and restart.
        micConfigObserver = NotificationCenter.default.addObserver(
            forName: .AVAudioEngineConfigurationChange, object: engine, queue: .main
        ) { [weak self] _ in
            guard let self = self, !self.stopping, !self.rearming else { return }
            do {
                try self.installMicTap()
                FileHandle.standardError.write("mic engine reconfigured — tap re-armed\n".data(using: .utf8)!)
            } catch {
                FileHandle.standardError.write("mic tap re-arm failed: \(error)\n".data(using: .utf8)!)
            }
        }

        // AVAudioEngineConfigurationChange doesn't reliably fire when only the
        // default *input* device swaps, so follow that directly and re-arm.
        startDefaultInputListener()

        // Watchdog for the silent-mic failure (see installMicTap). Needs system
        // capture as a reference that audio is actually flowing.
        if captureSystem { startMicWatchdog() }
    }

    // installMicTap (re)installs the tap on the current input and starts the
    // engine. Safe to call again after a configuration change: it removes any
    // prior tap and re-reads the input format (which may have changed) so the
    // converter matches the new device. Writes continue to the same micFile.
    private func installMicTap() throws {
        guard let engine = engine, let file = micFile else { return }
        rearming = true
        defer { rearming = false }
        // Stop before rebinding the HAL device so the change takes effect, then pin
        // the engine's input to the *current* default input device. AVAudioEngine's
        // implicit binding can silently latch a stale/silent device (seen with the
        // built-in mic when the meeting app already held it).
        if engine.isRunning { engine.stop() }
        bindDefaultInputDevice(engine)
        let input = engine.inputNode
        input.removeTap(onBus: 0) // no-op if none; required before re-installing
        // Is there a usable input device yet? (0ch/0Hz = none.)
        let probe = input.outputFormat(forBus: 0)
        guard probe.channelCount > 0, probe.sampleRate > 0 else {
            // Input temporarily gone. A later config/device change re-arms us.
            throw RecorderError.micUnavailable
        }
        guard let target = AVAudioFormat(commonFormat: .pcmFormatFloat32,
                                         sampleRate: Output.sampleRate,
                                         channels: Output.channels,
                                         interleaved: false) else {
            throw RecorderError.micUnavailable
        }
        // format: nil makes the tap use the node's *current* format at install
        // time, so it can never mismatch (the crash we hit when the device/format
        // was still settling after a rebind, e.g. the 96 kHz built-in mic). The
        // converter is (re)built lazily from each buffer's real format. The whole
        // install is wrapped so an AVFAudio NSException can't crash the recorder.
        var converter: AVAudioConverter?
        var converterInFormat: AVAudioFormat?
        let installed = guarded("installTap") {
            input.installTap(onBus: 0, bufferSize: 4096, format: nil) { [weak self] buffer, _ in
                guard let self = self, !self.paused else { return }
                let inFormat = buffer.format
                guard inFormat.sampleRate > 0, buffer.frameLength > 0 else { return }
                if converterInFormat != inFormat {
                    converter = AVAudioConverter(from: inFormat, to: target)
                    converterInFormat = inFormat
                }
                guard let conv = converter else { return }
                let ratio = target.sampleRate / inFormat.sampleRate
                let capacity = AVAudioFrameCount(Double(buffer.frameLength) * ratio) + 1024
                guard let out = AVAudioPCMBuffer(pcmFormat: target, frameCapacity: capacity) else { return }
                var fed = false
                var convError: NSError?
                conv.convert(to: out, error: &convError) { _, status in
                    if fed { status.pointee = .noDataNow; return nil }
                    fed = true
                    status.pointee = .haveData
                    return buffer
                }
                if convError == nil, out.frameLength > 0 {
                    try? file.write(from: out)
                    self.micSamplesWritten += AVAudioFramePosition(out.frameLength)
                }
            }
        }
        guard installed else { throw RecorderError.micUnavailable }
        engine.prepare()
        try engine.start()
        micSilentTicks = 0 // give the fresh tap time before the watchdog judges it
    }

    // guarded runs an AVFAudio call that may throw an Objective-C NSException
    // (which Swift can't catch), logging and swallowing it so a mic failure never
    // terminates the process. Returns whether it completed without an exception.
    @discardableResult
    private func guarded(_ what: String, _ block: () -> Void) -> Bool {
        var reason: NSString?
        let ok = MMDRunCatchingExceptions(block, &reason)
        if !ok {
            FileHandle.standardError.write("mic \(what) exception: \(reason ?? "?")\n".data(using: .utf8)!)
        }
        return ok
    }

    // defaultInputDeviceID returns the system's current default input device.
    private func defaultInputDeviceID() -> AudioDeviceID? {
        var deviceID = AudioDeviceID(0)
        var size = UInt32(MemoryLayout<AudioDeviceID>.size)
        var addr = Self.defaultInputAddress
        let status = AudioObjectGetPropertyData(
            AudioObjectID(kAudioObjectSystemObject), &addr, 0, nil, &size, &deviceID)
        guard status == noErr, deviceID != kAudioObjectUnknown else { return nil }
        return deviceID
    }

    // bindDefaultInputDevice pins the engine's input HAL unit to the current
    // default input device instead of trusting AVAudioEngine's implicit binding.
    @discardableResult
    private func bindDefaultInputDevice(_ engine: AVAudioEngine) -> Bool {
        guard let unit = engine.inputNode.audioUnit, var dev = defaultInputDeviceID() else { return false }
        let status = AudioUnitSetProperty(
            unit, kAudioOutputUnitProperty_CurrentDevice, kAudioUnitScope_Global, 0,
            &dev, UInt32(MemoryLayout<AudioDeviceID>.size))
        return status == noErr
    }

    // startDefaultInputListener re-arms the tap when the default input changes
    // mid-meeting (AirPods in/out, switching input in System Settings).
    private func startDefaultInputListener() {
        var addr = Self.defaultInputAddress
        let block: AudioObjectPropertyListenerBlock = { [weak self] _, _ in
            guard let self = self, !self.stopping, !self.rearming else { return }
            do {
                try self.installMicTap()
                FileHandle.standardError.write("default input changed — mic rebound\n".data(using: .utf8)!)
            } catch {
                FileHandle.standardError.write("mic rebind failed: \(error)\n".data(using: .utf8)!)
            }
        }
        let status = AudioObjectAddPropertyListenerBlock(
            AudioObjectID(kAudioObjectSystemObject), &addr, DispatchQueue.main, block)
        if status == noErr { defaultInputListener = block }
    }

    // startMicWatchdog catches the silent-mic failure that has no error path: if
    // system audio keeps flowing but the mic produces no samples for ~10s, the tap
    // is bound to a dead device — force a rebind + re-arm.
    private func startMicWatchdog() {
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + 5, repeating: 5)
        timer.setEventHandler { [weak self] in
            guard let self = self, !self.stopping, !self.paused, !self.rearming else { return }
            let sys = self.samplesWritten
            let mic = self.micSamplesWritten
            let systemFlowing = sys > self.lastSystemSamples
            let micFlowing = mic > self.lastMicSamples
            self.lastSystemSamples = sys
            self.lastMicSamples = mic
            self.micSilentTicks = (systemFlowing && !micFlowing) ? self.micSilentTicks + 1 : 0
            guard self.micSilentTicks >= 2 else { return } // ~10s of audio with a dead mic
            self.micSilentTicks = 0
            do {
                try self.installMicTap()
                FileHandle.standardError.write("mic silent while system active — tap re-armed\n".data(using: .utf8)!)
            } catch {
                FileHandle.standardError.write("mic watchdog re-arm failed: \(error)\n".data(using: .utf8)!)
            }
        }
        timer.resume()
        micWatchdog = timer
    }

    func stop() async throws {
        stopping = true
        micWatchdog?.cancel()
        micWatchdog = nil
        if let o = micConfigObserver {
            NotificationCenter.default.removeObserver(o)
            micConfigObserver = nil
        }
        if let block = defaultInputListener {
            var addr = Self.defaultInputAddress
            AudioObjectRemovePropertyListenerBlock(
                AudioObjectID(kAudioObjectSystemObject), &addr, DispatchQueue.main, block)
            defaultInputListener = nil
        }
        try? await stream?.stopCapture() // a dead stream must NOT block finalization below
        stream = nil
        engine?.inputNode.removeTap(onBus: 0)
        engine?.stop()
        engine = nil
        // Closing the AVAudioFiles flushes their WAV headers. This MUST run even if
        // stopCapture threw (e.g. the stream already died) — otherwise the files are
        // left with a 0-frame header and the whole recording is unreadable.
        audioFile = nil
        micFile = nil
        if let a = activity {
            ProcessInfo.processInfo.endActivity(a)
            activity = nil
        }
    }

    // SCStreamOutput: receives audio sample buffers.
    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer,
                of type: SCStreamOutputType) {
        guard type == .audio, !paused, sampleBuffer.isValid,
              let pcm = sampleBuffer.toPCMBuffer() else { return }
        do {
            if audioFile == nil {
                audioFile = try AVAudioFile(forWriting: outputURL, settings: Output.settings)
            }
            try audioFile?.write(from: pcm)
            samplesWritten += AVAudioFramePosition(pcm.frameLength)
        } catch {
            FileHandle.standardError.write("write error: \(error)\n".data(using: .utf8)!)
        }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        FileHandle.standardError.write("stream stopped: \(error)\n".data(using: .utf8)!)
        // The system capture stream can die mid-recording (display change, etc.).
        // Restart it (writing continues to the same WAV) so we don't silently lose
        // the participants' audio for the rest of the meeting.
        guard !stopping, captureSystem, systemRestarts < 5 else { return }
        systemRestarts += 1
        Task {
            do {
                try await startSystem()
                FileHandle.standardError.write("system stream restarted (#\(systemRestarts))\n".data(using: .utf8)!)
            } catch {
                FileHandle.standardError.write("system stream restart failed: \(error)\n".data(using: .utf8)!)
            }
        }
    }

    var seconds: Double { Double(max(samplesWritten, micSamplesWritten)) / Output.sampleRate }

    enum RecorderError: Error { case noDisplay, micUnavailable }
}

// CMSampleBuffer → AVAudioPCMBuffer (float32) for AVAudioFile to convert on write.
extension CMSampleBuffer {
    func toPCMBuffer() -> AVAudioPCMBuffer? {
        guard let fmtDesc = CMSampleBufferGetFormatDescription(self),
              let asbd = CMAudioFormatDescriptionGetStreamBasicDescription(fmtDesc),
              let format = AVAudioFormat(streamDescription: asbd) else { return nil }
        let frames = AVAudioFrameCount(CMSampleBufferGetNumSamples(self))
        guard frames > 0,
              let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames) else { return nil }
        buffer.frameLength = frames
        let status = CMSampleBufferCopyPCMDataIntoAudioBufferList(
            self, at: 0, frameCount: Int32(frames), into: buffer.mutableAudioBufferList)
        return status == noErr ? buffer : nil
    }
}

// --- entrypoint -------------------------------------------------------------

func fail(_ message: String) -> Never {
    FileHandle.standardError.write((message + "\n").data(using: .utf8)!)
    exit(1)
}

// Usage:
//   system-audio-recorder <output.wav> [seconds] [--mic <mic.wav>] [--mic-only]
//     no seconds → record until SIGTERM/SIGINT; --mic also captures the mic;
//     --mic-only records ONLY the mic to <output.wav> (no ScreenCaptureKit, so
//     no Screen Recording permission) — used for quick voice notes.
var positional: [String] = []
var micPath: String?
var micOnly = false
do {
    let argv = CommandLine.arguments
    var i = 1
    while i < argv.count {
        if argv[i] == "--mic", i + 1 < argv.count {
            micPath = argv[i + 1]
            i += 2
        } else if argv[i] == "--mic-only" {
            micOnly = true
            i += 1
        } else {
            positional.append(argv[i])
            i += 1
        }
    }
}
guard let outPath = positional.first else {
    fail("usage: system-audio-recorder <output.wav> [seconds] [--mic <mic.wav>] [--mic-only]")
}
let outputURL = URL(fileURLWithPath: outPath)
let micURL = micPath.map { URL(fileURLWithPath: $0) }
let duration = positional.count >= 2 ? (Double(positional[1]) ?? 0) : 0 // 0 = until signal

guard #available(macOS 13.0, *) else {
    fail("ScreenCaptureKit audio capture requires macOS 13+")
}

// Mic-only ignores --mic (the single positional WAV is the mic recording).
let recorder = SystemAudioRecorder(outputURL: outputURL,
                                   micURL: micOnly ? nil : micURL,
                                   captureSystem: !micOnly)

@Sendable func stopAndExit() {
    Task {
        try? await recorder.stop()
        print(String(format: "done: wrote %.1fs of audio", recorder.seconds))
        exit(recorder.seconds > 0 ? 0 : 2) // exit 2 = ran but captured nothing
    }
}

Task {
    do {
        let mode = duration > 0 ? "\(Int(duration))s" : "until signal"
        let source = micOnly ? "mic only" : "system audio"
        print("recording \(source) (\(mode)) → \(outputURL.path)")
        try await recorder.start()
        if duration > 0 {
            try await Task.sleep(nanoseconds: UInt64(duration * 1_000_000_000))
            stopAndExit()
        }
    } catch {
        fail("error: \(error)")
    }
}

// Signal-driven control (how the Go bridge drives the recording):
//   SIGTERM/SIGINT → stop & finalize    SIGUSR1 → pause    SIGUSR2 → resume
// Sources must be retained or they are deallocated and never fire.
var signalSources: [DispatchSourceSignal] = []
let signalActions: [(Int32, () -> Void)] = [
    (SIGTERM, { stopAndExit() }),
    (SIGINT, { stopAndExit() }),
    (SIGUSR1, { recorder.paused = true; print("paused") }),
    (SIGUSR2, { recorder.paused = false; print("resumed") }),
]
for (sig, handler) in signalActions {
    signal(sig, SIG_IGN)
    let source = DispatchSource.makeSignalSource(signal: sig, queue: .main)
    source.setEventHandler(handler: handler)
    source.resume()
    signalSources.append(source)
}

// Parent-death watchdog: if the bridge that launched us dies (or the menu-bar app
// quits and shuts the bridge down), we get reparented to launchd (ppid 1). An
// orphaned helper otherwise keeps its SCStream — and the macOS screen-capture
// indicator — alive forever. Poll ppid and self-terminate (finalizing the WAV).
let parentWatch = DispatchSource.makeTimerSource(queue: .main)
parentWatch.schedule(deadline: .now() + 2, repeating: 2)
parentWatch.setEventHandler {
    if getppid() == 1 {
        FileHandle.standardError.write("parent gone — self-terminating\n".data(using: .utf8)!)
        stopAndExit()
    }
}
parentWatch.resume()

RunLoop.main.run()
