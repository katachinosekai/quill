# Quill

A desktop app for Claude Code sessions. Unofficial; not affiliated with Anthropic.

Quill drives the `claude` CLI you already have installed (headless, stream-json), so it uses your own login, plan, settings, memory and hooks. It never asks for an API key.

## Features

- Sidebar of all your Claude Code sessions, grouped by folder, with search and live status
- Streaming chat with folded tool calls, diffs for edits, and thinking indicators
- Allow / Deny permission prompts inline
- Model and permission-mode pickers, Stop button, several sessions at once
- Takes over a session that is open in a terminal (or a background Claude process) when you send a message
- Remote Control toggle per session
- Account status with sign in / sign out

## Build

Requires Go and the Fyne build dependencies.

```sh
go build -tags wayland ./cmd/quill   # or without the tag on X11
./quill
```

`--claude <path>` picks a different CLI binary.

## License

MIT
