// MeetMD local panel. Served by the bridge, so all calls are same-origin.

const $ = (id) => document.getElementById(id);
const show = (id, visible) => $(id).classList.toggle("hidden", !visible);

let recordingId = null;

async function api(path, options) {
  const res = await fetch(path, options);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.message || res.statusText);
  return data;
}

function render(status) {
  const active = status.state === "recording" || status.state === "paused";
  const paused = status.state === "paused";
  const meeting = status.meeting || {};
  recordingId = active ? meeting.ID || null : null;

  if (active) {
    $("state").textContent = paused ? "⏸ Pausado" : "🔴 Gravando";
    $("info").textContent = [meeting.Project, meeting.Title].filter(Boolean).join(" · ") || "sem título";
    show("info", true);
    show("start", false);
    show("pause", !paused);
    show("resume", paused);
    show("stop", true);
    show("cancel", true);
  } else {
    $("state").textContent = "Pronto";
    show("info", false);
    show("start", true);
    show("pause", false);
    show("resume", false);
    show("stop", false);
    show("cancel", false);
  }
}

async function refresh() {
  try {
    const status = await api("/status");
    $("dot").classList.add("up");
    render(status);
  } catch (_) {
    $("dot").classList.remove("up");
    $("state").textContent = "Bridge offline";
  }
}

async function act(fn) {
  try {
    await fn();
    $("hint").textContent = "";
  } catch (err) {
    $("hint").textContent = err.message;
  }
  await refresh();
}

$("form").addEventListener("submit", (e) => {
  e.preventDefault();
  act(() =>
    api("/sessions/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        title: $("title").value,
        project: $("project").value,
        platform: "manual",
        startedAt: new Date().toISOString(),
      }),
    }),
  );
});

const sessionAction = (action) => () =>
  act(() => (recordingId ? api(`/sessions/${recordingId}/${action}`, { method: "POST" }) : null));

$("pause").addEventListener("click", sessionAction("pause"));
$("resume").addEventListener("click", sessionAction("resume"));
$("stop").addEventListener("click", sessionAction("stop"));
$("cancel").addEventListener("click", sessionAction("cancel"));

setInterval(refresh, 2000);
refresh();
