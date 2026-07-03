# MeetMD — Extension (Google Meet)

WebExtension (MV3) that detects meetings in Google Meet and triggers `start`/`stop` on the [local bridge](../bridge). It's a detection/UX helper — it does **not** capture audio or record files (that's the bridge).

## Components

| File | Role |
|---------|-------|
| `manifest.json` | MV3; content script on Meet, service worker, popup, host permission for the bridge |
| `lib/protocol.js` | Shared constants (bridge URL, platform, message types) |
| `content/meet.js` | Detects an active call on Meet + best-effort scrape of title/participants |
| `background/service-worker.js` | The only one that talks to the bridge; manages the active session |
| `popup/` | Bridge + recording status, and manual control (start/stop/cancel) |

## Flow

```
content/meet.js  --(MEETING_STARTED/ENDED)-->  service-worker  --HTTP-->  bridge (127.0.0.1:8765)
popup            --(START/STOP/CANCEL/STATUS)-->
```

The content script only observes the DOM and sends messages; the service worker (which has `host_permissions` for `127.0.0.1`) makes the HTTP calls. The session lives in `chrome.storage.local` because the MV3 SW is ephemeral.

**Coexists with the CLI:** the extension and the CLI (`meetmd start/stop`) are clients of the same bridge, which has a single active session. The popup reads the bridge's `/status` as the source of truth and reconciles the local state — so starting via the CLI and stopping via the popup (or vice versa) works; the badge syncs when the popup opens.

## Load (dev)

1. Start the bridge: `cd ../bridge && make run`
2. Chrome → `chrome://extensions` → enable **Developer mode** → **Load unpacked** → select this `extension/` folder.
3. Join a Google Meet: the badge turns 🔴 and recording starts on its own; when you leave, it ends and the `.md` files appear in `output_root`.
4. The popup shows the bridge status and allows manual start/stop (including outside of Meet).

## Known limitations (MVP)

- **Meet scrape is fragile:** the title comes from `document.title`; participants are best-effort and may come back empty (the bridge records anyway). Selectors are isolated in `content/meet.js`.
- Detection via the "Leave call" aria-label (en/pt). A new Meet UI may require adjusting the selectors.
- Google Meet only. Zoom/Teams are out of scope for the MVP.
- No custom icons yet (uses the Chrome default).
