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
   turns each into buttons, checkboxes or a text field.

## What an approval covers

- Every tool call of the request the plan answers.
- Subagents started (or resumed with SendMessage) for the plan inherit its
  approval until they finish (their task notification).
- A new user message ends the approval for new work. It does not end the
  approval of subagents already started or resumed for the plan.
- Peer messages, scheduled triggers and task notifications never approve.
- Read-only checks (`git status`, `ls`, `systemctl status`, `findmnt`,
  `kmem brief`, …) need no plan. Commands that run as root always need one.

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
