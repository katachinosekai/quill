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
