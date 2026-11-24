# Quill phone control API, version 1

The Android app uses this API to show and drive Quill desktop sessions. It runs on Quill's existing phone server: HTTPS with a self-signed certificate that the phone pins (fingerprint `fp` from the pairing link), on the port from the pairing link. It sits next to the existing `/folders.json`, `/events` and `/usage.json` endpoints, which keep working unchanged. `/events` items may carry an optional `body` (string, at most 200 characters, omitted when empty): the text the desktop notification for that event shows.

Status of this document: agreed shape (kmem fe1c74f). Any change after the first commit goes to the Android session before it is committed.

## Conventions

- Requests and responses are JSON, UTF-8, `Content-Type: application/json`. Responses carry `Cache-Control: no-store`.
- Every request sends `X-Quill-Key: <phone key>` (see Pairing). A missing, unknown or revoked key gets `403 forbidden` on every endpoint.
- Every successful response object has `"v": 1`. New fields can appear in any response without a version bump; the phone ignores fields it doesn't know and treats unknown enum values as the closest neutral case (unknown status = `idle`, unknown item kind = `notice`, unknown request state = `cancelled`).
- Times are Unix milliseconds (`...Ms`). Optional fields are left out rather than sent as `null`.
- Transport (2026-11-22, for slow links): the server offers HTTP/2 (ALPN `h2`, then `http/1.1`) and keeps idle connections open 90 s, so the phone can reuse one connection. Replies of 1 KiB or more are gzipped (`Content-Encoding: gzip`, `Vary: Accept-Encoding`) when the request sends `Accept-Encoding: gzip`; smaller replies and requests without it are unchanged. `/events` and `/phone/stream` are never compressed. `/phone/sessions` and `/phone/session/{id}/items` may take up to 60 s to send (other replies 10 s).
- Text fields hold Markdown the way Quill shows it; the phone renders it.
- Request bodies are capped at 64 KiB (`413 too_large`).
- Writes are limited to 10 per second per phone (`429 rate_limited` with `Retry-After` seconds).

### Errors

Any non-2xx response has this body:

```json
{"v": 1, "error": {"code": "not_found", "message": "no session abc"}}
```

| HTTP | code | meaning |
|---|---|---|
| 400 | `bad_request` | malformed JSON, missing field, bad value |
| 400 | `idempotency_key_missing` | a POST without `Idempotency-Key` |
| 403 | `forbidden` | missing, unknown or revoked key |
| 403 | `file_denied` | `GET /phone/session/{id}/file`: outside the allowed folders or on a deny list (`message` names the rule) |
| 403 | `files_off` | `GET /phone/session/{id}/file` while this phone's "Let this device open files" switch is off |
| 403 | `control_off` | a write while "Let paired phones control sessions" is off, or off for this phone |
| 404 | `not_found` | unknown path, session, request or queue entry |
| 409 | `stale` | the target changed: the request is no longer pending, the queue entry text no longer matches |
| 409 | `in_progress` | the same Idempotency-Key is still being handled |
| 409 | `not_running` | the session has no running Claude process and the action needs one (interrupt) |
| 409 | `file_busy` | another file read of this phone is still running |
| 409 | `blocked` | Quill refused the send the same way the desktop box would (session loading, taking over, held by a terminal), or the /command only works on the desktop (dialogs, browser, files, settings: /login, /mcp, /export, /add-dir, ...) |
| 426 | `update_required` | topic mode (hello `topics.on`): the older handoff start (`start.kmem`, handoff `resume` / `delete`); the message says to update Quill Android |
| 413 | `too_large` | body over 64 KiB, or `text` over 32 KiB |
| 422 | `idempotency_mismatch` | Idempotency-Key reused with a different method, path or body |
| 429 | `rate_limited` | too many writes or file reads; see `Retry-After` |
| 500 | `internal` | the request failed inside Quill Desktop; nothing is remembered for its Idempotency-Key, so it may be retried |
| 503 | `not_ready` | Quill is still starting |
| 503 | `kmem_unavailable` | a topics endpoint or `openTopic` while topic mode is off (kmem memory off or kmem not installed) |

The `message` is for logs; the phone keys off `code`.

### Idempotency-Key

Every POST must send `Idempotency-Key: <16-64 chars of [A-Za-z0-9_-]>`, a new random value per user action, reused only for retries of that same action.

- The server remembers, per phone, each key with a hash of method + path + body and the final response (status and body) for 24 hours, at most 500 keys per phone (oldest dropped first).
- A retry with the same key and the same request gets the stored response again, with header `Idempotent-Replay: true`. The action is not done twice.
- Same key, different method/path/body: `422 idempotency_mismatch`.
- Same key while the first request is still being handled: `409 in_progress`; retry after a short wait.
- Error responses are stored too, except `429`, `503` and `409 in_progress`, which may be retried with the same key.
- The store lives in memory. After a Quill restart (`boot` changes) old keys are forgotten; the phone must not retry a write across a boot change without checking the session first.

## Enums

### Session status (`status`)

| value | meaning (desktop sidebar text) |
|---|---|
| `running` | Claude is working ("running") |
| `waiting` | a permission, question, plan or rotate card waits for the user ("waiting for you") |
| `idle` | process running, turn finished ("idle") |
| `error` | the process failed ("error"); `error` field has the reason |
| `terminal` | the session is open in a terminal Claude, not in Quill ("open in a terminal") |
| `stopped` | no process (closed tab kept in the sidebar, or not started yet) |

### Item kinds (`kind`)

| value | Quill item | notes |
|---|---|---|
| `user` | User | a message the user sent (from any device) |
| `assistant` | Assistant | Claude's text; may carry `choices` |
| `thinking` | Thinking | Claude's thinking text, shown collapsed |
| `tool` | Tool | a tool call with `tool` and maybe `diff` |
| `permission` | Permission | a tool approval request; `request` set |
| `question` | Permission with tool AskUserQuestion | `request.questions` set |
| `plan` | Plan | a plan to approve; `request` set |
| `rotate` | Rotate | auto-handoff card ("context is getting full, rotate?") |
| `notice` | Notice | Quill's own information line |
| `error` | Error | an error line |

### Request state (`request.state`)

| value | meaning |
|---|---|
| `pending` | waiting for an answer; the only state that accepts `answer` |
| `allowed` | allowed once |
| `allowedAlways` | allowed and a rule was saved |
| `denied` | denied (or plan rejected, question dismissed) |
| `cancelled` | the turn ended or a newer message replaced it |
| `autoAllowed` | allowed by the mode or a read-only check, never asked |
| `planAsked` | held by plan-first until a plan is approved |
| `retryAllowed` | allowed through Approve-and-retry |
| `ruleAllowed` | allowed by a saved rule |

### Answer actions (`action`)

| value | valid for | extra fields |
|---|---|---|
| `allow` | `permission` | |
| `allowAlways` | `permission` with `request.canAllowAlways` | |
| `deny` | `permission`, `question` | `text`: optional reason sent to Claude |
| `approvePlan` | `plan` | optional `choice` (index into `request.choices`), `planAnswers` (by `request.questions` id), `approvalChoice` (index into `request.approval.options`, see below), `text` (the card's "Changes (optional)": sent to Claude with the go-ahead, appended as "Also: <text>"; the approval counts as without it; with a stop `approvalChoice` it goes after the refusal as for `rejectPlan`), `keepApproving` (true: a go-ahead also turns on the session's auto-approve, the card's "Keep approving in this tab" checkbox) |
| `rejectPlan` | `plan` | `text`: optional feedback sent to Claude |
| `answer` | `question` | `answers` (see below) |
| `choice` | `assistant` item with open `choices` | `index` |
| `rotate` | `rotate` | |
| `dismiss` | `rotate` | |

A plan whose approval question offers its own options, a go-ahead and a way to stop ("Can I run steps 1–3? Yes (recommended) or no?"), carries `request.approval`: `{"options": ["Yes", "No"], "stop": [false, true], "recommended": 0}` (`recommended` -1 = none). A pick between things to approve without a stop option ("Should it ship as v0.1.18, or v0.2.0?") carries it too, with `"dontRun": true`; its `options` are `request.choices` in the same order, and the card adds its own Don't run (`rejectPlan`). Quill Desktop shows these options instead of Approve / Don't run. `approvePlan` with `approvalChoice` answers with option i: a go-ahead approves the plan, with `planAnswers` if the plan has other questions; a stop option acts as `rejectPlan`, and its `planAnswers`, if any, are sent to Claude after the refusal ("Not running the plan: … Answers to your questions: …"). `approvePlan` with a `choice` that is a stop option also acts as `rejectPlan`. Plain `approvePlan` / `rejectPlan` still work.

## Objects

### Session

```json
{
  "id": "3f2a...",
  "title": "Rename greeting",
  "folder": "/home/user/Projects/demo",
  "folderName": "demo",
  "group": "Quill",
  "project": "Quill",
  "status": "idle",
  "busy": false,
  "kmem": "quill",
  "model": "opus",
  "mode": "default",
  "backend": "online",
  "context": {"used": 81234, "max": 200000},
  "pending": {"permissions": 0, "questions": 1, "plans": 0},
  "queue": 2,
  "background": 1,
  "epoch": 4,
  "rev": 1932,
  "updatedMs": 1795000000000,
  "focused": true,
  "tokens": 1532000
}
```

- `kmem` is left out when the session has no kmem name. `error` (string) appears only with status `error`.
- `backend`: `online` (Claude models) or `local` (Ollama models).
- `mode`: one of the `modes` lists from `/phone/sessions` (today `default`, `acceptEdits`, `plan`, `auto`; Local only `default`, `acceptEdits`, `plan`).
- `context.max` is 0 when not known yet.
- `queue`: number of queued messages; `background`: running background tasks.
- `suggestion` (optional, since 0.1.16): the prompt suggestion Quill Desktop shows as grey text in the session's empty message box; left out when there is none. It changes like any other session field, so the stream sends a `session` change when it appears, changes or goes away.
- `epoch` changes whenever the item list is renumbered (rewind, fork into this tab, history reload, transcript branch pruning). `rev` grows on every change to any item or to the queue of this session. Both are per session.
- `focused`: the session shown in the desktop's current view.
- `tokens`: the session's total tokens from its transcript (refreshed about every 5 s; a growth arrives as a `session` change).
- `project` (since 2026-11-21): the kmem project the session is grouped under. `group` holds the same value, so older phones group by project too. Quill Desktop has no folders any more (no Active, Inactive, Archive, Created by Claude or custom folders).
- Topic fields (since topics level 1, only in topic mode; see "Topics"): `topic` (the kmem topic the session coordinates), `topicTitle`, `legacyName` (the old kmem name of a session that predates topic tabs; it is to be merged into a topic), `agents` (subagents), `agentTokens` (their summed tokens) and `endWait` (the end-of-session check). All are left out when empty. `kmem` stays as before for older phones.
- `renamed: true` (optional) when the title was set by hand: offer "Restore original name".
- `autoApprove: true` (optional, since 2026-11-23) while the session's auto-approve is on: plan-first lets its tool calls run without a plan until the user turns it off (docs/plan-format.md, "Auto-approve"). Left out when off. Turn it on with `keepApproving` on `approvePlan`, off with `auto-approve-off`.

### Item

```json
{
  "seq": 812,
  "rev": 1930,
  "kind": "tool",
  "text": "",
  "streaming": false,
  "inPlan": false,
  "tool": {
    "name": "Edit",
    "id": "toolu_01...",
    "summary": "Edit main.go",
    "done": true,
    "isError": false,
    "result": "The file main.go has been updated.",
    "resultTruncated": false
  },
  "diff": {
    "path": "/home/user/Projects/demo/main.go",
    "lines": [{"op": "-", "text": "hi"}, {"op": "+", "text": "hello"}],
    "truncated": false
  }
}
```

- `seq`: position in the conversation, unique and increasing within an epoch.
- `rev`: the session `rev` at this item's last change.
- `text`: for `user`, `assistant`, `thinking`, `plan`, `notice`, `error`, `rotate`.
- `streaming`: the text is still arriving.
- `inPlan`: part of a plan-mode turn.
- `tool`: only for `tool` items. `result` is cut to 2 KiB (`resultTruncated` true when cut). `done` is false while the tool runs.
- `diff`: only for edit tools. `op` is `"+"`, `"-"` or `" "`. At most 400 lines (`truncated` when cut).
- No raw tool input is sent; `summary` is the one-line description the desktop shows.

Assistant items with a choice card add:

```json
"choices": ["Take the split next", "Finish the queue fix first"],
"choicesOpen": true
```

`choicesOpen` is true only while the desktop would still accept a click (the latest message, unanswered).

### Request (on `permission`, `question`, `plan`, `rotate` items)

```json
"request": {
  "id": "req_01...",
  "state": "pending",
  "tool": "Bash",
  "detail": "rm -rf build/",
  "canAllowAlways": true
}
```

- `id`: the request id used by `answer`. Plan cards raised by an assistant question (no Claude request behind them) and rotate cards have `id` `"seq:<seq>"`.
- `detail`: what is being asked (command, file path, URL), cut to 2 KiB.
- `question` items carry `questions` instead of `detail`:

```json
"questions": [
  {"question": "Which layout?", "header": "Layout", "multiSelect": false,
   "options": [{"label": "Tabs", "description": "One tab per session"}, {"label": "Split", "description": ""}]}
]
```

- `plan` items: the plan text is the item `text`; `request.steps` is the number of numbered steps (0 when none).

### Queue entry

```json
{"index": 0, "text": "also check the tests"}
```

## Endpoints

### POST /phone/hello

First call after pairing and after each app start. Body: `{"name": "Pixel 8", "app": "0.9.4"}`. Sets the phone's display name in Quill's phone list and its last-seen time.

Response: `{"v": 1, "phoneId": "a1b2c3d4", "control": true, "boot": "9f...", "quill": "0.1.15", "unlock": true, "lanUnlock": false}`. `control` says whether writes are allowed for this phone right now. `unlock`: this device must unlock before anything else works (see "Device lock and unlock"). `lanUnlock`: right now this device may unlock with its hardware key instead of the password (home-network unlock); older Quill Desktop builds leave it out (treat as `false`). `quill` is the Quill Desktop version (`"dev"` in local builds). `topics`: `{"api": 1, "on": true, "endCheck": true}`: topic mode is on (see "Topics") and `endCheck` says kmem has the end-of-session check (1.10.0+) so `endWait` can appear; older Quill Desktop builds leave it out (no topics: use the handoff flow). `files`: `{"api": 1, "on": false}`: the file endpoint's version and whether this device may open files right now (a phone whose "Let this device open files" switch is on; always `false` for other PCs); older Quill Desktop builds leave it out (no file links). `quillOperator`: `{"api": 1, "on": true}`: the Quill operator can run on this Quill Desktop (see "Quill operator"); older Quill Desktop builds leave it out. Needs an Idempotency-Key like every POST, but is allowed while control is off.

### GET /phone/sessions

```json
{
  "v": 1,
  "boot": "9f0c...",
  "cursor": 5521,
  "control": true,
  "usage": {
    "paused": false,
    "pausedUntilMs": 0,
    "fiveHour": {"pct": 41, "resetsAtMs": 1795003600000},
    "week": {"pct": 12, "resetsAtMs": 1795400000000},
    "credits": "not reported",
    "todayTokens": 3400000,
    "weekTokens": 110700000
  },
  "models": {"online": ["opus", "sonnet", "haiku"], "local": ["qwen3:14b"]},
  "modes": {"online": ["default", "acceptEdits", "plan", "auto"], "local": ["default", "acceptEdits", "plan"]},
  "projects": ["Quill", "Zomboid"],
  "actions": ["rename", "resume", "end", "delete", "project"],
  "settings": {"buildGate": true, "coordinator": true},
  "sessions": []
}
```

- `sessions`: Session objects for the LIVE sessions only (open tabs and running Quill sessions), in sidebar order: grouped by project, projects A-Z. In topic mode `group` is the topic's title for a topic session; a live session that is not a topic yet (`legacyName`) stays listed until merged. Outside topic mode, ended sessions are kmem handoffs (`/phone/handoffs`).
- `topics` (topic mode only; left out otherwise): the topics the "New session" sheet offers, see "Topics".
- `boot` changes when Quill restarts; the phone then drops all cached state.
- `cursor` is the stream position to pass to `/phone/stream` next.
- `projects`: the project names in the sidebar, for the "Project" picker (the user may also type a new one). `actions`: the session-menu endpoints this Quill Desktop supports; older builds leave it out, so the phone shows only the options listed.
- `usage.fiveHour` / `usage.week` are left out when unknown. `usage.credits` is the same text as Quill Desktop's usage panel ("not reported" when the plan reports none); `todayTokens` / `weekTokens` are the token sums of all sessions for today and the last 7 days.
- `settings`: two Quill Desktop Settings → General switches. `buildGate` is "Ask before building" (the plan card before a build), `coordinator` is "Coordinate with subagents" (the setting itself; Quill Desktop still uses the coordinator only for Online models). Both default to `true`. Quill Android changes them with `POST /phone/settings`.

### GET /phone/folders

`{"v": 1, "folders": [{"path": "/home/user/Projects/demo", "name": "demo"}]}`: the folders Quill knows (sidebar groups and recent folders). New sessions can only be started in one of these.

With `?topic=SLUG` in topic mode (`topic` is echoed): the topic's working folders only: its live coordinator's folder first, then the extra folders of its project (Quill Desktop's project folders). `404 not_found` for an unknown topic. `openTopic` accepts these in `folder`.

### GET /phone/session/{id}/items

Query:

| param | meaning |
|---|---|
| (none) | the latest window: the last `limit` items |
| `epoch` + `since` | changes after rev `since` in epoch `epoch` |
| `before` | `limit` items with `seq < before` (paging back) |
| `limit` | default 200, max 500 |
| `delta=1` + `have` | with `epoch` + `since` only: streaming deltas, see below |

Response:

```json
{
  "v": 1,
  "id": "3f2a...",
  "epoch": 4,
  "rev": 1932,
  "reset": false,
  "hasMore": true,
  "items": [],
  "queue": []
}
```

- `items`: Item objects, ascending `seq`. `queue`: the full current queue (Queue entries).
- With `epoch` + `since`: `items` holds every item whose `rev > since`, whole. When `epoch` is not the current one, or `since` is older than the server can answer, the response is the latest window with `reset: true`; the phone replaces its list.
- `hasMore`: older items exist before the first one returned.
- Streaming deltas (opt-in, 2026-11-22): with `delta=1`, `have=<seq>:<n>:<crc>[,<seq>:<n>:<crc>...]` (at most 16 entries) names items the phone already holds: `n` is the number of Unicode code points it has of that item's `text`, `crc` the CRC-32 (IEEE, as in `java.util.zip.CRC32`, hex) of those `n` code points as UTF-8. When such an `assistant` or `thinking` item comes back in an `epoch` + `since` reply and the phone's copy is a prefix of the current text, the item carries `"textFrom": n`, `"text"`: only the code points after `n`, and `"textLen"`: the whole text's length in code points; the phone appends. Otherwise (other kinds, `n` = 0, `n` longer than the text, crc mismatch, or the full window) the item comes whole without `textFrom`. A malformed `have` gets `400 bad_request`.

### GET /phone/stream

Long poll. Query: `cursor` (from `/phone/sessions` or the last stream reply), `wait` seconds (default 25, max 25).

Returns at once when something changed after `cursor`, otherwise after `wait` seconds with no changes.

```json
{
  "v": 1,
  "boot": "9f0c...",
  "cursor": 5530,
  "reset": false,
  "settings": {"buildGate": true, "coordinator": true},
  "changes": [
    {"type": "session", "session": {}},
    {"type": "removed", "id": "77aa..."},
    {"type": "order", "ids": ["3f2a...", "77aa..."]},
    {"type": "usage", "usage": {}},
    {"type": "control", "control": false},
    {"type": "files", "files": {"api": 1, "on": true}},
    {"type": "settings", "settings": {"buildGate": false, "coordinator": true}},
    {"type": "topics", "topics": []}
  ]
}
```

- A `session` change carries the whole Session object; its `epoch`/`rev` tell the phone whether to fetch items (only for the session on screen; others just update their row). `usage` is shaped as in `/phone/sessions`.
- A `topics` change (topic mode) carries the whole `topics` list of `/phone/sessions` when a topic's title, status, counts or live session changed.
- Every reply carries the current `settings` (as in `/phone/sessions`); a `settings` change comes when either switch changed, on the PC or from a device.
- `reset: true` (cursor too old, or a different `boot`): the phone refetches `/phone/sessions`.
- `control` and `files` changes are per phone: each phone gets only its own (`files` follows its "Let this device open files" switch, shaped like hello's `files`).
- At most one stream per phone; a second one ends the first with an empty reply. Revoking a phone ends its stream with `403`.

### GET /phone/session/{id}/file?path=...

Read-only: one file Claude mentioned in that session, for Quill Android's file viewer (design: `docs/design/phone-links.md`). `path`: the link target as the shared detector found it (`cmd/quill/testdata/links.json`): an absolute path, `~/...` (expanded on Quill Desktop) or a `file://` URL; without the `:N` line hint; URL-encoded; at most 4096 bytes.

```json
{"v": 1, "path": "/home/user/Projects/demo/main.go", "name": "main.go",
 "size": 5120, "mtimeMs": 1795000000000, "kind": "text", "lang": "go",
 "text": "package main\n...", "truncated": false}
```

- `path`: the requested path, expanded and cleaned (not the symlink target). `size` in bytes, `mtimeMs` the modification time.
- `kind`:
  - `text`: valid UTF-8 with no NUL in the first 8 KiB. `text` holds at most 256 KiB, cut after the last whole line (`truncated: true` when the file is longer). `lang` from the extension (`go`, `java`, `kotlin`, `python`, `js`, `ts`, `sh`, `json`, `yaml`, `toml`, `c`, `cpp`, `markdown`, `svg`, ..., else `text`).
  - `image`: `png`, `jpg`/`jpeg`, `gif`, `webp`, `bmp` whose bytes match the extension; `mime` and `data` (standard base64), at most 8 MiB. An SVG is `text`.
  - `too_large`: an image over 8 MiB; `binary`: anything else. Both carry the metadata only.

Rules (all on Quill Desktop):

- Only paths in that session's Claude output count: `assistant` and `plan` text, tool and permission `summary`, `diff.path`. The user's own messages, notices and thinking don't.
- Allowed roots: the session's folder and the user's home, after resolving symlinks; never `/proc`, `/sys`, `/dev` or `/run`, except the current user's mounted drives under `/run/media/<user>/` (another user's `/run/media`, `/run/user` and the rest of `/run` stay denied, also through symlinks).
- Deny list, on the path as written and resolved: `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.azure`, `~/.kube`, `~/.docker`, `~/.config/gcloud`, `~/.config/gh`, `~/.password-store`, `~/.local/share/keyrings`, `~/.pki`, browser and mail profiles (`~/.mozilla`, `~/.thunderbird`, `~/.config/google-chrome`, `~/.config/chromium`, `~/.config/BraveSoftware`, `~/.config/vivaldi`, `~/.config/microsoft-edge`, `~/.config/opera`, browser apps under `~/.var/app`), `~/.claude/.credentials.json`, kmem's data and Quill Desktop's own settings and keys, `.git` internals; file names `.env`, `.env.*`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`, `*.kdbx`, `id_rsa*`, `id_dsa*`, `id_ecdsa*`, `id_ed25519*`, `.netrc`, `.pgpass`, `.git-credentials`, `.npmrc`, `.pypirc`, `*credentials*`, `*secret*`, `*token*`, `local.properties` (case-insensitive); text containing a `PRIVATE KEY` header. Plus the user's own list in Quill Desktop → Devices ("Files phones may not open": folders/files or name patterns).
- Phones only: another Quill Desktop PC paired as a device gets `403 forbidden`. The usual pairing key and unlock apply (`403 forbidden`, `401 locked`); a file read does not count as activity for the unlock idle time.
- Per-device switch "Let this device open files" in Quill Desktop → Devices, off by default.
- Per phone: one file read at a time (`409 file_busy`), at most 2 per second, 60 and 64 MiB per 10 minutes (`429 rate_limited` with `Retry-After`).
- Every request, served or refused, is written to `phone-files.log` in Quill Desktop's phone folder (time, device, session, path, size, result; last 2000 lines); never sent to a phone. Quill Desktop → Devices → "Opened files..." shows a device's last 20. No desktop notification.

Errors: `400 bad_request` (empty, relative, too long, NUL, `..` segment, not UTF-8, a `file://` URL with a host); `404 not_found` (unknown session, not mentioned in it, missing, not a regular file, symlink loop: one reply for all, so the route can't probe which files exist); `403 file_denied`; `403 files_off`; `403 forbidden` (not a phone); `409 file_busy`; `429 rate_limited`.

### POST /phone/session/{id}/send

Body: `{"text": "run the tests"}` (1 byte to 32 KiB after trimming).

Runs exactly as typing into that session's desktop message box and pressing Enter: local slash commands are answered by Quill, a busy session queues the text, the usage pause prefixes its note, plan-first approval words count, a stopped session is started.

Response: `{"v": 1, "queued": false}`, or `{"v": 1, "queued": true, "queue": [...]}` with the full queue.

### POST /phone/session/{id}/answer

Body, one of:

```json
{"requestId": "req_01...", "action": "allow"}
{"requestId": "req_02...", "action": "answer", "answers": {"Which layout?": ["Split"]}}
{"requestId": "seq:640", "action": "approvePlan"}
{"seq": 902, "action": "choice", "index": 1}
```

- `answers`: question text -> chosen labels. A label not among the options is sent as the free-text "Other" answer. Every question of the request must be answered.
- `choice` uses the assistant item's `seq` and the option `index`.

Response: `{"v": 1, "item": {...}}` with the updated Item. `409 stale` when the request is not pending any more (answered on the desktop or another phone).

### POST /phone/session/{id}/interrupt

Body `{}`. Same as Escape / Stop on the desktop. Response `{"v": 1, "wasBusy": true}`. `409 not_running` without a process.

### POST /phone/session/{id}/auto-approve-off

Body `{}`. Turns the session's auto-approve off (as the desktop's chip and session menu do); a no-op when it is off. Response `{"v": 1, "autoApprove": false}`.

### POST /phone/session/{id}/queue/delete and /queue/send-now

Body: `{"index": 0, "text": "also check the tests"}`. `text` must equal the entry at `index` (`409 stale` otherwise). Response: `{"v": 1, "queue": [...]}`.

### Session menu: POST /phone/session/{id}/rename, /project, /resume, /end, /delete

The desktop's right-click session menu (revised 2026-11-21). A tab, its Claude session and its kmem name are one identity called the session, so there is no separate kmem action: rename renames the kmem name too, and the phone shows `kmem` read-only. There are no folders: `/place` and `places` are gone (`404`).

- `rename` `{"title": "New name"}` (1-200 characters after trimming; `400` when longer or missing). `{"title": ""}` = Restore original name. Response `{"v": 1, "session": {...}}`.
- `project` `{"project": "Zomboid"}` puts the session under that project (stored per kmem name); `""` = automatic (kmem's label for the name, else the first word of the title). Response `{"v": 1, "session": {...}}`.
- `resume` `{}` ("Start" on the desktop, for a live session whose Claude process isn't running): starts it. An ended session that still has a saved handoff starts fresh from it and the response holds the NEW session; `{"old": true}` continues the old conversation instead. Already running: no-op. `409 blocked` while a terminal holds it or it is being taken over. Response `{"v": 1, "session": {...}}`.
- `end` `{}`: as End session: the session saves its kmem handoff, closes and leaves the list (it shows up in `/phone/handoffs`). Response `{"v": 1, "saving": true}` while the save is pending.
- `delete` `{}`: as Delete session: the conversation is removed from disk and its kmem handoff goes to the kmem recycle bin. The phone confirms first. `409 blocked` when a terminal holds it. Response `{"v": 1, "removed": "<id>"}`.

### POST /phone/handoffs/{name}/resume and /delete

In topic mode both answer `426 update_required` (handoffs are read-only history; see "Topics"). The rest of this section is the behaviour outside topic mode.

- `resume` `{}`: as Resume in Quill Desktop's kmem panel: opens the live session that holds the name, else starts a fresh session from the handoff (through its ended session when Quill still knows it, keeping folder, model and mode; else in the home folder). Response `{"v": 1, "session": {...}}`.
- `delete` `{}`: moves every fact of the handoff to the kmem recycle bin (the phone confirms first). `409 blocked` while a live session holds the name (end or delete that session instead). Response `{"v": 1, "removed": "<name>"}`.
- `404` for an unknown handoff, `409 blocked` when kmem isn't available.
- The recycle bin itself is not on the phone: restoring and emptying it happen only in Quill Desktop's kmem panel, and emptying (kmem's final purge) only in a terminal where kmem asks the user to type the confirmation.

### POST /phone/usage/resume

Body `{}`. Same as "Resume now" on the pause banner. Response `{"v": 1, "usage": {...}}`.

### POST /phone/settings

Body: `{"buildGate": false}`, `{"coordinator": true}`, or both; booleans only. Sets the Quill Desktop Settings → General switches (the checkboxes follow) and sends a `settings` stream change. Response `{"v": 1, "settings": {"buildGate": false, "coordinator": true}}`. `400 bad_request` for an empty object, an unknown key or a value that isn't `true`/`false`; `403 control_off` as for every control write.

### GET /phone/settings/questions

Quill Desktop's open feature questions (since 2026-11-22). Updates keep every setting; only new, changed or removed features that need the user's input ask, once each, and Quill Android can answer them too. Empty until the Welcome was completed on the PC. Response, oldest first:

```json
{"v": 1, "questions": [
  {"id": "coordinator", "kind": "New", "title": "Coordinate with subagents", "question": "How should Claude work?",
   "text": "Claude splits your requests ...\nOnline sessions only (Claude models); local models work alone.",
   "choices": ["Coordinate with subagents (recommended)", "One session does everything"], "recommended": 0}
]}
```

- `kind`: `New`, `Changed`, `Removed` (show it as a tag) or `Notice` (empty `choices`: an OK only).
- `text`: what the feature does, or for `Changed`/`Removed` what changes or goes away and what happens to the current setting; lines split by `\n`.
- `recommended`: index into `choices`; preselect it.
- "Later" is the phone's own: just don't answer.

### POST /phone/settings/answer

Body `{"id": "coordinator", "choice": 0}` (`choice` is an index into `choices`; omit it for a `Notice`). Saves the choice and marks the question answered on both sides (an open dialog on the PC drops it); a `settings` stream change follows. Response: the remaining questions as in `GET /phone/settings/questions`. `400 bad_request` for a body without `id`, `404 not_found` for a question that isn't open or a choice out of range, `403 control_off` as for every control write.

### POST /phone/sessions/new

Body: `{"folder": "/home/user/Projects/demo", "backend": "online", "model": "opus", "mode": "default"}`. `folder` must be one of `/phone/folders`. Response: `{"v": 1, "session": {...}}`.

In topic mode a new session is a topic session: send `openTopic` instead of `folder` + `start` (see "Topics"). `start` is then refused with `426 update_required`.

Optional `start` (since 2026-11-21; refused in topic mode) does what the desktop's new-tab card does; without it the session starts as before and gets a kmem name from its title later (a tab, its session and its kmem name are one identity, so there is no "no kmem" choice):

- `{"kmem": "resume", "name": "quill-android"}`: link the handoff and send its resume prompt; the title becomes the name. `409 blocked` when the handoff is open in another session (`openIn` in `/phone/handoffs`; offer "Open that session" instead).
- `{"kmem": "new", "title": "Some name"}`: new kmem name from the title (empty title = the folder's default title).
- Anything else: `422 bad_request`.

### GET /phone/handoffs

In topic mode the list is always empty with `"readOnly": true` (nothing to resume; the old handoffs are the `history` of `GET /phone/topics`). Otherwise: `{"v": 1, "handoffs": [{"name": "quill-android", "savedMs": 1795250000000, "openIn": {"id": "<session id>", "title": "Quill Android 2"}}]}`: saved kmem handoffs, newest first (same list as the desktop card); `openIn` is the open tab or running session that holds the name, else `null`. Empty list when kmem isn't available. Older Quill Desktop builds answer `404`; hide the Resume part then.

### GET /phone/handoffs/slug?title=...

`{"v": 1, "slug": "some-name"}`: the kmem name a new session with that title would get (empty for an empty title).

## Topics

Topic mode (topic tabs, docs/design/topic-tabs.md) is on while hello's `topics.on` is `true`: kmem memory in use, kmem has the end-of-session check (1.10.0+) and the topic migration ran. Before that the API is exactly as it was. Each tab is the coordinator of one kmem topic, there is at most one live session per topic, and sessions are never resumed from old transcripts. Older Quill Desktop builds have none of this; the existing fields stay as they were, so older phones keep working apart from the handoff start (refused with `426 update_required`: "Update Quill Android ...").

### topics[] (in GET /phone/sessions, and the `topics` stream change)

```json
{"slug": "alpha", "title": "Alpha", "scope": ["alpha"], "live": "3f2a...", "status": "alpha is half done", "claimed": 1, "open": 4}
```

`"dormant": true` appears (omitted otherwise) when the user hid the topic under Dormant in the desktop "Open a topic" card. Hidden topics stay listed; Quill Android may show them folded later. Opening the topic un-hides it.

`"recent": true` appears (omitted otherwise) on the last five topics opened in Quill Desktop (its "Open a topic" card's Recent list); Quill Android can list them first.

`live` is the id of the topic's live session, `""` when none; for topic `quill` while the Quill operator can run it is the string `"operator"` (not a session id: open it with the Quill operator routes, see "Quill operator"). `status` is the first line of the `<topic>/status` fact (may be empty). `claimed` / `open`: task counts over the topic's scope. The kmem window's own topic (`kmem`) is not listed. Left out: `statusMs` (the age of the status fact; kmem 1.8.1 does not report it).

### Session: agents, endWait

```json
"agents": [{"id": "agentA", "name": "Fix join stall", "description": "Fix join stall", "type": "Explore", "tokens": 41000, "startedMs": 1795000000000, "done": false, "status": "running"}],
"agentTokens": 50000,
"endWait": {"why": "end", "checking": false, "asked": true,
  "blockers": [{"kind": "claim", "id": "t1", "title": "Ship it", "need": "checkpoint or release", "text": "claim t1 ..."}]}
```

- `agents` are the subagents Quill Desktop's strip shows (running ones, then recently finished, `doneMs` when done). `description` is the Agent call's full description (3-5 words, one line; `name` is the same cut to 80 characters); left out when the call gave none, then show `type`. It is additive; `agentTokens` sums their tokens ("3 subagents · 120k tok").
- `endWait` exists only while the end-of-session check runs (kmem 1.10.0+, hello `topics.endCheck`). `why`: `end`, `rotate`, `quit` or `checkpoint`. `checking`: kmem is being asked now. `asked`: the session was sent the blockers and must resolve them. `blockers`: end-check's entries (`kind` claim, lock, job, files, status or, with kmem 1.13.0, snapshot (the topic snapshot is not saved in this session; `saved` is when it was last saved, left out if never); `id`, `title`, `name`, `pid`, `what`, `repo`, `paths`, `need` as kmem gives them; show an unknown `kind` by its `text`) plus `text`, the sentence Quill Desktop shows. Quill never resolves blockers itself; the phone only shows them. It changes like any session field (a `session` change arrives when it appears, changes or goes away).

### POST /phone/sessions/new with openTopic

Body: `{"openTopic": {"topic": "alpha"}}` (an existing topic) or `{"openTopic": {"new": "Garden Planner"}}` (a title; a title that is an existing topic opens that topic; `kmem` is refused). Optional `folder`: one of `GET /phone/folders?topic=` (or a known folder); default the home folder. Model and mode are the defaults. Exactly one of `topic` and `new`.

Response `{"v": 1, "session": {...}, "switched": false, "topic": {"slug": "alpha", "title": "Alpha"}}`. If the topic is live (a tab, the kmem window or a running session with that name) nothing new starts: `switched: true` and `session` is the live one (the desktop switches to it). Otherwise a fresh coordinator starts and gets the topic's first prompt.

`openTopic` `quill` (the Quill operator's topic) never opens a tab: the answer is `{"v": 1, "switched": true, "operator": true, "starting": false, "topic": {...}}` without `session`. `starting: true` while the operator is still starting (it has no session yet): show the Quill sheet and follow `GET /phone/quill`.

Errors: `422 bad_request` (both or neither field, `kmem`), `404 not_found` (unknown `topic`), `400 bad_request` (folder), `409 blocked` (the topic is live outside Quill Desktop; take it over there), `503 kmem_unavailable` (not in topic mode).

### GET /phone/topics

Read-only Topics view: `{"v": 1, "topics": [...], "history": [...], "readMs": ...}`. Each topic is a `topics[]` entry plus `tasks`: its claimed tasks `{id, prio, title, owner, claim, checkpoint}` (`claim` as in `/phone/kmem/tasks`; `checkpoint` may be empty). `history`: the old kmem handoffs `{name, items, savedMs}`, newest first (read only; no Delete); `convertedTo` (kmem 1.13.0) names the topic an old handoff was converted into.

Topic snapshot (kmem 1.13.0, feature `topic-snapshot`; every field is left out when the topic has no snapshot or kmem is older, so older phones are unaffected):

```json
{"slug": "alpha", "...": "...", "tasks": [],
 "snapshotAt": 1795250000000, "snapshotBy": "alpha", "snapshotOld": true,
 "questions": [{"qid": "q1", "text": "Which layout?", "options": ["List", "Grid"], "recommended": "Grid", "askedMs": 1795240000000}]}
```

- `snapshotAt`: when the topic snapshot was last saved (ms); `snapshotBy`: the kmem name that saved it.
- `snapshotOld`: true when the snapshot is older than kmem's fixed 14 days; show it as an old snapshot.
- `questions`: the snapshot's open questions waiting on the user, in kmem's order (answered ones are left out). `options` may be empty; `recommended` (one of the options) and `askedMs` may be missing. The phone only shows them: the session asks the user and records the answer (`kmem handoff answer`).
- A topic's title is kmem's stored title when it has one (`kmem scope T --title`), as in `/phone/sessions` `topics[]`. `503 kmem_unavailable` outside topic mode. Cached up to 10 s like `/phone/kmem/*`.

### GET /phone/topics/search?q=...

The search of Quill Desktop's "Open a topic" card. `{"v": 1, "q": "garden", "matches": [...], "didYouMean": null}`. Each match: `name` (the topic's slug, what `openTopic` takes), `title`, `match` (`name`, `title` or `content`), `content` (only for `content`: one line of the kmem fact that mentions the query; show it as "mentions ..."), `dormant` (omitted unless true; dormant topics appear only because the query was typed), `live` (the live session id, `""` when none). Order: exact, prefix, then substring matches of the slug and title, then content matches (`kmem recall`, one per top-level topic); dormant ones last. `didYouMean`: `{"name", "title"}` of an existing topic that the query resembles (close spelling or a prefix) when the query itself is not a topic yet; `null` otherwise. Show it above "New topic". An empty `q` answers the Recent list (`match: "recent"`, newest first, at most 5). `503 kmem_unavailable` outside topic mode. Not cached (it asks kmem).

### GET /phone/topics/slug?title=...

`{"v": 1, "slug": "garden-planner", "exists": false, "live": ""}` for the "New topic" preview: the slug a title gets, whether that topic exists (then `title` is its title) and its live session id. `422` for `kmem`.

### What Quill Android has to do

- Read hello's `topics`; with `on`, use the topic flow below and stop using `start`, `/phone/handoffs` and the handoff Resume/Delete.
- Session list: one row per live topic session (`topic`, `topicTitle`), subagents collapsed ("3 subagents · 120k tok" from `agents` / `agentTokens`); `legacyName` marks an old session to merge.
- New session sheet: a topic picker from `topics[]` (live ones read "open in ..." and switch) and "New topic..." (preview with `/phone/topics/slug`); send `openTopic`.
- Topics screen from `GET /phone/topics`.
- Show `endWait.blockers` while ending.
- Handle `426 update_required` (an older flow) by telling the user to update.

## Quill operator

The Quill operator (docs/design/quill-operator.md) is Quill Desktop's hidden coordinator behind the Quill button. It never appears in `/phone/sessions` or under `/phone/session/{id}/...` (those answer 404 for it). Quill Android shows it as a bottom sheet with the chat, the status and a Changes list with Undo. The phone acts only as the user's hands: it chats, answers the operator's plan cards and undoes a settings change. There is **no route to change a setting** from the phone.

Detect it with hello's `quillOperator` (`{"api": 1, "on": true}`) and the entry `"quillOperator"` in the `actions` of `GET /phone/sessions` (present only while the operator can run here). An older Quill Android knows neither and hides the operator until it updates. Every route below answers `503 quill_unavailable` when the operator is off, or kmem memory is off or kmem is not installed. Every write needs control mode (`403 control_off`).

### GET /phone/quill

```json
{"v": 1, "api": 1, "state": "live", "status": "working", "waiting": "", "needs": {"count": 1, "unread": 1, "items": [{"key": "issue:t1", "title": "..."}]},
 "actions": ["stop", "send"], "operator": {...}, "changes": [...]}
```

`state`: `off`, `starting`, `panels`, `elsewhere` (live in a tab or outside Quill Desktop), `live`, `failed`, `saving`, `stopping`. `status` (the status line): `off`, `idle`, `working` (a turn or subagents run) or `waiting`; `waiting` says for what: `plan`, `question`, `permission` or `""`. `needs`: what needs the user (its pending plan or question, `quill issue:` tasks); `unread` is what the Quill button's bubble counts. `actions` is what the writes accept now. `operator` is the summary also under `operator` in the other answers: `state`, `waiting`, `epoch`, `rev`, and (here and on items) `status`, `dot` (the button's running dot), `label` ("Quill operator"), `model`, `actions`, `unread`, and `reason` / `button` while it is not running.

### GET /phone/quill/operator/items

As `GET /phone/session/{id}/items` (same query: `limit`, `epoch`/`since`, `before`, delta; same item JSON, `id` is `"quill-operator"`), plus `operator` (the summary). Empty before it ever ran.

### POST /phone/quill/operator/{op}

`op`: `send {text}`, `answer {requestId, action, ...}` (the plan and question cards, as `/phone/session/{id}/answer`; plan-first rules apply unchanged), `interrupt`, `queue/delete`, `queue/send-now`, `start`, `stop`, `restart`. The answer has the session route's fields plus `operator`. `409 stale` when the state doesn't offer the op (`start` while it runs, `send` while off), `409 not_running` for a chat op before it started, `409 blocked` for commands that are desktop-only. A message sent here counts as started by the user. `stop` runs the operator's end-of-session check, as Quill Desktop's Stop does.

### GET /phone/quill/settings/changes

`{"v": 1, "changes": [...]}`: the settings change log, newest first, at most 50 (also under `changes` in `GET /phone/quill`). Entry: `{"n": 3, "id": "animateSpeed", "label": "Text animation speed", "old": "normal", "new": "fast", "timeMs": ..., "why": "the user's request", "by": "operator", "undone": false, "canUndo": true}`. `canUndo` is true while the change is not undone and the setting still has the value it set.

### POST /phone/quill/settings/undo

Body `{"change": 3}` (the `n` of an entry). Restores the old value, as the Undo button on Quill Desktop. Answer `{"v": 1, "undone": 3, "changes": [...], "operator": {...}}`. `400 bad_request` (no number), `404 not_found` (not in the log), `409 stale` (already undone, the setting changed again since, or it can't change on this computer).

### Stream, notification

Stream change `{"type": "quill", "needs": {"count", "unread"}, "operator": {"state", "waiting", "epoch", "rev"}, "lastChange": 3}`: sent when any of it changes; re-read `GET /phone/quill/settings/changes` when `lastChange` moves. The existing `settings` change carries the setting values. Notification event type `quill`, title "Quill operator", body `needs approval: ...` / `has a question: ...` / `has a plan to approve: ...`, no session id; sent only while it waits and Quill Desktop is not showing it. Older apps drop unknown types.

## Control setting

Reads work for every paired phone, as today. Writes (every POST except `/phone/hello`) need both:

- the desktop setting "Let paired phones control sessions" (Settings → Sessions → Phone), off by default;
- the phone's own control switch in the phone list (on by default once the global setting is on).

Otherwise: `403 control_off`. When either changes, streams get a `control` change.

## Device lock and unlock

Every paired device must unlock with Quill Desktop's device password unless its Password switch (Quill Desktop → Devices) is off. Until it does, every request except `POST /phone/hello`, `POST /phone/unlock` and `POST /phone/unlock/challenge` gets `401 locked`. The pinned certificate and the pairing key (`X-Quill-Key`) stay mandatory for all of them; a changed certificate fingerprint means pairing again.

### POST /phone/unlock {password}

`{"password": "..."}` → `{"token": "<64 hex>", "idleMin": 15, "idleLeftSec": 900, "needed": true}`. Send the token as `X-Quill-Unlock` on every later request. It expires after `idleMin` minutes without a write (`POST /phone/unlock/touch` counts as one). Wrong password: `403 wrong_password`; after 5 failures in a row: `403 locked_out` for 15 minutes; no password set on the PC: `403 no_password`. With the Password switch off: `{"token": "", "needed": false}`.

`idleLeftSec`: seconds until the token expires if no further activity comes (whole seconds, rounded up); Quill Android can show it or plan its next touch with it.

### POST /phone/unlock/touch

Marks activity (send it for the user's touches and typing; reads, polling and the stream never count). Body `{}`. → `{"ok": true, "idleMin": 15, "idleLeftSec": 900}` (`idleLeftSec` only for a device whose Password switch is on). Every POST that passes the lock counts as activity, this one included. With an expired or unknown token: `401 locked`; an expired token is deleted and never comes back, so unlock again.

### Tokens across a Quill Desktop reload

A live reload of Quill Desktop (`quill --reload`, switching to a new build) keeps every unlock token, its last activity and the wrong-password counts and lock-outs: the device stays unlocked and the idle time keeps counting through the switch (no extra time). A full quit/restart of Quill Desktop, or its restart after a crash, locks every device again: tokens live only in memory.

### Home-network unlock (no password, hardware-key proof)

When Quill Android is on the trusted home network, it can unlock with a signature from its hardware-backed key instead of the password. All of these must hold, else the password is needed (`hello` says `"lanUnlock": false`):

- the device is a phone (its first hello did not say `quill-desktop`; never PCs), its Password switch is on and a device password is set;
- its switch "No password on this home network" in Quill Desktop → Devices is on (off by default; Linux only, elsewhere the switch is greyed out);
- it registered a key (`/phone/unlock/key`) from a password-unlocked session;
- its last password unlock was less than 30 days ago and the device password hasn't changed since;
- the request comes from one of Quill Desktop's own directly connected subnets (IPv4 or IPv6) and arrived on an address of that same interface: never a VPN (100.64.0.0/10, the Tailscale IPv6 range, interfaces `wg*`, `tun*`, `zt*`, `tailscale*`, point-to-point links), never loopback, link-local or Quill Desktop's own address. The phone's SSID/BSSID count for nothing;
- that subnet is the remembered home network and its IPv4 default gateway still has the remembered MAC address (read from `/proc/net/route` and `/proc/net/arp`). If the gateway MAC can't be read right now (e.g. no ARP entry), that request gets no waiver but the home network stays remembered.

The home network is recorded only when the user turns the switch on in Quill Desktop → Devices, from the connection of the phone's password unlock, which must still be valid at that moment (otherwise the switch stays off and Quill Desktop says to unlock with the password on the home network first). A password unlock never changes it. When the gateway MAC is readable and different, Quill Desktop forgets the home network and turns the switch off; the user turns it on again to record the new one.

After each key unlock Quill Desktop shows "Phone X unlocked without a password from <ip>" with **Revoke** (switch off, key dropped, the phone's session ends).

**POST /phone/unlock/key** `{"publicKey": "<base64 SubjectPublicKeyInfo DER, EC P-256>"}` → `{"v": 1, "ok": true, "lanUnlock": false}`. Only with the `X-Quill-Unlock` token of a password unlock (not one from a key unlock): else `403 password_session_required`; PCs get `403 not_a_phone`; another curve or a bad encoding `400 bad_request`. Registering again replaces the key. Register it after a password unlock (Android Keystore: `getPublic().getEncoded()`, standard base64).

**POST /phone/unlock/challenge** `{}` → `{"v": 1, "nonce": "<43 chars base64url>", "expiresMs": 1795250060000, "ttlMs": 60000}`. Works while locked. Single use, 32 random bytes, valid 60 s (`expiresMs` is Quill Desktop's clock), bound to this device; at most 4 open per device. `403 lan_unlock_off` when the home-network unlock doesn't apply right now; `403 locked_out` during a lock-out.

**POST /phone/unlock** `{"nonce": "...", "sig": "<base64 DER ECDSA signature>"}` (instead of `{password}`; sending both is `400`). The signature is ECDSA P-256 with SHA-256 (Android `SHA256withECDSA`) over the UTF-8 bytes of

```
quill-unlock-v1|<deviceId>|<nonce>|<fingerprint>
```

`<deviceId>` is `phoneId` from hello (the pairing link's `i`), `<nonce>` exactly as received, `<fingerprint>` the pinned certificate's SHA-256 in lowercase hex (the pairing link's `fp`, 64 chars). Success: `{"token": "...", "idleMin": 15, "idleLeftSec": 900, "needed": true, "method": "key"}`. Bad signature: `403 bad_signature`; unknown, used, expired or another device's nonce: `403 bad_nonce`; both count toward the same 5-try lock-out as wrong passwords (`403 locked_out`). `403 lan_unlock_off` if the conditions stopped holding (not counted). During a lock-out further failures neither add to nor reset the count; it starts again only after the 15 minutes.

## Pairing and per-phone keys

Each phone has its own key (before this, every phone shared the `phoneKey` preference):

- Quill keeps a phone list: id (8 hex chars), name, SHA-256 of the key, created, last seen, control switch. A request's key is hashed and compared in constant time against the list.
- "Pair phone" creates a new entry with a fresh 32-byte key (base64url, 43 chars) and shows the link. An entry that never made a request is deleted when the pairing dialog closes.
- Migration: an existing `phoneKey` becomes one entry named "Paired phone (before per-phone keys)", so phones paired earlier keep working until revoked.
- Revoke deletes the entry: its key gets `403 forbidden` at once and its open long polls end.
- `/folders.json`, `/events` and `/usage.json` accept any listed key.

Pairing link (same scheme, two new params):

```
quillpair://pair?h=192.168.1.20,100.101.2.3,desk.tailnet-name.ts.net&p=43117&k=<key>&fp=<sha256 hex>&n=desk&i=a1b2c3d4&v=2&f=quill-folders.json
```

- `i`: phone id. `v=2`: per-phone key, and the phone API is available once the desktop has it. Links without `v` are the old shared-key kind.
- `h`: hosts in the order to try: LAN private addresses first, then Tailscale addresses (100.64.0.0/10), then the Tailscale MagicDNS name when Tailscale is running. The server listens on all interfaces, so the tailnet address works with no extra setup; the pinned certificate covers every host.
- The phone tries the hosts in order (2 s connect timeout each) and starts with the one that worked last.
