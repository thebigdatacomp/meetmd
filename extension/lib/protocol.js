// Shared MeetMD constants, loaded by the content script, service worker, and
// popup. Uses `var` so the declarations attach to the global of each context
// (content isolated world, SW global, popup window) — no ES module imports.

// Bridge daemon endpoint (must match bridge config `port`, default 8765).
var MEETMD_BRIDGE_BASE = "http://127.0.0.1:8765";

// Platform tag sent to the bridge for Google Meet sessions.
var MEETMD_PLATFORM = "google-meet";

// Message types exchanged between content/popup and the service worker.
var MEETMD_MSG = {
  // content → background
  MEETING_STARTED: "MEETING_STARTED",
  MEETING_ENDED: "MEETING_ENDED",
  // popup → background
  START: "START",
  STOP: "STOP",
  CANCEL: "CANCEL",
  STATUS: "STATUS",
};
