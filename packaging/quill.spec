%global debug_package %{nil}
# No LTO: Fedora's default -flto objects (whisper.cpp, built through cgo) carry
# .gnu.debuglto_ sections that cgo's LDFLAGS check rejects (fedora:44 with
# redhat-rpm-config 344, 2026-11-22).
%global _lto_cflags %{nil}

Name:           quill
Version:        0.1.27
Release:        1%{?dist}
Summary:        Desktop app for Claude Code sessions
License:        GPL-3.0-or-later AND MIT AND BSD-3-Clause AND Apache-2.0 AND ISC AND (MIT-0 OR Unlicense) AND (Unlicense OR BSD-3-Clause)
URL:            https://github.com/katachinosekai/quill
Source0:        %{name}-%{version}.tar.gz
ExclusiveArch:  x86_64

Requires:       python3
Recommends:     hunspell
Recommends:     hunspell-en-US
BuildRequires:  golang >= 1.26
BuildRequires:  gcc
BuildRequires:  gcc-c++
BuildRequires:  cmake
BuildRequires:  make
BuildRequires:  desktop-file-utils
BuildRequires:  mesa-libGL-devel
BuildRequires:  libX11-devel
BuildRequires:  libXcursor-devel
BuildRequires:  libXrandr-devel
BuildRequires:  libXinerama-devel
BuildRequires:  libXi-devel
BuildRequires:  libXxf86vm-devel
BuildRequires:  wayland-devel
BuildRequires:  libxkbcommon-devel

%description
Quill is a desktop app for Claude Code sessions. It runs the claude CLI in
the background and gives each session a chat window, a session sidebar,
approvals, live updates and built-in offline speech to text. Requires the
claude CLI to be installed separately.

%prep
%autosetup

%build
export GOFLAGS="-mod=vendor -trimpath"
export CGO_ENABLED=1
sh scripts/build-whisper.sh
go build -ldflags "-X main.version=%{version}" -tags wayland -o quill-wayland ./cmd/quill
go build -ldflags "-X main.version=%{version}" -o quill-x11 ./cmd/quill

%install
install -Dm755 quill-wayland %{buildroot}%{_libexecdir}/quill/quill-wayland
install -Dm755 quill-x11 %{buildroot}%{_libexecdir}/quill/quill-x11
install -Dm755 packaging/quill.sh %{buildroot}%{_bindir}/quill
install -Dm644 packaging/io.github.katachinosekai.quill.desktop %{buildroot}%{_datadir}/applications/io.github.katachinosekai.quill.desktop
install -Dm644 cmd/quill/Icon.png %{buildroot}%{_datadir}/icons/hicolor/256x256/apps/io.github.katachinosekai.quill.png
install -Dm644 cmd/quill/icon.svg %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/io.github.katachinosekai.quill.svg

%check
desktop-file-validate %{buildroot}%{_datadir}/applications/io.github.katachinosekai.quill.desktop

%files
%license LICENSE NOTICE THIRD_PARTY_LICENSES.txt
%doc README.md
%{_bindir}/quill
%{_libexecdir}/quill/
%{_datadir}/applications/io.github.katachinosekai.quill.desktop
%{_datadir}/icons/hicolor/256x256/apps/io.github.katachinosekai.quill.png
%{_datadir}/icons/hicolor/scalable/apps/io.github.katachinosekai.quill.svg

%changelog
* Mon Nov 23 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.27-1
- Quill operator: Quill Desktop's own assistant behind the Quill button (settings with Undo, explanations; developer role with a Quill source folder, which now defaults to the checkout Quill Desktop runs from)
- Assistant box: undocked, it takes over the main message box (own drafts and suggestions, name chip, accent frame) and dims the rest; click outside or press Esc to close, click a tab to close and switch
- Approvals waiting in the Quill operator or kmem assistant turn their button amber and get their own notification, also on the phone
- Tray menu: Restart Quill Desktop, keeping sessions running
- Copy image: right-click an image in the chat or the viewer (needs wl-clipboard or xclip)
- Plan and choice cards: options with commas or colons stay whole; questions stay live through status replies
- Approved subagents keep their approval when resumed
- Bundles kmem 1.10.2 and Quill Android 0.9.38
* Mon Nov 23 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.26-1
- Languages: topic titles in any language; autocorrect follows the system language (with a picker); dates, sorting and search follow your language; voice input auto-detects the language with larger models
- Whole-letter text streaming
- Plan cards: recommended option highlighted, warning above the approval in a highlighted box, numbered steps on separate lines, buttons also when sources follow the question
- "Keep approving in this tab" auto-approve (root commands and other sessions' requests still need a plan)
- Stricter and more capable read-only gate
- Always-visible status line (also in the kmem window); the kmem window loads once per refresh and badges only what needs you; one kmem assistant at a time
- Windows keep their size; optional KDE rules remember window positions; device lock survives live reloads
- Crash reports recognise graphics driver resets
- Bundles kmem 1.9.3 and Quill Android 0.9.38
* Sun Nov 22 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.25-1
- Claude can coordinate with subagents: asked at startup (Welcome, or once after updating); Settings keeps the choice
- Updates keep your settings; only new, changed or removed features that need your input ask, once each, in one dialog (also answerable from Quill Android)
- Hints: ask before building; coordinate with subagents (the chat stays free, features go to subagents); plan-first covers subagents
- The kmem window, with the kmem assistant (kmem-lock-audit): it runs in the background from start with both developer and operator roles; closing the kmem window only hides it; dot on the kmem button; Stop assistant in the window
- kmem bubble replaces the banner, the recycle bin moves to Settings; kmem version under the message box, with a coloured "update"
- Hidden chips leave no blank space
- Plan card: options on the next line, approval by quick-choice Yes, numbered questions answered in the reply, a Show as written toggle, inline lettered options are checkboxes
- Portrait: tokens and context % in the session list rows
- Replies stream character by character
- Phone: settings API for Quill Android; password-free unlock on the trusted home network with hardware-key proof
* Sun Nov 22 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.24-1
- Smoother streaming: one paragraph layout, no blink, a steady reveal, and text keeps the message font size while it streams
- Centered text scales with the window: an overlong line wraps into even lines, wide lists are capped at 75% of the chat, and lists, lettered lists ("a.", "b)") and plan questions line up as one centered column (left-aligned with centering off)
- A line that starts with inline code is centered correctly
- Plan card: "(recommended)" next to the recommended option, which is picked by default; questions keep inline code; "with ..." no longer splits an option
- One Chips menu; Model, Mode, Voice and Tasks open menus like Online; the split-view button matches the stop button and sits above it
- Queued messages follow centering; message times redraw at once when turned on or off and sit closer to their message
- A fresh session loads kmem without a plan round; typed approvals like "Approve the plan, also ..." count
- Phone connection: gzip, more time for large pages, keep-alive, HTTP/2 and streaming deltas
- quill --reload takes a build waiting behind the update line; kmem 1.7.0
- Bundles Quill for Android 0.9.24: reuses its connection to Quill Desktop (far fewer handshakes on mobile data/VPN); folder/usage sync works with the device password and no longer runs when paired; downloads only the new words of a streaming reply; centered lists keep their shape (markers lined up, one centered column; left-aligned with centering off), lettered lists, plan questions keep inline code, centered queue
* Sun Nov 22 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.23-1
- Much less memory and CPU: the bundled Fyne parsed the fonts again for every chat message (1.5 GB and over a full CPU core after a few hours); now each font is parsed once (265 MB, idle CPU near 0); a safety net keeps the garbage collector from spinning
- Faster, leaner session scans (start-up allocations -85 %) and phone change tracking (-90 %)
- Claude desktop app sessions: their titles from the app, scratch workspaces shown as "Claude desktop app (scratch)"
- kmem panel: Claude's own auto memory (MEMORY.md) for the current project; recycle bin items open in their own window with the full reason and text, Restore and Delete for good
- kmem 1.6.3; renaming a tab also moves its kmem label and project
- Bundles Quill for Android 0.9.20
* Sun Nov 22 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.22-1
- A session that runs out of memory (a game or build it started) no longer takes Quill Desktop and the other sessions down: each Claude session runs in its own systemd scope
- Message times under each message (Settings: on/off, the system's 12/24-hour format or your own pattern with a preview)
- Message box: auto formatting next to autocorrect (capital letters, — dashes, Shift+Enter list continuation; Backspace undoes it); the Welcome asks about both
- Setup and Welcome: optional remote access through a VPN (Tailscale, NetBird, ZeroTier, Headscale, WireGuard, or Claude Code Remote Control only); skipped by default
- Settings cover every Welcome choice (new-session model, speech to text on/off)
- Plan cards: fixes from a two-model review (questions, numbering, options, approvals); plans are asked to number their questions by Quill itself
- kmem 1.6.2 bundled; renaming a tab records who renamed the handoff
- Bundles Quill for Android 0.9.19
* Sun Nov 22 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.21-1
- Replies are formatted while they stream in (no raw ** or # any more): finished paragraphs fade in, the last one is revealed word by word; Settings → General → "Animate new text" turns the animation off
- Plan cards find the approval question among a plan's numbered closing questions, so such plans get the card (with a control for each question)
- Bundles Quill for Android 0.9.18
* Sun Nov 22 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.20-1
- Plan cards answer their questions: an option marked recommended goes with Approve ("Other answers…" shows the rest), one Approve per option otherwise, and plans that ask several things get a control for each (Yes/No, options, checkboxes, a text field) with all answers sent together
- Phone API: plan items carry their choices and questions, and approvals can carry the answers (Quill for Android and Other PCs use them)
- Quill Desktop never updates or restarts on its own: a quiet line under the header offers Update / What's new / Later, and a new build on disk waits for "Restart into it"
- Quill's default kmem rules ask sessions to number a plan's open questions
- Bundles Quill for Android 0.9.18
* Sat Nov 21 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.19-1
- kmem 1.6.1 bundled: handoffs follow renames, stricter kmem-guard
- Welcome on new installs: online or local model, speech-to-text language (or off), ask first or let Claude work, kmem or Claude's auto memory, and Quill's default kmem rules (recommended for beginners) or none (advanced)
- Optional default kmem rules in Settings → Memory, installed only when you have no rules file
- kmem banner: items in the recycle bin and audit problems that wait for you
- Other PCs: Reload button for remote sessions; a blank reply heals itself with one reload; an older answer can't overwrite a newer one
- Empty messages no longer leave blank rows in the chat
- A typed "yes" approves a plan whose question has a note after it
- Bundles Quill for Android 0.9.16
* Sat Nov 21 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.18-1
- kmem 1.5.3 bundled: kmem-guard also checks commands from Claude Code's PowerShell tool (what Claude Code uses on Windows); the kmem-guard hook's matcher gets PowerShell, existing hooks are updated when Quill Desktop updates an older kmem (settings backed up first), and reads hook input sent as UTF-8 with a BOM or as UTF-16
- Other PCs: scrolling, clicks and any key keep the connection active
- Bundles Quill for Android 0.9.15
* Sat Nov 21 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.17-1
- kmem 1.5.1 bundled, now also on Windows: the Windows zip carries kmem, Setup sets it up (and offers Python 3 through winget when it's missing), hooks run through Python
- quill --resume NAME opens one kmem session (the live one, or a fresh one from its handoff) in the running Quill Desktop
- Option buttons for questions that mark their options "(option 1)", "(A)" and so on, however long; unmarked options may have up to 16 words
- Bundles Quill for Android 0.9.15
* Sat Nov 21 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.16-1
- Settings → Setup: what Quill Desktop needs, with one button each: Claude Code (Anthropic's installer, after asking), sign-in, kmem, python3 and hunspell (through your package manager, after asking), device password and local models; shown at first start when something required is missing
- Open-weight models: Local models window with Ollama status, suggested tool-capable models to pull with progress and larger-context copies, and other local servers with an Anthropic-compatible API (llama.cpp, vLLM, LM Studio, LiteLLM) whose models appear under Local
- Plan card for closing "Should I …?" proposals; questions that name options keep their buttons
- Phone API: each session's prompt suggestion (for Quill for Android)
- Windows: a zip with quill.exe, licences and a README; built without local paths; kmem isn't available on Windows yet
- Release signing covers the Windows files
- Bundles Quill for Android 0.9.14
* Sat Nov 21 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.15-1
- Sidebar lists live sessions grouped by kmem project; one identity for tab, session and kmem name; kmem panel with handoffs, tasks, memory and recycle bin; Auto-handoff settings row
- kmem is the default memory for Quill sessions; Quill bundles kmem 1.4.0 and updates an older installed kmem when it starts
- Slash commands fitted to Quill and kmem: /export (clipboard, file, kmem handoff), /rewind (files only; warns about kmem changes and offers to release claimed tasks), /memory (Quill's memory choice first), /permissions (add rules, Plan-first and Workspace tabs), /add-dir (session, project or settings), /hooks (kmem hooks marked and repairable), /mcp (tools, clear sign-in), /agents (create, edit, delete, kmem briefing), /chrome (Enabled by default), /rename, /remote-control
- Online/Local button opens a menu with Remote (Remote Control for the session)
- Other PCs: pair another Quill Desktop and see its live sessions as tabs; it asks the device password to connect, keeps them in memory only and disconnects when idle (15 min by default)
- Device password for every paired device, phones included (set it in Settings → Sessions → Devices; Quill for Android 0.9.10 or later asks for it)
- Windows build (make windows)
- Message box: Up and Down move through lines first, the draft is kept, two quick presses leave a draft for earlier messages
- Plan cards only for approval questions; either/or questions get option buttons
- Phone API: projects, handoffs, session menu actions, notification text, unlock
- Escape closes the open popup or dialog; reload never swaps while typing; message box cursor fixes; autocorrect, clickable links and many review fixes
- Bundles Quill for Android 0.9.11
* Sat Nov 21 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.14-1
- Select and copy chat text: drag over formatted text and across messages (snaps to lines and words), double-click a word, Ctrl+A, Copy button or Ctrl+C; clicks elsewhere clear it
- Right-click a message: copy, rewind to here (optionally undoing file changes), fork from here
- Session tools menu and slash commands: /rewind, /fork, /export, /diff, /tasks, /mcp, /chrome, /permissions, /memory, /hooks, /agents, /add-dir, /copy
- One tabbed Settings window
- Sessions running background tests show as running; queued messages can be edited (down or Escape undoes), sent now or reordered by dragging
- Smoother chat: plans stay in place, streaming markdown formats as it arrives, plan cards show Claude's question, two-option questions get buttons
- Auto-handoff also rotates once the prompt cache has gone cold and never during background tasks; forks don't take over the kmem identity
- Local models: correct context window after switching between Claude and a local model

* Fri Nov 20 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.13-1
- Auto-handoff: at 50% context Quill offers to rotate the session through its kmem handoff, at 75% it rotates on its own at a safe point (thresholds from kmem)
- Local models through Ollama: one Online/Local button next to the model picker, live model list (tool-capable models only), the model's real context window, no auto mode, a clear error when Ollama is down, and a "Remote Control for local sessions" setting
- Approval cards follow the centering setting
- Tabs glow in their status color like the sidebar
- Live updates keep queued messages, every tab's draft, prompt suggestions, each session's model and mode, and restarts in progress

* Fri Nov 20 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.12-1
- kmem sessions (when kmem is installed): new sessions can start a new kmem or resume a saved handoff, the sidebar shows each session's kmem name, and Restart as new session saves the handoff, resumes from it in a fresh session and deletes the old one
- Sessions started from a kmem-resume prompt are linked to their kmem automatically
- Prompts approved with the plan no longer show a line in the chat
- Pair phone: the instructions point to the scanner in Quill for Android (⚙ → Scan pairing QR code), since phone camera apps can't open pairing links

* Fri Nov 20 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.11-1
- Plan cards: replying with step numbers that cover every step (like "1-4" on a 4-step plan) approves the plan

* Fri Nov 20 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.10-1
- Plan-first lets far more read-only commands through without a plan round (quoted patterns, cd, variables, git -C, journalctl, tools in ~/.local/bin), plus your own list under "Read-only commands..."
- Auto-mode blocks: Approve and retry is one click; new "Always allow..." adds permission rules for this session or all sessions

* Fri Nov 20 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.9-1
- Approve and retry: the command you confirm goes straight to the normal Allow prompt (no longer gets stuck behind plan-first), and retrying never cancels a plan you already approved

* Fri Nov 20 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.8-1
- Usage for paired phones (5-hour and weekly limits, credits, token totals, per-session status) for Quill for Android's usage strip

* Fri Nov 20 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.7-1
- Plan card stays until you answer it or send a new request (no longer disappears when other turns end)

* Fri Nov 20 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.6-1
- Approvals always reach the session that asked (fixes plan approval sent to the wrong session); a typed "approve"/"yes" works mid-turn; Claude's limit message in every chat; usage credits status; split-view drag and drop; name field for new tabs; sharper pairing QR

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.5-1
- Desktop and phone notifications for approvals, questions, plans, finished and failed sessions; tray shows Quill

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.4-1
- Rounded folder drop-downs with right-click menus, horizontal usage strip, Phone pairing QR, lower memory use

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.3-1
- Plan-first approval fixes, queued messages, compact chat, security update (golang.org/x/image 0.43)

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.2-1
- Signed self-updater, split view message boxes, tab shortcuts, crash reporter, security fixes

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.1-1
- Phone folder sync for Quill for Android (pairing QR, local network, synced file, secret gist); tabs, split view and layout updates

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.0-1
- First packaged release
