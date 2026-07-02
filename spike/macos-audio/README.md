# M1 Spike — Capturing system audio on macOS (ScreenCaptureKit)

Proves the riskiest premise of MeetMD: capturing the **mixed system audio** (all the participants you hear), **without a virtual driver** and in a **browser/app-agnostic** way.

## Spike result (2026-06-08)

- ✅ **Validated end to end.** After granting **Screen Recording** to VSCode, the helper captured **3.2s of system audio** into a WAV of ~614 KB (48kHz × 2ch × 16-bit, duration confirmed via `afinfo`). It captures the mixed audio (all participants), **without a virtual driver** and browser/app-agnostic.
- ℹ️ **Permission is the only friction.** Without the **Screen Recording (TCC)** permission, the API returns `SCStreamErrorDomain Code=-3801 "user declined TCCs"`. The permission only takes effect **after restarting the app** that runs the binary. In the product, it is requested once during onboarding.
- 🎯 **Conclusion:** the biggest architectural risk is **solved and verified**.

## Build

```bash
swiftc -O SystemAudioRecorder.swift -o system-audio-recorder
```

## Grant permission and validate

1. Run once: `./system-audio-recorder out.wav 8`
2. On first use, macOS asks for **Screen Recording**. If the prompt does not appear, grant it manually in:
   **System Settings ▸ Privacy & Security ▸ Screen Recording** → enable the **terminal** (Terminal/iTerm/VS Code) that is running the binary.
3. **Restart the terminal** (the permission only takes effect after reopening) and run again:
   ```bash
   # play any audio/video during the capture
   ./system-audio-recorder out.wav 8
   afinfo out.wav   # should show ~8s, 48kHz, 2 channels
   ```
4. Success = `out.wav` with the expected duration and audible audio.

## How it becomes a product

This helper is the prototype of the binary that the **Go bridge** will invoke on macOS (`internal/audio` with the `darwin` build tag): the bridge manages start/stop and the Swift helper performs the capture, recording the WAV that whisper.cpp (M2) transcribes. In the final product, the permission is requested once during onboarding.

## Notes

- `excludesCurrentProcessAudio = true` avoids capturing the helper's own audio.
- Output: 16kHz, mono, PCM 16-bit — whisper.cpp's native format (no resample).
- Windows (WASAPI loopback) and Linux (PipeWire/PulseAudio monitor) are analogous paths, without the permission friction of macOS.
