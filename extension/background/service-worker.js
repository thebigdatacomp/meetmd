// MeetMD service worker: the only context that talks to the local bridge.
// Content script and popup send messages; this relays them to the bridge HTTP
// API and tracks the active session (persisted, since MV3 SWs are ephemeral).

importScripts("../lib/protocol.js");

const STORAGE_KEY = "meetmd.session";
const BADGE_RECORDING = "●"; // ●
const BADGE_COLOR = "#d93025";

// --- session state (persisted in chrome.storage.local) ----------------------

async function getState() {
  const o = await chrome.storage.local.get(STORAGE_KEY);
  return o[STORAGE_KEY] || { recording: false, sessionId: null, title: "" };
}

async function setState(state) {
  await chrome.storage.local.set({ [STORAGE_KEY]: state });
  await chrome.action.setBadgeText({ text: state.recording ? BADGE_RECORDING : "" });
  if (state.recording) {
    await chrome.action.setBadgeBackgroundColor({ color: BADGE_COLOR });
  }
}

async function resetState() {
  await setState({ recording: false, sessionId: null, title: "" });
}

// --- bridge client ----------------------------------------------------------

async function bridge(path, options) {
  const res = await fetch(MEETMD_BRIDGE_BASE + path, options);
  if (!res.ok) {
    let message = res.statusText;
    try {
      message = (await res.json()).message || message;
    } catch (_) {
      /* non-JSON error body */
    }
    throw new Error(message);
  }
  return res.json();
}

// syncedState reconciles local state with the bridge's /status (the source of
// truth), so the badge/popup stay accurate even when the CLI started or stopped
// the session. Returns the authoritative state plus whether the bridge is up.
async function syncedState() {
  let status;
  try {
    status = await bridge("/status", { method: "GET" });
  } catch (_) {
    return { state: await getState(), online: false };
  }

  const recording = status.state === "recording";
  const meeting = status.meeting || {};
  const next = {
    recording,
    sessionId: recording ? meeting.ID || null : null,
    title: recording ? meeting.Title || "" : "",
  };

  const local = await getState();
  if (next.recording !== local.recording || next.sessionId !== local.sessionId) {
    await setState(next); // also refreshes the badge
  }
  return { state: next, online: true };
}

async function startRecording(meeting) {
  const state = await getState();
  if (state.recording) return state; // already recording — idempotent

  const title = (meeting && meeting.title) || "";
  const result = await bridge("/sessions/start", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      title,
      platform: MEETMD_PLATFORM,
      participants: (meeting && meeting.participants) || [],
      startedAt: new Date().toISOString(),
    }),
  });
  const next = { recording: true, sessionId: result.sessionId, title };
  await setState(next);
  return next;
}

async function stopRecording() {
  const state = await getState();
  if (!state.recording || !state.sessionId) {
    await resetState();
    return null;
  }
  const result = await bridge(`/sessions/${state.sessionId}/stop`, { method: "POST" });
  await resetState();
  return result;
}

async function cancelRecording() {
  const state = await getState();
  if (state.recording && state.sessionId) {
    await bridge(`/sessions/${state.sessionId}/cancel`, { method: "POST" }).catch(() => {});
  }
  await resetState();
}

// --- message routing --------------------------------------------------------

const HANDLERS = {
  [MEETMD_MSG.MEETING_STARTED]: async (msg) => ({ state: await startRecording(msg.meeting) }),
  [MEETMD_MSG.MEETING_ENDED]: async () => ({ result: await stopRecording() }),
  [MEETMD_MSG.START]: async (msg) => ({ state: await startRecording(msg.meeting || {}) }),
  [MEETMD_MSG.STOP]: async () => ({ result: await stopRecording() }),
  [MEETMD_MSG.CANCEL]: async () => {
    await cancelRecording();
    return {};
  },
  [MEETMD_MSG.STATUS]: async () => {
    const { state, online } = await syncedState();
    return { state, health: online };
  },
};

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  const handler = HANDLERS[msg && msg.type];
  if (!handler) {
    sendResponse({ ok: false, error: "unknown message type" });
    return false;
  }
  handler(msg)
    .then((data) => sendResponse({ ok: true, ...data }))
    .catch((err) => sendResponse({ ok: false, error: err.message }));
  return true; // keep the channel open for the async response
});
