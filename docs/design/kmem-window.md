# kmem operations window (design)

Status: design, 2026-11-22. kmem task t17eac3. Base: master 596036f.
Scope: Quill Desktop only (Quill Android unchanged apart from not seeing the
hidden session). No code in this document's commit.

## 1. Goals and non-goals

Goals

- The toolbar "kmem" button (with its count bubble, `kmembadge.go`) opens a
  dedicated **kmem window**: a second fyne window, not the dialog that
  `showKmemPanel` (`kmempanel.go:116`) shows today.
- Left side: Overview (with "Waiting for you"), Handoffs, Tasks, Memory.
- Right side: the chat of the **kmem-lock-audit** assistant, kmem's
  developer and operator.
- The assistant runs in the background from Quill Desktop's start; closing
  the window only hides it. "Stop assistant" and Quit save its kmem handoff
  and stop it.
- kmem stays self-reliant: the window works without any model (panels only).
- Reports from other sessions while the window is closed are queued as kmem
  tasks and counted on the bubble.
- The hidden-session machinery is reusable for worker tabs (task tadaf70).

Non-goals

- No kmem changes in Quill Desktop. New kmem features are the assistant's
  developer work (with the user's approval).
- The recycle bin stays in Settings → General → Memory (kmem) (`kmembin.go`).
  The window links to it but does not embed it.
- No window positioning or always-on-top. Wayland does not allow either.
- No second kmem-lock-audit process. If one is live elsewhere, the window
  offers to open it.

## 2. User decisions (2026-11-22)

**Superseded in part (user 2026-11-22, kmem fact fc6b73c): one kmem
assistant, both roles; the only always-running background session.**
kmem-lock-audit keeps the developer role (Projects/kmem, install.sh,
launcher and kmem-guard, releases and staged rollouts, RULES file structure,
pzlock code, the kmem backlog) and the operator role; there is no kmem-dev.
It starts with Quill Desktop and keeps running while the window is closed
(close only hides the window). Decisions 2 and 5 below are replaced by this;
§5, §6, §10 and §14 describe the current behaviour.

1. The chat lives inside the kmem window.
2. ~~The assistant runs only while the window is open.~~ Replaced: it runs
   from Quill Desktop's start until "Stop assistant" or Quit, which ask it to
   save handoff `kmem-lock-audit`, wait up to 60 s, then stop it.
3. Model = the "New sessions" model preference (`newModel`, `desk.go:1021`).
   Settings adds an "Off" option for the window's assistant.
4. kmem stays self-reliant. With no usable model the window shows the panels
   only, and the chat area is replaced by a one-line reason.
5. ~~kmem development moves to a separate session named **kmem-dev**.~~
   Replaced: kmem-lock-audit is both developer and operator.
6. Reports that arrive while the window is closed become kmem tasks titled
   `kmem issue: …` with topic `kmem`. The bubble counts them
   (`parseKmemNeeds`), and the assistant handles them on its next run.

## 3. Window layout

The window is created once per open with `d.app.NewWindow("kmem — Quill Desktop")`.
Its default size is 1280x800, and it remembers the last size in the prefs
`kmemWinW`/`kmemWinH`. It cannot remember its position because Wayland does
not allow it. The window holds one `container.NewHSplit`, and the split
offset is kept in the pref `kmemWinSplit` (default 0.45).

```
+--------------------------------------------------------------------------------------+
| kmem — Quill Desktop                                                          _ [] X |
+------------------------------------------+-------------------------------------------+
| [Overview] [Handoffs] [Tasks] [Memory]   | kmem-lock-audit · operator   ● running    |
|                                          | model: Claude (New sessions)  [Save now]  |
| Waiting for you (4)                      |-------------------------------------------|
|  2 kmem issue(s) from other sessions     |  > kmem brief                             |
|     [Ask the operator]                   |  > kmem handoff get kmem-lock-audit       |
|  1 item in the recycle bin  [Review bin] |                                           |
|  Daily maintenance: 3 warnings           |  Two issues are waiting:                  |
|     [Open report] [Explain]              |  1. kmem issue: maint flags /mnt/c paths  |
|  kmem audit: AUDIT PROBLEMS ... [Explain]|     (td12618 already covers it) ...       |
| ---------------------------------------- |                                           |
| kmem brief                               |  Plan: retire t9a1b2c as a duplicate of   |
|  ...                                     |  td12618 ... Approve?                     |
| Sections over 8 KB (maintenance asks to  |  [Approve plan & run] [Change...]         |
|  split them): load-opt/... [Suggest split]|                                          |
| ---------------------------------------- |                                           |
| 63 topics, 9 handoffs, 41 open tasks,    |-------------------------------------------|
| 1 item in the recycle bin (Settings →    | [ Ask kmem-lock-audit...           ] [>]  |
| General → Memory (kmem)).                |                                           |
+------------------------------------------+-------------------------------------------+
```

When there is no model, the right side shows one line and nothing else, for
example:

```
| The kmem assistant is off (Settings → General → Memory (kmem) → Assistant).  |
```

Other one-line reasons:

- "kmem-lock-audit is live in the tab “…” [Open it]"
- "The local model can't use tools; the assistant needs one that can."
- "Claude Code is not signed in."

Left side:

- The four tabs are today's `kmemPanel` content
  (`kmempanel.go:128-140`, `reload` at `:168`), unchanged except:
  - all dialogs take the kmem window as their parent;
  - "Waiting for you" gains a "kmem issue" row;
  - the "Explain", "Suggest split" and "Ask the operator" buttons prefill the
    chat input. They never send by themselves, and they are hidden when the
    chat is off.
- The Handoffs tab shows the operator's own handoff as
  "live in the kmem window" (see `handoffOwner` in §4).

Right side:

- A `chatView` built with `newChatViewWith(d, width, shift)`
  (`chatview.go:115`) plus a window-owned input entry and send button.
- It does not use `newPane` (`split.go:97`), because `focusPane`
  (`split.go:422`) focuses `d.win`.
- Plan cards, question cards and permission cards render as they do in the
  main window. Clicks resolve through `ownerOf` (`owner.go:25`), which already
  searches every session.

Keys:

- The kmem window gets its own Escape handler (closes overlays, then the
  window, with the same close flow as X), Ctrl+C, Ctrl+A and Enter-to-send.
- `shortcuts.go:63-77` and `escape.go:35-44` stay bound to `d.win`.
- The new-tab shortcut and the tab-number shortcuts are not bound in the kmem
  window.

Focus:

- On open: `w.Show()` then `w.RequestFocus()`. Under Wayland focus may not
  move. That is acceptable, so it is not retried in a loop.
- A second click on the kmem button while the window is open calls
  `RequestFocus` again and does not open a second window.

## 4. Hidden session model

The operator is an ordinary `deskSession`:

- `cwd` = `~/.local/share/quill/kmem-window` (created on first use), so its
  transcripts sit in their own Claude projects folder;
- model = the window model (§9);
- mode `default`;
- kmem name `kmem-lock-audit` in `d.org.Kmem`.

It also gets a new field `hidden bool`, saved in `sessMeta` as `Hidden`.
`d.org.Hidden map[string]bool` keeps ended hidden ids out of the sidebar
after a restart. A second, cheap signal is the cwd: `isKmemWindowDir(cwd)`.
Every place below treats a session as hidden if either is true, which also
covers the transcript scan (`d.order`), where no `deskSession` exists.

The router `d.viewFor(s) *chatView` (new) returns:

1. `d.chat` when `d.current == s`;
2. otherwise the pane's view;
3. otherwise the kmem window's view when `s` is the window's session.

`handleEvent`, `chatFor`, `handleExit` and `notify` call `viewFor` instead of
their own `d.current == s` / `paneFor` branches.

Filter list, verified at 596036f:

| # | Place (file:line) | What it does today | Hidden-session treatment |
|---|---|---|---|
| 1 | `projects.go:216` `liveSession` | sidebar liveness | false for hidden |
| 2 | `desk.go:825` `rebuildRows` (loops `:839`, `:844`) | sidebar rows from `d.sessions` and `d.order` | skip hidden ids and `isKmemWindowDir(info.Cwd)` |
| 3 | `desk.go:2050-2066` `refresh` | sets `d.order`/`d.infos`, terminal detection | keep in `d.infos` (window needs title/usage); rows filtered by #2 |
| 4 | `desk.go:2026` `runningSessions` | all running sessions | add `runningVisible()`; callers below choose |
| 5 | `desk.go:2120` `confirmQuit` | "N sessions running" | count hidden separately: "the kmem assistant will save its handoff first"; quit runs the close flow (§5) with the same 60 s cap |
| 6 | `desk.go:475`, `desk.go:485` RC toggles | turn on Remote Control for all | skip hidden (never RC) |
| 7 | `desk.go:1955` `autoRemote` (called `desk.go:1471`) | auto RC on start | return for hidden |
| 8 | `rcnames.go:30` `syncRCNames` | RC titles | skip hidden |
| 9 | `reload.go:166` `trySwap` / `:175` takeover check / `:204` handover | hot reload hands running sessions to the new process | hand the hidden session over too, flagged `Hidden`; new process adopts it hidden and reopens the kmem window if it was open (state `KmemWin bool` in `restoreState`); see open question 8 |
| 10 | `reload.go:303` `saveQueues`, `restore.go:42` `saveSessions` | persist queues/meta | keep, persist `Hidden` |
| 11 | `tabs.go:126` `deleteEmptySessions` | purges empty sessions | skip hidden (its history is in kmem, not the tab) |
| 12 | `keeper_unix.go:395` `keepSession` | keeper holds the pipe across crashes | keep, add `Hidden` to `handedSession` so a crash restore stays hidden |
| 13 | `usagepause.go:67` `usageSource` | picks a session for usage reads | may use hidden (harmless) |
| 14 | `usagepause.go:229` pause, `:266` resume | sends pause/resume messages | include hidden only while the window is open (it is, if running) |
| 15 | `limitmsg.go:19` `checkLimitMessage` | copies the limit notice | keep |
| 16 | `phoneapi.go:227` `sessionIDs` | phone session list | already from `d.groups`/`d.tabs`; ensure hidden never enters either |
| 17 | `phoneapi.go:348` `knownFolders` | phone folder picker | skip hidden and `isKmemWindowDir` |
| 18 | `phoneapi.go:454` `scan` | phone change feed | follows #16; also skip hidden in `a.track` |
| 19 | `kmem.go:233` `linkKmem` | maps transcript kmem names to ids | keep (the operator's id must map to `kmem-lock-audit`) |
| 20 | `kmemcard.go:15` `handoffOwner` | "is this handoff live?" | count hidden as live; `kmempanel.go:214-226` shows "live in the kmem window" and "Open" raises the window |
| 21 | `kmempanel.go:329` `resumeHandoffNamed`, `:353` `resumeNamedFromCLI` | resume by name | for `kmem-lock-audit` open the kmem window instead of a tab |
| 22 | `kmem.go:288` `relaunchViaKmem`, `kmem.go:452` `finishKmemRelaunch`, `relaunch.go:277` `replaceWith` | context rotation / auto-handoff (`autohandoff.go:115`) | new session inherits `hidden`, swaps into the window's view, no tab |
| 23 | `kmem.go:513` `finishKmemEnd` | archives, closes tab | for hidden: no `closeTab`/placement; mark ended only |
| 24 | `notify.go:36` `seen` | suppress desktop notice | true if the kmem window is focused-and-shown and `s` is its session |
| 25 | `notify.go:89` `alert` / `phoneEvent` | desktop + phone notices | desktop: title "kmem assistant …", click raises the kmem window; phone: skip |
| 26 | `desk.go:1479` `handleEvent` routing (`:1502`, `:1512`) | UI queue for current/pane | via `viewFor` |
| 27 | `desk.go:1773` `chatFor` | update view | via `viewFor` |
| 28 | `desk.go:1561` `handleExit` (`:1589`) | show exit error | via `viewFor`; window shows "stopped" header |
| 29 | `desk.go:1948` `notify` (`:1950`) | notice in chat | via `viewFor` |
| 30 | `desk.go:2350` `allViews` | theme/scale refresh of every view | append the kmem window's view |
| 31 | `desk.go:1707` `sendTo` (`:1758` clears `d.input`) | send path | unchanged; window clears its own entry |
| 32 | `background.go:64` IPC | `resume NAME`, `show` | add `kmem-window` (open the window); `resume kmem-lock-audit` routes to it (#21) |
| 33 | `kmembadge.go:231` `showKmemPanelTab` | opens dialog on a tab | opens the window on that tab |
| 34 | `kmempanel.go`, `kmembin.go` dialogs (`d.win` at `kmempanel.go:118,122,311,405,435,450`; `kmembin.go:28,32,116,133,143,148,165,172`) | parent window | take a `parent fyne.Window` (kmem window when opened from it) |

The `d.current` invariant: the hidden session is never `d.current` and never
in `d.tabs`. Most of today's logic already keys on those two, which is why
the list above is finite.

## 5. Lifecycle state machine

One kmem assistant, both roles; the only always-running background session
(user 2026-11-22, fc6b73c). The window and the assistant have separate
lifetimes: the window only shows the assistant's chat.

```
   Quill Desktop starts (kmemAssistBoot, after a reload's restore)
   or the window opens while nothing runs (not after Stop assistant)
   CLOSED ─────────────────────────► PROBING
     ▲                                  │ kmem memory off (boot only) / kmem missing /
     │                                  │ model Off / no sign-in / Ollama tool-use probe fails
     │                                  ├──────────────► PANELS_ONLY (chat line shows reason)
     │                                  │ kmem-lock-audit live in a tab (handoffOwner, not hidden)
     │                                  ├──────────────► ELSEWHERE (window offers "Move it here")
     │                                  │ hidden session handed over by a hot reload
     │                                  ├──────────────► LIVE (adopted, nothing resumed)
     │                                  ▼
     │                               STARTING  new hidden session + resume prompt
     │                                  │       (+ "handle open 'kmem issue:' tasks")
     │                                  ▼
     │          crash / exit          LIVE ◄── window close/open only hide/show the chat
     │   ┌──────────────────────────────┤
     │   ▼                              │ Stop assistant (window) or Quit
     │ FAILED (header shows error,      ▼
     │  [Restart]) ───────────────► SAVING   kmemSave{end:true}
     │                                  │    sendTo(kmemAssistStopMessage); 60 s timer
     │            saved / 60 s / fail   ▼
     └──────────────────────────────STOPPING  p.Stop(5 s); markEnded
```

Details

- **Start with Quill Desktop.** `watch` calls `kmemAssistBoot` after
  `applyRestore`. It needs kmem memory on, the model setting not Off and the
  probe passing. If kmem-lock-audit is live in a tab, nothing starts and the
  window keeps its "Move it here" offer.
- **Closing the window hides it.** Nothing is saved or stopped; claims,
  background jobs and subagents go on. Reopening shows the same session in
  the same chat view (unsent input kept); `chatView.show` catches up on what
  happened while it was hidden.
- **Run dot.** A small dot on the kmem button's bottom-right corner shows
  while the assistant's process runs (`kmembadge.go`, `kmemAssistDot`).
- **Stop assistant** (window header) runs the save-and-stop flow. The window
  then says "kmem-lock-audit is stopped" with a "Start assistant" button;
  reopening the window does not restart it, nor does anything else until
  Quill Desktop starts again.
- **The save is not blocking.** The header shows "saving its handoff, then
  stopping"; the main window's status line reports the outcome.
  - On success: "Saved kmem handoff “kmem-lock-audit”."
  - On timeout: a question with "Stop without saving", "Keep running until
    saved" and "Keep it running" (cancels the stop). The handoff save may
    refuse while claims, jobs or uncommitted files are unrecorded, so the
    failure is shown, not swallowed (rule 24).
- **Mid-turn stop.** The turn is interrupted, then the stop message is sent.
  The 60 s cap still applies.
- **Pending plan card on stop.** The save is pre-allowed: `kmemSaveAllowed`
  passes `kmem handoff save` while `s.kmemSave` is set; a pending plan card
  is cancelled.
- **Claims on stop (rule 19).** The stop message asks it to release the
  claims it is not working on and `kmem task transfer ID kmem-lock-audit`
  (48 h lease) the rest. The next start's prompt tells it to reclaim them.
- **Background jobs** are fine: it is always running. It saves checkpoints at
  safe points (its hint, and auto-handoff at `safePoint`).
- **Crash or kill** (Quill crash, OOM, power): the keeper (#12) may hand the
  process back. If not, the next start resumes from the last saved handoff;
  if that save is older than the last start (pref `kmemWinStarted`), the
  first notice says: "The last session ended without saving; working from the
  save of <time>."
- **Quit** (`confirmQuit` → `kmemQuitFlow`) runs the save-and-stop flow,
  window open or not (bounded by the same 60 s, no question), then exits.
- **Hot reload** hands the process over (#9) whether or not the window is
  open. `restoreState.KmemWin` reopens the window only if it was open;
  `kmemAssistBoot` adopts the handed-over hidden session either way.
- **Peer name.** The hidden session starts with `claude --name
  kmem-lock-audit` (`processOptions`), so other sessions see and message it
  as kmem-lock-audit in ListAgents/SendMessage instead of the folder name plus
  a number ("kmem-window-51"). `kmem issue:` tasks remain the fallback when it
  isn't running.
- **Ollama probe.** When the window model is local, Quill sends a one-shot
  `/api/show` request and checks that `capabilities` contains `tools`.
  - Missing → PANELS_ONLY with the reason line.
  - The result is cached per model for 24 h.
  - `checkOllama` (`backend.go:295`) still runs at `ensureProcess`.
- **Plan-first** applies unchanged: reads run free and writes go through the
  plan card.
  - Start-up reads under `--as kmem-lock-audit` pass via `sendResume` →
    `kmemResumeAllowed` (`kmem.go:372`).
  - Known gap: a user message typed while the operator's subagents run sets
    `inTurn` and ends their `agentGrants` coverage
    (`desk.go:1743`, `:2474`, `:2500-2521`). The hint tells the operator not to use
    subagents for writes. The window shows "subagents running; messages now
    pause them" under the input while `len(s.agentGrants) > 0`.

## 6. kmemAssistantHint (final text, 894 characters)

Appended to `sessionPrompt(...)` for the hidden session only, via
`sessionPromptFor(s)`. Coordinator and build-gate hints are off for it.
Replaced 2026-11-22 (fc6b73c): both roles, always running, no kmem-dev.

```
You are kmem-lock-audit, Quill Desktop's kmem developer and operator; use kmem --as kmem-lock-audit. Start: kmem brief, kmem handoff get kmem-lock-audit, open "kmem issue:" tasks (topic kmem). Operator: explain audit/maint warnings, review the recycle bin (only the user empties it), suggest splits (rule 29: split downward, dry run first), tidy tasks, check handoffs, debug reports. Developer: Projects/kmem, install.sh, launcher, kmem-guard, releases, rollouts, RULES file, pzlock code, backlog. Reads are free; show the plan and get the user's approval before any write or release; one fact per kmem add. Never: kmem trash empty, hand repair of history/kmem data/backups, pzlock clear. You always run in the background (background jobs, reviewer subagents ok); sessions reach you by SendMessage. Save at safe points; when Quill Desktop asks, release or transfer claims and save your handoff.
```

## 7. Rules compliance

| Rule | Risk in the window | How the design complies |
|---|---|---|
| 4 | Operator explains a dead pzlock holder | Hint: explain only; `pzlock clear` stays the user's |
| 14 | — | Operator edits no repos |
| 17 | New name kmem-dev | kmem-dev starts with a fresh handoff and announces itself; kmem-lock-audit keeps name and handoff |
| 18 | Hand edits to kmem data | Hint forbids; writes only via kmem commands, one fact per add |
| 19 | Window-only operator goes "gone" on close; claims takeable after 6 h | Close flow: release unworked claims, transfer the rest (48 h), reclaim on open |
| 20 | Start-up | Resume prompt runs `kmem brief` + handoff get |
| 21 | AUDIT PROBLEMS | Operator explains; never repairs history by hand; user decides |
| 22 | Personal data in examples | Operator writes no fixtures; docs use /home/user, example.com |
| 23 | Operator subagents | Prompts carry `kmem context` KMEM-BRIEF; hint discourages subagent writes |
| 24 | Save refuses with unrecorded claims/jobs/files | Graceful close with visible failure; auto-handoff checkpoints for crashes |
| 25 | Guard blocks | Follow, record false positives (operator may `kmem add` under kmem/rules-process/false-positives after approval) |
| 26 | Bin | Operator may `kmem trash ID "reason"` after approval; never empties. UI says "recycle bin"; `trash` is the command, "bin" the older word. Kmem Lock Audit sees no conflict; note for kmem-dev that kmem's own messages mix the two |
| 29 | Splits | Suggest only, dry run first, one split task for the owner; never rename/merge; leave `kmem fixed` topics alone |

The contract with kmem (fd398ac) is unchanged. Quill Desktop still parses the
`AUDIT PROBLEMS:` and `bin: N item(s)` lines from the brief.

## 8. Reports while the window is closed, and the bubble

Today a session that finds a kmem problem looks up the kmem-lock-audit session
and uses SendMessage (pinned pref f37efdb). A closed operator can't answer.
New flow:

1. The reporting session runs
   `kmem task add "kmem issue: <one line>" --topic kmem` (with evidence ids in
   the text). It tells the user in one line. It does not repair anything.
2. The bubble counts it at the next `refreshKmemNeeds` (every 30 min, on
   start, and on every window reload). Waiting for you shows
   "N kmem issue(s) from other sessions [Ask the operator]".
3. On the next open, the operator's start-up lists those tasks and proposes a
   plan per issue:
   - fix it in kmem state (approved writes), or
   - forward it to kmem-dev: leave the task open with a `kmem-dev:` note in
     its title, or a fact pointer.
   It finishes them with `kmem task done`.
4. If the window is open, sessions may still SendMessage. Filing the task is
   always correct.

How sessions learn this is open question 2 (a kmem brief line, or a new shared
rule 30). Either way it is the user's rule decision, and the kmem side belongs
to kmem-dev.

Code change (`kmembadge.go`):

```go
// kmemIssue: a report from a session for the kmem operator (user 2026-11-22).
func kmemIssue(t kmemTask) bool {
    return t.Topic == "kmem" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(t.Title)), "kmem issue:")
}
type kmemNeeds struct { …; Issues []kmemTask; RolloutWait string }
// parseKmemNeeds: t.Status != "done" && kmemIssue(t) → n.Issues
// Count(): + len(n.Issues) (+1 if RolloutWait != "", see question 4)
```

- Issues are not user tasks. `userTask` stays as it is, and an issue is
  counted once.
- `readKmemNeeds` already lists all tasks, so no extra kmem call is needed.
- A claimed issue (owner set) still counts until it is done.

## 9. Model setting

Settings → General → Memory (kmem) gains:

- "kmem assistant: [Same as New sessions ▾]" with the options "Same as New
  sessions" (default) and "Off". The pref is `kmemAssistantModel`, with the
  values `""` (follow `newModel`) and `off`.
- With "Off", the window is panels-only and the right side shows its reason
  line.
- A local model must pass the tool-use probe (§5).
- Changing the setting while the window is open takes effect on the next open.
  The chat header says so.
- kmem not available → no assistant and no window chat. The kmem button
  behaves as today (information dialog).

## 10. Roles: one kmem assistant, both roles

Superseded (user 2026-11-22, fc6b73c): there is no kmem-dev. kmem-lock-audit
keeps both roles and is the only always-running background session.

| | kmem-lock-audit (hidden, always running) |
|---|---|
| Developer | Projects/kmem repo, install.sh, launcher and kmem-guard, releases and staged rollouts (with background checks and reviewer subagents), structural RULES file edits (rule content stays the user's), pzlock code, the kmem backlog; writes and releases only after the user's approval |
| Operator | audit + maint warnings, recycle bin review (user empties), split suggestions and follow-ups, task tidying, handoff checks, debugging reports, pzlock status explanations (rule 4), Monday check-in |
| Never | kmem trash empty, hand repair of history, kmem data or backups, pzlock clear |
| Long work | yes (background jobs, rollouts) |
| Runs | from Quill Desktop's start until Stop assistant or Quit |
| Reached by | SendMessage to kmem-lock-audit; `kmem issue:` tasks when it isn't running |

The migration steps below were for the abandoned split; steps 1, 3, 4 and 8
(kmem-dev) do not apply. Kept for history.

Migration steps. These are for the user and the sessions named. Quill Desktop
and this design do not run them.

1. Finish the kmem 1.8.1 rollout first (recommended). Its remaining stages are
   zomboid-diagnostic, networking, quill, kmem-lock-audit (the window) and
   kmem-dev last. If the split comes first, kmem-dev takes the rollout over
   with `kmem rollout status` and its handoff.
2. kmem-lock-audit moves its review scripts from /tmp/kmem170 to
   `Projects/kmem/tools/rollout/` and commits them in the kmem repo.
3. The user starts a new session and names it **kmem-dev** (new kmem name).
   kmem-lock-audit moves the dev state into it:
   - it saves a fresh `kmem-dev` handoff whose pointers come from its own
     handoff (fb1124e, f90e48c, f854312, tbba9ae, t32b878, td12618);
   - it transfers its dev claims to kmem-dev (`kmem task transfer`);
   - it removes those items from its own handoff at the next save.
   The handoff is not renamed: kmem-lock-audit keeps its name and handoff
   (rule 17). kmem-dev announces its name to the live sessions.
4. kmem-dev adds `kmem-dev` to `kmem-resume`'s AS/TOPIC tables (fd1d891).
   kmem-lock-audit's entry stays and remains reachable as
   `kmem-resume-pz-lock-audit`.
5. Monday check-in timer `kmem-checkin-pz-lock-audit` (Mon 2026-11-23 10:07):
   kmem-dev edits `~/.local/bin/kmem-checkin-pz-lock-audit`.
   - It runs `quill --kmem-window` when Quill Desktop is running (new IPC
     verb, #32).
   - Otherwise it runs
     `kmem task add "kmem issue: check-in due" --topic kmem`.
   - At the check-in the operator asks the user how kmem and pzlock went, then
     does td56c1c and t7a8638 (asking first).
6. The user decides how reporting sessions learn the `kmem issue:` flow
   (question 2). If that is a rule, the user approves it (rules-process 2/6)
   and kmem-dev edits the RULES file.
7. The pinned preference f37efdb ("send issues to the kmem lock audit session
   with SendMessage") gets a replacing fact that says to file a `kmem issue:`
   task. The user approves the wording, and the operator writes it with
   `--replaces`.
8. The Quill 2 contract (fd398ac) moves to kmem-dev's handoff. kmem-dev now
   owns the brief format.

## 11. Implementation plan

Phase 0, done by the coordinator on master (small, so the worktrees don't
fight over `desk.go`). It adds the stubs only:

- `deskSession.hidden`;
- `desk.kmemWin *kmemWindow` (nil-safe);
- `d.viewFor(s)` (returns current or pane today);
- `isKmemWindowDir`;
- `sessMeta.Hidden`, `handedSession.Hidden`, `restoreState.KmemWin`.

Build and tests must stay green. A, B and C branch from it.

| Agent | Worktree / branch | Owns (only these files) | Work |
|---|---|---|---|
| A: window + panels | `kmemwin-ui` | new `kmemwindow.go`; `kmempanel.go`, `kmembin.go`, `kmembadge.go`, `layoutmode.go` (button handler) | Window open/focus/size/split prefs; move `kmemPanel` into the left side; `parent fyne.Window` for all dialogs (#34); Waiting rows incl. issues; `kmemIssue` + `Count`; window Escape/Ctrl shortcuts; `showKmemPanelTab` (#33); panels-only reason line API `setChatReason(string)` |
| B: hidden session | `kmemwin-hidden` | `desk.go`, `projects.go`, `rcnames.go`, `tabs.go`, `restore.go`, `reload.go`, `usagepause.go`, `phoneapi.go`, `notify.go`, `kmemcard.go`, `kmem.go`, `relaunch.go`, `keeper_unix.go`, `keeper_windows.go`, `limitmsg.go` | Filter list #1-#31 (except #21/#32/#33), `viewFor` routing, `runningVisible`, confirmQuit text, reload hand-over of hidden |
| C: assistant lifecycle | `kmemwin-assist` | new `kmemassist.go`; `memorymode.go` (hint, `sessionPromptFor`), `kmemsetup.go` (setting), `background.go` (IPC `kmem-window`), `backend.go` (tool-use probe) | State machine §5, close/save/60 s, claims message, crash note, ELSEWHERE, Ollama probe, embed `newChatViewWith` + input in the right side via A's `kmemWindow.setChat(obj)` |
| D (optional): tests + docs | `kmemwin-tests` | new `kmemwindow_test.go`, `kmemassist_test.go`, `docs/` | Test plan §12 after A-C merge |

Merge order is A, B, C, D. C needs A's `setChat`/`setChatReason` and B's
routing. Each branch compiles on its own against the Phase 0 stubs.
`kmempanel.go:321` and `:329` (#21) belong to A, which calls C's
`d.openKmemWindow()`. Phase 0 adds that as a stub.

## 12. Test plan

The tests use the existing harness: `newHarness`, `test.NewTempApp`, the fake
`ClaudeBin` and `d.kmemRun` for kmem output.

Unit tests:

- `parseKmemNeeds`:
  - an issue task counts;
  - a done issue doesn't;
  - an issue that is also a "USER:" task counts once;
  - topic other than `kmem` doesn't count;
  - case-insensitive prefix.
- Bubble `Count` with issues and the rollout flag.

Hidden filter tests, one assertion per row of §4:

- the hidden session is not in sidebar rows, tabs or `phoneAPI.sessionIDs`;
- it is not in `knownFolders`;
- RC is not started;
- `deleteEmptySessions` keeps it;
- `handoffOwner("kmem-lock-audit")` returns it;
- `saveSessions` writes `Hidden`;
- a restore keeps it hidden;
- `allViews` includes the window view while open.

Routing tests:

- events for the hidden session update the window view, never `d.chat`;
- `notify` text lands in the window;
- `seen` is true only while the window is shown.

Lifecycle tests (the 60 s cap is the var `kmemWinSaveWait`, set to 200 ms in
tests):

- open → STARTING sends the resume prompt with `resumeNext`;
- close → the save message is sent and `kmemSave.end` is set;
- the fake handoff list shows a newer save → stop, not archived, no tab
  closed;
- no save → stop after the timeout, and the failure note is shown;
- reopen during SAVING → no second process;
- model Off → panels only with the reason line;
- a live kmem-lock-audit in a tab → ELSEWHERE, nothing started;
- context rotation keeps the session hidden.

Plan-first tests:

- read-only `kmem --as kmem-lock-audit brief` passes on the first turn;
- `kmem add` is held for the plan card;
- `kmem handoff save` passes during the close.

Dialog parent tests:

- the Review bin and Delete handoff dialogs open on the kmem window's canvas
  when called from it.

Manual checks (user, KDE Wayland):

- open, close and reopen;
- focus behaviour;
- quit with the window open;
- `quill --reload` with the window open;
- `quill --kmem-window` from a terminal;
- Ollama with qwen3:8b (tools) and with a non-tool model.

## 13. Risks

- **The filter list misses a site.** The hidden session would then leak into
  the sidebar or the phone list, or be swept by `deleteEmptySessions`.
  Mitigations: the `d.current`/`d.tabs` invariant, the cwd fallback, and one
  test per row. Reviewers grep `range d.sessions` and `runningSessions()` on
  each merge.
- **The 60 s close and the handoff refusals.** The save can fail when claims
  or jobs are unrecorded, and the user may not read the status note. This is
  accepted: the failure is shown, and auto-handoff checkpoints limit the loss.
- **A second fyne window.** It is new ground (no secondary windows today). On
  Wayland, focus and position can't be relied on. Dialog overlays on the
  wrong canvas are a real risk; #34 must be complete.
- **Plan-first gap.** User messages during subagent runs end their coverage.
  Mitigation: the hint plus the input hint.
- **Two kmem-lock-audit sessions.** Possible if the user resumes the handoff
  in a tab by hand while the window is starting. `handoffOwner` is checked at
  PROBING and again just before `ensureProcess`.
- **Cost.** The operator spends tokens whenever the window is open, even
  idle. The resume prompt keeps start-up to the brief, the handoff and the
  issues. The window could be closed after inactivity (out of scope).
- **Reports get lost.** This happens if sessions still SendMessage to a
  closed operator. Question 2 and migration step 7 address it.

## 14. Decisions (user 2026-11-22: the recommended option for all ten)

Superseded in part (user 2026-11-22, fc6b73c): one kmem assistant, both
roles; the only always-running background session. Question 1 (role split)
is void: there is no kmem-dev. Questions 5 and 6 now apply to "Stop
assistant" and Quit instead of closing the window, which only hides it.
Question 8: hot reload keeps the assistant whether or not the window is open
and reopens the window only if it was open.

Decided: role split after the 1.8.1 rollout; a kmem brief line (no new
rule); the bubble counts every open "kmem issue:" task and a paused
rollout; on close release idle claims and transfer the rest (48 h lease);
closing mid-turn asks it to stop and save at once; a live kmem-lock-audit
tab is offered to move into the window; hot reload keeps the assistant and
reopens the window; the Monday check-in opens the window, else files
"kmem issue: check-in due"; the old kmem dialog is removed. The questions
as asked:

1. When should the role split happen? After the 1.8.1 rollout finishes (recommended), or now with kmem-dev taking the rollout over?
2. How do sessions learn to file "kmem issue: …" tasks instead of messaging the operator? A line in kmem brief (recommended), a new shared rule 30, or both?
3. Should the bubble count every open "kmem issue:" task (recommended), or only issues nobody has claimed?
4. Should the bubble also count a staged rollout that is paused waiting for the user? Yes (recommended) or no?
5. Claims when the window closes: release the ones it isn't working on and transfer the rest to kmem-lock-audit with the 48 h lease (recommended), or release all of them?
6. Closing the window mid-turn: ask it to stop and save at once (recommended), or let the turn finish first (still within 60 s)?
7. kmem-lock-audit is already live in a normal tab when you open the window: offer to move it into the window by saving and resuming (recommended), or just offer to open that tab?
8. Quill Desktop hot reload with the window open: keep the assistant running and reopen the window (recommended), or save its handoff and stop it?
9. Should the Monday check-in timer open the kmem window when Quill Desktop runs and otherwise file "kmem issue: check-in due" (recommended), or always file the task only?
10. Should the old kmem dialog be removed once the window exists (recommended), or kept as a fallback?
