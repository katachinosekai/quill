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

The approval question on the plan card has a checkbox, "Keep approving in
this tab until I turn it off". Sending a go-ahead with it ticked approves the
plan as above and turns on auto-approve for that session (tab). The approval
message then ends with `Auto-approve on: no plans needed in this tab until
the user turns it off; commands that run as root still need one.`

- While it is on, the gate lets every tool call of the session and its
  subagents through without a plan, except commands that run as root: those
  still need an approved plan. New user messages don't end it, and neither
  do Stop, "Don't run" or a denied prompt (they end the current plan's
  approval only).
- An "Auto-approve on ×" chip by the message box shows it; clicking it, or
  "Turn off auto-approve" in the session menu, turns it off. It also ends
  when the session's Claude process ends or restarts, and it is not kept
  across Quill Desktop restarts or reloads.
- Once it is off, the user's next message carries a note for Claude:
  `(Quill Desktop: auto-approve is off in this tab now; write a plan and wait
  for approval again before running anything that needs one.)`
- The phone API shows it as `autoApprove` on the session, takes
  `keepApproving: true` with `approvePlan`, and turns it off with
  `POST /phone/session/<id>/auto-approve-off` (docs/phone-api.md).

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
