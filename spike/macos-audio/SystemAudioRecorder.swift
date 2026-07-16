// MeetMD — macOS audio capture helper.
//
// Captures the system audio (every meeting participant you hear) with
// ScreenCaptureKit, and the microphone (your own voice) on the SAME stream via
// ScreenCaptureKit's microphone capture (macOS 15+).
//
// Why the mic rides the SCStream: it is the one transport that has never failed
// here. Two previous mic paths broke, both silently:
//   - AVAudioEngine.inputNode → silent, or -10868 "format not supported" once a
//     meeting app (a browser in a call) already held the built-in mic.
//   - AVCaptureSession        → started fine, delivered zero samples in a real
//     meeting, with no error to observe.
// ScreenCaptureKit's own mic capture is Apple's first-class API for exactly this
// (a meeting recorder capturing system + mic), so it shares one stream, one
// permission model, one failure domain. AVCaptureSession is kept only as the
// fallback for macOS 13/14 (and for mic-only voice notes, which start no stream).
//
// A mic problem must NEVER cost us the system audio: if the stream refuses to
// start with mic capture on, it is restarted without it.
//
// Build:  swiftc -O SystemAudioRecorder.swift -o system-audio-recorder
// Run:    ./system-audio-recorder out.wav 8 --mic mic.wav

import Foundation
import AVFoundation
import CoreMedia
import ScreenCaptureKit

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

// Every diagnostic goes to stderr, which the Go bridge captures into bridge.log.
// The mic has failed three times with nothing to look at; that ends here.
private func log(_ message: String) {
    FileHandle.standardError.write((message + "\n").data(using: .utf8)!)
}

@available(macOS 13.0, *)
final class SystemAudioRecorder: NSObject, SCStreamOutput, SCStreamDelegate,
                                 AVCaptureAudioDataOutputSampleBufferDelegate {
    private let outputURL: URL
    private let micURL: URL? // when set, the mic is captured to a second channel
    // When false (mic-only mode, e.g. quick voice notes) ScreenCaptureKit is not
    // started at all, so no Screen Recording permission is needed — only the mic
    // is recorded, to outputURL.
    private let captureSystem: Bool
    private var stream: SCStream?
    private var audioFile: AVAudioFile?
    private var samplesWritten: AVAudioFramePosition = 0

    // Mic. Primary path is ScreenCaptureKit (see file header); AVCaptureSession is
    // the fallback. micSource records which one is live, so the logs say so.
    private var micSession: AVCaptureSession? // fallback only
    private var micFile: AVAudioFile?
    private var micSamplesWritten: AVAudioFramePosition = 0
    private var micConverter: AVAudioConverter?
    private var micConverterFormat: AVAudioFormat?
    private let micQueue = DispatchQueue(label: "com.tbdc.meetmd.mic")
    private let micTarget = AVAudioFormat(commonFormat: .pcmFormatFloat32,
                                          sampleRate: Output.sampleRate,
                                          channels: Output.channels, interleaved: false)!
    private var micSource = "none" // screencapturekit | avcapture | none
    private var micDestURL: URL?   // where the mic is being written, if at all
    private var micFallbackObserver: NSObjectProtocol?

    // Mute gate: when the system microphone is muted (e.g. the keyboard's mic-mute
    // key), the OS delivers pure digital silence to every capturer. We keep writing
    // that silence to preserve the mic timeline (so You:/Participants stay aligned
    // when merged), and record the muted time ranges so the bridge can drop those
    // stretches from the transcript — your voice is not recorded while you are
    // muted. The participants ride a separate channel (system audio) and are never
    // touched by this. Ranges are in milliseconds from the recording start.
    private var micMutedNow = false
    private var micMuteStartMs: Int = 0
    private var mutedRanges: [(start: Int, end: Int)] = []

    // Watchdog: purely diagnostic. It only reports "the system is producing audio
    // but the mic has produced none" — it never re-arms anything (a re-arm loop is
    // what crashed the recorder before and cost a whole meeting).
    private var micWatchdog: DispatchSourceTimer?
    private var lastSystemSamples: AVAudioFramePosition = 0
    private var lastMicSamples: AVAudioFramePosition = 0
    private var micSilentTicks = 0
    private var micWarned = false

    // stopping guards the stream-death restart (so an intentional stop doesn't
    // trigger a restart); systemRestarts caps recovery attempts.
    private var stopping = false
    private var systemRestarts = 0

    // While recording we hold a power-management activity so the display/system
    // don't sleep — display sleep invalidates the SCStream and kills the system
    // capture mid-meeting.
    private var activity: NSObjectProtocol?

    // paused is read on the audio queues and written from signal handlers, so it
    // is guarded by a lock. While paused, samples are dropped, which keeps the
    // recording continuous (no gap) across pause/resume.
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

        // The mic WAV must exist before the stream starts: ScreenCaptureKit can
        // deliver mic buffers the moment capture begins.
        micDestURL = captureSystem ? micURL : outputURL
        if let dest = micDestURL {
            micFile = try AVAudioFile(forWriting: dest, settings: Output.settings)
            if let device = AVCaptureDevice.default(for: .audio) {
                log("mic: default input = \(device.localizedName)")
            } else {
                log("mic: no default audio input found")
            }
        }

        if captureSystem {
            do {
                try await startSystem() // may enable the mic on the same stream
            } catch {
                // Don't leave a 0-frame mic WAV behind: it is exactly the input that
                // later makes whisper fail and reads as "the mic broke".
                discardMicFile()
                throw error
            }
            // Only fall back if ScreenCaptureKit did not take the mic, so we never
            // capture it twice.
            if micDestURL != nil, micSource == "none" {
                do {
                    try startMicFallback()
                } catch {
                    log("mic: AVCaptureSession fallback unavailable: \(error)")
                }
            }
            startMicWatchdog()
        } else {
            // Mic-only: the mic IS the recording, so a failure here is fatal.
            try startMicFallback()
        }
    }

    // discardMicFile drops the mic WAV we pre-created when the recording never
    // actually started, so nothing downstream trips over an empty file.
    private func discardMicFile() {
        micFile = nil
        if let dest = micDestURL {
            try? FileManager.default.removeItem(at: dest)
            micDestURL = nil
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

        if micFile != nil, #available(macOS 15.0, *) {
            do {
                try await startStream(filter: filter, withMic: true)
                // A stream restart can land here while the AVCaptureSession fallback
                // from an earlier attempt is still running — both paths would then
                // write the same mic WAV, interleaving two copies of the audio.
                // ScreenCaptureKit wins; tear the fallback down.
                if micSession != nil {
                    log("mic: stopping the AVCaptureSession fallback — ScreenCaptureKit took the mic")
                }
                stopMicFallback()
                micSource = "screencapturekit"
                log("mic: capturing via ScreenCaptureKit (same stream as the system audio)")
                return
            } catch {
                // A mic problem must never cost us the meeting: drop the mic from the
                // stream and bring the system audio up regardless.
                log("mic: SCK captureMicrophone failed (\(error)) — restarting the stream WITHOUT the mic")
            }
        }
        try await startStream(filter: filter, withMic: false)
    }

    private func startStream(filter: SCContentFilter, withMic: Bool) async throws {
        let config = SCStreamConfiguration()
        config.capturesAudio = true
        config.sampleRate = Int(Output.sampleRate)
        config.channelCount = Int(Output.channels)
        config.excludesCurrentProcessAudio = true
        // Minimal video — SCStream still produces frames, but we ignore them.
        config.width = 100
        config.height = 100
        if withMic, #available(macOS 15.0, *) {
            config.captureMicrophone = true
        }

        let stream = SCStream(filter: filter, configuration: config, delegate: self)
        try stream.addStreamOutput(self, type: .audio, sampleHandlerQueue: .global(qos: .userInitiated))
        if withMic, #available(macOS 15.0, *) {
            try stream.addStreamOutput(self, type: .microphone, sampleHandlerQueue: micQueue)
        }
        try await stream.startCapture()
        self.stream = stream
    }

    // startMicFallback captures the default input with AVCaptureSession. Used on
    // macOS 13/14, for mic-only voice notes, and if the SCK mic refuses to start.
    private func startMicFallback() throws {
        guard micFile != nil else { return }
        guard let device = AVCaptureDevice.default(for: .audio) else {
            throw RecorderError.micUnavailable
        }
        let input = try AVCaptureDeviceInput(device: device)
        let session = AVCaptureSession()
        guard session.canAddInput(input) else { throw RecorderError.micUnavailable }
        session.addInput(input)

        let output = AVCaptureAudioDataOutput()
        output.setSampleBufferDelegate(self, queue: micQueue)
        guard session.canAddOutput(output) else { throw RecorderError.micUnavailable }
        session.addOutput(output)

        // This session used to die at runtime with nobody watching — that is exactly
        // how the mic failed silently. Observe it.
        micFallbackObserver = NotificationCenter.default.addObserver(
            forName: .AVCaptureSessionRuntimeError, object: session, queue: nil
        ) { note in
            let err = note.userInfo?[AVCaptureSessionErrorKey] ?? "unknown"
            log("mic: AVCaptureSession runtime error: \(err)")
        }

        session.startRunning()
        self.micSession = session
        micSource = "avcapture"
        log("mic: capturing via AVCaptureSession (fallback) — device=\(device.localizedName)")
    }

    // stopMicFallback tears the AVCaptureSession path down, observer included.
    // stopRunning() blocks until in-flight capture callbacks drain, so no buffer can
    // still be on its way to writeMic afterwards.
    private func stopMicFallback() {
        if let observer = micFallbackObserver {
            NotificationCenter.default.removeObserver(observer)
            micFallbackObserver = nil
        }
        micSession?.stopRunning()
        micSession = nil
    }

    // writeMic converts a mic buffer (device format) to 16kHz mono and appends it.
    // Shared by both mic paths; always runs on micQueue, so it is serial.
    private func writeMic(_ pcm: AVAudioPCMBuffer) {
        guard let file = micFile, pcm.frameLength > 0, pcm.format.sampleRate > 0 else { return }
        let inFormat = pcm.format
        if micConverterFormat != inFormat {
            micConverter = AVAudioConverter(from: inFormat, to: micTarget)
            micConverterFormat = inFormat
            log("mic: input format \(Int(inFormat.sampleRate))Hz \(inFormat.channelCount)ch")
        }
        guard let converter = micConverter else { return }
        let ratio = micTarget.sampleRate / inFormat.sampleRate
        let capacity = AVAudioFrameCount(Double(pcm.frameLength) * ratio) + 1024
        guard let out = AVAudioPCMBuffer(pcmFormat: micTarget, frameCapacity: capacity) else { return }
        var fed = false
        var convError: NSError?
        converter.convert(to: out, error: &convError) { _, status in
            if fed { status.pointee = .noDataNow; return nil }
            fed = true
            status.pointee = .haveData
            return pcm
        }
        if convError == nil, out.frameLength > 0 {
            // Track mute BEFORE writing, timestamped at this buffer's start so the
            // range lines up with whisper's timestamps (both in mic-WAV time).
            trackMute(out, atMs: Int(Double(micSamplesWritten) / Output.sampleRate * 1000))
            do {
                try file.write(from: out) // keep writing silence: preserves the timeline
                micSamplesWritten += AVAudioFramePosition(out.frameLength)
            } catch {
                log("mic: write error: \(error)")
            }
        }
    }

    // trackMute opens/closes a muted range as the mic flips between pure silence
    // (system-muted) and real audio. A live mic always carries a noise floor, so an
    // all-zero buffer means the input is muted, never just a quiet speaker.
    private func trackMute(_ buffer: AVAudioPCMBuffer, atMs: Int) {
        let silent = isSilent(buffer)
        if silent, !micMutedNow {
            micMutedNow = true
            micMuteStartMs = atMs
            log("mic: input muted (silence) — dropping this stretch from the transcript")
        } else if !silent, micMutedNow {
            micMutedNow = false
            mutedRanges.append((micMuteStartMs, atMs))
            log("mic: input active again")
        }
    }

    private func isSilent(_ buffer: AVAudioPCMBuffer) -> Bool {
        guard let channels = buffer.floatChannelData else { return false }
        let frames = Int(buffer.frameLength)
        for c in 0 ..< Int(buffer.format.channelCount) {
            let data = channels[c]
            for i in 0 ..< frames where data[i] != 0 { return false }
        }
        return true
    }

    // writeMutedRanges persists the muted time ranges next to the mic WAV so the
    // bridge can drop those stretches. Missing file = nothing was muted.
    private func writeMutedRanges() {
        guard let dest = micDestURL else { return }
        if micMutedNow { // still muted at stop → close the final range
            mutedRanges.append((micMuteStartMs, Int(Double(micSamplesWritten) / Output.sampleRate * 1000)))
        }
        guard !mutedRanges.isEmpty else { return }
        let body = mutedRanges.map { "\($0.start) \($0.end)" }.joined(separator: "\n") + "\n"
        let url = dest.appendingPathExtension("muted")
        do {
            try body.data(using: .utf8)?.write(to: url)
        } catch {
            // Fail loud: without this file the bridge cannot drop the muted stretches,
            // so the user's muted audio would be transcribed.
            log("mic: could not write muted-ranges sidecar (\(error)) — muted audio may be transcribed")
        }
    }

    // startMicWatchdog reports a mic that produces nothing while the meeting clearly
    // has audio. Diagnostic only — it deliberately does not try to "fix" anything.
    private func startMicWatchdog() {
        guard micFile != nil else { return }
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + 10, repeating: 10)
        timer.setEventHandler { [weak self] in
            guard let self = self, !self.stopping, !self.paused, !self.micWarned else { return }
            let sys = self.samplesWritten
            let mic = self.micSamplesWritten
            let systemFlowing = sys > self.lastSystemSamples
            let micFlowing = mic > self.lastMicSamples
            self.lastSystemSamples = sys
            self.lastMicSamples = mic
            self.micSilentTicks = (systemFlowing && !micFlowing) ? self.micSilentTicks + 1 : 0
            if self.micSilentTicks >= 2 { // ~20s of system audio with a dead mic
                self.micWarned = true
                log("mic: WARNING — system audio is flowing but the mic has produced " +
                    "no samples in ~20s (source=\(self.micSource))")
            }
        }
        timer.resume()
        micWatchdog = timer
    }

    func stop() async throws {
        stopping = true
        micWatchdog?.cancel()
        micWatchdog = nil
        // Both mic paths must be down before micFile is released below, so no
        // callback can still be writing to it.
        stopMicFallback()
        try? await stream?.stopCapture() // a dead stream must NOT block finalization below
        stream = nil
        // Closing the AVAudioFiles flushes their WAV headers. This MUST run even if
        // stopCapture threw (e.g. the stream already died) — otherwise the files are
        // left with a 0-frame header and the whole recording is unreadable.
        audioFile = nil
        micFile = nil
        writeMutedRanges()

        // Never fail silently: say exactly what each channel captured.
        let systemSeconds = Double(samplesWritten) / Output.sampleRate
        let micSeconds = Double(micSamplesWritten) / Output.sampleRate
        log(String(format: "summary: system=%.1fs mic=%.1fs (source=%@)",
                   systemSeconds, micSeconds, micSource))
        // Keyed on micDestURL, not micURL: in mic-only mode (a voice note) micURL is
        // nil and the mic writes to outputURL — the one mode where the mic IS the
        // whole recording must not be the one mode that stays quiet about failing.
        if micDestURL != nil, micSamplesWritten == 0 {
            log("mic: FAILED — captured 0 samples (source=\(micSource)). " +
                "The recording was saved with the system audio only.")
        }

        if let a = activity {
            ProcessInfo.processInfo.endActivity(a)
            activity = nil
        }
    }

    // SCStreamOutput: system audio on .audio, and (macOS 15+) the mic on .microphone.
    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer,
                of type: SCStreamOutputType) {
        guard !paused, sampleBuffer.isValid, let pcm = sampleBuffer.toPCMBuffer() else { return }
        if #available(macOS 15.0, *), type == .microphone {
            writeMic(pcm)
            return
        }
        guard type == .audio else { return }
        do {
            if audioFile == nil {
                audioFile = try AVAudioFile(forWriting: outputURL, settings: Output.settings)
            }
            try audioFile?.write(from: pcm)
            samplesWritten += AVAudioFramePosition(pcm.frameLength)
        } catch {
            log("write error: \(error)")
        }
    }

    // AVCaptureAudioDataOutput (fallback path) delivers mic buffers here.
    func captureOutput(_ output: AVCaptureOutput, didOutput sampleBuffer: CMSampleBuffer,
                       from connection: AVCaptureConnection) {
        guard !paused, sampleBuffer.isValid, let pcm = sampleBuffer.toPCMBuffer() else { return }
        writeMic(pcm)
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        log("stream stopped: \(error)")
        // The system capture stream can die mid-recording (display change, etc.).
        // Restart it (writing continues to the same WAVs) so we don't silently lose
        // the participants' audio for the rest of the meeting.
        guard !stopping, captureSystem, systemRestarts < 5 else { return }
        systemRestarts += 1
        Task {
            do {
                try await startSystem()
                log("system stream restarted (#\(systemRestarts))")
            } catch {
                log("system stream restart failed: \(error)")
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
    log(message)
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
        log("parent gone — self-terminating")
        stopAndExit()
    }
}
parentWatch.resume()

RunLoop.main.run()
