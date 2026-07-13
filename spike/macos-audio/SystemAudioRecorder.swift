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
        let micDest: URL? = captureSystem ? micURL : outputURL
        if let dest = micDest {
            micFile = try AVAudioFile(forWriting: dest, settings: Output.settings)
            if let device = AVCaptureDevice.default(for: .audio) {
                log("mic: input padrão = \(device.localizedName)")
            } else {
                log("mic: nenhum input de áudio padrão encontrado")
            }
        }

        if captureSystem {
            try await startSystem() // may enable the mic on the same stream
            // Only fall back if ScreenCaptureKit did not take the mic, so we never
            // capture it twice.
            if micDest != nil, micSource == "none" {
                do {
                    try startMicFallback()
                } catch {
                    log("mic: fallback AVCaptureSession indisponível: \(error)")
                }
            }
            startMicWatchdog()
        } else {
            // Mic-only: the mic IS the recording, so a failure here is fatal.
            try startMicFallback()
        }
        log("mic: fonte=\(micSource)")
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
                micSource = "screencapturekit"
                log("mic: capturando via ScreenCaptureKit (mesmo stream do áudio do sistema)")
                return
            } catch {
                // A mic problem must never cost us the meeting: drop the mic from the
                // stream and bring the system audio up regardless.
                log("mic: SCK captureMicrophone falhou (\(error)) — reiniciando o stream SEM mic")
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
        NotificationCenter.default.addObserver(
            forName: .AVCaptureSessionRuntimeError, object: session, queue: nil
        ) { note in
            let err = note.userInfo?[AVCaptureSessionErrorKey] ?? "desconhecido"
            log("mic: AVCaptureSession runtime error: \(err)")
        }

        session.startRunning()
        self.micSession = session
        micSource = "avcapture"
        log("mic: capturando via AVCaptureSession (fallback) — device=\(device.localizedName)")
    }

    // writeMic converts a mic buffer (device format) to 16kHz mono and appends it.
    // Shared by both mic paths; always runs on micQueue, so it is serial.
    private func writeMic(_ pcm: AVAudioPCMBuffer) {
        guard let file = micFile, pcm.frameLength > 0, pcm.format.sampleRate > 0 else { return }
        let inFormat = pcm.format
        if micConverterFormat != inFormat {
            micConverter = AVAudioConverter(from: inFormat, to: micTarget)
            micConverterFormat = inFormat
            log("mic: formato de entrada \(Int(inFormat.sampleRate))Hz \(inFormat.channelCount)ch")
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
                log("mic: write error: \(error)")
            }
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
                log("mic: AVISO — o áudio do sistema está fluindo mas o mic não gravou " +
                    "nenhum sample em ~20s (fonte=\(self.micSource))")
            }
        }
        timer.resume()
        micWatchdog = timer
    }

    func stop() async throws {
        stopping = true
        micWatchdog?.cancel()
        micWatchdog = nil
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

        // Never fail silently: say exactly what each channel captured.
        let systemSeconds = Double(samplesWritten) / Output.sampleRate
        let micSeconds = Double(micSamplesWritten) / Output.sampleRate
        log(String(format: "resumo: sistema=%.1fs mic=%.1fs (fonte=%@)",
                   systemSeconds, micSeconds, micSource))
        if micURL != nil, micSamplesWritten == 0 {
            log("mic: FALHOU — 0 samples capturados (fonte=\(micSource)). " +
                "A reunião foi salva apenas com o áudio dos participantes.")
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
