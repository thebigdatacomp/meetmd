# MeetMD — Architecture Spec

- **Date:** 2026-06-08
- **Status:** Implemented (with evolutions)
- **Author:** Robson Müller

> This spec records the initial design. What was built has evolved in some
> areas — current source of truth: `CLAUDE.md`. Main differences: the **main
> UI is the menu-bar app** (not the extension); detection on **Safari is via
> AppleScript** in the bridge; whisper.cpp runs **arm64+Metal**; there is **You vs
> Participants diarization** (mic on a 2nd channel), **VAD/anti-hallucination**, config
> **hot-reload** and a **LaunchAgent service** (with a caveat about TCC permissions — see #3/#4).

## 1. Objective

Capture meetings and deliver the transcript **structured in Markdown** in a local directory of a project, ready for Claude to process. The user should not have to copy/paste anything: the meeting ends, and the files are already there in the right format.

## 2. Requirements

### Functional
- **F1.** Capture the audio of **all participants** in the meeting (not just the user's microphone).
- **F2.** Work in a **browser-agnostic** way (Chrome, Firefox, Safari) — and ideally even with desktop apps.
- **F3.** Automatically detect the start/end of a meeting on Google Meet (MVP).
- **F4.** Transcribe locally, without the audio leaving the machine.
- **F5.** Write the output as structured `.md` in a configurable directory, with the raw transcript + ready-made `summary.md`/`actions.md` (templates) for Claude to fill in.
- **F6.** Maintain a navigable `INDEX.md` of all meetings.

### Non-functional
- **NF1.** Privacy: audio never leaves the machine; temporary `.wav` deleted at the end by default.
- **NF2.** Zero recurring cost in the MVP (no paid API).
- **NF3.** Pluggable transcriber (`Transcriber` interface) — swap whisper.cpp for an API later without refactoring the rest.
- **NF4.** User setup as simple as possible, given the inherent cost of audio capture in the OS.

## 3. Architecture decisions and tradeoffs

### 3.1. Where to capture the audio — **OS loopback** (decided)

Two alternatives were evaluated:

| | Tab audio (`getDisplayMedia`) | OS loopback (chosen) |
|---|---|---|
| Captures all participants | ✅ (tab audio) | ✅ (system output) |
| Browser-agnostic | ❌ Chromium-only (Firefox/Safari don't capture tab audio) | ✅ any browser + desktop apps |
| User setup | Trivial (share-tab prompt) | Medium — depends on the OS |
| Core of the solution | In the browser | In the native bridge |

**Choice: OS loopback**, because F2 (truly agnostic) and F1 are hard requirements. Consequence: the core lives in the Go bridge, and the extension becomes a detection/metadata helper.

**Cost per platform:**
- **Windows:** native WASAPI loopback — simple.
- **Linux:** PulseAudio/PipeWire monitor source — simple.
- **macOS:** more sensitive. Two options:
  - **ScreenCaptureKit** (macOS 13+) captures system audio with permission, no driver. Preferred.
  - **Virtual driver (BlackHole)** as a fallback for macOS < 13.

> ⚠️ macOS is the biggest implementation risk. Validate capture via ScreenCaptureKit early, before investing in the rest.

### 3.2. Separate mic capture (minimal diarization)

The system loopback is the **mixed** audio (all participants, including you via the return). To allow at least the **"me vs. others"** separation, the bridge captures **two channels**:
- **Channel A:** system loopback (other participants).
- **Channel B:** user's microphone (you).

Each channel is transcribed and merged by timestamp. Real per-person diarization (pyannote etc.) is a future upgrade — outside the MVP.

### 3.3. Transcription — **local whisper.cpp** (decided)

- **whisper.cpp** running in the bridge: private (NF1), no cost (NF2), good quality. Suggested default model: `ggml-base` or `ggml-small` (pt). Configurable.
- Browser SpeechRecognition was **discarded**: Chrome-only (breaks F2) and only handles the mic well (breaks F1).
- Whisper API sits behind the `Transcriber` interface (NF3), enabled by a flag, for anyone who wants more quality while accepting cost + audio leaving the machine.

### 3.4. Local bridge — **Go server** (decided)

- Go daemon listening on `127.0.0.1:<port>` (suggested default: `8765`), local loopback only.
- Aligns with the TBDC stack (Go in Bora), easy to package as a single cross-platform binary.
- A Native Messaging Host alternative was considered (no open port), but per-OS registration is more annoying and hinders agnostic/browserless use. Local HTTP is simpler and more flexible.

### 3.5. Role of the extension

The extension (WebExtension MV3, portable across Chrome/Firefox) does **only**:
1. Detects that the user entered/left a Google Meet (URL match `meet.google.com/*`).
2. Reads the meeting **title** and the **participant names** from the DOM (which the OS audio does not provide).
3. Fires `POST /sessions/start` (on entering) and `POST /sessions/stop` (on leaving or clicking stop).

Since the core is agnostic, the extension is **replaceable**: a tray app + calendar integration would do the same, 100% browserless. In the MVP it stays as the extension because it is the fastest path to the Meet metadata.

## 4. End-to-end flow

```
1. User joins a Meet
2. Extension detects → reads title + participants from the DOM
3. POST /sessions/start { title, platform, participants, startedAt }
4. Bridge creates the session folder, starts recording 2 channels (loopback + mic) → temp .wav
5. User leaves the Meet (or clicks "stop")
6. POST /sessions/{id}/stop
7. Bridge stops the capture → runs whisper.cpp on each channel → merges by timestamp
8. Bridge writes transcript.md + summary.md + actions.md + meeting.md
9. Bridge updates INDEX.md
10. Bridge deletes the temporary .wav (config)
11. Claude reads the directory and processes it
```

## 5. Bridge HTTP API (local)

| Method | Route | Description |
|--------|------|-------------|
| `GET` | `/health` | Extension checks whether the bridge is running |
| `GET` | `/status` | Current state (idle / recording + session) |
| `POST` | `/sessions/start` | Starts capture. Body: `{title, platform, participants[], startedAt}` → `{sessionId}` |
| `POST` | `/sessions/{id}/stop` | Finalizes, transcribes, writes the `.md` → `{sessionDir, files[]}` |
| `POST` | `/sessions/{id}/cancel` | Aborts and discards the capture |

Errors in JSON `{error, message}`. No auth in the MVP (local loopback); evaluate a shared token later.

## 6. Configuration

File `~/.meetmd/config.yaml`:

```yaml
output_root: /Users/robsonmuller/dev/projects/tbdc/<project>/meetings
port: 8765
language: pt
whisper:
  engine: local            # local | api
  model_path: ~/.meetmd/models/ggml-small.bin
audio:
  capture_mic: true        # channel B (you)
  delete_wav_on_finish: true
```

## 7. Repo structure

```
meetmd/
├── bridge/                 # daemon Go (núcleo)
│   ├── cmd/meetmd/         # main.go
│   ├── internal/
│   │   ├── server/         # HTTP local + handlers
│   │   ├── audio/          # captura loopback por SO (build tags darwin/windows/linux)
│   │   ├── transcribe/     # interface Transcriber + whisper.cpp + (api)
│   │   ├── writer/         # geração dos .md + INDEX
│   │   └── config/
│   └── go.mod
├── extension/              # WebExtension MV3
│   ├── manifest.json
│   ├── content/            # detecção Meet + scrape do DOM
│   ├── background/         # chamadas ao bridge
│   └── popup/              # UI start/stop + status
└── docs/specs/
```

## 8. Out of scope (MVP)

- Zoom/Teams (web or desktop) — only Google Meet in the MVP.
- Real per-person diarization.
- Automatic LLM summarization inside the tool (Claude does it later, reading the files).
- Cloud / multi-machine sync.

## 9. Risks and mitigations

| Risk | Mitigation |
|-------|-----------|
| Audio capture on macOS (biggest uncertainty) | ScreenCaptureKit spike before the rest; BlackHole as fallback |
| Meet DOM scrape breaks with a UI change | Isolate selectors in a module; degrade to empty title/participants without crashing |
| Transcription quality in pt | Allow swapping the model (small/medium) via config |
| OS audio/screen permissions | Document the setup; check permission in `/health` |

## 10. Suggested milestones

1. **M1 — Audio spike:** loopback capture working on the 3 OSes (macOS focus), records `.wav`.
2. **M2 — Transcription:** whisper.cpp integrated, `.wav` → `transcript.md`.
3. **M3 — Complete bridge:** HTTP API + `.md` writer + INDEX + config.
4. **M4 — Extension:** Meet detection + scrape + start/stop.
5. **M5 — Polish:** delete `.wav`, status popup, per-OS setup doc.
