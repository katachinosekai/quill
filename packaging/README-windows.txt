Quill Desktop for Windows
=========================

Start quill.exe. Open Settings → Setup: it shows what Quill needs and installs
it with your OK:

- Claude Code (Anthropic's official installer runs in a PowerShell window)
- your Claude sign-in
- Codex CLI and its existing sign-in, for Codex sessions
- a device password, so Quill for Android and other PCs can connect
- kmem, if you have it (shared memory: handoffs, tasks, rules). Setup can
  install Python 3 with winget when the selected kmem is a Python script.
- local open-weight models through Ollama or another server with an
  Anthropic-compatible API (optional)

Not on Windows yet: voice input and spell checking. Self-update isn't tested on Windows: to
update, replace quill.exe with the new one.

Quill is free software under the GPL-3.0-or-later; see LICENSE, NOTICE and
THIRD_PARTY_LICENSES.txt.
