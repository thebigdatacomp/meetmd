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
final class SystemAudioRecorder: NSObject, SCStreamOutput, SCStreamDelegate {
    private let outputURL: URL
    private let micURL: URL? // when set, the mic is captured to a second channel
    private var stream: SCStream?
    private var audioFile: AVAudioFile?
    private var samplesWritten: AVAudioFramePosition = 0

    // Mic capture (the user's own voice, which never reaches system output).
    private var engine: AVAudioEngine?
    private var micFile: AVAudioFile?

    // paused is read on the audio queue and written from signal handlers, so it
    // is guarded by a lock. While paused, samples are dropped, which keeps the
    // recording continuous (no gap) across pause/resume.
    private let lock = NSLock()
    private var pausedFlag = false
    var paused: Bool {
        get { lock.lock(); defer { lock.unlock() }; return pausedFlag }
        set { lock.lock(); pausedFlag = newValue; lock.unlock() }
    }

    init(outputURL: URL, micURL: URL?) {
        self.outputURL = outputURL
        self.micURL = micURL
    }

    func start() async throws {
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

        // Mic failure (e.g. no permission) must not abort the system capture.
        do {
            try startMic()
        } catch {
            FileHandle.standardError.write("mic indisponível: \(error)\n".data(using: .utf8)!)
        }
    }

    // startMic taps the default input device and writes it, resampled to 16kHz
    // mono, to a separate WAV. The same `paused` flag drops mic samples too.
    private func startMic() throws {
        guard let micURL = micURL else { return }
        let engine = AVAudioEngine()
        let input = engine.inputNode
        let inputFormat = input.outputFormat(forBus: 0)
        guard let target = AVAudioFormat(commonFormat: .pcmFormatFloat32,
                                         sampleRate: Output.sampleRate,
                                         channels: Output.channels,
                                         interleaved: false),
              let converter = AVAudioConverter(from: inputFormat, to: target) else {
            throw RecorderError.micUnavailable
        }
        let file = try AVAudioFile(forWriting: micURL, settings: Output.settings)

        input.installTap(onBus: 0, bufferSize: 4096, format: inputFormat) { [weak self] buffer, _ in
            guard let self = self, !self.paused else { return }
            let ratio = target.sampleRate / inputFormat.sampleRate
            let capacity = AVAudioFrameCount(Double(buffer.frameLength) * ratio) + 1024
            guard let out = AVAudioPCMBuffer(pcmFormat: target, frameCapacity: capacity) else { return }
            var fed = false
            var convError: NSError?
            converter.convert(to: out, error: &convError) { _, status in
                if fed { status.pointee = .noDataNow; return nil }
                fed = true
                status.pointee = .haveData
                return buffer
            }
            if convError == nil, out.frameLength > 0 {
                try? file.write(from: out)
            }
        }
        try engine.start()
        self.engine = engine
        self.micFile = file
    }

    func stop() async throws {
        try await stream?.stopCapture()
        audioFile = nil // flush/close
        engine?.inputNode.removeTap(onBus: 0)
        engine?.stop()
        engine = nil
        micFile = nil
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
    }

    var seconds: Double { Double(samplesWritten) / Output.sampleRate }

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
//   system-audio-recorder <output.wav> [seconds] [--mic <mic.wav>]
//     no seconds → record until SIGTERM/SIGINT; --mic also captures the mic.
var positional: [String] = []
var micPath: String?
do {
    let argv = CommandLine.arguments
    var i = 1
    while i < argv.count {
        if argv[i] == "--mic", i + 1 < argv.count {
            micPath = argv[i + 1]
            i += 2
        } else {
            positional.append(argv[i])
            i += 1
        }
    }
}
guard let outPath = positional.first else {
    fail("usage: system-audio-recorder <output.wav> [seconds] [--mic <mic.wav>]")
}
let outputURL = URL(fileURLWithPath: outPath)
let micURL = micPath.map { URL(fileURLWithPath: $0) }
let duration = positional.count >= 2 ? (Double(positional[1]) ?? 0) : 0 // 0 = until signal

guard #available(macOS 13.0, *) else {
    fail("ScreenCaptureKit audio capture requires macOS 13+")
}

let recorder = SystemAudioRecorder(outputURL: outputURL, micURL: micURL)

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
        print("recording system audio (\(mode)) → \(outputURL.path)")
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

RunLoop.main.run()
