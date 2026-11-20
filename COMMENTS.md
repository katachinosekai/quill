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

Plan-first approval (default on, sidebar "Plan first, then approve all", preference `planFirst`; `planGate`): the first `can_use_tool` of a request is denied with `planAskMessage` (nothing runs; Claude is asked to list every command/change and stop), shown as a small "held" line. When that turn ends, a Plan card appears ("Approve plan & run all" / "Don't run"). Approving sends `planGoMessage` and sets `runAll`, so every `can_use_tool` until the next `result` is allowed automatically (shown as "Approved with the plan"). With the option off, prompts work one by one as below.

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

Remote Control names carry Quill's folder (rcnames.go, user 2026-11-19, for the Android app in Projects/quill-android). For every running session with Remote Control on, Quill sends control_request `{subtype:"rename_session", title:"<Folder> · <Title>", source:"host"}`. It does this after RC turns on and on every `rebuildRows` when the name changed (move, rename, automatic demotion, folder rename). `rowPlace` records which top folder each session landed in. The CLI relays the title to claude.ai (verified for real: both names showed) and also appends a `custom-title` record to the transcript, which the user accepted. It counts as a user rename, so the CLI stops auto-titling that session, and `claude --resume` in a terminal shows the prefixed title. That is why sessions are only renamed once they have a real title (`transcript.Info.Named`: custom/ai/summary title, or a Quill rename); prompt-only sessions wait for their ai-title. `sideOrg.RC[id] = {sent, base}` is saved before sending, and when a transcript title equals `sent`, Quill shows `base` instead (`unRCTitle` in `refresh`), so its sidebar never shows the prefix and prefixes never stack. A different title (renamed on claude.ai) is taken as the new base. Failed renames are not retried until the name changes.

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

Go can't hot-swap code, so a new build takes over in place. `watchBinary` polls the running executable (symlinks resolved) every second; once it changed, is at least 1 s old, executable and starts with ELF magic, `trySwap` runs when nobody typed for 15 s (or right away after `quill --reload`), no permission prompt is open and no takeover is in progress. Claude sessions are NOT stopped: `session.Process.Handoff` stops the reader cleanly (read deadline, keeps a partial line), clears CLOEXEC on the child's stdin/stdout pipe ends (via SyscallConn, never `File.Fd()`, which would switch the pipe to blocking mode and break read deadlines; `Attach` sets the fds non-blocking (and close-on-exec) again before `os.NewFile` so they stay pollable; after an exec (`Handoff.FromExec`) it reuses the inherited fd, inside the same process (failed exec, tests) it dups it so two os.Files never share one fd, otherwise the SECOND handoff hung forever: bug seen 2026-11-19; Handoff also gives up waiting for the reader after 3 s) and returns the pid + fd numbers; they go into $TMPDIR/quill-restore-<pid>.json together with the session, draft, window size, scroll and Remote Control state, then `syscall.Exec`s the new binary. The new process (`applyRestore`) reloads each handed session's transcript, then `session.Attach`es to the same child (same pid, still our child, so wait4 works) and keeps streaming, even mid-turn; Remote Control stays on because the CLI process never restarted. Child stderr goes to a temp log file (not a pipe), so nothing can block while Quill restarts. If exec fails, the sessions are re-attached in the same process. Sessions that could not be handed off are resumed with --resume (`restoreState.Running`). Build with `go build -o dist/quill.tmp ... && mv dist/quill.tmp dist/quill`, then `dist/quill --reload`.

## Background mode (background.go)

`quill --reload` sends "reload" over the instance socket: the running Quill swaps to the new build as soon as no turn is running, skipping the 15 s typing wait (`forceSwap`). Use it after every build.

Closing the window hides it (`confirmClose` -> `hideWindow`) when a system tray is available (`desktop.App`); sessions keep running. Tray menu: Show Quill, Quit (Quit shows the window and goes through `confirmQuit`, which asks before stopping running sessions, then `app.Quit`). Single instance: $XDG_RUNTIME_DIR/quill.sock; a second launch sends "show" and exits, the running one shows its window and answers "ok". After a live update (exec) the new process skips the check (QUILL_RESTORE set), re-creates the socket, and stays hidden if it was hidden (`restoreState.Hidden`, started with `app.Run()`).

## UI notes

- Local slash commands (localcmds.go): the headless CLI rejects interactive-only commands ("/upgrade isn't available in this environment"), so Quill handles /upgrade (opens claude.ai/upgrade), /login, /logout, /clear (new session in the same folder), /model <name>, /exit|/quit itself before anything is sent; they are listed first in the / suggestions and replace the CLI's entries of the same name.

- Auto-scroll: the chat follows new messages/thoughts/steps while the view is at the bottom (`follow`); scrolling up (OnScrolled, not caused by Quill itself) stops following, and a glowing "Latest" button (pulsing accent behind it) appears at the bottom of the chat; clicking it glides down to the newest content (320 ms ease, `glideBottom`) and resumes following.

- Speech to text (stt.go): mic button above Send. Fully local: `pw-record` (16 kHz mono) -> `ffmpeg` clean-up -> whisper.cpp `whisper-cli -nt -np` with `~/.local/share/quill/models/ggml-base.en.bin` (overrides: QUILL_WHISPER, QUILL_WHISPER_MODEL); the temp audio folder is deleted after transcription, the text is appended to the input (never sent automatically). First click shows a privacy notice (nothing collected or sent to Anthropic; preference `sttNoticeShown`). If the engine is missing, a dialog shows the one-time setup command (`sttSetupCommand`: clone whisper.cpp v1.7.6, CPU build, download the model). Status shows on the live line ("● Listening...", "Turning speech into text...").

- Flicker: never refresh the whole main pane on chrome updates; `setLabel`/`setText` and the select/usage guards only touch widgets whose content changed (Fyne re-lays out containers when a WIDGET's min size changes, but not for canvas.Text, so the header is refreshed explicitly when its subtitle or Remote Control text changes).

- Centered Markdown lists: Fyne's list layout ignores alignment, so in Center mode lists become paragraphs prefixed with "• " / "1. " (`centerLists`, nested lists indented); Left mode keeps real lists.
- Message layout: message text alignment is switchable in the sidebar ("Text: Left / Center" radio next to the Remote Control option; preference `centerText`, default Center); user messages in a dark full-width bubble, Claude's replies plain (the left/right split was removed).

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
- Sessions sit directly under the main folders (Active, your folders, Inactive, Archive, Created by Claude); project subfolders are off by default (user 2026-11-19) and come back with "Group by project" in any folder's right-click menu (preference `groupByProject`).
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

## Bundled Fyne (third_party/fyne)

Quill builds against a patched copy of Fyne v2.8.2 in `third_party/fyne` (go.mod `replace`). The only change is in `widget/entry.go`: an `Alignment` field on `Entry`, applied to the text and placeholder segments, and `CursorPosition` adds `rowAlignOffset` so the cursor lines up with centered text (clicks and selection already handle alignment upstream). The diff is `third_party/fyne-entry-align.patch`.

To upgrade Fyne: copy the new version from the module cache into `third_party/fyne` (without `_test.go`/`testdata`), re-apply the patch (`patch -p1 -d third_party/fyne < third_party/fyne-entry-align.patch`), fix any rejected hunks by hand, run the tests (`TestCenteredTyping`).

## Built-in speech to text (internal/whisper)

whisper.cpp v1.7.6 is bundled in `third_party/whisper.cpp` (CPU backend only; the GPU backends, examples, tests and bindings were left out) and linked statically through cgo. Run `scripts/build-whisper.sh` once before `go build`/`go test`; it builds the static libraries into `third_party/whisper.cpp/build` (git-ignored) with AVX2/FMA/F16C so the binary runs on any x86-64 CPU from about 2013 on. `-f` forces a rebuild.

Microphone capture uses malgo (miniaudio), which loads PipeWire/PulseAudio/ALSA at runtime, so users need no extra packages. Models are downloaded inside Quill on first use into `~/.local/share/quill/models`: `ggml-base.en-q5_1.bin` for English, `ggml-base-q5_1.bin` for every other language (about 57 MB each).

Live transcription (`live.go`): audio stays in memory; every 400 ms the audio since the last pause is re-transcribed with `audio_ctx` shrunk to the clip length (the main speed-up), nothing runs during silence, and a phrase is committed after 0.7 s of quiet or 20 s of speech. The model is freed 5 minutes after the last use.

## Crash keeper (cmd/quill/keeper.go)

Starting `quill` runs a small keeper process that launches the window as a child (env `QUILL_KEEPER` = keeper socket). The window sends each Claude session's stdin/stdout pipes to the keeper over the socket (SCM_RIGHTS) when a session starts, when an approval request arrives or is answered, and drops them when the session ends; it also reports the open session. Because the keeper holds copies of the pipes, Claude never sees EOF when the window dies. If the window exits abnormally (signal other than TERM/INT/HUP, or a non-zero code) the keeper starts a new window with the pipes as extra files (fd 3, 4, ...) and a restore file, and the window re-attaches exactly like after a live update. Restarts back off from 0.5 s to 30 s but never stop, so sessions only end when Quill itself ends them. A normal quit (exit 0) closes the keeper's copies. A live update from a window started before the keeper existed execs into the keeper with the handed-off pipes (`keeper.adopt`), so protection starts without a restart; if the keeper cannot start, the update falls back to the plain window path. `QUILL_NO_KEEPER=1` disables it.

Sessions re-attached after a crash are no longer children of the window, so `session.Attach` falls back to polling the PID (`waitForeign`) when `Wait` returns ECHILD.
