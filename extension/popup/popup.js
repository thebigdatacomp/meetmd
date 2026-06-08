// MeetMD popup: shows bridge health + recording state and offers manual
// start/stop/cancel. All bridge calls go through the service worker.

const $ = (id) => document.getElementById(id);

function send(type, extra) {
  return chrome.runtime.sendMessage({ type, ...(extra || {}) });
}

function setHidden(el, hidden) {
  el.classList.toggle("hidden", hidden);
}

async function refresh() {
  let res;
  try {
    res = await send(MEETMD_MSG.STATUS);
  } catch (_) {
    res = { ok: false };
  }

  const healthy = !!(res && res.ok && res.health);
  $("health").classList.toggle("up", healthy);
  $("health").title = healthy ? "Bridge conectado" : "Bridge offline";

  if (!healthy) {
    $("status").textContent = "Bridge offline";
    $("hint").textContent = "Inicie o bridge MeetMD no terminal e reabra.";
    ["start", "stop", "cancel"].forEach((id) => setHidden($(id), true));
    setHidden($("meeting"), true);
    return;
  }

  const recording = !!(res.state && res.state.recording);
  $("hint").textContent = "";
  if (recording) {
    $("status").textContent = "🔴 Gravando";
    $("meeting").textContent = (res.state && res.state.title) || "Reunião sem título";
    setHidden($("meeting"), false);
    setHidden($("start"), true);
    setHidden($("stop"), false);
    setHidden($("cancel"), false);
  } else {
    $("status").textContent = "Pronto";
    setHidden($("meeting"), true);
    setHidden($("start"), false);
    setHidden($("stop"), true);
    setHidden($("cancel"), true);
  }
}

async function withBusy(action) {
  try {
    const res = await action();
    if (res && res.ok === false) throw new Error(res.error || "erro");
  } catch (err) {
    $("hint").textContent = err.message || "erro";
  }
  await refresh();
}

document.addEventListener("DOMContentLoaded", () => {
  $("start").addEventListener("click", () =>
    withBusy(() => send(MEETMD_MSG.START, { meeting: { title: "Gravação manual" } })),
  );
  $("stop").addEventListener("click", () => withBusy(() => send(MEETMD_MSG.STOP)));
  $("cancel").addEventListener("click", () => withBusy(() => send(MEETMD_MSG.CANCEL)));
  refresh();
});
