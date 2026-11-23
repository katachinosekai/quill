# kmem on Quill Android (design)

Status: design, 2026-11-22; open questions decided 2026-11-22 (fd77947). kmem task tc6efac (includes t90b538's "needs
you" mark). Base: Quill Desktop master 088a937, Quill Android master c6f872b
(0.9.29). Scope: both apps. No code in this document's commit.

Related: `docs/design/kmem-window.md` (the kmem window, kmem facts f6e5f52 and
fc6b73c), `docs/design/topic-tabs.md` (fabd19d, §3.11 phone API), update rule
f79d93a, approval scope `planscope.go` (t2debee).

## 1. Goal and non-goals

Goal: the user can see and handle on Quill Android what Quill Desktop's kmem
window offers: what kmem waits on them for, the tasks, the memory, and the
kmem assistant (kmem-lock-audit) with its chat and plan approval, so a plan
or question from the assistant never has to wait until the user is at the PC.

Non-goals

- No kmem changes. Quill Desktop runs the same `kmem` commands the kmem
  window runs today; new kmem output (for example `--json` for `find`, `get`,
  `trash list`) is kmem-lock-audit's developer work, after the user approves.
- No emptying of the recycle bin from the phone, ever (kmem rule 26; `kmem
  trash empty` also needs a terminal on the PC).
- No audit repair, `pzlock clear`, rule edits, rollouts or kmem installs
  from the phone.
- No second kmem assistant. Quill Android only views and drives the one that
  Quill Desktop runs.
- No kmem access when Quill Desktop is not reachable (no offline copy).
- No changes to the kmem assistant's model or the kmem settings from the
  phone in this design (see Open question 9).

## 2. Current state

### Quill Desktop

- kmem window (`kmemwindow.go`, `kmempanel.go`): left side has tabs Overview
  (with "Waiting for you", the kmem brief, sections over 8 KB, counts),
  Topics (handoffs and topic status), Tasks (read-only, grouped by topic) and
  Memory (topic filter, full-text find, fact view). Right side is the kmem
  assistant's chat.
- "Waiting for you" and the bubble on the kmem button (`kmembadge.go`,
  `kmemNeeds`): recycle bin count, audit problems, maintenance warnings (with
  the report file), open "kmem issue:" tasks, tasks for the user (topic
  `user` or titles starting "USER:"), and a paused rollout. It is read from
  `kmem brief --json`, `kmem task list --json` and `kmem rollout status
  --json`, every 30 min and on every window reload.
- Recycle bin (`kmembin.go`): Settings → General → Memory (kmem); restore
  and empty (the empty runs in a terminal).
- kmem assistant (`kmemassist.go`): the only always-running hidden session.
  It starts with Quill Desktop and keeps running when the window is closed.
  States: closed, probing, panels, elsewhere, starting, live, saving,
  stopping. The header shows the state, the model line ("model: X (kmem
  assistant setting | New sessions)"), Restart (after FAILED), Restart (the
  model setting changed), Save now and Stop assistant. Stop and Quit run the
  save-and-stop flow: a stop message, a 60 s countdown, then a three-way
  question (Stop without saving / Keep running until saved / Keep it
  running).
- Phone API (`phoneapi.go`, `phonewrite.go`, `phonemenu.go`,
  `phonehandoffs.go`, `phonesettings.go`, `phoneunlock.go`): `/phone/hello`,
  `sessions`, `stream` (long poll with change ring), `session/{id}/items`,
  writes under `session/{id}/...`, `handoffs` (+ resume, delete to bin),
  `settings`, `settings/questions` + `settings/answer`, `unlock*`. Every
  body has `"v": 1`. Every write needs control mode ("Let paired devices
  control sessions" + the phone's Control switch), an `Idempotency-Key`, and
  at most 10 writes/s per phone (`internal/folders/api.go`). Phones that
  need unlocking send `X-Quill-Unlock`.
- The assistant is invisible to phones: `sessionIDs` skips it, `preload`
  and `write` answer `not_found`, and `alert` sends its notifications only
  to the desktop (`notify.go`, kmem-window.md §4 #25).

### Quill Android

- kmem shows up in three places only: the `kmem` name on session rows
  (`SessionBars`), "Or resume a kmem handoff" in the New session dialog
  (`MainActivity.newSessionDialog`, long-press: Resume, Delete handoff → bin),
  and the delete-session warning.
- Reusable parts: `ChatView` (items, streaming, permission, question and
  plan cards), `PlanOverlay` (plan questions under their reply lines),
  `Remote` (stream loop, writes with retries), `PhoneApi`, `Notifier`
  (event types approval, question, plan, finished, error), `SessionBars`
  (side list, portrait and landscape), `UpdateNotes` + the after-update
  dialog (f79d93a).

## 3. Screens on Quill Android

### 3.1 Entry point and the "needs you" mark (t90b538)

- A **kmem button** at the top of the side list (both orientations), shown
  only when Quill Desktop reports kmem (§4.1). Like Quill Desktop's:
  - a count bubble = `needs.count` (same rules as `kmemNeeds.Count`);
  - a small dot while the assistant runs;
  - **"needs you" highlight** (accent outline and a "!" in the bubble) while
    the assistant waits for the user: a pending plan, question or permission,
    or the save-timeout question.
- Tapping opens the **kmem screen**. Back returns to the session.
- Notification (t90b538): a "kmem assistant needs you" notification when
  the assistant starts waiting for the user; tapping it opens the kmem screen
  on the Assistant tab. Its own channel setting, like the session types.

### 3.2 Layout

- Portrait: one pane with four tabs at the top: Overview, Tasks, Memory,
  Assistant. The Assistant tab carries the needs-you mark.
- Landscape (phone and tablet): panels left (Overview, Tasks, Memory as
  tabs) and the assistant chat right, like the kmem window; the divider
  reuses `SessionBars`' drag and remembers its width per orientation.
- Pull to refresh on every panel; panels also refresh from stream changes
  (§4.4).

### 3.3 Overview

- **Waiting for you (N)**: one row per kind, same texts as Quill Desktop:
  kmem issues (titles), recycle bin count, audit problems, maintenance
  warnings (count + report name), paused rollout, tasks for the user.
- Per row: "Tasks" jumps to the Tasks tab filtered; "Explain" / "Ask the
  assistant" fill the assistant's input (never send; shown only while the
  assistant takes input).
- **Recycle bin (N)**: count with "Empty it on Quill Desktop (Settings →
  General → Memory (kmem))." The list and Restore come with phase 4.
- **kmem brief**: the text, scrollable, monospace off.
- **Sections over 8 KB** and the counts line (topics, handoffs, open tasks).

### 3.4 Tasks

- List of open and claimed tasks by priority: `[id] p2 · claimed by quill ·
  title`; claimed rows mark gone / lease / lease expired / handoff.
- Filter chips: All, Waiting for you, kmem issues, and one per top-level
  topic (from `topics[]`); a search box filters titles.
- Tap a task: its detail (`kmem show`: why, notes, checkpoints, history).
- Safe actions (phase 4, Open question 2), each behind a confirm dialog that
  names the task: **Done** (with a one-line result) and **Release**. A
  claim of a live session is never released (kmem rule 19); the button is
  hidden and the server refuses.
- "Ask the assistant about this task" fills the assistant's input.

### 3.5 Memory (read-only)

- Topic list with sizes and a filter box (like `memoryTab`).
- Search (`kmem find`) with the results as rows `[id] kind · topic · text`.
- Tap a topic: its current facts (`kmem get`); tap a fact: `kmem show`.
- Long-press a fact: Copy id, Ask the assistant about it. No add, retire or
  trash in the first version (Open question 8).

### 3.6 Assistant

- `ChatView` on the assistant's items: the same cards, `PlanOverlay`,
  choices and queue as a session. Approve, Don't run, Allow, Deny and
  answers use the existing answer actions.
- Header: `kmem-lock-audit · kmem assistant`, the state line (`● running`,
  `● saving its handoff, then stopping · 42 s`, `stopped: <reason>`), the
  model line exactly as Quill Desktop shows it, and the buttons Quill Desktop
  shows in that state: Start assistant, Stop assistant, Restart, Save now.
- Stop assistant asks "Stop the kmem assistant? It saves its kmem handoff
  first (up to 60 s)." The chat stays visible while it saves and stops, so
  the user can still approve or answer (t90b538).
- The save-timeout question appears as a card with its three choices; the
  first answer on either side wins and closes it on the other.
- When the assistant is off, the pane shows Quill Desktop's reason line
  ("The kmem assistant is off ...") and, where Quill Desktop offers one, its
  button.
- Messages typed while it is busy queue like any session's.

## 4. Phone API

All new routes live under `/phone/kmem/`, answer `"v": 1` like the existing
ones, use the same errors (`{"error": {code, message}}` via
`folders.WriteError`) and run kmem off the UI thread. `503 kmem_unavailable`
when kmem memory is off or kmem is missing; `409 kmem_busy` when a kmem call
is already running for this phone (§5.4).

### 4.1 Capability

`POST /phone/hello` gains `"kmem": {"api": 1, "on": true}` (left out by
older Quill Desktop). Quill Android shows the kmem button only when `api >=
1 && on`. A later breaking change raises `api`; Quill Android shows "Update
Quill Desktop for kmem" for an unknown higher level it can't read.

### 4.2 Read endpoints

`GET /phone/kmem/overview`

```json
{"v": 1, "boot": "…",
 "needs": {"count": 7, "bin": 5, "audit": "", "maint": 3,
           "maintReport": "maint-20261122-114600.txt", "rolloutWait": "",
           "issues": [{"id": "t1a2b3c", "prio": 3, "title": "kmem issue: …", "topic": "kmem"}],
           "userTasks": []},
 "assistant": {"state": "live", "waiting": "plan", "model": "opus",
               "modelSource": "kmem assistant setting", "rev": 1732300000123, "epoch": 1732300},
 "brief": "…", "bigTopics": [{"name": "quill/android/releases", "kb": 11}],
 "counts": {"topics": 41, "handoffs": 6, "openTasks": 34}, "readMs": 1732300000000}
```

- `assistant.state`: `off | panels | elsewhere | starting | live | saving |
  stopping | failed`; `waiting`: `"" | plan | question | permission |
  saveTimeout`; `saveLeftS` while saving; `reason` and `button` (label) when
  the chat side shows a reason line.

`GET /phone/kmem/tasks?topic=quill&all=0`

```json
{"v": 1, "tasks": [{"id": "tc6efac", "prio": 4, "status": "claimed", "title": "…", "topic": "quill",
  "owner": "quill", "claim": "live|gone|lease|leaseExpired|handoff", "needsUser": false, "issue": false,
  "actions": ["done", "release"]}],
 "topics": ["kmem", "quill", "user", "zomboid"]}
```

- `actions` is what the server will accept from this phone (§5.2), so
  Quill Android never guesses.

`GET /phone/kmem/task/{id}` → `{"v": 1, "id", "text"}` (`kmem show`, text).

`GET /phone/kmem/topics` → `{"v": 1, "topics": [{"name", "facts", "kb"}]}`.

`GET /phone/kmem/find?q=…` and `GET /phone/kmem/get?topic=…` →
`{"v": 1, "facts": [{"id", "kind", "topic", "text", "pinned"}], "truncated": false}`.
kmem has no `--json` for these yet: Quill Desktop parses the text lines (as
`factIDs` and `parseTopics` do) and falls back to `{"text": …}` for lines
it can't read. `q` is 2-200 characters; at most 200 facts, 64 KiB.

As built (phase 3, `cmd/quill/phonekmemmemory.go`):

- `GET /phone/kmem/find?q=…` (plain words or a regex, as `kmem find`) →
  `{"v": 1, "facts": [{"id", "kind", "topic", "text", "why"?, "pinned",
  "expired"?}], "truncated": false}`. `why` is split off the text (kmem's
  `  WHY: `), `expired` only when true; a fact's text may span lines. A line
  kmem printed that isn't a fact comes before the first fact as `{"text",
  "pinned": false}` (no `id`); kmem's own notices are left out. `400
  bad_request` for `q` outside 2-200 characters or a bad regex (message from
  kmem); no matches = empty `facts`. At most once a second per phone: `429
  rate_limited` with `Retry-After` (seconds).
- `GET /phone/kmem/get?topic=…` → `{"v": 1, "topic", "facts": [as find],
  "sections": [{"name", "facts", "kb"}], "truncated": false}`. `sections`
  lists the topic's sections when it has any (`kb` has one decimal, as kmem
  prints it here: `13.2`); `facts` holds the topic's own facts. `400
  bad_request` for a name outside `[A-Za-z0-9._/-]` (or starting with `-`,
  with `..` or `//`), `404 not_found` when kmem has no current facts under it.
- `GET /phone/kmem/fact?id=f…` (`kmem show`) → `{"v": 1, "fact": {"id",
  "kind", "topic", "text", "why"?, "state"?, "pinned", "expired"?, "binned",
  "recorded", "by", "source"?, "validFrom"?, "validTo"?, "task"?,
  "supersededBy"?, "history": [{"at", "what", "by"}]}}`. `state` is
  `superseded` or `retired` (absent while current), `recorded` is kmem's
  RFC 3339 time, `by` the kmem name that wrote it, `history` kmem's change
  lines (`superseded by f…`, `retired: …`, `binned: …`, `restored from the
  bin`). Fact ids only (`f` + 6-16 hex): `400 bad_request` otherwise, `404
  not_found` for an unknown or purged id. Cited paths are not sent.
- The topic list is phase 1's `/phone/kmem/topics`. All three run one kmem
  process each (no cache), count toward the one-at-a-time limit per phone
  (`409 kmem_busy`, shared with the phase 1 reads), answer `503
  kmem_unavailable` when kmem is off or doesn't answer, and are GET only: no
  add, retire, pin or trash from the phone. Text, why, topic, `by` and
  history are scrubbed (§5.5); a fact text is capped at 32 KiB.

`GET /phone/kmem/bin` (phase 4) → `{"v": 1, "items": [{"id", "kind",
"days", "by", "reason", "title"}]}` (from `parseBin`).

### 4.3 The assistant session

It stays hidden: not in `/phone/sessions`, not in `order`, and the existing
`/phone/session/{id}/...` routes keep refusing its id (old phones see
nothing new). It gets its own routes that call the same handlers with the
assistant's `deskSession`:

| Route | Body | Same as |
|---|---|---|
| `GET /phone/kmem/assistant/items` | query as `session/{id}/items` | `items` (epoch, rev, delta, have) |
| `POST /phone/kmem/assistant/send` | `{text}` | `send` (queues while busy) |
| `POST /phone/kmem/assistant/answer` | as `session/{id}/answer` | `answer` (approvePlan, rejectPlan, allow, deny, answer, choice) |
| `POST /phone/kmem/assistant/interrupt` | `{}` | `interrupt` |
| `POST /phone/kmem/assistant/queue/{op}` | `{index, text}` | `queueAction` |
| `POST /phone/kmem/assistant/start` | `{}` | "Start assistant" (`kmemAssistRestart` / probe) |
| `POST /phone/kmem/assistant/stop` | `{}` | `kmemAssistStopNow` (save-and-stop) |
| `POST /phone/kmem/assistant/restart` | `{}` | Restart: FAILED → `kmemAssistRestart`; model changed → `kmemAssistReboot` |
| `POST /phone/kmem/assistant/save` | `{}` | Save now (`saveHandoffNow`) |
| `POST /phone/kmem/assistant/choose` | `{"choice": 0\|1\|2}` | `kmemAssistChoose` (save-timeout question) |

Every write answers `{"v": 1, "assistant": {…as in overview}}` (plus `item`
or `queue` where the session route returns them). A write in a state that
doesn't offer that button answers `409 stale`.

As built (phase 2): items answers `id: "kmem-assistant"` (the Claude session
id is not sent; a new assistant session is a new `epoch`) and an empty list
with `epoch: 0` before the assistant ever ran; items and every write carry
`assistant` as in the overview plus `actions` (subset of `start`, `stop`,
`restart`, `save`, `send`, `choose`: what the routes accept now, so Quill
Android never guesses) and, while the save-timeout question is open,
`question: {text, choices: [3 labels]}`. `choose` uses `{"choice": n}`;
`queue/{op}` is `queue/delete` or `queue/send-now`. The answer of whichever
app is second is `409 stale`.

These routes work whether or not the kmem window is open: the assistant runs
with the window closed. Items come from the assistant's `deskSession`; the
item tracker (`track`) must keep a tracker for it (today `scan` drops
trackers of hidden ids).

### 4.4 Stream

`/phone/stream` gains one change type, added in `scan`:

```json
{"type": "kmem", "needs": {"count": 7}, "assistant": {"state": "live", "waiting": "plan", "epoch": 1732300, "rev": 1732300000456}}
```

It is emitted when the count, the state, `waiting` or the assistant's rev
changes. Old phones ignore unknown change types (`Remote`'s change switch
drops them in its `default` branch, 0.9.29). The needs count updates on the
existing 30 min timer, after every kmem write from the phone, and when the
kmem window reloads.

### 4.5 Notifications

`alert` for the hidden session also emits a phone event
`{type: "kmem", quill: "", title: "kmem assistant", body}` when the
assistant starts waiting for the user, with the same "not seen" rule as the
desktop notification (an open kmem screen on the phone counts as seen).
**Old phones:** `Notifier.show` indexes `LABELS` by `TYPES.indexOf(type)`,
but `enabled(type)` returns false for types not in `TYPES` first (0.9.29),
so old phones drop the event. New Quill Android adds `kmem` to `TYPES` and
`LABELS` ("kmem assistant needs you").
As built: `body` is `needs approval: …`, `has a question: …`, `has a plan to
approve: …` or `did not save its kmem handoff in time: choose what
happens.` (scrubbed like other kmem text, at most 300 bytes); no `kmem`
event for finished turns or errors.

### 4.6 Task and bin writes (phase 4)

- `POST /phone/kmem/task/{id}/done` `{"confirm": "tc6efac", "result": "…"}`
- `POST /phone/kmem/task/{id}/release` `{"confirm": "tc6efac"}`
- `POST /phone/kmem/bin/{id}/restore` `{"confirm": "f467b16"}`

`confirm` must equal the id (a second guard behind the phone's confirm
dialog). Quill Desktop runs them as `kmem --as quill-phone ...`; the result
records "from Quill Android (<phone name>)". Answers: `{"v": 1, "task": {…}}`
or `{"v": 1, "restored": id}`.

## 5. Security

### 5.1 Same pairing, unlock and control rules

- Every route sits behind the existing phone server: pairing key, revoked
  phones refused, `X-Quill-Unlock` when the phone must unlock.
- Reads need pairing (and unlock) only, like `/phone/sessions`. Every
  `/phone/kmem/*` POST needs control mode (`a.control(phone)`), an
  `Idempotency-Key`, and passes the 10 writes/s limit, like every write
  today.

### 5.2 Nothing destructive without confirmation

- Quill Android asks before Stop assistant, Done, Release and Restore; the
  server needs `confirm` on task and bin writes.
- Release is refused for a live session's claim (rule 19) and Done for a
  task someone else claimed; neither uses `--force`. The server computes
  `actions` (§4.2) from these rules.
- Moving facts or tasks to the recycle bin from the phone: not in this
  design (Open question 8). The existing handoff delete stays.
- **Only the user empties the recycle bin, and only on the PC.** No route
  runs `kmem trash empty`; a test checks that no phone route can reach it.
- `deviceCommandBlocked` applies to assistant `send` as it does today.

### 5.3 Approvals from the phone

- A plan approved on the phone is the user's approval: the answer route
  calls `answerPlanWith` / `answerPlanMsg`, which call `startPlan`; the
  scope rules of `planscope.go` apply unchanged (covered turn, background
  work it starts, its notifications, SendMessage resume).
- A peer (cross-session) or channel message, or a scheduled trigger, never
  approves and ends coverage, whether the plan card is answered on the PC
  or the phone. Quill Android sends approvals only through `answer`
  (approvePlan) or a typed approval through `send`, the same two paths
  Quill Desktop accepts from the user.
- Stop, Don't run, or a denied prompt from the phone call the same
  `revokePlan` path.
- A stale card (already answered on the PC) gets `409 stale`; the phone
  refreshes.

### 5.4 Rate limits

- Writes: the existing 10/s per phone.
- Reads that start a kmem process: one at a time per phone (`409
  kmem_busy`), the overview cached for 10 s (shared by all phones), `find`
  at most 1/s per phone (`429 rate_limited` with `Retry-After`).
- Assistant items use the in-memory tracker (no kmem process).

### 5.5 No personal data in responses

- Text from kmem (brief, facts, task text, bin reasons) has the home
  directory replaced by `~` and the user name by `user` before it leaves
  Quill Desktop (`/home/user/...` style); the maintenance report is sent as
  a file name only.
- No session ids of other Claude sessions, tokens, pairing keys or emails
  in kmem responses; owners are kmem names (`quill`).
- Sizes are capped (64 KiB per response, 32 KiB per fact).
- Tests use placeholder data only (kmem rule 22).

## 6. Update behaviour (rule f79d93a)

- Updates of either app keep all settings silently.
- The kmem screen is a new feature that needs no input: it appears
  silently when Quill Desktop reports `kmem.on` (Open question 10).
- The new notification type is a new feature with a setting: it is asked
  once in the after-update dialog of Quill Android, with the recommended
  choice picked (Open question 5), together with any other question of that
  update. "Later" asks again at the next start and applies nothing.
- If the user changes the default on Quill Desktop's side (for example a
  phone-side kmem toggle in Settings → Phones), it is served through
  `/phone/settings/questions` (kind `new`) and an answer on either side
  counts for both.
- Older Quill Desktop: no `kmem` in hello → no kmem button, no question.
  Older Quill Android: never calls the new routes and never sees the
  hidden session; the new stream change and the phone event are ignored
  (§4.4, §4.5).
- Downgrades never clear answers.

## 7. Phases

Sizes are rough line counts of new or changed code including tests.

| Phase | What | Quill Desktop | Quill Android |
|---|---|---|---|
| 1 (MVP) | Capability in hello; `GET /phone/kmem/overview` and `tasks`; `kmem` stream change; kmem button with bubble, run dot and needs-you mark; Overview and Tasks (read-only, filters) in portrait and landscape | S-M (~350) | M (~600) |
| 2 | Assistant: items, send, answer, interrupt, queue; Start/Stop/Restart/Save now; save countdown and timeout question; tracker for the hidden session; `kmem` phone event + notification | M (~450) | M-L (~700, mostly reusing ChatView) |
| 3 | Memory: topics, find, get, fact detail; Explain/Ask prefill buttons | S (~250) | M (~450) |
| 4 | Task Done/Release, task detail, bin list + Restore, all with confirm | S-M (~300) | S-M (~350) |

Order: each phase lands on Quill Desktop first and Quill Android follows
(t90b538 already waits for Quill Desktop's side). Phase 1 alone already
answers "is the assistant waiting for me?".

## 8. Test plan

### Go harness (Quill Desktop, `cmd/quill`)

- `phoneapi_kmem_test.go`: every route with a fake kmem (the
  `kmemcontract_test.go` pattern): shapes, `"v": 1`, `503
  kmem_unavailable`, `409 kmem_busy`, caching, `429` on find.
- Control off → `403 control_off` on every POST; revoked phone → 403;
  unlock-needed phone without token → refused.
- Hidden session: still absent from `/phone/sessions` and refused on
  `/phone/session/{id}/...`; present on `/phone/kmem/assistant/*`.
- Approvals: phone `approvePlan` sets `runAll` and coverage like the plan
  card; a peer message after a phone approval ends coverage (reuse
  `planscope_test.go` cases through the phone route).
- Stop from the phone runs the save-and-stop flow; `choose` 0/1/2 match
  the desktop dialog; stale states → `409 stale`.
- Task writes: `confirm` mismatch → 400; live claim release → 409; no route
  reaches `trash empty`.
- Redaction: the home directory and user name never appear in any
  `/phone/kmem/*` response (fixture facts contain `/home/user` style
  paths and `user@example.com`).
- Stream: the `kmem` change appears once per change, not on every scan.

### qphone (Quill Android emulator, `scripts/emu-test.sh`)

- `scripts/testdata/phoned.go` serves the new routes with placeholder data.
- qphone (portrait) and qp90 (tablet, landscape): kmem button shown only
  with the capability; bubble count and needs-you mark follow stream
  changes; tabs switch; Overview, Tasks filters, Memory search render;
  assistant plan card approve via PlanOverlay; Stop shows the confirm, the
  countdown and the timeout card; notification opens the Assistant tab.
- Old Quill Desktop (no capability): no kmem button, no crash.
- Unit tests: parsing of overview/tasks JSON, the needs-you state,
  `Notifier` ignoring unknown types.

### Manual check on the phone

1. Pair with Quill Desktop with control on; the kmem button shows the same
   count as the PC's bubble.
2. Ask the assistant on the PC for a plan; close the kmem window; the phone
   notifies, the mark shows; approve on the phone; the PC card closes.
3. Stop the assistant from the phone; watch the countdown; Start it again.
4. Turn control off: buttons disable, reads still work.
5. Rotate the phone; open on the tablet in landscape.

## 9. Open questions (decided)

All 12 decided by the user on 2026-11-22, each with the recommended option
(kmem fact fd77947).

1. Where does kmem open on Quill Android? **Decided:** a kmem button with bubble at the top of the side list opening a full kmem screen.
2. Which task actions may the phone take directly? **Decided:** Done and Release with confirmation, never a live session's claim.
3. What may the phone do with the recycle bin? **Decided:** show the list and Restore with confirmation (emptying stays on Quill Desktop only).
4. How is the assistant session exposed? **Decided:** its own /phone/kmem/assistant routes, still hidden from the session list.
5. When does the phone notify about kmem? **Decided:** only when the assistant waits for the user (plan, question, permission, save timeout).
6. May the phone stop the assistant? **Decided:** Start, Stop, Restart and Save now all on the phone.
7. The save-timeout question (Stop without saving / Keep running until saved / Keep it running): **Decided:** on both apps, first answer wins.
8. How far does the Memory view go? **Decided:** read-only find and get.
9. May the phone change the kmem assistant's model setting? **Decided:** no, Quill Desktop only.
10. Does the kmem screen need a first-run question after the update? **Decided:** no, it appears silently when Quill Desktop supports it.
11. Landscape layout? **Decided:** panels left and the assistant right, like the kmem window.
12. Release order? **Decided:** each phase on Quill Desktop first, Quill Android after.
