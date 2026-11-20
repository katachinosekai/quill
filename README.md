# Quill

A desktop app for Claude Code sessions on Linux. Unofficial; not affiliated with Anthropic.

Quill drives the `claude` CLI you already have installed (headless, stream-json), so it uses your own login, plan, settings, memory and hooks. It never asks for an API key.

## Install

Download from [Releases](https://github.com/katachinosekai/quill/releases/latest):

- **Fedora / Nobara (RPM):** `sudo dnf install ./quill-<version>-1.fc44.x86_64.rpm`
- **Any Linux (AppImage):** `chmod +x Quill-<version>-x86_64.AppImage && ./Quill-<version>-x86_64.AppImage`

Quill needs the Claude Code CLI (`claude`) installed and signed in. Installed copies update themselves from signed releases (see Security).

## Features

**Sessions**
- Browser-style tabs (drag to reorder, middle-click to close, Ctrl+T new, Ctrl+1–9 to switch) with folder drop-downs, or a vertical sidebar
- Folders: Active, Inactive, Archive, Created by Claude and your own, with Format Rules for sorting; Active keeps a fixed order
- Split view (2 or 3 panes), each with its own full message box; Auto split shows your Active sessions side by side
- Rename, restart as a fresh session that takes over the job, delete, open in split, drag between folders
- Takes over a session that is open in a terminal when you send a message

**Chat**
- Streaming replies with thoughts inline, tool chips (hidden until you open them), diffs for edits
- Up/Down recalls earlier messages; messages sent while Claude is busy are queued, editable, and sent when it finishes (Ctrl+Enter sends now)
- Centering options for chat, typing, status and tabs; width sliders for chat and message box
- Image, audio and video previews with a built-in video player
- Live offline speech to text (whisper.cpp, 99 languages); your voice never leaves the computer

**Approvals**
- Allow / Deny prompts inline, answer buttons for Claude's questions
- Plan first: Claude lists everything it will do, you approve once (a typed "yes" works); read-only commands pass without asking
- Auto-mode blocks are shown with an Approve and retry button

**Reliability**
- Live self-update with session hand-off; a crash keeper keeps Claude sessions running if the window crashes and reconnects them
- Usage panel with plan limits and a banner when a limit is reached
- Optional crash reports: you choose what to redact (optionally with a tool-less Claude agent) and review the exact text before it's sent

## Quill for Android

[Quill for Android](https://github.com/katachinosekai/quill-android) is a companion app for your phone or tablet that uses Claude Code's Remote Control to reach your sessions. It mirrors Quill's tabs and folders (synced from Quill over your local network or a secret gist) and updates itself from signed releases. Each Quill release ships the matching Android APK.

## Security

- **Signed updates:** releases carry `SHA256SUMS` and an ed25519 `SHA256SUMS.sig`. Quill installs an update only if the signature matches the key built into the app, the file matches the signed checksum and GitHub's digest, and the version is strictly newer. Downloads go over HTTPS to GitHub only.
- **Local first:** speech to text, media previews and crash-report redaction run on your computer. Link previews only fetch public internet addresses.
- **Phone sync:** HTTPS with a pinned certificate and a 256-bit pairing key.
- **Private files:** hand-over and crash files live in your private runtime folder.
- Dependencies are checked with `govulncheck`.

Report security problems privately through GitHub's security advisories for this repo.

## Build from source

Requires Go, CMake, a C/C++ compiler and the Fyne build dependencies (Wayland or X11 development headers).

```sh
scripts/build-whisper.sh                 # once: builds the bundled whisper.cpp
go build -tags wayland ./cmd/quill       # or without the tag for X11
./quill
```

`scripts/build-packages.sh` builds the RPM and AppImage in a Fedora container and signs the release folder. Notes on the bundled Fyne patch, the keeper, updates and signing are in `COMMENTS.md`.

## License

MIT. Bundled components keep their own licenses (Fyne BSD-3-Clause, whisper.cpp MIT, malgo/miniaudio, rsc.io/qr).
