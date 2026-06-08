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
    private var stream: SCStream?
    private var audioFile: AVAudioFile?
    private var samplesWritten: AVAudioFramePosition = 0

    init(outputURL: URL) {
        self.outputURL = outputURL
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
    }

    func stop() async throws {
        try await stream?.stopCapture()
        audioFile = nil // flush/close
    }

    // SCStreamOutput: receives audio sample buffers.
    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer,
                of type: SCStreamOutputType) {
        guard type == .audio, sampleBuffer.isValid,
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

    enum RecorderError: Error { case noDisplay }
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
//   system-audio-recorder <output.wav>            → record until SIGTERM/SIGINT
//   system-audio-recorder <output.wav> <seconds>  → record for a fixed time (spike/testing)
let args = CommandLine.arguments
guard args.count >= 2 else {
    fail("usage: system-audio-recorder <output.wav> [seconds]")
}
let outputURL = URL(fileURLWithPath: args[1])
let duration = args.count >= 3 ? (Double(args[2]) ?? 0) : 0 // 0 = until signal

guard #available(macOS 13.0, *) else {
    fail("ScreenCaptureKit audio capture requires macOS 13+")
}

let recorder = SystemAudioRecorder(outputURL: outputURL)

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

// Graceful stop on SIGTERM/SIGINT (how the Go bridge ends a recording).
// Sources must be retained or they are deallocated and never fire.
var signalSources: [DispatchSourceSignal] = []
for sig in [SIGTERM, SIGINT] {
    signal(sig, SIG_IGN)
    let source = DispatchSource.makeSignalSource(signal: sig, queue: .main)
    source.setEventHandler { stopAndExit() }
    source.resume()
    signalSources.append(source)
}

RunLoop.main.run()
