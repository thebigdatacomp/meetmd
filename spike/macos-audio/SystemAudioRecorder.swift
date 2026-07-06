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

    // Mic capture (the user's own voice, which never reaches system output).
    // Captured with AVCaptureSession, NOT AVAudioEngine.inputNode: only capture can
    // share the built-in mic with the meeting app (a browser in a call). The engine
    // path failed there — silent, or error -10868 "format not supported" when the
    // browser already held the mic — which is why AirPods worked but the built-in
    // mic didn't. AVCaptureSession opens the device non-exclusively.
    private var micSession: AVCaptureSession?
    private var micFile: AVAudioFile?
    private var micSamplesWritten: AVAudioFramePosition = 0
    private var micConverter: AVAudioConverter?
    private var micConverterFormat: AVAudioFormat?
    private let micQueue = DispatchQueue(label: "com.tbdc.meetmd.mic")
    private let micTarget = AVAudioFormat(commonFormat: .pcmFormatFloat32,
                                          sampleRate: Output.sampleRate,
                                          channels: Output.channels, interleaved: false)!

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

    // startMic captures the default audio input via AVCaptureSession and writes it,
    // converted to 16kHz mono, to `dest`. AVCaptureSession opens the device
    // non-exclusively, so it captures the built-in mic even while the meeting app
    // (a browser in a call) already holds it — where AVAudioEngine.inputNode failed.
    private func startMic(to dest: URL?) throws {
        guard let micURL = dest else { return }
        self.micFile = try AVAudioFile(forWriting: micURL, settings: Output.settings)

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

        session.startRunning()
        self.micSession = session
    }

    // AVCaptureAudioDataOutput delivers mic buffers in the device's native format;
    // convert each to 16kHz mono (rebuilding the converter lazily if the format
    // changes, e.g. an AirPods ↔ built-in swap) and append to the mic WAV. Runs
    // serially on micQueue.
    func captureOutput(_ output: AVCaptureOutput, didOutput sampleBuffer: CMSampleBuffer,
                       from connection: AVCaptureConnection) {
        guard !paused, sampleBuffer.isValid, let file = micFile,
              let pcm = sampleBuffer.toPCMBuffer(),
              pcm.frameLength > 0, pcm.format.sampleRate > 0 else { return }
        let inFormat = pcm.format
        if micConverterFormat != inFormat {
            micConverter = AVAudioConverter(from: inFormat, to: micTarget)
            micConverterFormat = inFormat
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
            do {
                try file.write(from: out)
                micSamplesWritten += AVAudioFramePosition(out.frameLength)
            } catch {
                FileHandle.standardError.write("mic write error: \(error)\n".data(using: .utf8)!)
            }
        }
    }

    func stop() async throws {
        stopping = true
        // stopRunning() blocks until in-flight capture callbacks drain, so it is
        // safe to release micFile right after (no callback races on it).
        micSession?.stopRunning()
        micSession = nil
        try? await stream?.stopCapture() // a dead stream must NOT block finalization below
        stream = nil
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
