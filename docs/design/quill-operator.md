# Quill operator and the assistant box (design)

Status: design, 2026-11-24. Base: master 914ae23.
Scope: Quill Desktop (a new "Quill" button, the Quill operator session, one
animated assistant box for kmem and the Quill operator, a docked side-panel
state, a settings command Quill Desktop gives its operator), the phone API
changes and what Quill Android shows. No code in this document's commit.

Related: `docs/design/topic-tabs.md` (one live session per topic §3.2,
end-of-session check §3.8, topic snapshot §3.8a), `docs/design/kmem-window.md`
(hidden session model §4, lifecycle §5, bubble §8), `docs/design/kmem-android.md`
(phone routes for the kmem assistant §4.3). kmem facts: fe21517 (this
decision), f3a1828 (plan approval scope), fdd52bd (plan cards), f6fa284 (gate:
own kmem name, "Approved."), f77c3d0 (topic snapshot), fc6b73c (kmem assistant
is the only always-running hidden session; this design changes that).

## 1. User decisions this design starts from

From 2026-11-24 (fe21517 and the coordinator's follow-up):

1. A new **"Quill"** button between "Settings" and "kmem", in the toolbar row
   above the message box (bottom left). It opens the **Quill operator**.
2. The Quill operator is **this existing coordinator session** (kmem name
   `quill`, the topic tab that coordinates Quill Desktop and Quill Android work
   with subagents in worktrees). It moves into the button. It is not a new
   assistant. It works the way the kmem window's always-running kmem
   assistant does: it runs hidden and has its own chat, badge and status.
3. Its roles: it improves Quill (through the plan-first gate, worktrees and
   reviews, as today), and it **manages Quill Desktop settings when the user
   asks**. It changes settings only on request, only through a small settings
   command that Quill Desktop provides (never by editing preference files),
   and every change shows in the operator chat with an **Undo** button.
4. Clicking kmem or Quill plays an animation: a **box grows from the button**
   and covers the chat history. Closing animates it back into the button. kmem
   and Quill use the same box mechanism. Today kmem has a separate window;
   that changes (§3.7).
5. **The box has its own message box.** The main message box always belongs
   to the current tab, so a message can't go to the wrong assistant. (This
   replaces the first idea that the main message box would switch targets.)
6. **Dock to side:** the box can also sit as a right-side panel, so the chat
   stays visible. Both states, the switch between them, the remembered choice
   and split-view behaviour are designed here (§3.4).
7. The Quill button shows a small **working dot** and the **badge**: the
   operator is a busy coordinator hidden behind a button.
8. Button space in split view: weigh "Settings · Quill · kmem" against one
   "Assistants" button with tabs (Q11).
9. Quill Android shows the box as a **bottom sheet**.
10. Order: this is built **after** the kmem window fixes that are in progress
    now: duplicate refreshes t17ad60, badge t982851 and status indicator
    te3470e. The Quill button reuses their badge and status code.

## 2. What exists today (code facts, 914ae23; paths under `cmd/quill/`)

- **Buttons.** Landscape: `layoutButtons` builds `d.settingsBtn`,
  `d.kmemBtn` (`openKmemWindow`) and `d.kmemBtnBox =
  kmemButtonWithBubble()` (`layoutmode.go:36-42`). `viewToolbar` /
  `placeToolbar` put `settingsBtn, kmemBtnBox, pcsBtn, spacer, viewBox` in the
  toolbar above the message box. In split view they go into the first pane's
  slot and the view buttons into the last pane's slot
  (`layoutmode.go:138-176`). Portrait sidebar: `openSettings` and `openKmem`
  at the bottom (`desk.go:602-610`), with no bubble.
- **Settings** is a modal `dialog.NewCustom` with tabs General, Setup,
  Sessions, Permissions, Tools and Memory (`showSettings`,
  `layoutmode.go:114`). Checkboxes write `Preferences()` directly (about 200
  `Set…` calls across the package). `setUserPref` marks a user-chosen value
  (`featureask.go:90`). `settingsChanged` syncs the General checkboxes and
  phone streams for the two phone-settable switches (`phonesettings.go:36`,
  `POST /phone/settings` at `:75`). Animation settings: "Animate new text"
  (`prefAnimateText`) and "Text animation speed" Slow/Normal/Fast/Instant
  (`prefAnimateSpeed`, `streamtext.go:46-80`, `desk.go:570-575`). Layout
  glide: `glideDuration` 180 ms (`widgets.go:56`), on when `d.animate`
  (`main.go:133`). There is no reduced-motion setting.
- **kmem window** (`kmemwindow.go`). It is a second fyne window: `kmemPanel`
  tabs on the left (`kmempanel.go:139`), the assistant chat on the right
  (`setChat`), and `kmemWinOperator = "kmem-lock-audit"` (`kmemwindow.go:19`).
  `openKmemWindow` (`:37`) and `close` (`:76`) remember size and split.
  Escape closes overlays, then the selection, then the window (`:166`). Wiring
  lives in `kmemwire.go` (open and close hooks to `kmemAssistOpen` and
  `kmemAssistClose`).
- **kmem assistant lifecycle** (`kmemassist.go`). States closed, probing,
  panels, elsewhere, starting, live, saving and stopping (`:64-79`).
  `kmemAssistBoot` (`:153`) starts it with Quill Desktop. `kmemAssistProbe`
  (`:227`) checks kmem, model, sign-in and the handoff owner.
  `kmemAssistStart` (`:298`) starts a hidden session (`s.hidden`,
  `org.Hidden`, `org.Kmem`) in `kmemWindowDir()` and resumes from its handoff.
  Other paths: `kmemAssistElsewhere`/`MoveIn` (`:362-398`, "live in a tab:
  move it here" saves there and resumes hidden), `kmemAssistAttach`/`Chat`
  (`:400-465`, a chat built once with its own `sendEntry`, header, Restart,
  Save now and Stop assistant), `kmemAssistClose` (`:628`, close = hide),
  `kmemAssistSaveStop` (`:654`, Stop and Quit, 60 s cap) and
  `kmemAssistRunning`/`kmemAssistDot` (`:901-909`).
- **Hidden-session plumbing.** `isHiddenSession` = `s.hidden` or the
  kmem-window cwd (`kmemwindow_stub.go:58`). `viewFor` routes events to the
  kmem window's view (`queue.go:47`). `seen` (`notify.go:37`) and `alert`
  (`notify.go:96`) treat every hidden session as "kmem assistant" (desktop
  title, phone `kmem` event). `handoffOwner` counts the kmem window's session
  as live (`kmemcard.go:15`), and `ownerLabel` says "live in the kmem window".
  `d.hiddenID` filters phone lists (`phoneapi.go:313-464`). The 34-place
  filter list is in kmem-window.md §4.
- **Badge** (`kmembadge.go`): `kmemNeeds` (`:46`), `kmemIssue` "kmem issue:"
  tasks (`:64`), `bubble` (`:154`), the run dot `kmemRunDotSize` (`:151`),
  `badgeLayout` (`:131`) and `watchKmemNeeds` every 30 min (`:291`). t982851
  reworks this into per-item seen times; te3470e adds the working indicator.
- **Topic sessions** (`topics.go`): `topicOwner` = `handoffOwner`
  (`:324`), `pickTopic` (`:365`), `openTopic` (`:420`). End-check and rotation:
  `endCheckFor` (`endcheck.go:80`; topic sessions only, the kmem assistant
  keeps the handoff flow), `rotateTopic` (`:234`), `quitConfirmed`
  (`desk.go:2440`). External live sessions: `extsessions.go`. Subagent strip:
  `newAgentStrip` (`agentstrip.go:55`).
- **Session hints** (`memorymode.go`): `coordinatorHint` (`:77`),
  `topicCoordinatorHint`, `kmemAssistantHint` (`:136`) and
  `sessionPromptFor` (`:141`; the hidden kmem assistant gets no coordinator
  hint).
- **Gate.** Plan-first applies to every session, the hidden one too.
  `planCovers` (`planscope.go:80`) follows the approval scope f3a1828.
  Auto-approve per tab: `autoCovers` (`autoapprove.go:67`, user-started turns
  only, never peer or trigger turns), `autoRootRequest` (`:83`; the crude root
  check is in `gatebatch.go:416`), the chip `d.autoBtn` on the message box
  (`desk.go:1053`). Read-only commands are in `readonly.go`; kmem calls under
  the session's own name follow f6fa284 (`readOnlyKmem`, `:1146`).
- **Main input.** `d.input` is bound to `d.current` throughout (history,
  slash menu, autocorrect, voice, stop, model/mode, auto chip;
  `desk.go:1067-1125`, `send` `:1756`, `sendTo` `:1893`). Split panes have
  their own inputs (`split.go:194`).
- **Local control channel.** A Unix socket `quill.sock` in `XDG_RUNTIME_DIR`
  (`background.go:19`, listener `:128`, verbs `show`, `kmem-window`, `reload`
  … `:156`) is used by `quill --reload`, `--resume`, `--kmem-window` and
  `--resume-all` (`main.go:38-45`). Per-session environment variables can be
  set: `opts.Env` (`internal/session/session.go:239`).
- **Keys** (`shortcuts.go`): Ctrl+T new tab, Ctrl+1-9 tabs, Escape
  (`escape`: overlay, then selection, then interrupt the busy current
  session, `:82`).
- **Phone.** The kmem assistant has `/phone/kmem/assistant/{items, send,
  answer, interrupt, queue, start, stop, restart, save, choose}`
  (`phonekmemassist.go`, kmem-android.md §4.3), a `kmem` stream change and a
  `kmem` notification. Hidden sessions never appear in `/phone/sessions`.

## 3. Quill Desktop design

### 3.1 The Quill operator session

- It is the topic coordinator of topic `quill` (kmem name `quill`), run as a
  **hidden topic session**. It is not a tab, not in the sidebar, and never
  `d.current`. It keeps everything a topic session has: the topic briefing
  and snapshot at start (f77c3d0), the end-of-session check on End, rotation
  and Quit, the subagent strip, and plan-first.
- **Working folder:** the Quill source folder (Q4). Without one (an ordinary
  user who has no source), only the settings role exists (Q3). The folder is a
  setting, Settings → General → Quill operator → "Quill source folder", never
  a path built into the code.
- **Prompt:** `sessionPromptFor` adds `topicCoordinatorHint("quill")` plus a
  new `quillOperatorHint` (draft, about 700 characters):

  > You are the Quill operator, Quill Desktop's coordinator for Quill Desktop
  > and Quill Android; use kmem --as quill. You run hidden behind the Quill
  > button; the user talks to you in its box. Developer: improve Quill
  > Desktop and Quill Android through subagents in worktrees, with plans,
  > reviews and tests, as before. Settings: change Quill Desktop settings
  > only when the user asks in this chat, only with `quill settings`
  > (list/get are free; set and undo need the approved plan); never edit
  > preference files. Security settings are the user's: say where they are.
  > Other sessions report problems as `quill issue:` tasks (topic quill);
  > list and plan them at start.

- **Lifecycle:** generalize the kmem assistant's state machine into a shared
  `assistant` type with two instances, `kmem` and `quill`
  (`kmemassist.go` → `assist.go` + per-role config: name, cwd, hint, end
  flow, labels). The states are the same. Differences for `quill`:
  - Start: with Quill Desktop, like kmem (Q2). Fresh topic session from the
    briefing and snapshot (`topicPromptFor`) via `sendResume`, so the
    plan-first startup exception applies.
  - Stop / Quit / rotation: the **end-check flow** (`endTopic`,
    `rotateTopic` with `hidden` inherited and the new session swapped into the
    box view; kmem-window §4 #22), not the handoff save. Quit runs it in
    parallel with the visible topic tabs (60 s cap, "Quit anyway?" list).
  - Elsewhere: if topic `quill` is live in a tab, the box shows
    "The Quill operator is open in the tab "Quill". [Move it here]" (§5.1).
    If it is live in a terminal (K6, `extsessions.go`), the box shows
    "running elsewhere · Take over", as on the topic card.
- **One live session per topic** (topic-tabs §3.2) still holds:
  `handoffOwner`/`topicOwner` count the hidden operator as the live `quill`
  session. The "Open a topic" card shows Quill as "live in the Quill
  operator", and its button opens the box instead of a tab. Every
  session-creating path that would set kmem name `quill` (card, phone
  `openTopic`, `--resume quill`, `--resume-all`, rotation) switches to the
  operator. `--resume-all` no longer reopens `quill` as a tab, because the
  operator starts on its own.
- fc6b73c ("the only always-running hidden session") is replaced by a new fact:
  two always-running hidden sessions, kmem-lock-audit and quill (§5.3).

### 3.2 The buttons: dot, badge, status

- Order in the toolbar: **Settings · Quill · kmem · PCs** (landscape toolbar,
  `placeToolbar`; portrait sidebar `desk.go:602-610`, which also gets the
  bubbles). The Quill button uses the app icon (`icon.go`) at button size.
- **Working dot** (colour `colRunning`, pulsing at most once a second) while the
  operator's turn runs or any of its subagents run. The idle dot (`colIdle`, as
  kmem's run dot) shows while it is merely running. No dot means stopped.
  The source is the te3470e status (thinking, tool, waiting for approval, idle,
  stopped) and `conv.Background()`.
- **Badge** with the same rules as kmem's reworked badge (t982851): only
  items that need the user. For Quill: the operator waits on the user (plan,
  question, permission, settings-change confirmation if Q6 asks for one);
  open `quill issue:` tasks (topic quill) from other sessions (Q17); an
  end-check that is waiting. Per-item seen times, Mark all read, and a setting
  to turn the badge off. The badge code becomes role-generic
  (`assistNeeds{role}`); kmem keeps its own items.
- Tooltip / status line in the box header: "Quill operator · working: Bash
  (2 subagents) · last activity 12 s ago", the te3470e indicator.
- Desktop notifications: `alert` for a hidden session uses its role's title
  ("Quill operator needs approval: …"). Clicking the notification opens the
  box. `seen` is true while the box is open, or docked, and showing it.

### 3.3 The assistant box (overlay state)

One mechanism, `assistbox.go`, shows one assistant at a time: kmem or Quill.

- **Layer.** The chat column becomes `Stack(chatHistory, boxLayer)`. The box
  covers the chat history area only. The header, tab row and main message box
  stay visible and keep working for the current tab (decision 5). The box is a
  rounded card (`bg(colInput, 12, …)`) with:
  - header: role title ("Quill operator" / "kmem assistant"), the status
    indicator, model, buttons (Quill: Checkpoint now, Restart, Stop operator;
    kmem: Save now, Restart, Stop assistant), **Dock** (§3.4) and Close (×);
  - for Quill: the operator's subagent strip (`agentStrip` bound to the
    hidden session);
  - the chat view (`newChatViewWith`, as `kmemAssistChat`; plan cards and
    choice replies render the same, fdd52bd);
  - **its own message box** at the bottom of the box: a full `sendEntry`
    with history keys, slash menu, autocorrect, voice, send, stop, and the
    operator's own model / mode / auto-approve chip, placeholder "Message the
    Quill operator" / "Ask kmem-lock-audit". The chat and input are built
    once per role and kept across open/close (unsent text stays).
  - kmem only: the panels (Overview, Topics, Tasks, Memory) as tabs in a left
    column inside the box, or a tab row on narrow widths with "Assistant" as
    one tab (Q9).
- **Focus.** Opening focuses the box's message box. Closing returns focus to
  the main message box. A visible accent outline marks the box with focus,
  so it is always clear which box the typing goes into.
- **Animation (open).** A plain rounded rectangle grows from the button's
  rect (in canvas coordinates) to the box rect, easing out (`fyne.NewAnimation`,
  `AnimationEaseOut`). The real content is not laid out per frame: it fades in
  over the last 30 % once the rect reaches full size. Chat views with long
  histories are expensive to relayout. **Close** plays the reverse: the
  content fades out, then the rect shrinks into the button. Switching from
  kmem to Quill while one is open cross-fades the content inside the open box
  without shrinking.
- **Duration** follows "Text animation speed": Slow 320 ms, Normal 220 ms,
  Fast 140 ms, Instant 0 (Q13). It is 0 when "Animate new text" is off, when
  **Reduce motion** is on (new switch in Settings → General, defaulting to the
  system setting where readable: GNOME `enable-animations`, Windows client-area
  animation; Q14), or when `d.animate` is false (tests). With 0 the box
  appears and disappears at once.
- **Closing:** ×, Escape (§3.6), clicking the same button again. Clicking the
  other assistant's button switches. Clicking a tab, a sidebar row, Ctrl+T or
  Ctrl+1-9 closes the box first (animated), then switches (Q16).
- **Hidden ≠ stopped.** Closing hides the box, and the assistant keeps
  running, as the kmem window does today.

### 3.4 Dock to side (panel state)

- **Dock** (header button, a pin icon) turns the box into a **right-side
  panel** next to the chat: `HSplit(chatColumn, panel)`, so the chat history
  and the main message box stay visible. The panel has the same content
  (header, strip, chat, own message box). kmem's panels become a tab row
  above its chat in the panel.
- **Width:** a drag handle; the default is 38 % of the chat area, minimum
  360 px, and the chat column keeps at least 480 px. If the window is too
  narrow for both, Dock is disabled ("window too narrow to dock") and a
  docked panel falls back to the overlay box until the window widens.
- **Switching:** Dock animates the box from its overlay rect to the right
  panel rect (the same rect animation, same duration rules). **Undock** (the
  same button) animates back to the overlay rect. The button's click on a
  docked assistant closes the panel (animated into the button). A docked
  panel stays open while you switch tabs (it is not over the history), unlike
  the overlay.
- **Remembered choice:** per assistant, `assistDock.quill` / `assistDock.kmem`
  (bool) and the width `assistDockW` (shared). The button opens the assistant
  in its last state. Whether it was open at quit is not restored, apart from
  a hot reload (`restoreState` keeps "box open: role, docked"), as the kmem
  window does today.
- **One assistant docked at a time.** Opening kmem while Quill is docked
  switches the panel's content (cross-fade), with the same remembered state.

### 3.5 Split view

- **Overlay state:** the box covers the history areas of **all panes**: one
  box over the whole pane row. Each pane's own message box stays visible and
  belongs to its pane. The box has its own message box. With two or three panes
  this is the only rect wide enough (Q12).
- **Docked state:** the panel sits right of the whole pane row. The panes
  share the remaining width (`evenColumns`). If that leaves a pane under its
  minimum width, the panel opens as the overlay instead, with a one-line note.
- Buttons: the toolbar's first-pane slot gets Settings · Quill · kmem (Q11
  weighs one "Assistants" button instead). The box grows from whichever
  button was clicked.

### 3.6 Keyboard

- **Ctrl+Shift+O** opens or closes the Quill operator and **Ctrl+Shift+K**
  opens or closes kmem (Q15). Both are registered on `d.win`'s canvas next to
  Ctrl+T.
- **Escape** inside the box, in order: an open popup or menu in the box, a
  selection in the box's chat, then the box closes. It **never interrupts** the
  operator. The box's stop button and Ctrl+Enter ("stop & send") in the box's
  input do that. Escape in the main message box keeps today's behaviour for the
  current tab. While the box is focused, `desk.escape` must not interrupt
  `d.current` (the box handles Escape first).
- Tab and Shift+Tab move between the main message box and the box's message
  box when both are visible (docked).
- The main input's hint line (`d.folder`) gains "Ctrl+Shift+O Quill ·
  Ctrl+Shift+K kmem".

### 3.7 The kmem window becomes the kmem box

- The separate window (`kmemwindow.go`) is replaced by the box (overlay or
  docked) with the same content. The panels use `p.parent()` = `d.win`, and
  dialogs open over the main window.
- `--kmem-window` and the IPC verb `kmem-window` open the kmem box. The
  Monday check-in keeps working.
- Window prefs `kmemWinW/H/Split` are no longer read. The split offset becomes
  the in-box panel width.
- Optional **Pop out** (Q10): opens the box content in a window as today,
  for multi-monitor use. Recommended: not in the first release.

### 3.8 Permissions: the settings command

Settings writes go through one small surface that Quill Desktop provides to
its operator, never by editing preference files.

- **Registry** `settingsreg.go`: each operator-settable setting is
  `{id, label (as in Settings), section, type (bool | enum | int range |
  string pattern), get, apply}`. `apply` runs on the UI thread and calls the
  same code the Settings control calls (then `settingsChanged`-style sync, so
  the dialog, chips and phones update). Only registered ids exist. Excluded,
  and refused with "this is a security setting; it's in Settings →
  Permissions → …": plan-first and auto-approve, read-only commands,
  permissions rules, phone pairing and control, Remote Control, update checks,
  sign-in, kmem setup and memory mode, the operator's own model setting (Q7).
- **Command** (the `quill` binary, through `quill.sock`):
  - `quill settings list [--json]` and `quill settings get ID [--json]` are
    read-only: they are added to the built-in read-only set in `readonly.go`.
  - `quill settings set ID VALUE --why "<the user's request>"` and
    `quill settings undo CHANGE_ID` are writes.
- **Who may write:** Quill Desktop gives the operator's process a per-launch
  random token in its environment (`opts.Env`, `QUILL_OPERATOR_TOKEN`), and
  the socket accepts `set`/`undo` only with it. Other sessions get "only the
  Quill operator changes settings; ask the user". On Linux the socket also
  checks the peer process (`SO_PEERCRED`) and its parent chain for the
  operator's Claude pid. Windows uses the token only.
- **Only on request** (decision 3; f3a1828): Quill Desktop refuses `set`/`undo`
  unless the operator's **current turn was started by the user** (a message
  typed in the box or sent from Quill Android, or a plan card answer), with
  the same test as `autoCovers`. Peer messages, task notifications and
  scheduled triggers never change settings. The plan-first gate applies as
  well: `set` is not read-only, so it needs the approved plan, or
  auto-approve (Q6).
- **Every change shows and can be undone.** Quill Desktop itself (not the
  model) inserts a **change card** into the operator chat: "Changed Text
  animation speed: Normal → Fast. [Undo]", with the `--why` text. A change log
  (`settingsChanges` pref, last 50: id, old, new, time, why, by operator) backs
  it. Undo restores the old value if the setting still has the value the change
  set; otherwise it says "changed again since; nothing undone". The box header
  has a "Changes" list. Changes the user makes in Settings are not logged here.
- **Settings role without a source folder** works the same. The developer role
  (repo writes) only exists with a source folder.

### 3.9 Gate interaction

- Plan-first, the approval scope f3a1828, plan cards fdd52bd, "Approved."
  f6fa284 and the root check all apply to the operator unchanged; the
  operator is an ordinary session to the gate.
- **Auto-approve** for the operator: the chip sits on the **box's** message
  box, with the text "Keep approving the Quill operator until I turn it off".
  Moving the tab into the box (§5.1) ends a tab's auto-approve. The root check
  (`autoRootRequest`) still needs a plan. Settings writes under auto-approve
  still need a user-started turn (§3.8) and still show change cards.
- End-check during Stop, rotation and Quit: `endCheckAllowed` as for topic
  tabs.
- kmem calls: `--as quill` is the operator's own name, so f6fa284's read-only
  `brief`/`rules` apply.

## 4. Quill Android

- **Bottom sheet.** A Quill button next to the kmem button at the top of the
  side list, with the same bubble and working dot. Tapping it opens a bottom
  sheet that rises from the bottom edge (`ModalBottomSheet`, half height,
  draggable to full height). The sheet holds the operator chat with its own
  input, the status line, the subagent summary ("3 subagents · 120k tok") and
  a "Changes" list with Undo. kmem's screen becomes the same kind of sheet
  (tabs Overview, Tasks, Memory, Assistant). The system "Remove animations"
  setting (animator duration scale 0) turns the sheet motion off. Landscape
  tablets may show the sheet as a right side sheet (the "docked" state).
- **Phone API** (Quill Desktop side, generalized from `phonekmemassist.go`):
  - `GET /phone/quill` gives `{state, status, waiting, needs, actions,
    changes[]}`.
  - Chat and controls mirror the kmem assistant routes:
    `/phone/quill/operator/{items, send, answer, interrupt, queue/{op}, start,
    stop, restart, checkpoint}`.
  - `POST /phone/quill/settings/undo {change}` (control mode).
  - Stream change `{"type":"quill", needs, operator:{state, waiting, epoch,
    rev}}`, plus the existing `settings` change.
  - Notification type `quill` ("Quill operator needs you").
  - hello `quillOperator: {api: 1}`.
  - `/phone/sessions` never lists the operator. `topics[]` lists `quill` with
    `live: "operator"`, and `openTopic quill` answers `{switched: true,
    operator: true}`.
- A message sent from the phone counts as user-started for §3.8 and f3a1828.
- **Older Quill Android:** the operator is hidden, so the Quill topic drops
  out of their session list and they can't reach it until they update (Q20).

## 5. Migration

### 5.1 Moving the current `quill` tab into the button

At the first start with the feature, if topic `quill` is live in a tab, the
Quill box shows "The Quill operator is open in the tab "Quill". [Move it
here]" (no automatic move). Move (Q1, recommended: **live move**):

1. Wait for a safe moment (no permission prompt open in the tab). Running
   subagents and turns may go on.
2. End the tab's auto-approve. Close the tab **without stopping** the process.
   Set `s.hidden`, `org.Hidden[id]`, keep `org.Kmem[id] = quill`, and save.
3. Attach the session's view to the box (`viewFor` returns the box's view).
   Its items, queue, strip and plan state come along, because they live on
   the `deskSession`.
4. Notice in the box: "Moved from the tab "Quill"; it keeps running here."
   The coordinator gets one line: "You now run hidden behind the Quill
   button as the Quill operator; see the operator hint."

If the tab is not running, the operator simply starts fresh from the briefing
and snapshot.

### 5.2 Code generalization (no visible change)

- `isHiddenSession` → `hiddenRole(s) string` ("kmem", "quill", ""), stored as
  `org.Hidden[id] = role` (old `true` reads as "kmem").
  Callers: `viewFor`, `seen`, `alert`, `ownerLabel`, `handoffOwner`,
  `hiddenID` (phone), reload handover and keeper (`Hidden` → role),
  `restore.go`, `deleteEmptySessions`, RC skips, usage pause. Walk the 34-row
  list of kmem-window §4 for the second role.
- The kmem-window cwd test stays for kmem. The operator's cwd is the source
  folder, so for transcripts with no `deskSession`, only `org.Hidden` marks it.

### 5.3 Facts and docs

- New fact replacing fc6b73c (via `--replaces`, after the user's approval):
  two always-running hidden sessions, kmem-lock-audit (kmem) and quill (Quill
  operator), both behind toolbar buttons.
- kmem config `sessions[quill]` keeps title "Quill"; topic-tabs.md §3.1
  table: Quill is "the Quill operator; not a tab".
- kmem-window.md §3 gets a note that the window became the kmem box.

## 6. Risks

- **Prompt injection into settings.** A web page or file could tell the
  operator to change a setting. Mitigations: registry allowlist without
  security settings, user-started-turn check, plan-first, the visible change
  card with Undo, and the change log.
- **Token cost of two always-running sessions.** The operator is a busy
  coordinator: it pays its briefing at every start, and its subagents run in
  the background. The working dot and the strip make this visible. Q2 can make
  it start on first open instead.
- **Hidden coordinator work goes unnoticed** (a plan waiting behind a closed
  button). Covered by the badge, working dot, notifications and phone events.
- **The wrong-target message** is avoided by the box's own message box
  (decision 5), the focus outline and distinct placeholders. A residual risk
  remains when docked with two inputs visible.
- **Animation cost** on long chats: only a plain rect is animated, and the
  content is laid out once.
- **The generalization of hidden sessions** touches about 30 places. A miss
  shows the operator in the sidebar or the phone list, or drops a notification.
  Mitigated by the role tests ported from `kmemwin_hidden_test.go`.
- **Live move edge cases:** plan state or a permission prompt mid-move, and
  auto-approve "in this tab". The move waits for no open prompt and ends
  auto-approve.
- **Token leaks to subagents.** Subagents run inside the operator's process,
  so their Bash calls carry `QUILL_OPERATOR_TOKEN`. That is acceptable: the
  gate tracks their calls (planscope) and the user-turn and change-card rules
  still apply.
- **Conflicts with the kmem window fixes** (t17ad60, t982851, te3470e) in
  `kmembadge.go`, `kmemassist.go` and `kmemwindow.go`. Phase order below.
- **Windows:** the AF_UNIX socket and token only, no peer check.

## 7. Phased plan (subagent-sized, file ownership)

Phase 0: the user answers §8, and the kmem window fixes t17ad60, t982851 and
te3470e merge.

Phase 1: refactors with no visible change.

| Sub | Work | Files owned |
|---|---|---|
| A | Hidden roles (`hiddenRole`, `org.Hidden` role values, viewFor/seen/alert/ownerLabel/handoffOwner/hiddenID, reload, keeper, restore) + tests | new `hidden.go`, `kmemwindow_stub.go`, `queue.go`, `notify.go`, `kmemcard.go`, `phoneapi.go` (filters only), `reload.go`, `restore.go`, `keeper_unix.go`, `keeper_windows.go` |
| B | Role-generic assistant lifecycle and badge (`assist.go` from `kmemassist.go`; `assistNeeds` from `kmembadge.go`); kmem behaviour unchanged | `kmemassist.go`, `kmembadge.go`, new `assist.go` |

Phase 2 (parallel after phase 1):

| Sub | Work | Files owned |
|---|---|---|
| C | Assistant box: overlay layer, own message box, grow/shrink animation, durations, Reduce motion setting, dock-to-side panel, remembered state, split view, Escape and shortcuts, toolbar and portrait buttons with dot and badge | new `assistbox.go`, `layoutmode.go` (`placeToolbar`, buttons), `desk.go` (`buildMain` chat stack, portrait buttons, General: Reduce motion), `shortcuts.go`, `escape.go`, `split.go` (panel width) |
| D | kmem in the box: panels in the box, window retired, `--kmem-window` → box | `kmemwindow.go`, `kmemwire.go`, `kmempanel.go` (parent), `background.go` (verb) |
| E | Quill operator role: start/stop/end-check/rotation hidden, elsewhere / take over, live move-in, `quillOperatorHint`, "Quill source folder" setting, `quill issue:` badge items, one-live paths | new `quilloperator.go`, `memorymode.go`, `topics.go`, `endcheck.go` (hidden rotation), `extsessions.go` |
| F | Settings command: registry, socket verbs, `quill settings` CLI, token, peer check, user-turn check, change log, change cards and Undo, read-only list/get | new `settingsreg.go`, `background.go` (verbs), `main.go` (subcommand), `readonly.go`, `planscope.go` / `autoapprove.go` (user-turn helper), `internal/session` (env) |

Phase 3:

| Sub | Work | Files owned |
|---|---|---|
| G | Phone API for the operator, settings undo, stream, notifications, hello, topics `live: operator` | new `phonequillop.go`, `phonekmemassist.go` (shared helpers), `phoneapi.go`, `docs/phone-api.md` |
| H | Quill Android: Quill button, bottom sheets for Quill and kmem, Changes + Undo | Quill Android repo (after G) |
| I | Migration notice and the fact update, doc notes (§5.3) | `quilloperator.go` (notice), docs |

The coordinator merges each in its own worktree, runs the full suite once at
merge, and the Quill Android work follows the desktop API.

## 8. Questions for the user

1. Moving the current Quill tab: live move of the same session (keeps context and running subagents) (recommended) / end-of-session check, then a fresh hidden session / ask each time.
2. When the operator runs: starts with Quill Desktop and keeps running, like kmem (recommended) / starts on the first click of the Quill button / runs only while its box is open.
3. Roles: settings for every user, developer work only when a Quill source folder is set (recommended) / developer only / settings only.
4. Operator working folder: the Quill source folder (recommended) / its own folder with the source added as an extra folder.
5. Settings channel: `quill settings` command over Quill Desktop's local socket with a per-launch operator token (recommended) / a tool Quill Desktop serves to the operator (MCP) / the operator only proposes and you click Apply.
6. Approving a settings change: covered by the approved plan in a turn you started, applied at once with a change card and Undo (recommended) / an extra Apply click on every change / no plan needed for settings.
7. Which settings the operator may change: everything except security settings (plan-first, auto-approve, read-only commands, permissions, phone pairing and control, Remote Control, updates, sign-in, kmem setup, its own model) (recommended) / all settings / appearance and text settings only.
8. Undo: a log of the last 50 changes, each undoable while the setting still has that value (recommended) / only the last change / 10 minutes per change.
9. kmem in the box: panels as tabs on the left inside the box, the chat on the right; narrow boxes get an "Assistant" tab (recommended) / chat only with a "Panels" button / keep kmem as its own window.
10. The old kmem window: replaced by the box, Pop out later if needed (recommended) / replaced with a Pop out button now / keep the window alongside.
11. Button space: Settings · Quill · kmem as three buttons, in split view in the first pane's toolbar (recommended) / one "Assistants" button with Quill and kmem tabs in the box / three buttons, collapsing to "Assistants" only when the toolbar is too narrow.
12. Split view, overlay state: one box over all panes' histories, pane message boxes stay (recommended) / box over the first pane only / leave split view while the box is open.
13. Animation length: from Text animation speed (Slow 320, Normal 220, Fast 140, Instant 0 ms), none when Animate new text is off (recommended) / fixed 200 ms / its own setting.
14. Reduce motion: a switch in Settings → General that follows the system setting by default (recommended) / switch only / system setting only.
15. Shortcuts: Ctrl+Shift+O Quill operator, Ctrl+Shift+K kmem, Escape closes the box (recommended) / Ctrl+O and Ctrl+K / no shortcuts.
16. Clicking a tab or sidebar row while the overlay is open: close the box, then switch (recommended) / keep the box open over the new tab.
17. Reports from other sessions: `quill issue:` tasks (topic quill) counted on the Quill badge (recommended) / SendMessage only / both.
18. Docking: remembered per assistant, a docked panel stays open across tab switches (recommended) / remembered once for both / always open as overlay.
19. Auto-approve for the operator: available on the box's message box, ended by the tab move (recommended) / not available for the operator.
20. Older Quill Android: the operator is hidden there until the phone updates (recommended) / keep listing it as a session for old phones.
21. Quill Android: bottom sheets for Quill and kmem with chat, status and Changes + Undo (recommended) / chat only first / in a later release.
22. Timing: phase 1 refactors start only after the kmem window fixes merge (recommended) / start sub A now, since it touches other files.
23. fc6b73c: replace it with "two always-running hidden sessions, kmem-lock-audit and quill", written after your approval (recommended) / keep kmem the only always-running one and start the operator on demand.

## User answers (2026-11-23)

1. Moving the Quill tab: move the running session as it is.
2. The operator starts with Quill Desktop.
3. Roles: settings for everyone; developer work only when a Quill source folder is set.
4. Working folder: the Quill source folder.
5. Settings channel: `quill settings` over the local socket with a per-launch token.
6. A settings change is covered by the approved plan and applied at once, with Undo.
7. The operator may change every setting except security settings.
8. Undo: a log of the last 50 changes.
9. kmem in the box: panels as tabs on the left, chat on the right.
10. Old kmem window: replaced by the kmem box, with a Pop out button (the box as its own window) in the first version.
11. Button space: three buttons (Settings · Quill · kmem).
12. Split view: one box over all panes.
13. Animation length: its own setting (not tied to Text animation speed).
14. Reduce motion: a switch that follows the system setting.
15. Shortcuts: Ctrl+Shift+O (Quill) and Ctrl+Shift+K (kmem).
16. Clicking a tab while the box is open: close the box, then switch.
17. Reports from other sessions: `quill issue:` tasks on the badge.
18. Docking: remembered per assistant.
19. Auto-approve for the operator: available; moving the tab ends the tab's auto-approve.
20. Older Quill Android: hide the operator until the phone updates.
21. Quill Android: bottom sheets with chat, status and a Changes list with Undo.
22. Timing: start after the kmem window fixes merge.
23. kmem fact fc6b73c: replace with "two always-running hidden sessions".
