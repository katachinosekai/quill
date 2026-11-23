# Links in Quill Android's chat (design)

Status: design, 2026-11-22, open questions not decided yet. User decision
f7fe6b5 ("Quill Android links like Quill Desktop ... design first"). Base:
Quill Desktop master c21db01, Quill Android master 1c0c4ff (0.9.35). Scope:
both apps. No code in this document's commit.

Related: `docs/phone-api.md` (pairing, unlock, control, errors),
`docs/design/kmem-android.md` (capability and security pattern this follows).

## 1. Goal and non-goals

Goal: in Quill Android's chat, links behave like in Quill Desktop's chat:

1. Bare URLs, Markdown links and absolute file paths in messages are tappable.
2. Web links (http/https) open in a new page of the phone's browser (an
   external intent), never inside Quill Android.
3. Files and other non-web links open in an in-app viewer; file contents come
   from Quill Desktop over the phone API.

Non-goals: no writing, renaming, deleting or uploading files from the phone; no
directory browsing; no proxying of web pages through Quill Desktop; no file
access while Quill Desktop is unreachable; no change to `quillPromptHint`.

## 2. Current state

### Quill Desktop

- `autolink.go`: `autolinks()` turns bare `http://`, `https://` and `file://`
  URLs into Markdown links, outside fenced blocks, indented code and inline
  code spans; trailing `.,;:!?*_` is dropped; existing `[x](url)` and `<url>`
  stay as they are. Cases in `autolink_test.go`.
- `chatview.go` `markdown()`: Fyne `RichTextFromMarkdown` renders the links;
  a tap calls `app.OpenURL` for any scheme (browser for web, the system
  handler for `file://`).
- `media.go` `findMedia()`: absolute or `~/` paths and URLs with an image,
  audio or video extension, which exist as regular files, get previews under
  the message (at most 6); tapping opens the lightbox (`viewer.go`) or mpv.
  Other absolute paths (for example `/home/user/Projects/demo/main.go`) are
  shown as plain text: not tappable today.
### Quill Android

- `Markdown.java` parses `[text](url)` into runs with `link`; no bare URL or
  path detection.
- `MarkdownView.java` makes link runs a `ClickableSpan`: only http/https open
  (`ACTION_VIEW`), every other scheme does nothing.
- No file or image viewer; the closest pattern is `KmemScreen`'s full-screen
  `Dialog`.

## 3. Link detection (shared rules)

One rule set, implemented in Go (Quill Desktop) and Java (Quill Android), fed
by one case file `cmd/quill/testdata/links.json` (input text, expected links
with kind and range). Quill Android keeps a copy in its test resources; a unit
test fails when its copy's SHA-256 differs from the one recorded in
`docs/phone-links-cases.sha256` of the Quill Desktop version it targets.

Where links are found

- In prose only: not in fenced blocks, indented code or inline code spans
  (same as `autolinks`). Exception for paths: see Open question 2.
- In `user`, `assistant`, `plan` and `notice` text, tool `summary`, and
  `diff.path` (the tool header becomes tappable). Not in `thinking`.

Kinds

| kind | match | target |
|---|---|---|
| web | `https?://` + `[^\s<>()\[\]`"']+`, trailing `.,;:!?*_` dropped | browser |
| file URL | `file://` + absolute path, percent-decoded | file viewer |
| path | `/` or `~/` + at least one more `/`-separated segment, chars `[^\s<>"'`()\[\]*]`, trailing `.,;:!?` dropped | file viewer |
| md link | `[label](target)`: target classified by the rows above or §5 | by target |
| other scheme | `scheme:` in a Markdown link target only | §5 |

Path details

- Not a path: a lone `/`, `//...`, things that look like a fraction or a ratio
  (`1/2`, `and/or`: no leading `/`), URL paths inside a web link.
- A suffix `:N` or `:N-M` (line or range, as Claude writes `main.go:120`) is
  kept as a hint, not part of the path (Open question 3).
- `~` is expanded on Quill Desktop, never on the phone (the phone sends the
  text as written).
- Relative paths (`src/main.go`) are not links (Open question 4).
- At most 50 links per item; the rest stay plain text.

Shared test cases (first set; each is a row of `links.json`)

~~~
Open https://login.example.com/a/gb77 now.      -> web "https://login.example.com/a/gb77"
see **https://x.io/a**                          -> web "https://x.io/a"
(https://x.io/p)                                -> web "https://x.io/p"
run `curl https://x.io` first                   -> none
[docs](https://x.io/d) and <https://x.io/e>     -> web d, web e
file:///tmp/a%20b.png, ok                       -> file "/tmp/a b.png"
edited /home/user/Projects/demo/main.go.        -> path "/home/user/Projects/demo/main.go"
see ~/notes/todo.md:12                          -> path "~/notes/todo.md" line 12
and/or 1/2 /                                    -> none
```fenced /home/user/x```                       -> none
[the plan](/home/user/Projects/demo/PLAN.md)    -> path, label "the plan"
[call](tel:+10000000000)                        -> other "tel:..."
~~~

## 4. Web links

- Allowlist: `http` and `https` open directly on tap, like Quill Desktop.
- How: `Intent.ACTION_VIEW` with `CATEGORY_BROWSABLE`,
  `FLAG_ACTIVITY_NEW_TASK` and `Browser.EXTRA_CREATE_NEW_TAB = true`, without
  `EXTRA_APPLICATION_ID` (so browsers don't reuse one tab for Quill). No
  WebView, no Custom Tab inside Quill Android.
- Never auto-open: only a tap opens; nothing opens on render, on a
  notification or when a stream item arrives.
- Long-press: menu "Copy link" / "Share link" / "Open".
- Misleading labels: when a Markdown label itself looks like a URL with a
  different host than the target, the tap shows a confirm with the real URL.
- Addresses that only exist on the PC (`localhost`, `127.0.0.0/8`, `[::1]`,
  `0.0.0.0`): Open question 9.

## 5. Other link schemes

| scheme | behaviour |
|---|---|
| `file:` | file viewer (§6), same rules as a path |
| `mailto:`, `tel:`, `sms:`, `geo:` | confirm dialog showing the full target, then `ACTION_VIEW` (`ACTION_SENDTO` for mailto) |
| `quillpair:` | not a link (pairing goes through the scanner only) |
| `intent:`, `android-app:`, `javascript:`, `data:`, `content:`, `vbscript:` | never opened; long-press can copy |
| anything else | confirm "Open `<scheme>` link with another app?", then `ACTION_VIEW`; no app: toast |

## 6. File links: phone API

### 6.1 Capability

`POST /phone/hello` gains `"files": {"api": 1, "on": true}`; `on` says the
file switch (§7.6) allows this device right now. Older Quill Desktop leaves it
out.

### 6.2 GET /phone/session/{id}/file?path=...

Read-only. `path` is the link target exactly as the phone found it (with `~`,
without the line hint), URL-encoded, at most 4096 bytes.

```json
{"v": 1, "path": "/home/user/Projects/demo/main.go", "name": "main.go",
 "size": 5120, "mtimeMs": 1795000000000, "kind": "text", "lang": "go",
 "text": "package main\n...", "truncated": false}
```

- `kind`: `text` (with `text`, `lang` from the extension, `truncated`),
  `image` (with `mime` and `data`, standard base64), `binary` or `too_large`
  (metadata only, nothing of the contents).
- Text: valid UTF-8 with no NUL in the first 8 KiB. Images: `png`, `jpg`,
  `jpeg`, `gif`, `webp`, `bmp` whose first bytes match
  (`http.DetectContentType`); `svg` is sent as text. Everything else: binary.
- Limits (Open question 7): text sent up to 256 KiB, cut at a line boundary
  (`truncated: true`); images up to 8 MiB; larger files `too_large`.
- The hidden kmem assistant session gets the same handler at
  `/phone/kmem/assistant/file` (phase 4).

Errors (the usual `{"error": {code, message}}`):

| HTTP | code | when |
|---|---|---|
| 400 | `bad_request` | empty, relative, too long, NUL, `..` segment, non-UTF-8 |
| 404 | `not_found` | not mentioned in this session (§7.1), missing, or not a regular file |
| 403 | `file_denied` | outside the allowed roots or on the deny list (`message` names the rule, not the contents) |
| 403 | `files_off` | the file switch is off for this device |
| 429 | `rate_limited` | §7.7 |
| 409 | `file_busy` | another file request of this phone is still running |

A path not mentioned and a missing file get the same 404, so the endpoint
can't be used to probe which files exist.

## 7. Security

This endpoint lets a phone read files from the PC. Every rule below holds on
Quill Desktop; Quill Android's checks are only for display.

### 7.1 Which paths are allowed

All of these must hold:

1. Mentioned: the path appears in that session's items as the phone API
   sends them (text, tool summary, diff path), found by the same detector as
   §3 (Open question 1 decides whose messages count).
2. Under an allowed root after resolving symlinks: the session's folder or
   the user's home (Open question 5 for `/tmp` and others).
3. Not on the deny list (§7.2), checked on both the path as written and the
   resolved path.
4. A regular file (not a directory, device, FIFO, socket); never under
   `/proc`, `/sys`, `/dev` or `/run`.

### 7.2 Deny list (never served, even when mentioned)

- Directories: `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.azure`, `~/.kube`,
  `~/.docker`, `~/.config/gcloud`, `~/.config/gh`, `~/.password-store`,
  `~/.local/share/keyrings`, `~/.pki`, browser profiles (`~/.mozilla`,
  `~/.config/google-chrome`, `~/.config/chromium`,
  `~/.config/BraveSoftware`, `~/.config/vivaldi`, `~/.var/app/*` browser
  apps), `~/.claude` credentials (`.credentials.json`), Quill Desktop's own
  preferences and TLS key (phones, keys, device password hash), kmem's data
  directory (kmem has its own routes), any `.git/` internals.
- File names (case-insensitive): `.env`, `.env.*`, `*.pem`, `*.key`, `*.p12`,
  `*.pfx`, `*.jks`, `*.keystore`, `*.kdbx`, `id_rsa*`, `id_ecdsa*`,
  `id_ed25519*`, `.netrc`, `.pgpass`, `.git-credentials`, `.npmrc`, `.pypirc`,
  `credentials*`, `*secret*`, `*token*`, `local.properties`.
- Contents: a text file whose first 256 KiB contain `-----BEGIN` ... `PRIVATE
  KEY-----` is refused (`file_denied`, "looks like a private key").
- The list lives in one Go table with a test per entry; extending it needs no
  phone change. User-added entries: Open question 6.

### 7.3 Symlinks, `..` and races

- A `..` segment in the request is a `400`, not cleaned away.
- `~` and `file://` are expanded, then `filepath.Clean`, then
  `filepath.EvalSymlinks`; root and deny checks run on the result (and the
  written path), so a link in the project pointing at `~/.ssh/id_ed25519` is
  denied.
- The resolved path is opened with `O_NOFOLLOW`; `fstat` must show a regular
  file with the same device and inode as the `stat` of the resolved path,
  else `404`. The read stops at the size limit even if the file grows.

### 7.4 Pairing and unlock

- Behind the existing phone server: pinned certificate, `X-Quill-Key`
  (revoked: `403 forbidden`), `X-Quill-Unlock` when the device must unlock
  (`401 locked`). A file read does not extend the unlock idle timer (only
  writes do).
- Other Quill Desktop PCs paired as devices: Open question 8.

### 7.5 Control mode

It is a read, so control mode is not the gate; the file switch (§7.6) is.

### 7.6 The file switch

Quill Desktop → Devices, per device: "Let this device open files"; its
default is Open question 10. Off: `403 files_off`, and Quill Android shows
"Opening files is off in Quill Desktop → Devices" in the viewer. Changing it
sends a `control`-style stream change so the capability updates live.

### 7.7 Rate limits

- One file request at a time per phone (`409 file_busy`).
- At most 2 per second and 60 per 10 minutes per phone; at most 64 MiB of
  file data per phone per 10 minutes (`429 rate_limited`, `Retry-After`).

### 7.8 Audit

- Every request (served or refused) appends a line to Quill Desktop's
  `phone-files.log` in its config directory: time, device id and name, session
  id, path, size, result code. Kept at 2000 lines; never sent to any phone.
- Quill Desktop → Devices shows the last 20 lines per device.
- A desktop notification per served file: Open question 11.

### 7.9 Data on the phone

- Contents are kept in memory only while the viewer is open; never written to
  disk, the share sheet or the clipboard as a file. Selecting and copying
  text is allowed.
- The viewer sets `FLAG_SECURE` (no screenshots, hidden in recents): Open
  question 12.

## 8. The in-app viewer (Quill Android)

- A full-screen `Dialog` (the `KmemScreen` pattern) opened by a tap on a file
  or path link; Back closes it. Header: file name, the path shortened with
  `~`, size and modified time; buttons Copy path, Reload, Close.
- Text: monospace, line numbers, wrap on/off, scroll to the line hint.
  Syntax-light colouring by `lang`: comments, strings, numbers and a short
  keyword list for go, java, kotlin, python, js/ts, sh, json, yaml, toml, c;
  everything else plain. Markdown files show rendered (`MarkdownView`) with a
  Raw toggle. `truncated`: a bar "Showing the first 256 KiB of 3.1 MB".
- Images: decoded with `inSampleSize` to the screen size, pinch zoom and pan.
- `binary`: "Binary file, not shown" with name, size and type.
  `too_large`: "Too large to show (41 MB)". Both offer Copy path only.
- Errors in place: not mentioned / missing ("Quill Desktop can't find this
  file"), `file_denied` (its rule), `files_off`, `rate_limited` (with retry
  time), offline (below).

## 9. Offline and old Quill Desktop

- Web links and other schemes work with no Quill Desktop connection.
- Quill Desktop unreachable: a tap on a file link opens the viewer with
  "Quill Desktop is not reachable" and Retry; nothing is cached across opens.
- Older Quill Desktop (no `files` capability): paths and file URLs are shown
  as plain text (not tappable), like today; long-press offers Copy path.
- Capability `api` higher than Quill Android knows: "Update Quill Android to
  open files".
- Locked device: the tap goes through the normal unlock prompt first.

## 10. Phases

Sizes are rough line counts of new or changed code including tests.

| Phase | What | Quill Desktop | Quill Android |
|---|---|---|---|
| 1 | Shared detector rules and `links.json`; Quill Android bare URL linkify, scheme handling, new browser page, long-press menu | S (~120, case file + Go test against `autolinks`) | S-M (~300) |
| 2 | File endpoint, capability, allow/deny rules, symlink handling, limits, audit log, file switch in Devices | M (~500) | - |
| 3 | Viewer: text, images, binary/too large, errors; path links tappable when the capability is on | - | M (~500) |
| 4 | Syntax-light colouring, Markdown render toggle, line hints, kmem assistant file route | S (~80) | S-M (~300) |

Order: phase 1 can ship alone. Phase 2 lands on Quill Desktop before phase 3.

## 11. Tests

Go (Quill Desktop, `cmd/quill`)

- `links_test.go`: every row of `testdata/links.json` through the Go detector;
  `autolinks` stays consistent with it for web and file URLs.
- `phonefiles_test.go` (table, temp home with placeholder files under
  `/home/user` style paths): allowed file; unmentioned file (404); other
  session's path (404); `..` (400); symlink from the project into a fake
  `~/.ssh` (403); symlink loop (404); every deny-list entry (403); file with
  a fake `PRIVATE KEY` header (403); directory, FIFO, `/proc/self/environ`
  (404); file swapped after the check (404); text cut at 256 KiB on a line
  boundary; image over the limit (`too_large`); binary sniffing; UTF-8 edge
  cases.
- Gates: revoked key (403), locked device (401), file switch off (403),
  rate limits and `file_busy`, one audit line per request, no audit line or
  path sent in any other response.
- Fixtures use placeholders only (`/home/user`, `user@example.com`, fake
  keys); kmem rule 22.

Quill Android

- Unit: `LinksTest` runs the shared `links.json` and the checksum check;
  scheme decisions (§4, §5) map to the right intent or dialog; viewer state
  from each response kind and error.
- qphone and qp90 (`scripts/emu-test.sh`): `phoned.go` serves `/file` with
  placeholder text, image, binary and error replies; tap a URL (intent
  recorded, not opened in-app), tap a path (viewer shows text, scrolls to the
  line), image zoom, binary/too-large notes, old Quill Desktop (no
  capability: paths not tappable).

Manual: tap a URL and a path from a real Claude reply; ask for `~/.ssh/config`
(deny message, audit line in Devices); turn the file switch off and tap again.

## 12. Open questions

1. Which mentions make a file readable? (a) any message in the session, user's included; (b) only Claude's messages, tool summaries and diff paths (recommended); (c) no mention check, roots and deny list only.
2. Are paths inside inline code (`/home/user/x.go`) tappable? (a) yes, when the whole code span is one path (recommended); (b) never, like URLs; (c) yes, any path inside inline code.
3. What happens with a `:N` line suffix? (a) open the file and scroll to line N (recommended); (b) ignore the suffix; (c) treat it as part of the file name.
4. Are relative paths (`src/main.go`) links? (a) no (recommended); (b) yes, resolved against the session's folder.
5. Which roots may files come from? (a) the session's folder and the user's home (recommended); (b) also `/tmp`; (c) the session's folder only.
6. Can the user add their own deny-list entries? (a) no, the built-in list only; (b) a list in Quill Desktop → Devices (recommended).
7. Size limits? (a) text 256 KiB shown, images 8 MiB (recommended); (b) text 1 MiB, images 25 MiB like Quill Desktop's previews; (c) text 64 KiB, images 2 MiB.
8. May other Quill Desktop PCs paired as devices use the file endpoint? (a) no, phones only (recommended); (b) yes, same rules.
9. What does a tap on a `localhost` / `127.0.0.1` link do? (a) open it on the phone as is; (b) show "This address is on Quill Desktop's PC" with Copy and Open anyway (recommended); (c) not tappable.
10. Default of the per-device "Let this device open files" switch? (a) off until the user turns it on (recommended); (b) on for every paired phone; (c) no switch, pairing and unlock are enough.
11. Notify on Quill Desktop when a phone opens a file? (a) no, audit log only (recommended); (b) a notification for every file; (c) a notification only for files outside the session's folder.
12. Block screenshots in the viewer (`FLAG_SECURE`)? (a) no (recommended); (b) yes, always; (c) yes, only for files outside the session's folder.
13. Should Quill Desktop also make non-media file paths tappable (opening its own viewer), so both apps match exactly? (a) yes, as a separate task after this one (recommended); (b) no, Quill Android only; (c) yes, in phase 2.
