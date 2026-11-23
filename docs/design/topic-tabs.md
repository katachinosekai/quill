# Topic tabs and merged coordinator sessions (design)

Status: design, 2026-11-22. Base: master 9c28dd8 (Quill 0.1.25).
Scope: Quill Desktop (tabs, sidebar, start card, kmem window, phone API), the
phone API changes Quill Android needs, the kmem changes this depends on
(proposals for kmem-lock-audit, which owns kmem development), the merge
procedure for the Zomboid and Quill sessions, and a check of the shared rules.
No code in this document's commit.

Related: `docs/design/kmem-window.md` (hidden assistant session, Handoffs
panel), kmem tasks t4cb4ee (subagent token use), t32b878 (pinned facts fill the
context cap), tadaf70 (worker tabs), tf0ac88 (Stop must not kill subagents).

## 1. User decisions this design starts from (2026-11-22)

1. Sessions work as coordinators with subagents (Settings → General
   "Coordinate with subagents", v0.1.25, `coordinatorHint` in
   `cmd/quill/memorymode.go`). Per-task sessions and their names ("Zomboid
   Launcher", "Zomboid Diagnostic", "Zomboid Networking 2", "Quill 2",
   "Quill Android", "kmem-window-51") no longer fit.
2. **Topic tabs**: one coordinator session per topic (Zomboid, Quill, …);
   tasks are done by subagents. kmem is the kmem button and window (its
   assistant kmem-lock-audit), never a tab.
3. **Merges**: the three Zomboid sessions merge into one Zomboid coordinator;
   Quill Desktop ("Quill 2") and Quill Android merge into one Quill
   coordinator that gives Android builds and releases to subagents (the
   release flow between the two sessions becomes internal). Order between the
   two merges is open (recommended: Zomboid first). The sessions merge
   themselves at safe points; Quill 2 coordinates.
4. **Topics drive kmem context**: a new tab picks a topic from the existing
   top-level kmem topics or names a new one; tab title, kmem name and handoff
   follow it; `kmem context` for the session and for every subagent brief is
   scoped to the topic first; pinned preferences stay short.
5. **Only active topic tabs** (kmem mode): tabs and the session list show
   only live topic tabs; the Inactive, Archive and "Created by Claude" groups
   go; raw Claude transcripts stay on disk but are not listed; live sessions
   started outside Quill Desktop appear only with a kmem topic name, under
   that topic. Claude-memory mode keeps today's sidebar.
6. **No resume**: a topic has ONE state: the topic's kmem handoff (name =
   topic, e.g. `quill`, `zomboid`) plus `kmem context` scoped to the topic.
   Opening a topic tab always starts a FRESH session that loads
   `kmem handoff get <topic>` and the scoped context; no `claude --resume`
   of old transcripts, no resume list. Old per-session handoffs merge once
   into their topic's handoff and then retire as history with kmem's own
   commands (nothing deleted). The kmem window's Handoffs tab becomes a
   read-only view of topic handoffs and their history.
7. Because nothing resumes, **save discipline** matters (§3.8), there is
   **one live session per topic** (§3.2), and the phone's "start" becomes
   **"open topic"** (§3.10).

## 2. What exists today (code facts, 9c28dd8)

- **Identity.** A tab is a Claude session id. Title from `rowText`
  (`cmd/quill/desk.go`): `org.Titles[id]` (rename, or the kmem name copied by
  `syncTitleToKmem` in `cmd/quill/kmemcard.go`), else transcript title, else
  "New session". kmem name = `org.Kmem[id]` (`sideOrg`, `cmd/quill/org.go`,
  preference `sidebarOrg`).
- **Start card.** `kmemCard` (`kmemcard.go`): "Resume a kmem handoff, or name
  a new one"; free name → `kmemSlug`; `renameKmem` runs `kmem handoff rename`.
  Resume of a handoff = fresh session primed from it (`pickResume`,
  `identity.go` "Resume from the handoff … Open old conversation continues
  the old one").
- **Projects.** `projectOf` (`cmd/quill/projects.go`): `org.Projects`, else
  kmem config `sessions[NAME].project` / first word of `.topic`
  (`kmemSessions`), else first word of the title.
- **Sidebar.** Since 135bd4b `rebuildRows` (`desk.go`) lists only live
  sessions (`liveSession`, `projects.go`: open non-stale tab, or running
  non-terminal session) grouped by project (`projectGroups`); `d.groups`
  feed the folder chips (`syncBookmarks`, `cmd/quill/tabs.go`) and the phone
  API. The memory mode does not change this.
- **Landscape tabs.** `d.tabs` (preference `tabs`, `maxTabs` 12),
  chips in `syncTabs` (`tabs.go`), drag in `tabstrip.go`; `applyLayoutMode`
  (`layoutmode.go`) switches tab row vs sidebar; portrait token caption
  `rowCaption` (`sidecaption.go`).
- **Subagents.** `BgTask{ID, Tool, What, Log}`, `Background()`, `Working()`,
  `NoteNotifications` (`internal/chat/chat.go`); plan subagents tracked by
  tool_use_id/agent id (`agentGrants`, `trackAgentCalls`, `desk.go`). No
  per-subagent tokens (t4cb4ee).
- **Hidden session.** kmem assistant: `deskSession.hidden`, `org.Hidden`;
  `addTab`, `syncTabs`, `phoneAPI.sessionIDs` skip it.
- **kmem window.** Handoffs panel (`cmd/quill/kmempanel.go`): flat list with
  Show / Resume (or Open when live) / Delete (trash).
- **Saving.** `endWithSave` / `endAndSaveTo` (`identity.go`, `kmem.go`) save
  a handoff on End. **Quit does not**: `confirmQuit`/`quitMessage`
  (`desk.go`) only lets the hidden kmem assistant save ("conversations are
  saved and can be resumed"). Auto-handoff (`autohandoff.go`, policy
  soft=50 hard=75 mode=auto) rotates via `relaunchViaKmem` only at
  `safePoint`, which requires `conv.Background() == 0`: a coordinator with
  running subagents never reaches it.
- **Leftovers of the old groups.** `sideOrg.Place` values
  `inactive`/`archive`/`auto`, `Folders`, `placement`/`setPlacement`/
  `autoPlace`/`dropOn` (`org.go`), `projectRows`; places still written by
  End (`identity.go`, `org.go` menu, `askEndProject`, `phonemenu.go`),
  `endAndSaveTo(…, placeArchive)` (`kmem.go`), relaunch → Archive
  (`relaunch.go`), `kmemassist.go`; `rules.go` sorting dialog ("Put sessions
  started by tools in Created by Claude", Active/Inactive/Archive) and
  `rulePlace`/`stickyPlace`/`fixedActive`/`resortNow`, referenced only
  inside `rules.go`; `quill --resume-all [archive]` (`main.go`,
  `background.go`); texts naming Inactive/Archive in `identity.go`,
  `relaunch.go`.
- **Phone API** (`cmd/quill/phoneapi.go`, `phonehandoffs.go`,
  `phonewrite.go`): `GET /phone/sessions` (`apiSession`: id, title, group,
  kmem, project, tokens, background, …), `GET /phone/folders`
  (`knownFolders`: every cwd of live and past sessions), `GET
  /phone/handoffs` (`apiHandoff{name, savedMs, openIn}`), `GET
  /phone/handoffs/slug`, and the write call's `start: apiStart{kmem, name,
  title}` (resume a handoff or start a new name).
- **kmem 1.8.1.** `kmem context "TASK" --topic T[,T]` only **boosts** those
  topics (×2.5 in `rank_facts`); pinned facts are global and come first;
  3 facts; open tasks matched by words. `kmem handoff rename OLD NEW` refuses
  when NEW exists. No handoff merge or retire command; `kmem retire ID` and
  `kmem trash` work per fact.

## 3. Topic tabs in Quill Desktop

### 3.1 Model

A **topic** is a kmem name: a lowercase slug. One topic = at most one live
coordinator tab = one kmem handoff of the same name = one config "sessions"
label. The topic owns a **scope**: the top-level kmem topics its briefings
draw from.

| Topic   | Title   | Scope (top-level kmem topics, today)                                                  |
|---------|---------|---------------------------------------------------------------------------------------|
| zomboid | Zomboid | zomboid, load-opt, launcher, network, chunkstream, server, client-pack, memtest, todo |
| quill   | Quill   | quill                                                                                 |
| kmem    | kmem    | kmem (kmem window assistant; not a tab)                                               |

kmem topics are never merged or renamed (rule 29); the scope is how several
existing topics belong to one tab. Other top-level topics (desktop, dune,
opencode, …) stay unowned until a tab picks them.

Stored in kmem: config `sessions[TOPIC]` gains `"scope": [...]` next to
`as`/`topic`/`project` (K1, §4), so `kmem resume`, `kmem context` and Quill
Android see the same thing. Quill keeps `org.Kmem[id] = TOPIC`.

### 3.2 Opening a topic (start card) and one live session per topic

`kmemCard` becomes **"Open a topic"** (no Resume list, no free session name):

- Topic buttons: topics with a handoff/label first (title, "saved 2 h ago"),
  then unowned top-level kmem topics from `kmem topics` (first segment,
  size), then "New topic…" (free text → `kmemSlug`, refused if it equals an
  existing handoff of another kind).
- Opening a topic **always starts a fresh session** whose first prompt is
  `kmem brief`, `kmem handoff get <topic>` and
  `kmem context "<first task>" --scope <topic>` (K2; until then `--topic`
  with the scope list). No `claude --resume`, no "Open old conversation".
- **One live session per topic.** If the topic is live (a tab, the kmem
  window, or an external live session with that kmem name, K6), the button
  reads "open in tab Zomboid" and switches to it (existing
  `handoffOwner`/`ownerLabel`); never a second coordinator. The same check
  runs in `addSession` paths that set a kmem name (phone start, CLI
  `--resume-all`, relaunch), so no path can create a duplicate. Rotation
  (auto-handoff) ends the old session before the fresh one claims the topic.
- New topic: Quill asks kmem to write the label (`as` = title, `scope` =
  [topic], K1).

### 3.3 Titles and old names

- Title = topic display name (`as`, else capitalised slug);
  `syncTitleToKmem` copies `as` for topic tabs.
- Old session names are not shown: `rowText` drops the "· kmem NAME" meta
  when NAME is the topic. During a merge the old tabs read "Zomboid
  Launcher → merging into Zomboid" until they end.
- `tabMenu` on a topic tab has no Rename; "Switch topic…" = save this
  topic's handoff, end, open the other topic fresh.

### 3.4 Subagent strip (inside each tab)

A thin row under the tab header (both layouts), hidden when nothing runs:

`● Fix join stall (Explore) · 41k tok · 6 min   ● ELTF build (bash) · 2 min   [+3]`

- Source: `conv.BackgroundTasks()` for jobs; subagents from the Agent
  tool_use (description, subagent_type, agent id) already tracked by
  `trackAgentCalls`; tokens per agent id from task-notification usage /
  stream usage (t4cb4ee: new `AgentUse{ID, Name, Type, Tokens, Started,
  Done}` in `internal/chat`, filled by `NoteNotifications` and the stream
  handler).
- Click: dropdown (`showDropdown`) with task text, tokens, log path,
  worktree, "Stop this subagent" (TaskStop only; never the whole turn,
  tf0ac88).
- Finished subagents stay greyed 10 min with their final count; `statsLine`
  adds "subagents Nk". Tab chip gets a count badge; portrait row meta keeps
  "· N in background".

### 3.5 Sidebar, tabs, kmem window

- Landscape: one chip per live topic tab; folder chips row (`syncBookmarks`)
  has nothing to show in kmem mode and is hidden.
- Portrait: one row per live topic (project header = topic title).
- **kmem window Handoffs tab becomes read-only**: one entry per topic handoff
  (title, saved, items, live in tab / not live, scope) with **Show** and
  "Open topic" (= §3.2, switches if live); no Resume, no Delete of topic
  handoffs from Quill (facts can still go to the recycle bin through kmem by
  the user). Under each topic, a collapsed **History** list: the retired
  per-session handoffs merged into it (`merged into zomboid` line, §5), Show
  only. Handoffs not belonging to any topic are listed under "Unassigned"
  (Show only) until the user assigns or retires them (Q12).
  `kmempanel.go` render only; data = `kmem handoff list --json` + sessions
  labels (K4 adds `merged_into`).

### 3.6 Existing tabs and sessions

- No automatic merge. Existing tabs keep running until their session merges
  (§5); then they end and close. Their transcripts stay on disk, unlisted.
- One-time notice per old non-topic tab: "This session predates topic tabs.
  Merge it into a topic at a safe point."

### 3.7 Only active topic tabs (kmem mode)

1. Tabs and the session list show only live topic coordinators. Non-topic
   live sessions (old names until merged, scratch) show under their project
   with the merge notice (Q14).
2. Remove the places: End = save handoff + stop + close tab, no place is
   written. Delete in kmem mode: the `inactive`/`archive`/`auto` branches in
   `org.go`, `identity.go`, `kmem.go`, `relaunch.go`, `kmemassist.go`,
   `phonemenu.go`; the dead parts of `rules.go`; `projectRows` and the
   `Folders` UI; `--resume-all` becomes "reopen the topics that were live at
   quit" (fresh sessions from their handoffs, Q16). Texts change to "ended;
   its state is in the topic handoff".
3. Raw Claude transcripts stay on disk (`SessionsDir`), unlisted. `d.order`/
   `d.infos` are still scanned for usage/stats (`dayWeekTokens`) but feed no
   list. Optional (Q17): **"Find old conversation"** in the kmem window:
   search transcript titles and first prompts (`d.order`), full-text over
   the JSONL on demand off the UI thread; result rows date · folder · title
   · kmem name; action "View read-only" only (no continue, decision 6), plus
   "Copy as kmem fact…" to lift something into the topic.
4. External live sessions (terminal/other tools, `d.terms`, `internal/live`)
   appear only when they carry a kmem topic name, under that topic, as the
   topic's live session ("live in a terminal"; "Take over" via `takeOver`).
   Quill learns the name from K6.
5. Claude-memory mode (`kmemMemory()` false): no topics; keep today's
   sidebar. Note: today that is also "live sessions by project", not the old
   history groups (`rebuildRows` has no mode branch). Q18 asks whether
   Claude-memory mode gets history groups back; if yes, the places code moves
   behind `!kmemMemory()` instead of being deleted.

### 3.8 Save discipline (nothing resumes, so unsaved work is lost)

- **End** (tab close, End, Switch topic, rotation): `endWithSave` as today;
  the close waits for the save (existing `kmemEndTimeout`); a failed save
  keeps the session running (existing).
- **Quit**: change `confirmQuit` to run the same save flow for every visible
  topic session in parallel (like `kmemQuitFlow` for the assistant), bounded
  (60 s each), then stop. The dialog text changes from "can be resumed" to
  "each topic saves its handoff first". A session that cannot save (busy,
  lock held, subagents running) is listed; Quit anyway needs a second
  confirm and records "unsaved at quit" in kmem (`kmem add
  handoff-notes/<topic>`, K7 optional) so the next session knows.
- **Crash / power loss**: protected only by checkpoints. The topic session
  saves a **checkpoint handoff** at safe points: after each merged subagent
  result, after each released pzlock turn, before long waits, and at least
  every 60 min of activity (coordinator hint text + a Quill nudge when the
  last save is older than 60 min and the session is idle). Checkpoints use
  the same `kmem handoff save <topic>` (supersedes the previous state lines).
- **Coordinator safe point**: `safePoint` (`autohandoff.go`) requires zero
  background tasks; a coordinator rarely has zero. Change: a coordinator is
  at a save point when its own turn is idle, no plan/permission is pending
  and its background subagents are recorded (`--job` lines with agent id,
  task, worktree); rotation (hard threshold) still waits for zero running
  subagents, or asks the user (Q19).
- **Auto-handoff policy** (soft 50 / hard 75 / auto) unchanged; at hard the
  rotation now opens the topic fresh in the same tab.

### 3.9 kmem context per topic

Every session prompt and every subagent brief uses
`kmem context "<task>" --scope <topic>` (K2): scope topics first, others only
to fill; pinned facts capped and scope-filtered (K3, t32b878). Quill injects
the scope into the first prompt and `coordinatorHint` ("brief subagents with
`kmem context "<task>" --scope <topic>`"). Pinned user preferences are short
single-line facts.

### 3.10 Phone API and Quill Android

- `GET /phone/sessions`: kmem mode returns only live topic sessions (plus
  non-topic live ones until merged); `group` = topic title. New fields per
  session (omitempty): `topic`, `topicTitle`, `legacyName` (old name while a
  merge is pending), `agents[]` {id, name, type, tokens, startedMs, done},
  `agentTokens`. Body gains `topics[]` {slug, title, scope, live
  (session id or empty), savedMs}.
- `GET /phone/handoffs`: becomes the read-only topic view: {topic, title,
  savedMs, items, live, history[] {name, savedMs, mergedInto}}. `openIn`
  stays for older phones.
- New-session **start** (`phonewrite.go` `start`, `apiStart`): replaced by
  `openTopic: {topic}` (existing topic) or `openTopic: {new: "Title"}`;
  Quill applies §3.2 (switch if live, else fresh session from the handoff).
  The old `start.kmem`/`name` resume form is refused in kmem mode with an
  error telling older phones to update. `GET /phone/handoffs/slug` stays for
  "New topic" previews.
- `GET /phone/folders`: kmem mode: the topic's working folders (`org.Dirs`
  by project + the coordinator's cwd); past transcripts no longer
  contribute. Claude-memory mode unchanged.
- **Quill Android must change**: session list = one row per live topic
  (subagent strip collapsed: "3 subagents · 120k tok", tap expands);
  "New session" sheet = topic picker (`topics[]`, live ones say "Open");
  remove "Resume handoff" and the free name entry; Handoffs screen read-only
  (topic + history, Show only); send `openTopic`; show `legacyName` during
  merges. Done by an Android subagent after the desktop API merges.

### 3.11 Migration of existing org data (one-time, kmem mode)

Guarded by preference `topicMigration=1`, idempotent:

- Backup: copy `sidebarOrg` and `tabs` to `sidebarOrgPreTopics` /
  `tabsPreTopics` first.
- `org.Place`: all entries dropped (liveness decides).
- `org.Titles`: kept for live ids; others dropped.
- `org.Kmem`: kept (maps old transcripts to handoffs; used by "Find old
  conversation").
- `org.Folders`: dropped. `org.Projects`, `org.Dirs`: re-keyed to the topic
  slug when the old kmem name merged into a topic.
- `org.Ended`, `org.Hidden`, `org.RC`: kept.
- `d.tabs`: tabs of ended sessions removed; live ones stay.
- Archived/inactive transcripts **without a kmem handoff**: not listed;
  reachable only by "Find old conversation" if built (Q20).
- One notice: "Sessions are now topic tabs; N ended sessions' handoffs are
  merged or listed in kmem → Handoffs; M older conversations stay on disk."

## 4. kmem dependencies (proposals for kmem-lock-audit)

- **K1 sessions scope.** `kmem session set NAME --as TITLE --scope a,b,c`,
  `kmem session get NAME --json` (kmem writes its own config; Quill never
  edits it).
- **K2 scoped context.** `kmem context "TASK" --scope NAME`: facts and open
  tasks from the scope's topics first, others only to fill; `more:` line
  names the scope. Same for `kmem recall`.
- **K3 pinned share** (t32b878): pins at most ~1/3 of the cap; scope-tagged
  pins only for that scope; overflow as "pinned: N more (kmem get …)".
- **K4 handoff merge/retire.** `kmem handoff merge TOPIC OLD...`: pointers
  unioned, state lines copied with their source name, claims transferred,
  olds marked `merged-into TOPIC` and listed by `handoff list --json` as
  history (`merged_into`), `handoff resolve OLD` → TOPIC; `kmem handoff
  retire NAME REASON` for handoffs with no topic. Without K4, §5 uses
  rename + save + `kmem retire` per fact.
- **K5 resume prompt** names the scope and the no-resume model ("fresh
  session; state = handoff TOPIC").
- **K6 live session names.** `kmem sessions --json`: live Claude sessions
  with the name they last used (`--as`, handoff saves, claims) for §3.2
  one-live-per-topic and §3.7 item 4.
- **K7 (optional)** `handoff save --partial "reason"` for an unsaved quit.

Until K1–K2 ship: `kmem context "TASK" --topic <scope list>` (boost only).

## 5. Merge procedure (for the sessions and the user; not run from this doc)

### 5.1 Current holdings (read 2026-11-22 ~16:00, read-only)

pzlock: holder free, queue empty, game not running (daemon up). Last entries
used batch names (csb-join3, eltf139-ship, …), all released.

| Session (kmem name)                         | Handoff (saved)         | Claims | Lock | Jobs / notes |
|---------------------------------------------|-------------------------|--------|------|--------------|
| Zomboid Launcher (`zomboid-launcher`, was `launcher`) | 13 items, 04:23 | none | none | none; master dc9bae7 unreleased; open td47d24 (unclaimed) |
| Zomboid Diagnostic (`zomboid-diagnostic`)   | 7 items, 11:20          | **t5d321c** (MALLOC check, checkpoint) | none | **systemd user job `pzcapture-malloc-check` running, no time limit** |
| Zomboid Networking 2 (`networking`)         | 8 items, 02:56 (+ f091025) | none | none | none; tb618e9 JOIN STALL open, unclaimed |
| Quill 2 (`quill`)                           | 7 items, 14:22          | **tf0ac88** | none | worktrees under Projects/quill-wt |
| Quill Android (`quill-android-2`)           | 4 items, 15:30          | none   | none | waits for Quill 2's "release prep" 0.1.25 |

Handoffs to merge or retire (all from `kmem handoff list`):

| Handoff | Target |
|---------|--------|
| zomboid-diagnostic, zomboid-launcher, networking, fps-ab | → `zomboid` |
| quill (already the topic name), quill-android-2 | → `quill` |
| kmem-lock-audit, new-session-6 (kmem work) | → kmem topic (Q11: keep `kmem-lock-audit` as the kmem handoff name, or `kmem`) |
| the two phone-test handoffs (b3, phone-test-reply-…), the Tailscale device-setup handoff (install-tailscale-…) | no topic: retire as history (Q12) |

### 5.2 Safe point (each old session)

1. No pzlock turn held or queued under any name it used (`pzlock status`).
2. No game it launched is running; logs of its last test read or copied.
3. Background jobs ended or recorded as `--job` lines (a job that outlives
   the session, like `pzcapture-malloc-check`, with how to collect it).
4. Claims transferred to the topic (`kmem task transfer ID zomboid`) or
   released.
5. Own uncommitted files in shared repos committed or `--wip` (rule 14).
6. No subagent of its own running.

Then: `kmem handoff save <its name> --state "merging into zomboid" …
--transfer zomboid` (rule 24), announce (rule 17), stop taking work, tell
the user "ready to merge".

### 5.3 Forming the topic handoff

Without K4 (kmem 1.8.1 only):

1. The last Zomboid session at its safe point is the merger (recommended:
   Zomboid Diagnostic: the only claim and the pzcapture job).
2. Merger: `kmem handoff get` each other old handoff, then `kmem handoff
   rename zomboid-diagnostic zomboid`.
3. Merger: `kmem handoff save zomboid` with the union of pointers (fps-ab,
   launcher, networking, diagnostic; de-duplicated), one state line per
   source ("from launcher: …"), `--job` for pzcapture, `--lock "none"`,
   claims owned by name zomboid.
4. Merger: for each other old handoff, `kmem handoff save <old> --state
   "merged into zomboid <date>"` and `kmem retire` its older state lines
   ("merged into zomboid"); facts stay in history; no trash (rule 26 is for
   wrong/sensitive facts).
5. Label: `kmem session set zomboid --as Zomboid --scope …` (K1), or until
   then kmem-lock-audit sets it with kmem's own commands.
6. User: in Quill Desktop "Open a topic" → Zomboid; the fresh coordinator
   runs `kmem brief`, `kmem handoff get zomboid`, claims transferred tasks
   `--as zomboid`, checks `systemctl --user status pzcapture-malloc-check`.
7. User closes the old tabs (their handoffs are saved).

With K4: steps 2–4 = `kmem handoff merge zomboid zomboid-diagnostic
zomboid-launcher networking fps-ab`.

**Quill**: Quill Android saves `quill-android-2` at a safe point (no build or
release running; the pending "release prep 0.1.25" exchange finished or
carried as a state line). `quill` already has the topic name: Quill 2 runs
step 3 as `kmem handoff save quill` adding Android's pointers (quill/android,
f81c2a2, Projects/quill-android/COMMENTS.md) and state lines; step 4 marks
`quill-android-2` merged. Release decision f5767e4 (Quill is the base,
Android ports it) becomes an internal order: release prep = desktop release
subagent, then one Android build subagent (memory caps, quill-rel worktree).
Quill 2 then ends and the user opens topic Quill fresh (decision 6).

### 5.4 Announcements (rule 17)

- Each old session at its safe point: "<old name> merging into Zomboid; lock
  entries: none; claims transferred: …" to the live sessions and
  kmem-lock-audit.
- New coordinator, first turn: "Zomboid (topic coordinator) is live;
  replaces Zomboid Launcher, Zomboid Diagnostic, Zomboid Networking 2."
- An old session holding or queuing a pzlock entry does not merge until it
  is released under the old name (rule 17). Today the queue is empty.

## 6. pzlock and rules check

One coordinator holding the turn for its subagents' tests fits rule 5
("subagents never acquire; their parent session runs pzlock") and rule 1
(hold for test and log reading) if the coordinator acquires before the test
subagent starts and releases after the subagent reports logs copied. Gaps:

1. **Who launches.** Rule 1: the session holding the turn launches. A
   subagent running `pz_launch`/lockboot.sh launches inside its parent's
   turn; the wording does not say that is allowed.
2. **PZLOCK_NAME** (8e2): the subagent needs the parent's lock name.
3. **Several sessions assumed**: 8e ("message them before acquiring",
   "~15 min when others queue"), 9/11/15 ("announcement to the other
   sessions"), 14 (others' work): with one coordinator, parallel subagents
   in the same repo are the new "others".
4. **Interrupt kills subagents** (tf0ac88): a Stop can end a test subagent
   with the game up and the lock held; the coordinator then closes the game
   (rule 7), copies logs, releases.
5. **Lock names**: entries use batch names, rule 2 says `<session>`.
   Coordinators: topic plus batch (`zomboid:eltf-ship`); rule 17 "keep the
   old name" applies to entries held or queued at merge time.
6. One locked batch at a time per coordinator.

Proposed rule text (the user decides; rule content is the user's):

- **5.** Subagents never acquire or release; their parent session runs
  pzlock. A parent may let one subagent launch and read logs inside the
  parent's turn: it acquires for that batch, passes `PZLOCK_NAME`, and
  releases after the subagent reports the logs copied; if the subagent dies,
  the parent closes the game (rule 7) and releases.
- **2** (name clause): `acquire "<session>[:<batch>]" …`; topic coordinators
  use their topic name (`zomboid:eltf-ship`).
- **8e** (addition): a coordinator runs at most one locked batch at a time
  and queues its own subagents' tests.
- **14** (addition): parallel subagents of one session count as others: each
  gets its own worktree or listed paths, and the parent commits only those.
- **17** (addition): merging sessions into a topic coordinator: each reaches
  a safe point (no lock entry, game closed, logs read, claims transferred,
  jobs recorded) before the merge; the coordinator announces the old names
  it replaces.
- **24** (wording): "Before /clear, a session swap, End or Quit, and at
  checkpoints, save the topic handoff …; sessions do not resume old
  transcripts: the next session starts fresh with `kmem brief` + `kmem
  handoff get <topic>`."

Rules 1, 3, 4, 6, 7, 8, 8b–8d, 8e2, 8f, 19 need no change (8e2 is covered by
the new rule 5 text).

## 7. Risks

- **Coordinator context over several repos**: coordinator keeps decisions
  and results; per-repo work, logs, builds go to subagents with scoped
  briefs; checkpoints + rotation keep it small.
- **Lost work without resume**: mitigated by §3.8 (save on End and Quit,
  checkpoints, nudges); a crash loses at most work since the last
  checkpoint; transcripts remain on disk (Find old conversation).
- **Mid-test merge**: excluded by the safe point.
- **Lost claims**: transferred by name before saving; live claims never
  taken (rule 19); coordinator claims `--as <topic>` first and checks
  `[handoff]` entries.
- **pzcapture job** outlives sessions: must be a `--job` line in `zomboid`.
- **Duplicate coordinators**: one-live-per-topic check in every
  session-creating path; external sessions need K6 to be seen.
- **Rotation blocked by subagents** (`safePoint` needs zero background).
- **Quill merge**: Android builds use 6 GB caps; one heavy subagent at a
  time with desktop test suites.
- **Older Quill Android** using `start.kmem` resume: refused with an update
  message.

## 8. Phased plan (subagent-sized, file ownership)

Phase 0: user decisions (§9), rule text (§6).
Phase 1: Zomboid merge (§5; no code; works on kmem 1.8.1).
Phase 2: kmem (kmem-lock-audit, its process): K1, K2, K3, K6, K5; K4/K7
optional.
Phase 3: Quill Desktop, one subagent each, own worktree:

| Sub | Work | Files owned |
|-----|------|-------------|
| A | Topic model, "Open a topic" card, one-live-per-topic check, fresh start prompt | `kmemcard.go`, `projects.go`, `identity.go` (resume paths), new `topics.go` |
| B | Subagent strip + per-agent tokens (t4cb4ee) | `internal/chat` (agent usage), new `agentstrip.go`, `buildMain` hook in `desk.go` |
| C | Titles, hide old names, tab menu (after A) | `desk.go` `rowText`, `tabs.go` (`syncTabs`, `tabMenu`), `sidecaption.go` |
| D | Read-only Handoffs tab with topic history | `kmempanel.go` |
| E | Save discipline: quit saves, checkpoints nudge, coordinator safe point | `desk.go` (`confirmQuit`, `quitMessage`), `autohandoff.go`, `memorymode.go` (hint text) |
| F | Remove places + org migration (after A) | `org.go`, `kmem.go`, `relaunch.go`, `kmemassist.go`, `phonemenu.go`, `rules.go`, `main.go`, `background.go`, new `topicmigrate.go` |
| G | Phone API: sessions/handoffs/folders/openTopic (after A, B, F) | `phoneapi.go`, `phonehandoffs.go`, `phonewrite.go`, API doc |
| H | Optional Find old conversation (after D) | new `kmemfind.go`, tab hook in `kmempanel.go` |
| I | External live sessions under topic (after A, K6) | `projects.go` (`liveSession`, `projectGroups`), `sidebar.go` |

A, B, D run in parallel; C, E, F after A; G after A, B, F; H after D; I after
A and K6. Coordinator merges; full suite once at merge.

Phase 4: Quill merge (§5.3) after Zomboid proves out (Q1), then the Quill
Android changes of §3.10 by an Android subagent.

## 9. Open questions

1. Merge order: Zomboid first, Quill after it proves out (recommended) / Quill first / both together.
2. Zomboid merger: Zomboid Diagnostic renames its handoff to zomboid (recommended) / Zomboid Launcher does / start a fresh zomboid handoff from all four.
3. Wait for kmem handoff merge (K4) before merging: no, use rename + save + retire now (recommended) / yes, wait for K4.
4. Zomboid scope: zomboid, load-opt, launcher, network, chunkstream, server, client-pack, memtest, todo (recommended) / zomboid only / let me edit the list.
5. Rule 5 change (subagent may launch inside the parent's turn; parent acquires and releases): adopt the proposed text (recommended) / keep rule 5 and have the coordinator launch itself / other wording.
6. Lock names for coordinators: topic:batch, e.g. zomboid:eltf-ship (recommended) / topic only / keep free batch names.
7. Rule 8e and 14 additions (one locked batch per coordinator; parallel subagents count as others): adopt both (recommended) / 8e only / neither.
8. Rule 17 addition (merge only at a safe point) and rule 24 wording (save on End/Quit/checkpoints, no resume): adopt both (recommended) / 17 only / neither.
9. kmem changes for kmem-lock-audit: queue K1, K2, K3, K5, K6 as kmem tasks now (recommended) / K2 + K3 only / later.
10. K4 handoff merge/retire command: queue it as optional after the Zomboid merge (recommended) / needed before / not needed.
11. kmem topic handoff name: keep kmem-lock-audit (recommended) / rename it to kmem.
12. Handoffs with no topic (the two phone-test handoffs and the Tailscale setup handoff): retire as history (recommended) / keep them under "Unassigned" / assign them to a topic.
13. Subagent strip: thin row under the tab header in both layouts (recommended) / tab chip badge only / Settings only.
14. Non-topic live sessions (old names until merged, scratch): list under their project with a merge notice (recommended) / hide them / separate "Other" group.
15. Places code (Inactive/Archive/Created by Claude) in kmem mode: delete it after a one-time backup of the old org data (recommended) / keep it behind Claude-memory mode only / keep everything.
16. quill --resume-all: reopen the topics that were live at quit as fresh sessions (recommended) / remove the flag.
17. "Find old conversation" (view read-only, copy to kmem): build it after topic tabs ship (recommended) / with them / not needed.
18. Claude-memory mode sidebar: keep today's live sessions by project (recommended) / bring back Inactive/Archive history groups there.
19. Rotation while subagents run: wait for them to finish, then rotate (recommended) / ask me / rotate and record them as jobs.
20. Archived/inactive transcripts without a kmem handoff: leave on disk, reachable only by search (recommended) / one read-only archive pointer per project / show once for review before hiding.
21. External live sessions with a topic name: show as the topic's live session with Take over (recommended) / separate rows / do not show.
22. Quit with a topic that cannot save: list it and ask for a second confirm (recommended) / block quit until it saves / quit without asking.
23. Quill Android: one row per topic, topic picker, read-only handoffs, openTopic (recommended) / keep the current session list until a later release.
