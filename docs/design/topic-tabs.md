# Topic tabs and merged coordinator sessions (design)

Status: design, 2026-11-22. Base: master 9c28dd8 (Quill 0.1.25).
Scope: Quill Desktop (tabs, sidebar, start card, kmem window, end/quit flows,
phone API), the phone API changes Quill Android needs, the kmem commands this
assumes (the kmem-side plan is kmem-lock-audit's, who owns kmem development),
the merge procedure for the Zomboid and Quill sessions, and a check of the
shared rules. No code in this document's commit.

Related: `docs/design/kmem-window.md`, kmem tasks t4cb4ee (subagent token
use), t32b878 (pinned facts fill the context cap), tadaf70 (worker tabs),
tf0ac88 (Stop must not kill subagents).

## 1. User decisions this design starts from (2026-11-22)

1. Sessions work as coordinators with subagents ("Coordinate with
   subagents", v0.1.25, `coordinatorHint` in `cmd/quill/memorymode.go`).
   Per-task sessions and their names ("Zomboid Launcher", "Zomboid
   Diagnostic", "Zomboid Networking 2", "Quill 2", "Quill Android",
   "kmem-window-51") no longer fit.
2. **Topic tabs**: one coordinator per topic (Zomboid, Quill, …); tasks are
   done by subagents. kmem is the kmem button and window (assistant
   kmem-lock-audit), never a tab.
3. **Merges**: the three Zomboid sessions merge into one Zomboid coordinator;
   Quill Desktop ("Quill 2") and Quill Android merge into one Quill
   coordinator that gives Android builds and releases to subagents (the
   release flow between them becomes internal). Order is open (recommended:
   Zomboid first). Sessions merge themselves at safe points; Quill 2
   coordinates.
4. **Topics drive kmem context**: a new tab picks a top-level kmem topic or
   names a new one; title and kmem name follow it; every session and subagent
   brief is scoped to the topic first; pinned preferences stay short.
5. **Only active topic tabs** (kmem mode): no Inactive/Archive/"Created by
   Claude" groups; transcripts stay on disk unlisted; external live sessions
   show only with a kmem topic name, under it. Claude-memory mode keeps
   today's sidebar.
6. **No resume, no handoffs for topic state.** A topic's state is
   kmem-native:
   - claimed tasks with checkpoints = work in progress;
   - open tasks = next steps;
   - topic facts = knowledge;
   - one current **status fact** per topic (updated with `--replaces`) =
     the narrative;
   - a generated **topic briefing** (`kmem context --topic <topic>`: open and
     claimed tasks with checkpoints, status fact, top topic facts, short
     pinned prefs) loaded by every fresh topic session and every subagent
     brief;
   - an **end-of-session check** (`kmem end-check`, name TBD, JSON result),
     run by Quill Desktop on end and on quit: checkpoint or release claimed
     tasks, note uncommitted files and running jobs, release the pzlock turn;
     it refuses until done;
   - existing handoffs are converted once, then retired as history;
   - rule 24 reworded (the user decides): "Before a session ends or is
     cleared, run the end-of-session check."
   Opening a topic always starts a FRESH session; one live session per topic.

## 2. What exists today (code facts, 9c28dd8)

- **Identity.** A tab is a Claude session id. Title from `rowText`
  (`cmd/quill/desk.go`): `org.Titles[id]`, else transcript title, else "New
  session". kmem name = `org.Kmem[id]` (`sideOrg`, `cmd/quill/org.go`).
- **Start card.** `kmemCard` (`cmd/quill/kmemcard.go`): "Resume a kmem
  handoff, or name a new one"; `pickResume`/`pickNew`; `renameKmem` runs
  `kmem handoff rename`. Resume prompt: `resumePrompt` (`cmd/quill/kmem.go`,
  `kmem-resume NAME --print` or a built-in "run `kmem brief` and `kmem
  handoff get NAME`"), sent by `sendResume`.
- **Save machinery** (`kmem.go`): `kmemSave{name, asked, spare, prompt, end,
  place, keep}`; `endAndSave`/`endAndSaveTo` send `kmemEndMessage` ("run
  `kmem handoff save NAME`…"); `relaunchViaKmem` sends `kmemSaveMessage`;
  `saveHandoffNow` (/export "Save as kmem handoff", `keep`);
  `kmemAfterTurn` checks after each turn whether `kmem handoff list` shows a
  save newer than `asked` (or a recent save by this session id), then
  `finishKmemEnd` (stop, place, close tab) or `finishKmemRelaunch` (fresh
  session + `sendResume`, delete old). `kmemIntro` tells a new session
  "Quill asks you to run `kmem handoff save`".
- **Plan-first gates** (`kmem.go`): `kmemSaveAllowed` (while `kmemSave` is
  set: read-only commands + `kmem …` + `pzlock status`, `kmemSaveExtra`),
  `kmemReleaseAllowed` (`kmem task release ID` after a rewind),
  `kmemResumeAllowed` / `firstTurnKmemName` (startup commands with
  `--as NAME`).
- **Quit** (`confirmQuit`, `quitMessage` in `desk.go`): only the hidden kmem
  assistant saves (`kmemQuitFlow`); visible sessions just stop ("can be
  resumed").
- **Auto-handoff** (`cmd/quill/autohandoff.go`): policy from `kmem handoff
  policy` (now soft=50 hard=75 mode=auto); at hard + `safePoint` →
  `relaunchViaKmem`; `safePoint` needs `conv.Background() == 0`, which a
  coordinator with running subagents rarely reaches; cold rotation after the
  cache TTL (`scheduleCold`).
- **Sidebar.** Since 135bd4b `rebuildRows` lists only live sessions
  (`liveSession`, `cmd/quill/projects.go`) grouped by project
  (`projectGroups`); `d.groups` feed folder chips and the phone API; no
  memory-mode branch.
- **Landscape tabs**: `d.tabs`, `syncTabs`, `tabMenu` (`cmd/quill/tabs.go`),
  `tabstrip.go`; `applyLayoutMode` (`layoutmode.go`); portrait caption
  `rowCaption` (`sidecaption.go`).
- **Subagents**: `BgTask`, `Background()`, `NoteNotifications`
  (`internal/chat/chat.go`); `agentGrants`/`trackAgentCalls` (`desk.go`);
  no per-agent tokens (t4cb4ee).
- **kmem window** Handoffs panel (`cmd/quill/kmempanel.go`): Show / Resume
  (Open when live) / Delete (trash).
- **Leftovers of the old groups**: `sideOrg.Place` `inactive`/`archive`/
  `auto`, `Folders`, `placement`/`setPlacement`/`autoPlace`/`dropOn`
  (`org.go`), `projectRows`; places written by End (`identity.go`, session
  menu, `askEndProject`, `phonemenu.go`), `endAndSave` → Archive, relaunch →
  Archive (`relaunch.go`), `kmemassist.go`; `rules.go` sorting dialog and
  `rulePlace`/`stickyPlace`/`fixedActive`/`resortNow`, referenced only inside
  `rules.go`; `quill --resume-all [archive]` (`main.go`, `background.go`).
- **Phone API** (`phoneapi.go`, `phonehandoffs.go`, `phonewrite.go`):
  `GET /phone/sessions`, `GET /phone/folders` (`knownFolders`: cwd of live
  and past sessions), `GET /phone/handoffs` (`apiHandoff{name, savedMs,
  openIn}`), `GET /phone/handoffs/slug`, write call `start: apiStart{kmem,
  name, title}`.
- **kmem 1.8.1**: `kmem context "TASK" --topic T[,T]` only boosts those
  topics (×2.5); pinned facts global and first; 3 facts; tasks matched by
  words. No end-check; `kmem handoff save` does the safe-point checks
  (claims, pzlock turn, own background jobs, own uncommitted files).

## 3. Quill Desktop design

### 3.1 Topic model

A **topic** is a top-level kmem topic slug (`zomboid`, `quill`). One topic =
at most one live coordinator tab; the session's kmem name (`--as`) is the
topic. Its **scope** is the top-level kmem topics its briefing draws from:

| Topic   | Title   | Scope (top-level kmem topics today)                                                   |
|---------|---------|---------------------------------------------------------------------------------------|
| zomboid | Zomboid | zomboid, load-opt, launcher, network, chunkstream, server, client-pack, memtest, todo |
| quill   | Quill   | quill                                                                                 |
| kmem    | kmem    | kmem (kmem window assistant; not a tab)                                               |

kmem topics are never merged or renamed (rule 29); the scope lists them.
Where the scope and display title live is kmem's call (assumed: config
`sessions[TOPIC]` with `as` and `scope`, readable by `kmem context --topic`
and Quill; Quill keeps only `org.Kmem[id] = TOPIC`). Status fact location
assumed `TOPIC/status` (one current fact).

### 3.2 Opening a topic; one live session per topic

`kmemCard` becomes **"Open a topic"** (no resume list, no free name):

- Buttons: topics with a scope label first (title + first line of the status
  fact + "N claimed · M open tasks"), then other top-level kmem topics from
  `kmem topics`, then "New topic…" (free text → `kmemSlug`).
- Opening always starts a **fresh** session. New first prompt (replaces
  `resumePrompt`/`kmemIntro`): "You are the <Title> coordinator; kmem name
  <topic> (`kmem --as <topic>`). Run `kmem brief` and `kmem context --topic
  <topic>`; continue the claimed tasks from their checkpoints; keep
  `<topic>/status` current with `--replaces`; brief subagents with `kmem
  context "<task>" --topic <topic>`." Sent through `sendResume` so the
  plan-first startup exception (`kmemResumeAllowed`) still applies.
- **One live per topic**: if the topic is live (a tab, the kmem window, or an
  external live session with that name, K6) the button reads "open in tab
  Zomboid" and switches. The same check guards every session-creating path
  that sets a kmem name (phone open-topic, `--resume-all`, rotation,
  relaunch); rotation ends the old session (end-check passed) before the
  fresh one starts.
- No `claude --resume` of old transcripts from kmem mode; "Open old
  conversation" (`identity.go`) goes.

### 3.3 Titles and old names

Title = topic title; `rowText` drops "· kmem NAME" when NAME is the topic;
old tabs read "Zomboid Launcher → merging into Zomboid" until they end.
`tabMenu`: no Rename; "Switch topic…" = end-check, end, open the other topic.

### 3.4 Subagent strip

Thin row under the tab header (both layouts), hidden when nothing runs:
`● Fix join stall (Explore) · 41k tok · 6 min   ● ELTF build · 2 min   [+3]`.
Sources: `conv.BackgroundTasks()`, the Agent tool_use tracked by
`trackAgentCalls`, per-agent tokens from task notifications / stream usage
(t4cb4ee: `AgentUse{ID, Name, Type, Tokens, Started, Done}` in
`internal/chat`). Click: dropdown with task, tokens, log, worktree, "Stop
this subagent" (TaskStop only, tf0ac88). Finished ones stay greyed 10 min;
`statsLine` adds "subagents Nk"; tab chip gets a count badge.

### 3.5 Sidebar, tabs, kmem window

- Landscape: one chip per live topic; folder chips row hidden in kmem mode.
- Portrait: one row per live topic.
- **kmem window: Handoffs tab → "Topics" tab, read-only.** Per topic: title,
  live in tab / not live, status fact (with age), claimed tasks with owner
  and last checkpoint, open tasks count, scope; buttons **Show briefing**
  (`kmem context --topic T`) and **Open topic** (§3.2). Under each topic a
  collapsed **History**: the converted, retired handoffs (Show only). Old
  handoffs not yet converted: "Unconverted handoffs" (Show only) until the
  merge converts them. No Resume, no Delete. Data: `kmem task list --json`,
  `kmem get T/status`, labels, `kmem handoff list --json` for history.

### 3.6 Existing tabs and sessions

No automatic merge. Old tabs keep running until their session merges (§5),
then end. One-time notice per old non-topic tab: "This session predates
topic tabs. Merge it into a topic at a safe point."

### 3.7 Only active topic tabs (kmem mode)

1. Tabs and session list show only live topic coordinators; non-topic live
   sessions (old names until merged, scratch) under their project with the
   merge notice (Q14).
2. Remove the places: End = end-check + stop + close tab; no place written.
   Delete in kmem mode the `inactive`/`archive`/`auto` branches in `org.go`,
   `identity.go`, `kmem.go`, `relaunch.go`, `kmemassist.go`,
   `phonemenu.go`, the dead parts of `rules.go`, `projectRows`, `Folders`
   UI. `--resume-all` → "reopen the topics that were live at quit" as fresh
   sessions (Q16).
3. Transcripts stay on disk unlisted; `d.order`/`d.infos` still scanned for
   usage. Optional (Q17) "Find old conversation" in the kmem window: search
   titles/first prompts (full text on demand, off the UI thread); "View
   read-only" and "Copy as kmem fact…"; no continue.
4. External live sessions (`d.terms`, `internal/live`) appear only with a
   kmem topic name, as that topic's live session ("live in a terminal",
   Take over via `takeOver`). Needs K6.
5. Claude-memory mode (`kmemMemory()` false): unchanged (today: live
   sessions by project; `rebuildRows` has no mode branch). Q18: bring back
   history groups there, in which case the places code moves behind
   `!kmemMemory()` instead of being deleted.

### 3.8 End-of-session check (replaces the handoff save flows)

Assumed command (kmem-lock-audit decides the final name and schema):

```
kmem end-check --as <topic> [--session <claude session id>] --json
exit 0 = ok, 1 = blockers, 2 = kmem error
{"ok": false, "name": "zomboid",
 "blockers": [
   {"kind": "claim", "id": "t5d321c", "need": "checkpoint or release"},
   {"kind": "lock",  "name": "zomboid:eltf-ship", "need": "release"},
   {"kind": "job",   "pid": 1234, "what": "…", "need": "end or note"},
   {"kind": "files", "repo": "Projects/zomboid-eltf", "paths": ["…"], "need": "commit or note"},
   {"kind": "status", "need": "update <topic>/status"}],
 "notes": ["…"]}
```

Quill flow, one helper `endCheck(s, why)` used by End, Switch topic,
rotation, Quit and /clear:

1. Quill runs `kmem end-check --json` itself (off the UI thread).
2. ok → finish (stop, close tab; rotation: fresh session from §3.2).
3. blockers → Quill sends the session one message listing them ("Before
   this session ends: checkpoint or release t5d321c; release your pzlock
   turn after the logs are read; note or end job …; then reply with one
   line"), sets `s.endWait` (replaces `kmemSave`), and re-runs the check
   after each finished turn (replaces `kmemAfterTurn`'s handoff-time test).
   `spare` turns as today; after them: "the end was cancelled; nothing was
   stopped" (existing wording).
4. Quill never resolves blockers itself (rule 2: only the session releases
   its pzlock turn; claims are the session's).

### 3.9 What changes in Quill Desktop's kmem code

| Today | Change |
|-------|--------|
| `kmemSave` struct, `kmemSaveMessage`, `kmemEndMessage`, `saveHandoffNow` | `endWait{why, asked, spare, keep}`; `endCheckMessage(blockers)`; /export "Save as kmem handoff" → **"Checkpoint now"**: ask the session to checkpoint claims and update the status fact, then run end-check in report-only mode (session goes on) |
| `endAndSave`, `endAndSaveTo(id, place)`, `finishKmemEnd` | `endTopic(id)`: end-check flow, then stop + `markEnded` + close tab; no place; hidden assistant path unchanged apart from the check |
| `kmemAfterTurn` (handoff list time compare, `kmemHandoffText` session-id fallback) | re-run end-check after each turn while `endWait` is set; no handoff reads |
| `relaunchViaKmem`, `finishKmemRelaunch`, `resumePrompt`, `kmemIntro`, `sendResume` | `rotateTopic(id)`: end-check, then fresh session with the §3.2 prompt via `sendResume`; old session deleted as today; `kmem-resume` no longer used |
| `kmemSaveAllowed` + `kmemSaveExtra` (`kmem`, `pzlock status`) | `endCheckAllowed`: same read-only set plus `kmem task checkpoint/release/done` for the listed claim ids, `pzlock release <its lock name>` and `git commit` limited to listed paths only if the user allows it (Q24) |
| `kmemReleaseAllowed` | unchanged |
| `kmemResumeAllowed`, `firstTurnKmemName` | unchanged; the allowed startup set becomes `kmem brief`, `kmem context --topic <topic>`, `kmem task list` (no `handoff get`) |
| `checkAutoHandoff`, `safePoint`, `scheduleCold`, `handoffPolicy` | same thresholds (policy read from kmem; name may move from `kmem handoff policy` to a kmem-native one); hard threshold → `rotateTopic`; coordinator safe point = own turn idle, nothing pending, no subagent running (else wait, Q19) |
| `confirmQuit`/`quitMessage` | run the end-check flow for every visible topic session in parallel (60 s cap each, like `kmemQuitFlow`); text "each topic runs its end-of-session check first"; a topic that cannot pass is listed and needs a second confirm (Q22) |
| `kmemCard`, `pickResume`, `pickNew`, `renameKmem`, `kmemChoices`, `applyKmemChoice`, `askKmemName` | "Open a topic" card (§3.2); no handoff rename from Quill |
| `kmempanel.go` Handoffs tab, `trashHandoffNamed` | read-only Topics tab (§3.5); no Delete |
| `autohandoff.go` messages naming handoffs | "end-of-session check" wording |

### 3.10 Topic briefing for subagents

Quill injects into the first prompt and `coordinatorHint`: "brief every
subagent with `kmem context "<task>" --topic <topic>`" (rule 23 marker
stays). Pinned user preferences are short single-line facts; the pin share is
capped (t32b878).

### 3.11 Phone API and Quill Android

- `GET /phone/sessions`: kmem mode returns only live topic sessions (+
  non-topic live ones until merged); `group` = topic title. New per-session
  fields (omitempty): `topic`, `topicTitle`, `legacyName`, `agents[]` {id,
  name, type, tokens, startedMs, done}, `agentTokens`, `endWait` (end-check
  blockers while ending). Body gains `topics[]` {slug, title, scope, live
  (session id or ""), status, statusMs, claimed, open}.
- `GET /phone/handoffs`: kept for older phones but read-only and empty of
  resumable entries in kmem mode; new `GET /phone/topics` = the Topics view
  (status, claimed tasks with checkpoints, open count, history of converted
  handoffs). `GET /phone/handoffs/slug` → `GET /phone/topics/slug` for "New
  topic" previews.
- New-session `start` (`phonewrite.go` `apiStart`): replaced by
  `openTopic: {topic}` or `openTopic: {new: "Title"}`; Quill applies §3.2
  (switch if live, else fresh). `start.kmem`/`name` refused in kmem mode
  with "update Quill Android".
- `GET /phone/folders`: kmem mode lists the topic's working folders
  (`org.Dirs` by project + the coordinator's cwd).
- **Quill Android changes**: session list = one row per live topic with the
  subagent strip collapsed ("3 subagents · 120k tok"); "New session" sheet
  = topic picker from `topics[]`; remove Resume handoff and free name; read
  Topics screen (status, tasks, history; no Delete); send `openTopic`; show
  `legacyName` during merges and `endWait` blockers while ending. Done by an
  Android subagent after the desktop API merges.

### 3.12 Migration of existing org data (one-time, kmem mode)

Preference `topicMigration=1`, idempotent; first copy `sidebarOrg` and `tabs`
to `sidebarOrgPreTopics`/`tabsPreTopics`. Then: `org.Place` all dropped;
`org.Titles` kept for live ids; `org.Kmem` kept (maps transcripts to old
names for search); `org.Folders` dropped; `org.Projects`/`org.Dirs` re-keyed
to topic slugs; `org.Ended`/`Hidden`/`RC` kept; `d.tabs` of ended sessions
removed. Archived/inactive transcripts without a kmem handoff: unlisted,
only searchable (Q20). One notice: "Sessions are now topic tabs; the state
of each topic is in kmem → Topics; N older conversations stay on disk."

## 4. kmem commands this assumes (kmem-lock-audit plans the kmem side)

- **K1 topic labels**: title and scope per topic (`kmem session set …` or
  equivalent), readable as JSON.
- **K2 topic briefing**: `kmem context [TASK] --topic T --json?`: claimed
  tasks with last checkpoint, open tasks, `T/status`, top facts from the
  scope, short pins. **Breaks**: today `--topic` is a boost on `context` and
  `recall`; callers (briefs in all sessions) change meaning; needs a version
  check in Quill (`kmem version --json`).
- **K3 pin share** (t32b878).
- **K4 end-check** with JSON (§3.8); covers what `handoff save` checks today
  plus the status fact.
- **K5 handoff conversion**: `kmem handoff convert NAME --topic T` (state
  lines → checkpoints / tasks / facts / status proposal; pointers kept as a
  history fact), then retire as history; `handoff list --json` marks
  converted ones.
- **K6 live session names**: `kmem sessions --json` (Claude session id →
  last `--as` name) for one-live-per-topic and external sessions.
- **Policy**: auto-rotation thresholds keep living in kmem (renamed or not).

Until K2/K4 ship, Quill keeps the current handoff flows (feature switch on
`kmem version`).

## 5. Merge procedure (for the sessions and the user; not run from this doc)

### 5.1 Current holdings (read 2026-11-22 ~16:00, read-only)

pzlock: holder free, queue empty, game not running (daemon up). Last entries
used batch names (csb-join3, eltf139-ship, …), all released.

| Session (kmem name) | Handoff (saved) | Claims | Lock | Jobs / notes |
|---|---|---|---|---|
| Zomboid Launcher (`zomboid-launcher`, was `launcher`) | 13 items, 04:23 | none | none | none; master dc9bae7 unreleased; td47d24 open, unclaimed |
| Zomboid Diagnostic (`zomboid-diagnostic`) | 7 items, 11:20 | **t5d321c** (MALLOC check, checkpoint) | none | **systemd user job `pzcapture-malloc-check` running, no time limit** |
| Zomboid Networking 2 (`networking`) | 8 items, 02:56 (+ f091025) | none | none | none; tb618e9 JOIN STALL open, unclaimed |
| Quill 2 (`quill`) | 7 items, 14:22 | **tf0ac88** | none | worktrees under Projects/quill-wt |
| Quill Android (`quill-android-2`) | 4 items, 15:30 | none | none | waits for Quill 2's "release prep" 0.1.25 |

Handoffs to convert and retire:

| Handoff | Target topic |
|---|---|
| zomboid-diagnostic, zomboid-launcher, networking, fps-ab | zomboid |
| quill, quill-android-2 | quill |
| kmem-lock-audit, new-session-6 (kmem work) | kmem (Q11) |
| the two phone-test handoffs and the Tailscale device-setup handoff | none: retire as history (Q12) |

### 5.2 Safe point (each old session)

1. No pzlock turn held or queued under any name it used.
2. No game it launched running; last test's logs read or copied.
3. Background jobs ended, or written down (a job that outlives the session,
   `pzcapture-malloc-check`, becomes a checkpoint on t5d321c with how to
   collect it).
4. No subagent of its own running.
5. Own uncommitted files in shared repos committed or noted (rule 14).

### 5.3 Converting into the topic (kmem-native)

Each old session at its safe point:

1. Claims: `kmem task checkpoint ID "<state>"`, then `kmem task transfer ID
   zomboid` (t5d321c by Diagnostic; tf0ac88 stays with Quill 2 until the
   Quill merge).
2. Open threads in its handoff state → tasks under the topic (`kmem task add
   … --topic zomboid`) or facts in the scope (`kmem add`), one per line;
   "waiting for the user" items become tasks with that wording.
3. Its narrative → one line for the status fact. The **last** session
   (recommended: Zomboid Diagnostic) writes `zomboid/status` combining all
   lines (`--new` the first time, later `--replaces`).
4. End-check passes (K4; on 1.8.1 the equivalent is `kmem handoff save
   <old> --state "converted into zomboid"`, which runs the same checks).
5. Its handoff is converted/retired as history (K5, or `kmem retire` of its
   state facts with "converted into zomboid tasks/status"; nothing trashed).
6. Announce (rule 17), stop taking work, tell the user "ready".

Then the user opens topic **Zomboid** (§3.2): the fresh coordinator runs
`kmem brief` + `kmem context --topic zomboid`, claims the transferred tasks
`--as zomboid`, checks `systemctl --user status pzcapture-malloc-check`.
The user closes the old tabs.

**Quill**: Quill Android converts `quill-android-2` at a safe point (no
build/release running; the pending "release prep 0.1.25" finished or a
task); Quill 2 converts `quill` and writes `quill/status`; release decision
f5767e4 (desktop is the base, Android ports it) becomes an internal subagent
order (desktop release subagent, then an Android build subagent with the
memory caps). Quill 2 ends; the user opens topic Quill fresh.

### 5.4 Announcements (rule 17)

Each old session: "<old name> merging into Zomboid; lock entries: none;
claims transferred: …". New coordinator: "Zomboid (topic coordinator) is
live; replaces Zomboid Launcher, Zomboid Diagnostic, Zomboid Networking 2."
An old session holding or queuing a lock entry does not merge until it is
released under the old name.

## 6. pzlock and rules check

One coordinator holding the turn for its subagents' tests fits rule 5 and
rule 1 if the coordinator acquires before the test subagent starts and
releases after the subagent reports logs copied. Gaps:

1. Rule 1: "the session holding the turn launches"; a subagent launching
   inside its parent's turn is not covered.
2. 8e2: the subagent needs the parent's `PZLOCK_NAME`.
3. 8e, 9, 11, 14, 15 assume several Zomboid sessions; parallel subagents of
   one coordinator are the new "others".
4. tf0ac88: a Stop can kill a test subagent with the game up; the parent
   closes the game (rule 7), copies logs, releases.
5. Lock names are batch names; rule 2 says `<session>`.
6. One locked batch at a time per coordinator.
7. Rule 24 and the handoff wording in rule 19 details assume handoffs.

Proposed text (the user decides):

- **5.** Subagents never acquire or release; their parent session runs
  pzlock. A parent may let one subagent launch and read logs inside the
  parent's turn: it acquires for that batch, passes `PZLOCK_NAME`, and
  releases after the subagent reports the logs copied; if the subagent dies,
  the parent closes the game (rule 7) and releases.
- **2** (name clause): `acquire "<session>[:<batch>]" …`; topic
  coordinators use their topic name (`zomboid:eltf-ship`).
- **8e** (addition): a coordinator runs at most one locked batch at a time
  and queues its own subagents' tests.
- **14** (addition): parallel subagents of one session count as others: each
  gets its own worktree or listed paths; the parent commits only those.
- **17** (addition): sessions merging into a topic coordinator first reach a
  safe point (no lock entry, game closed, logs read, claims checkpointed and
  transferred) and the coordinator announces the old names it replaces.
- **24** (user's wording): "Before a session ends or is cleared, run the
  end-of-session check." Optional second sentence: "A new session starts
  with `kmem brief` and `kmem context --topic <topic>`."

Rules 1, 3, 4, 6, 7, 8, 8b–8d, 8e2, 8f need no change (8e2 is covered by the
new rule 5). Rule 19's one-liner stays; its details mention handoffs and
change with K4/K5 (kmem-lock-audit).

## 7. Risks

- **Coordinator context over several repos**: decisions and results only;
  per-repo work to subagents with topic briefings; rotation keeps it small.
- **Lost work without resume**: only what is not in tasks/checkpoints/status
  is lost; mitigated by end-check on End/Quit/rotation, "Checkpoint now",
  and a nudge when `<topic>/status` is older than 60 min of activity.
  A crash loses work since the last checkpoint; transcripts stay on disk.
- **Status fact drift**: one fact rewritten by several subagents; only the
  coordinator writes it (hint text).
- **Mid-test merge**: excluded by the safe point.
- **Lost claims**: checkpoint + transfer before ending; live claims never
  taken (rule 19).
- **pzcapture job** outlives sessions: must be in t5d321c's checkpoint.
- **Duplicate coordinators**: one-live-per-topic check on every path;
  external sessions need K6.
- **`--topic` meaning change** in kmem: old briefs silently change; Quill
  version-gates.
- **Rotation blocked by subagents**.
- **Older Quill Android** using `start.kmem`: refused with an update message.

## 8. Phased plan (subagent-sized, file ownership)

Phase 0: user decisions (§9), rule text (§6).
Phase 1: kmem side (kmem-lock-audit): K2, K4 first (they gate the no-handoff
model), then K1, K3, K5, K6.
Phase 2: Zomboid merge (§5); on kmem 1.8.1 it works with handoff save as
the check and retire per fact.
Phase 3: Quill Desktop, one subagent each, own worktree:

| Sub | Work | Files owned |
|---|---|---|
| A | Topic model, "Open a topic" card, one-live-per-topic, fresh prompt | `kmemcard.go`, `projects.go`, new `topics.go` |
| B | Subagent strip + per-agent tokens (t4cb4ee) | `internal/chat` (agent usage), new `agentstrip.go`, `buildMain` hook in `desk.go` |
| C | End-check flow replacing handoff saves (§3.8–3.9), Quit, rotation | `kmem.go`, `autohandoff.go`, `identity.go`, `desk.go` (`confirmQuit`, `quitMessage`) |
| D | Read-only Topics tab | `kmempanel.go` |
| E | Titles, old names, tab menu (after A) | `desk.go` `rowText`, `tabs.go`, `sidecaption.go` |
| F | Remove places + org migration (after A, C) | `org.go`, `relaunch.go`, `kmemassist.go`, `phonemenu.go`, `rules.go`, `main.go`, `background.go`, new `topicmigrate.go` |
| G | Phone API: sessions/topics/folders/openTopic (after A, B, C) | `phoneapi.go`, `phonehandoffs.go`, `phonewrite.go`, API doc |
| H | Optional Find old conversation (after D) | new `kmemfind.go`, hook in `kmempanel.go` |
| I | External live sessions under their topic (after A, K6) | `projects.go` (`liveSession`, `projectGroups`), `sidebar.go` |
| J | Hint texts (scoped briefs, status fact, checkpoints) | `memorymode.go` |

A, B, D, J in parallel; C after K4 is specified; E, F after A (F after C);
G after A, B, C; H after D; I after K6. Coordinator merges; full suite once
at merge.

Phase 4: Quill merge (§5.3) after Zomboid proves out (Q1); then the Quill
Android changes (§3.11) by an Android subagent.

## 9. Decisions (user 2026-11-22: the recommended option for all 25)

All 25 questions below were answered with their recommended option. The four rule changes (5, 8e/14, 17, 24) go into the rules file through kmem-lock-audit, which owns its structure.


1. Merge order: Zomboid first, Quill after it proves out (recommended) / Quill first / both together.
2. Zomboid merge timing: convert now on kmem 1.8.1 (handoff save as the check, retire per fact) (recommended) / wait for end-check and handoff convert in kmem.
3. Who writes the first zomboid/status fact: Zomboid Diagnostic as the last session (recommended) / Zomboid Launcher / the new coordinator from the converted tasks.
4. Zomboid scope: zomboid, load-opt, launcher, network, chunkstream, server, client-pack, memtest, todo (recommended) / zomboid only / let me edit the list.
5. Rule 5 change (subagent may launch inside the parent's turn; parent acquires and releases): adopt the proposed text (recommended) / keep rule 5 and have the coordinator launch itself / other wording.
6. Lock names for coordinators: topic:batch, e.g. zomboid:eltf-ship (recommended) / topic only / keep free batch names.
7. Rule 8e and 14 additions (one locked batch per coordinator; parallel subagents count as others): adopt both (recommended) / 8e only / neither.
8. Rule 17 addition (merge only at a safe point): adopt (recommended) / not needed.
9. Rule 24: your wording plus "A new session starts with kmem brief and kmem context --topic <topic>" (recommended) / your wording only.
10. Status fact required by the end-check (updated during the session): yes, as a blocker (recommended) / a warning only / not checked.
11. kmem topic name for the kmem assistant: keep kmem-lock-audit as its kmem name, topic kmem (recommended) / rename it to kmem.
12. Old handoffs with no topic (two phone tests, Tailscale setup): retire as history (recommended) / list under Unconverted / assign them to a topic.
13. Subagent strip: thin row under the tab header in both layouts (recommended) / tab chip badge only / Settings only.
14. Non-topic live sessions (old names until merged, scratch): list under their project with a merge notice (recommended) / hide them / separate "Other" group.
15. Places code (Inactive/Archive/Created by Claude) in kmem mode: delete it after a one-time backup of the old org data (recommended) / keep it behind Claude-memory mode only / keep everything.
16. quill --resume-all: reopen the topics that were live at quit as fresh sessions (recommended) / remove the flag.
17. "Find old conversation" (view read-only, copy to kmem): build it after topic tabs ship (recommended) / with them / not needed.
18. Claude-memory mode sidebar: keep today's live sessions by project (recommended) / bring back Inactive/Archive history groups there.
19. Rotation while subagents run: wait for them to finish, then rotate (recommended) / ask me / rotate and record them in checkpoints.
20. Archived/inactive transcripts without a kmem handoff: leave on disk, reachable only by search (recommended) / one read-only archive fact per project / show once for review before hiding.
21. External live sessions with a topic name: show as the topic's live session with Take over (recommended) / separate rows / do not show.
22. Quit with a topic whose end-check does not pass: list it and ask for a second confirm (recommended) / block quit until it passes / quit without asking.
23. Quill Android: one row per topic, topic picker, read-only Topics screen, openTopic (recommended) / keep the current session list until a later release.
24. Plan-first during the end-check: allow checkpoint/release of the listed claims and pzlock release without approval, commits still need approval (recommended) / allow listed-path commits too / everything needs approval.
25. Older Quill Android sending a resume start in kmem mode: refuse with an update message (recommended) / map it to open topic by the handoff's topic.
