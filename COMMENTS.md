# Claude Desk design notes

Go code in this repo carries no comments; notes live here.

## Build and test

- `export PATH=$HOME/.local/go/bin:$HOME/go/bin:$PATH`
- GUI build on this machine: `go build -tags wayland ./cmd/claude-desk` (no X11 dev headers installed). Without the tag, anything that compiles `cmd/claude-desk` (vet, test) fails on missing X11 headers, so use the tag there too.
- Full check: `go vet ./internal/... && go vet -tags wayland ./cmd/... && go test -race -tags wayland ./...`
- Screenshots: `DESK_SHOTS=1 go test -tags wayland -run Screenshots ./cmd/claude-desk/` writes `.shots/{history,permission,centered,terminal,new-session}.png` (git-ignored). They are rendered off-screen by Fyne's test driver with fixture transcripts and the fake claude.

## Layout

- `internal/protocol`: stream-json line parsing (`Event`), control request/response builders (initialize, interrupt, set_permission_mode, set_model, remote_control, can_use_tool answers).
- `internal/chat`: conversation model shared by live streams and saved transcripts (`Conversation.Apply(event)` returns the items that changed). Item kinds: user, assistant (streams via text deltas), tool (one-liner with summary, input, result, diff for Edit/Write/MultiEdit), permission, notice, error, thinking.
- `internal/transcript`: scans `~/.claude/projects/*/*.jsonl` (read-only, cached by mtime+size) and loads one transcript into a `chat.Conversation`.
- `internal/session`: one headless `claude` process per app session.
- `internal/live`: finds sessions open in a terminal.
- `internal/fakeclaude`: a scripted stand-in for the CLI. Test binaries re-exec themselves with `CLAUDE_DESK_FAKE=1` to become it. Prompts: `write` (Write + permission prompt), `demo` (thinking, text, Bash with result, Edit + permission prompt), `slow` (streams until interrupted), anything else echoes. `FAKE_ARGS_OUT` records argv, `FAKE_IGNORE_EOF` simulates a hung process.
- `cmd/claude-desk`: Fyne UI (`desk.go` sidebar, header, bottom bar, process wiring; `chatview.go` conversation rendering; `widgets.go` max-width layout, Enter/Shift+Enter entry; `theme.go` dark theme).

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

`--remote-control` is for interactive sessions only, but a headless session accepts control_request `{subtype:"remote_control", enabled:true|false}`; enabling returns `session_url` and `connect_url` (verified for real). The header has a per-session "Remote Control" checkbox, off by default; turning it on starts the process if needed and shows an "Open remotely" link. It resets to off whenever the process restarts.

## Sessions open in a terminal

`~/.claude/sessions/<pid>.json` (pid, sessionId, cwd, procStart). A session counts as open elsewhere when that pid is alive, not a zombie, and its `/proc/<pid>/stat` start time equals procStart; the app's own children are excluded. Such sessions are shown read-only with a note (input, pickers and Remote Control disabled, send refuses to start a second process). Their transcript reloads when the file changes (5 s poll).

## Transcripts and titles

Title order: custom title, ai-title record, summary, first prompt. Side-chain and meta records, command caveats, system reminders and task notifications are skipped; slash commands show as `/name args`. Message count = user + assistant text items. Groups are by the cwd recorded in the transcript, newest activity first. Long histories show the last 200 items with a "Show N earlier" button.

## UI notes

- Chat column is centered with a max width of 820 px. "Center message text" (sidebar, saved in preferences) centers user/assistant text; tool cards and diffs stay left-aligned.
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
