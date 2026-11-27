# Quill 0.1.30 release candidate

- Messages to named sessions appear in the recipient’s inbox on arrival. Quill submits them at idle and shows a receipt.
- The worker strip shows subagents and background jobs for the active session. Codex worker activity stays visible across parent turns.
- The optional kmem panel opens to the assistant, with tasks, memory and other utilities in the sidebar.
- The kmem panel shows whether Codex can run the configured kmem guard and startup hooks. Review changed or untrusted hooks in Codex with `/hooks`.
- Quill looks up guard evidence using the active Codex thread. With a compatible staged kmem, it can verify an idle staged session using one read-only probe per thread and rollout round.
- Existing kmem installations require a signed release and an independently provisioned trust key. Per-session rollout evidence requires kmem's `rollout-session-guard` capability and its matching launcher.

# Quill 0.1.29

- Codex uses the installed CLI and its existing sign-in. The model's reasoning effort is an inline slider.
- Codex uses Claude-style plan bars and a compact This session token summary.
- Plan card borders fit their content. Longer text wraps at the available width.
- Portrait keeps Settings, Quill, and kmem in the sidebar and lets you expand sessions to see their subagents.
- The Quill operator follows the latest message and offers a jump arrow after manual scrolling.
- Quill and kmem approval alerts clear when the request is answered or superseded.

## Verification

- Linux Wayland and native Windows focused checks cover the composer, usage, plan cards, subagents, operator scroll, and approval state.
- The Linux home-network unlock flow passed.
- Windows tests use the native test runner without a visible window. Use the installed build to spot-check layout with your display scale and provider sign-in after the update.
- Shared memory remains available through a separately installed compatible kmem.
