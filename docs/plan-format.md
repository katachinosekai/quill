# Plan format (spec v1)

With plan-first on (Settings → General), Quill Desktop runs no tool call that
changes anything until the user approves a plan. Quill Desktop tells each session this
format in its prompt (`planSpecHint` in `cmd/quill/memorymode.go`). The gate
and the plan card read plans in this format.

## The plan

1. A numbered step list, one line per step, saying what runs and why.
2. A step that starts a subagent says what the subagent will run.
3. The last line is the approval question, in bold:
   `**Can I run steps 1–N? Yes (recommended) or no?**`
4. Numbered choice questions may follow it, one per line, with their options
   in the line and the recommended one marked "(recommended)". The plan card
   turns each into buttons, checkboxes or a text field; the recommended
   option shows "(recommended)" and is highlighted, and no button shows a
   check until the user picks.
5. Any closing question (plan or not) comes last in the reply, after sources
   and links, and each question has its own line. (The cards also find a
   question followed only by a sources/links block, but the hint keeps
   replies in the shape the cards read best.)

## What an approval covers

- Every tool call of the request the plan answers.
- Subagents started (or resumed with SendMessage) for the plan inherit its
  approval until they finish (their task notification).
- A new user message ends the approval for new work. It does not end the
  approval of subagents already started or resumed for the plan.
- Peer messages, scheduled triggers and task notifications never approve.
- Read-only checks (`git status`, `ls`, `systemctl status`, `findmnt`,
  `kmem brief`, …) need no plan. Commands that run as root always need one.

### Auto-approve: until the user turns it off

The plan card shows the approval question once, as a radio group with one
"Send answers" button (decision fdd52bd). Between the radio group and "Send
answers" the card now has one more row: a checkbox, "Keep approving in this
tab until I turn it off" (not shown once auto-approve is on, on an answered
card, for a session on another computer, or with plan-first off). Sending a
go-ahead with it ticked approves the plan as above and turns on auto-approve
for that session (tab). The approval message then ends with `Auto-approve
on: no plans needed in this tab until the user turns it off; commands that
run as root still need one.` If that message can't be sent (Claude can't be
started, or the send fails), auto-approve goes off again.

- While it is on, the gate lets the tool calls of the turns it covers (next
  point) through without a plan, except commands that may run as root (see
  "What counts as root" below): those still need an approved plan. New user
  messages don't end it, and neither do Stop, "Don't run" or a denied prompt
  (they end the current plan's approval only).
- Which turns it covers. **Default, pending the user's choice** (decision
  f3a1828 says messages from peer sessions or scheduled jobs never approve
  anything): only turns the user started (a typed or phone message, the plan
  card), and turns started by the task notification of background work or a
  subagent that such a turn started. A turn started by a peer
  (cross-session) or channel message or a scheduled trigger, or the rest of
  a turn such a message arrives in, is plan-gated as if auto-approve were
  off; so are the calls of subagents started in such a turn (or before
  auto-approve was on, or nested ones Quill Desktop can't trace back), and
  of a subagent such a turn sends a message to (all of them, when Quill
  Desktop can't tell which one it wrote to).
- An "Auto-approve on ×" chip by the message box shows it; clicking it, or
  "Turn off auto-approve" in the session menu, turns it off at once: the
  current plan's approval and its subagents' grants end too, so the next
  call is gated even in the turn the approval started. Ending the session
  (End session, Switch topic) turns it off at once as well, before any
  handoff save runs. It also ends when the session's Claude process ends or
  restarts, and it is not kept across Quill Desktop restarts or reloads.
- Once it is off, the user's next message carries a note for Claude (sent
  with the message, not shown in its bubble, and kept for the next message
  if sending fails):
  `(Quill Desktop: auto-approve is off in this tab now; write a plan and wait
  for approval again before running anything that needs one.)`
  Until then the same note ends every plan-first refusal, so a turn a peer
  message, trigger or task notification started learns it too.
- The phone API shows it as `autoApprove` on the session, takes
  `keepApproving: true` with `approvePlan`, and turns it off with
  `POST /phone/session/<id>/auto-approve-off` (docs/phone-api.md).

#### What counts as root under auto-approve

The check is deliberately crude (`autoNeedsPlan` in `cmd/quill/gatebatch.go`):
a false alarm costs a plan, and read-only commands still run without one. A
command (Bash, or any tool with a `command` input) needs a plan when any word
of it, inside quotes, `-c`/`-e` strings, loop and brace bodies and
`find -exec` included (after dropping quotes and backslashes, decoding
`\x`/`\u`/octal escapes and expanding `{a,b}`), is:

- an escalator: `sudo`, `sudoedit`, `su`, `doas`, `pkexec`, `run0`, `kdesu`,
  `gksu`, `machinectl`, `systemd-run`, …;
- a system tool: package managers (`dnf`, `rpm`, `flatpak`, `pkcon`,
  `rpm-ostree`, …), `fwupdmgr`, `bootctl`, `nsenter`, `unshare`, `setpriv`,
  `busctl`, `loginctl`, `eval`, `sg`, `newgrp`, `flatpak-spawn`, `toolbox`,
  `distrobox-host-exec`, mount and disk tools, …;
- `systemctl` with a verb that changes something (not with `--user`), or
  `docker` / `podman` with anything but a read verb (`ps`, `images`,
  `inspect`, `logs`, …).

It also needs one when it logs in as root over ssh, sets a setuid/setgid bit
(`chmod u+s`, `chmod 4755`), runs a command named by a variable, a
substitution or a glob, pipes a decoder or downloader (`base64`, `xxd`,
`curl`, …) into a shell or interpreter, or gives an interpreter (`python3`,
`perl`, `node`, `awk`, …) code that starts other programs (`system`,
`subprocess`, `exec`, …). Scripts it runs are read at that moment and
checked the same way: files it names that exist (`sh x.sh`, `./x.sh`,
`source x`), the Makefile for `make`, `package.json` for `npm`, the
repository's git hooks for `git`.

Limits: the check can't see what a compiled program or a program built in
the session does (`go run`, `cargo run`, a binary written earlier), what an
interpreter's code builds at run time without the words above, imported
modules or libraries, files a command changes after the check (a background
job rewriting a script, a script fetched and run in a later call that the
check didn't read), files named only by a path it can't resolve (relative to
a directory the command `cd`s into), nor nested includes beyond three
levels. A script written earlier in the session is checked when it runs, not
refused for having been written. `sudo` normally still asks for a password,
and polkit for the user's consent.

A batch (one assistant message with several tool calls) is decided as a
whole: if the gate refuses one call, the others are refused too, and a
subagent started in that batch is stopped.

## Example

```
Plan:
1. Edit `cmd/quill/readonly.go`: add `findmnt` to the read-only commands, so mount checks need no plan.
2. Run `go test ./cmd/quill/ -run ReadOnly`: check the new rule.
3. Start a background subagent that runs the full test suite (`go test ./...`) and reports failures.
4. Commit on `fix/gate-batch2`: keep the tested change.

**Can I run steps 1–4? Yes (recommended) or no?**
1. Commit message style: short (recommended) or detailed?
```

The user answers "yes" or uses the plan card. The card sends `Approved.`,
optionally followed by `Answers: 1 → short.`
