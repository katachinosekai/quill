# Quill design notes

Go code in this repo carries no comments; notes live here.

## Build and test

- `export PATH=$HOME/.local/go/bin:$HOME/go/bin:$PATH`
- GUI build on this machine: `go build -tags wayland ./cmd/quill` (no X11 dev headers installed). Without the tag, anything that compiles `cmd/quill` (vet, test) fails on missing X11 headers, so use the tag there too.
- Full check: `go vet ./internal/... && go vet -tags wayland ./cmd/... && go test -race -tags wayland ./...`
- Real transcript probe: `QUILL_PROBE_TRANSCRIPT=<jsonl> QUILL_PROBE_OUT=<dir> go test -tags wayland -run TestRealTranscriptProbe ./cmd/quill/` renders a real session off-screen (bottom and scrolled up) and logs the render time.
- Screenshots: `DESK_SHOTS=1 go test -tags wayland -run Screenshots ./cmd/quill/` writes `.shots/{history,permission,centered,terminal,new-session}.png` (git-ignored). They are rendered off-screen by Fyne's test driver with fixture transcripts and the fake claude.

## Layout

- `internal/protocol`: stream-json line parsing (`Event`), control request/response builders (initialize, interrupt, set_permission_mode, set_model, remote_control, can_use_tool answers).
- `internal/chat`: conversation model shared by live streams and saved transcripts (`Conversation.Apply(event)` returns the items that changed). Item kinds: user, assistant (streams via text deltas), tool (one-liner with summary, input, result, diff for Edit/Write/MultiEdit), permission, notice, error, thinking.
- `internal/transcript`: scans `~/.claude/projects/*/*.jsonl` (read-only, cached by mtime+size) and loads one transcript into a `chat.Conversation`.
- `internal/session`: one headless `claude` process per app session.
- `internal/live`: finds sessions open in a terminal and releases them for takeover.
- `internal/account`: auth status / login / logout through the CLI.
- `internal/fakeclaude`: a scripted stand-in for the CLI. Test binaries re-exec themselves with `CLAUDE_DESK_FAKE=1` to become it. Prompts: `write` (Write + permission prompt), `demo` (thinking, text, Bash with result, Edit + permission prompt), `slow` (streams until interrupted), anything else echoes. `FAKE_ARGS_OUT` records argv, `FAKE_IGNORE_EOF` simulates a hung process.
- `cmd/quill`: Fyne UI (`desk.go` sidebar, header, bottom bar, process wiring; `chatview.go` conversation rendering; `widgets.go` max-width layout, Enter/Shift+Enter entry; `theme.go` dark theme).

## How a session is driven (verified with two tiny real haiku runs, fixtures in internal/protocol/testdata)

`claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages --permission-prompt-tool stdio [--resume <id> | --session-id <uuid>] [--model M] [--permission-mode M]`

- cwd = project folder, own process group, the user's normal login (no API key, no env changes). An `initialize` control_request is sent right after start.
- New sessions get a fresh UUID via `--session-id`, so the sidebar row, the transcript file and the process agree from the first message. Past sessions are resumed with `--resume <id>` on the first send; the process then stays up between turns.
- CLI permission modes: acceptEdits, auto, bypassPermissions, manual, dontAsk, plan. The app offers default (flag omitted), acceptEdits, plan, auto. Model picker: default (flag omitted), fable, opus, sonnet, haiku. Changing either on a running session sends set_model / set_permission_mode.
- Messages are user lines `{"type":"user","message":{"role":"user","content":text}}`.

## Permission prompts

- `--permission-prompts host` alone does not route anything to the app (the real Write was denied). With `--permission-prompt-tool stdio` the CLI emits `control_request` subtype `can_use_tool` (tool_name, input, tool_use_id, permission_suggestions).
- Answer: `control_response` success with `{behavior:"allow", updatedInput}` or `{behavior:"deny", message}`. "Allow always (this session)" also sends `updatedPermissions`: the CLI's own suggestions re-targeted to destination `session`, or an `addRules` rule for the tool name. Verified for real.
- `control_cancel_request` and the end of a turn mark open prompts "No longer needed". Other incoming control requests get an error reply so the CLI never waits.

## Stop, close, lifecycle

- Stop button: control_request `interrupt` (real run ended with terminal_reason `aborted_streaming`); SIGINT only if the request fails. An interrupted turn shows an "Interrupted" notice, not an error, and "No response requested." filler is hidden.
- Shutdown (`Process.Stop`): interrupt if busy, close stdin, wait, SIGTERM to the group, SIGKILL. Closing the window with running sessions asks first, then stops them all in parallel.
- Switching sessions only switches the view; processes keep running and their events keep updating their own conversation.
- Status dots: running (turn in progress), waiting for you (pending permission), idle (process up, no turn), error (process exited with an error), open in a terminal.

## Remote Control

The header shows status only ("● Remote Control on" / "Remote Control off" / "Remote Control..." while connecting; no link, user 2026-11-19); there is no per-session checkbox any more (user 2026-11-19). `setRemoteControl` stays for tests and future use.

`--remote-control` is for interactive sessions only, but a headless session accepts control_request `{subtype:"remote_control", enabled:true|false}`; enabling returns `session_url` and `connect_url` (verified for real). The header has a per-session "Remote Control" checkbox, off by default; turning it on starts the process if needed and shows an "Open remotely" link. It resets to off whenever the process restarts. "Remote Control for every session" (sidebar, preference `autoRemoteControl`, default ON, user 2026-11-19) turns it on quietly (no chat notice) every time Quill starts a Claude process (`autoRemote` from `ensureProcess`), so it survives restarts and live updates.

## Sessions open in a terminal (takeover)

`~/.claude/sessions/<pid>.json` (pid, sessionId, cwd, procStart, kind, status). A session counts as open elsewhere when that pid is alive, not a zombie, and its `/proc/<pid>/stat` start time equals procStart; the app's own children are excluded. Exiting an interactive CLI session can leave it held by a background process (`kind:"bg"`, `claude bg-spare`), which is why "exited in Konsole, still blocked in Quill" happened.

Input stays enabled with a note. Sending takes over (`desk.takeOver`): `live.Finder.Release` runs `claude stop <sessionId>` for bg sessions, otherwise SIGTERM, waits up to 5 s, then SIGKILL; then the transcript is reloaded and the session is resumed with the message. If the file says `status:"busy"` a confirm dialog comes first. The Remote Control checkbox stays hidden until the takeover. Transcripts of held sessions reload when the file changes (5 s poll).

## Account

`internal/account`: `claude auth status --json` (refreshed at start and every minute), `claude auth login` (prints any URL it outputs into a dialog link; cancellable), `claude auth logout` (confirmed). Header top-right button shows "email · Plan" or a highlighted "Sign in"; clicking it when signed in opens a menu (org, Refresh, Sign out).

## New session defaults

The dialog shows the folder plus a collapsed "Options" (model, permissions). The defaults are model "default" and mode "auto" (matching how the user runs the CLI), and the last choices are saved in preferences (`newModel`, `newMode`).

## Transcripts and titles

Title order: custom title, ai-title record, summary, first prompt. Side-chain and meta records, command caveats, system reminders and task notifications are skipped; slash commands show as `/name args`. Message count = user + assistant text items. Groups are by the cwd recorded in the transcript, newest activity first. Long histories show the last 200 items with a "Show N earlier" button.

## Live update (reload.go)

Go can't hot-swap code, so a new build takes over in place: `watchBinary` polls the running executable (symlinks resolved) every second; once it changed, is at least 1 s old, executable and starts with ELF magic, `trySwap` waits until no session is running/waiting or being taken over and nothing was typed for 15 s (`lastTyped`; retried on every 5 s refresh) (re-checked after each `result`), stops idle Claude processes (they resume on the next message), writes $TMPDIR/quill-restore-<pid>.json (session, draft, window size, scroll; the draft and scroll are captured again right before exec) and `syscall.Exec`s the new binary with QUILL_RESTORE set. Startup consumes it (`takeRestore` / `applyRestore`); sessions that were running before the swap (`restoreState.Running`) are resumed right away (process restarted with --resume, Remote Control comes back on via autoRemote). Build with `go build -o dist/quill.tmp ... && mv dist/quill.tmp dist/quill` so the file swaps atomically. Remote Control is off after a swap (process restart).

## Background mode (background.go)

Closing the window hides it (`confirmClose` -> `hideWindow`) when a system tray is available (`desktop.App`); sessions keep running. Tray menu: Show Quill, Quit (Quit shows the window and goes through `confirmQuit`, which asks before stopping running sessions, then `app.Quit`). Single instance: $XDG_RUNTIME_DIR/quill.sock; a second launch sends "show" and exits, the running one shows its window and answers "ok". After a live update (exec) the new process skips the check (QUILL_RESTORE set), re-creates the socket, and stays hidden if it was hidden (`restoreState.Hidden`, started with `app.Run()`).

## UI notes

- Flicker: never refresh the whole main pane on chrome updates; `setLabel`/`setText` and the select/usage guards only touch widgets whose content changed (Fyne re-lays out containers when a child's min size changes).

- Message layout: all message text is centered (preference `centerText`, default on); user messages in a dark full-width bubble, Claude's replies plain (the left/right split was removed).

- Text sizes: general UI text is one size (13 px, caption = text); user and assistant messages use `msgTheme` (16 px) via a ThemeOverride (`msgText`). A live status line under the chat frame, bottom left, outside the scroll (`chatView.live`, `liveText`; fixed height so the chat never jumps) shows "✻ Thinking...", the running tool, or "✻ Working..." while a turn runs (hidden while reply text streams).

- Usage panel (usage.go) in the blank space right of the chat, vertically centered, 36 px from the chat column (shown when the margin is >= 230 px and the full panel fits in height, `usageLayout`): plan usage from the CLI's `rate_limit_event` (`rate_limit_info.unifiedWindows.five_hour / seven_day`: utilization, resetsAt; last value saved in preference `rateLimit` and shown "as of"), this session's TRUE total tokens (in + out + cache read + cache write, from its transcript: `transcript.Info.Usage`, assistant `message.usage` deduplicated per message id) plus context, and today's / last 7 days' true totals on one line (compact, user 2026-11-19) across all transcripts (`Info.Days`, keyed by local date).

- Key hints (Enter send, Ctrl+Enter stop & send, Shift+Enter new line, / commands) sit in small grey text, right-aligned on the stats line under the input (`fitRight`: hidden when there is no room, so small windows stay usable) (where the folder name used to be; the folder is already in the header). The empty input just says "Message Claude".

- The chat bounds show as a thin outline only (`chatView.frame`, transparent fill, white stroke ~11% alpha); the scroll container sits INSIDE the outline so messages never draw over or under it (the scrollbar is inside the frame). No shadows: `ColorNameShadow` is transparent in the theme, which removes the dark overlay Fyne's scroll container draws at the top edge when scrolled (popups lose their drop shadow too).

- Slash commands (commands.go): the list comes from the `initialize` control response (`commands`: name, description, argumentHint). `session.FetchCommands` gets it at startup from a throwaway process in $HOME (no prompt, so no transcript; it does fire SessionStart hooks); a running session's own list (`Process.Commands`, includes project skills) replaces it. Typing `/word` shows up to 8 matches (prefix first, then substring) inline above the input (NOT a Fyne PopUp: overlays take keyboard focus, so typing froze while the list was open); Up/Down, Tab completes, Enter completes or sends when the full command is already typed, Esc closes.
- Steps live in the chat: consecutive tool steps collapse into one wrapping row of chips (✓ green done, ✗ red failed, … running + tool name; `stepRun`/`flowLayout`); clicking a chip opens that step's input/output inline under the row in `smallTheme` (`toggleStep`, state per *chat.Item). The separate steps panel below the input was removed (user 2026-11-19).
- Performance: stream events update the Conversation immediately but the UI is redrawn at most every 50 ms (`queueUI`/`flushUI`, batched per session; results, permission prompts, init and suggestions flush at once). Rendered chat views of the last 8 sessions are cached (`sessView`; dropped when the conversation object changes, the center option changes, or the session gets events while not shown). Opening a session renders the last 80 items.
- Ctrl+Enter = stop the running turn (interrupt, wait for the ack) and send. Plain Enter while Claude works sends immediately; the CLI delivers it between steps of the current turn.
- Sidebar slides open/closed (220 ms ease, `slideLayout`, fixed 330 px, no drag-resize); small accent arrows (`smallArrow`). The chat column is 65% of the space next to the sidebar (`chatWidth`: (window - sidebar) x 0.65, at least 560 px for readability; panes narrower than that are filled completely), so it grows when the window grows or the sidebar closes, and stays centered on the window. User messages are plain selectable labels (Markdown made every line its own paragraph with big gaps); only messages containing a ``` code block use the Markdown renderer. Tool/thinking headers and notices wrap (`foldHead`).
- The chat column and input are centered on the WINDOW (`windowCentered` shifts by half the sidebar width, clamped to the pane).
- Sidebar collapses/expands with ONE arrow (`sideToggle`) that rides the sidebar's right edge (left-pointing when open, right-pointing when closed; `slideLayout` positions it). During the slide the chat width is frozen at its final value (`chatFreeze`) so text is not re-wrapped every frame (smooth); preference `sidebarHidden`.
- Organisation (org.go, preference `sidebarOrg` JSON): per-session placement overrides (`active` / `inactive` / `archive` / `auto` / `f:<folderID>`), renamed titles (Quill-only; transcripts are never written), renamed built-in/project folder names (keyed by folder key), and custom folders (top level, after the active projects, ordered by latest activity, sessions newest first; empty ones stay visible as drop targets). Rows are `sideItem` widgets: left click opens a session / toggles a folder, right click opens the session menu (Rename, Resume, End session, Set active/inactive/archive/Created by Claude, Move to folder > ..., New folder, Automatic placement, Restore original name) or the folder menu (Rename, New folder, Restore original name, Delete folder = sessions back to automatic). Drag a session onto a folder (or onto another session to join its folder); the drop target is highlighted. End session stops Quill's own process, or (after a confirm) releases a terminal/background holder.
- The session list uses `compactTheme` (13 px titles, tight padding) with row heights 40 (session) / 26 (project folder) / 34 (top folder).
- Sidebar is a folder tree (no arrows; folder / open-folder icons, tap to toggle, `folderOpen`): active sessions (current, running, held by a terminal, or used in the last 30 min) grouped in project folders at the top, then top-level folders Inactive (other sessions from the last 7 days, open by default), Archive (7+ days, closed) and Created by Claude (closed), each with project subfolders. Search opens everything. Sessions whose transcript starts with entrypoint `sdk*` (headless, started by Claude/scripts) and that Quill did not create go to Created by Claude. Quill remembers the sessions it created (preference `createdSessions`, last 2000) so they stay in the main list.

- User messages always show in full (collapsing was removed at the user's request). Markdown code blocks are replaced by `wrapCode` segments (wrapped monospace in a dark card) because Fyne's CodeBlockSegment never wraps.
- Prompt suggestions: sessions run with `--prompt-suggestions true`; after a turn the CLI emits `{"type":"prompt_suggestion","suggestion":...}` (only while stdin stays open, which it does here). `Conversation.Suggestion` holds it until the next user message; the input shows it as the placeholder "... (Tab to use)" and Tab on an empty input fills it in (`sendEntry.AcceptsTab`).
- Chat column is centered with a max width of 760 px (shorter lines read better). Text 15 px, line spacing 7. Vertical spacing per item kind lives in `spaced` (chatview.go): user turns get a little space above (8/4), tool rows the least; user bubbles use `bgPad` for wider side padding. "Center message text" (sidebar, saved in preferences) centers user/assistant text; tool cards and diffs stay left-aligned.
- Single newlines in assistant text become paragraph breaks outside code blocks and lists (`hardBreaks`), since Fyne's markdown would join them.
- Thinking blocks render as a collapsed "Thought" row (expand to read). Current models often send thinking with empty text; those add no row, only "thinking..." in the stats line.
- Edit/Write/MultiEdit show as line diffs (first 40 lines, "Show N more"). Answered permission cards collapse to their outcome.
- Any item whose height changes forces a relayout of the chat column (otherwise expanded rows overlapped their neighbours).
- Tests drive the desk through `desk.do` / `desk.doWait` (normally `fyne.Do` / `fyne.DoAndWait`), swapped for a global mutex, because the Fyne test driver runs `fyne.Do` inline on the calling goroutine.

## What tests cover

- protocol: parsing of the two real runs (stream events, results, usage/context, can_use_tool, remote control responses), message builders.
- chat: real-run replay, streaming deltas, permission items, thinking items, diffs, user-text cleanup.
- transcript: synthetic and real transcripts, scanner grouping and cache.
- session (fake claude): args, permission round-trip for allow / allow always / deny, interrupt + next turn on the same process, remote control and mode/model requests, clean stop, stop escalation to signals, stop while busy interrupts first.
- live: liveness and procStart matching.
- cmd (fake claude, Fyne test driver): sidebar grouping/search/terminal row, read-only terminal session refuses to start claude, resume + permission prompt answered by tapping the real button + interrupt via the Stop button, two new sessions running side by side and surviving a switch, stop-all, Enter vs Shift+Enter, fixture rendering, hard breaks.

## Gaps / v2 ideas

- No real end-to-end run through the GUI itself (process wiring is covered with the fake; the protocol with real fixtures).
- Thinking text, images, subagent (side-chain) activity and todo lists are not shown.
- No attachments, slash-command palette, session rename/delete, or per-tool "always" rules beyond what the CLI suggests.
- Transcript reloads are full reloads (fine for normal sizes; very large transcripts load in the background).
- Terminal detection relies on `~/.claude/sessions/*.json`; if the CLI changes that format the "open in a terminal" state disappears (sessions then look idle, and sending would start a second process on the same id).
