// MeetMD content script: detects when a Google Meet call is active, scrapes
// best-effort metadata, and tells the service worker to start/stop recording.
//
// Meet's DOM is obfuscated and changes often, so all fragile selectors live in
// one place and every scrape degrades gracefully (empty → bridge still records).

(function () {
  const POLL_MS = 2000;
  const TITLE_PREFIX = /^Meet\s*[–\-—]\s*/i;
  const MAX_NAME_LEN = 60;

  const SELECTORS = {
    // "Leave call" button — the most reliable signal of an active call.
    // Multiple locales because aria-labels are localized.
    leaveCall: [
      '[aria-label*="Leave call" i]',
      '[aria-label*="Sair da chamada" i]',
      '[aria-label*="Abandonar chamada" i]',
      'button[jsname][data-tooltip*="Leave" i]',
    ],
    // Participant name tiles (best-effort).
    participantName: ["[data-participant-id] [data-self-name]", "[data-participant-id] [jsname]"],
  };

  function inCall() {
    return SELECTORS.leaveCall.some((sel) => document.querySelector(sel) !== null);
  }

  function scrapeTitle() {
    return (document.title || "").replace(TITLE_PREFIX, "").trim();
  }

  function scrapeParticipants() {
    const names = new Set();
    for (const sel of SELECTORS.participantName) {
      document.querySelectorAll(sel).forEach((el) => {
        const name = (el.getAttribute("data-self-name") || el.textContent || "").trim();
        if (name && name.length <= MAX_NAME_LEN) names.add(name);
      });
    }
    return [...names];
  }

  function scrapeMeeting() {
    return { title: scrapeTitle(), participants: scrapeParticipants() };
  }

  function notify(type, payload) {
    chrome.runtime.sendMessage({ type, ...payload }).catch(() => {
      // Service worker may be asleep/reloading; next poll retries the state.
    });
  }

  let active = false;

  function tick() {
    const nowInCall = inCall();
    if (nowInCall && !active) {
      active = true;
      notify(MEETMD_MSG.MEETING_STARTED, { meeting: scrapeMeeting() });
    } else if (!nowInCall && active) {
      active = false;
      notify(MEETMD_MSG.MEETING_ENDED, {});
    }
  }

  setInterval(tick, POLL_MS);
  tick();

  // Closing the tab mid-call counts as leaving.
  window.addEventListener("beforeunload", () => {
    if (active) notify(MEETMD_MSG.MEETING_ENDED, {});
  });
})();
