# MeetMD — Output Format Spec

- **Date:** 2026-06-08
- **Status:** Implemented
- **Author:** Robson Müller

> Implemented as specified, with two additions: routing **per project**
> (`output_root/<project>/…`) and the `project` field in the `meeting.md` frontmatter.
> Speakers today are `You` (mic) and `Participants` (system) — per-person diarization
> is future work (#5).

Defines the structure of the `.md` files that MeetMD writes — the tool's **main product**. The format is optimized for an LLM (Claude) to read and process: stable frontmatter, predictable sections, relative links.

## 1. Directory structure

```
<output_root>/
├── INDEX.md
├── 2026-06-08-1430-sprint-planning-7/
│   ├── meeting.md        # metadata + overview (generated)
│   ├── transcript.md     # full transcript (generated)
│   ├── summary.md        # empty TEMPLATE — Claude fills it in
│   └── actions.md        # empty TEMPLATE — Claude fills it in
└── 2026-06-08-1600-client-x-call/
    └── ...
```

- **Folder name:** `YYYY-MM-DD-hhmm-<title-slug>`. Chronologically sortable, unique, readable.
- **`slug`:** title in lowercase, without accents, spaces → `-`. No title → `reuniao`.

## 2. Frontmatter convention

All files share an identity block in the YAML frontmatter:

```yaml
---
id: 2026-06-08-1430-sprint-planning-7
title: Sprint Planning 7
date: 2026-06-08
start: "14:30"
end: "15:12"
duration_min: 42
platform: google-meet
participants:
  - Robson Müller
  - Alessandro
  - Leonardo
source: meetmd
---
```

Derived fields (`duration_min`) are computed by the bridge. `participants` comes from scraping Meet; empty if unavailable.

## 3. `meeting.md`

The meeting's entry point. Full frontmatter + overview + links to the other files.

```markdown
---
id: 2026-06-08-1430-sprint-planning-7
title: Sprint Planning 7
date: 2026-06-08
start: "14:30"
end: "15:12"
duration_min: 42
platform: google-meet
participants: [Robson Müller, Alessandro, Leonardo]
source: meetmd
status: raw            # raw | summarized
---

# Sprint Planning 7

> Meeting captured by MeetMD on 2026-06-08 14:30 (42 min).

## Files
- [Full transcript](transcript.md)
- [Summary](summary.md) — _to be filled in_
- [Actions](actions.md) — _to be filled in_

## Participants
- Robson Müller
- Alessandro
- Leonardo
```

The `status` field lets Claude know whether the summary has already been generated (`raw` → not yet).

## 4. `transcript.md`

Full transcript with timestamps and a minimal speaker label (`You` vs `Participants`, from the 2-channel separation — see architecture spec §3.2).

```markdown
---
id: 2026-06-08-1430-sprint-planning-7
title: Sprint Planning 7
date: 2026-06-08
source: meetmd
kind: transcript
---

# Transcript — Sprint Planning 7

[00:00:04] Participants: Alright, let's start with the sprint board.
[00:00:11] You: Sounds good. Issue 70 is already closed, so what's left...
[00:01:23] Participants: About the deploy, I think we should hold off until Friday.
...
```

- Timestamps `[hh:mm:ss]` relative to the meeting's start.
- Speaker label limited to `You` / `Participants` in the MVP (no per-person diarization).
- Raw Whisper text, unedited.

## 5. `summary.md` (template to be filled in)

Generated **empty**, with sections and instructions for Claude. The tool does not call an LLM (decision: transcript + ready-made structure).

```markdown
---
id: 2026-06-08-1430-sprint-planning-7
title: Sprint Planning 7
date: 2026-06-08
source: meetmd
kind: summary
status: empty          # empty | filled
---

# Summary — Sprint Planning 7

<!-- MeetMD: fill in from transcript.md. Remove this comment when done. -->

## TL;DR
_(2-3 sentences)_

## Topics discussed
-

## Decisions
-

## Open questions
-
```

## 6. `actions.md` (template to be filled in)

```markdown
---
id: 2026-06-08-1430-sprint-planning-7
title: Sprint Planning 7
date: 2026-06-08
source: meetmd
kind: actions
status: empty
---

# Actions — Sprint Planning 7

<!-- MeetMD: extract action items from transcript.md. One per line. -->

| # | Action | Owner | Due | Status |
|---|--------|-------|-----|--------|
|   |        |       |     | open   |
```

## 7. `INDEX.md` (root)

Maintained by the bridge on every new meeting. Most recent table row at the top.

```markdown
---
source: meetmd
kind: index
updated: 2026-06-08
---

# Meetings — MeetMD

| Date | Meeting | Duration | Platform | Status |
|------|---------|----------|----------|--------|
| 2026-06-08 14:30 | [Sprint Planning 7](2026-06-08-1430-sprint-planning-7/meeting.md) | 42 min | Google Meet | raw |
| 2026-06-08 16:00 | [Client X call](2026-06-08-1600-client-x-call/meeting.md) | 28 min | Google Meet | raw |
```

## 8. Usage contract with Claude

The intended flow: the user points Claude at `<output_root>` and asks, for example, _"summarize the last meeting"_. Claude:

1. Reads `INDEX.md` → finds the most recent meeting.
2. Opens `meeting.md` (context) and `transcript.md` (content).
3. Fills in `summary.md` and `actions.md`, switches `status: empty → filled`.
4. Updates `status: raw → summarized` in `meeting.md` and in `INDEX.md`.

The format is stable and predictable precisely so that this contract works without ambiguity.

## 9. Format decisions

- **Markdown + YAML frontmatter:** human-readable and trivial for an LLM to parse.
- **Separate files** (transcript / summary / actions) instead of a single one: Claude can rewrite `summary.md` without touching the raw transcript, and the transcript can be large.
- **Templates with instruction-comments (`<!-- MeetMD: ... -->`):** they guide the LLM and are removable, without polluting the final doc.
- **`status` fields:** they make the pipeline idempotent — you can tell what has already been processed.
