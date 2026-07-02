# MeetMD — Menu-bar app (macOS)

Icon in the macOS top bar that controls the [local bridge](../bridge). Thin HTTP client, no Xcode (compiles with `swiftc`).

## What it does

- **Icon reflects state:** 🔴 recording · ⏸ paused · 🎙 ready · ⚠︎ bridge offline.
- **Popup when a meeting is detected:** when the bridge detects a Google Meet in Safari (`ask` mode), it asks *"Start recording?"* with **Record** / **Not now** (it won't ask again for the same declined meeting).
- **Menu:** Start · Pause/Resume · Stop and save · Open files folder · **Settings…** · Quit.
- **Settings:** native window (reads/writes via the bridge's `GET`/`PUT /settings`) — output folder, language, default project, automatic detection (ask/automatic/off), include microphone, and delete audio after transcribing. Internal paths (model, helper, VAD) are not exposed; the model is fixed at `small`. Changes apply without restarting (hot-reload).
- If the bridge is offline, it tries to start it (`meetmd serve`) — it looks for the binary in `MEETMD_BIN`, next to the app, or in the common paths.

## Build & run

```bash
swiftc -O MeetMDBar.swift -o meetmd-bar -framework Cocoa
./meetmd-bar    # appears in the top bar; no Dock icon
```

To start at login: add `meetmd-bar` under **Settings ▸ General ▸ Login Items**.

## Permissions

Audio capture and Safari detection belong to the **bridge**, not to this app — so the permissions (Screen Recording, Automation) are requested by the process running `meetmd serve`. See [../spike/macos-audio/README.md](../spike/macos-audio/README.md).

## Limitations (MVP)

- Recordings started from the popup carry no project (they go to the base `output_root`); use the panel/CLI with `-p` to separate by project.
- Pure client: it doesn't embed the bridge, it only controls it.
