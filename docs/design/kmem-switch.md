# Switching a Quill Desktop install from Claude memory to kmem

Status: approved by the user 2026-11-24. Target: Quill Desktop 0.1.29,
before the first patron release of kmem. kmem side: kmem-lock-audit, after the
rules split (kmem task tf320c4).

## Why

Since 0.1.28 Quill Desktop no longer bundles kmem (decision f9fb3d9). New users
start on Claude Code's auto memory (`MEMORY.md` per project). A patron who
installs kmem later must be able to switch without losing notes, rules or
settings, and without breaking running sessions. Today "Use kmem" only flips
the memory mode.

## Flow

1. **Detect.** Quill Desktop finds `kmem` and `kmem-guard` (PATH,
   `~/.local/bin`), reads `kmem version --json`, checks python3 and that kmem's
   core rules are installed. Anything missing: the dialog says what and how to
   fix it; no switch.
2. **Preview.** "Switch to kmem" dialog lists every change:
   - each project's `MEMORY.md` (`~/.claude/projects/<slug>/memory/MEMORY.md`)
     to import, with its fact count and target topic (default: the project
     folder name, editable);
   - hooks to add to `~/.claude/settings.json` (kmem-guard);
   - memory mode Claude → kmem;
   - Claude Code's auto memory turned off.
   The user can untick projects. Nothing happens before "Switch".
3. **Back up.** One dated folder
   `~/.local/share/quill/kmem-switch-backup-YYYYMMDD-HHMM/` with
   `~/.claude/settings.json`, every listed `MEMORY.md`, and Quill Desktop's
   preferences file. Shown in the dialog with an "Open folder" button.
4. **Import.** Per project: `kmem import memory-md FILE --topic T --json`
   (new kmem command): one fact per note/bullet, kind `fact`, tagged
   `imported-from: MEMORY.md <date>`, duplicates skipped, a summary
   `{imported, skipped, topic}` returned. `MEMORY.md` files stay as they are
   on disk, but Claude Code's auto memory is turned off as part of the switch
   (user 2026-11-24), so notes aren't kept in two places; the preview lists
   this change, the backup holds the setting, and Undo turns it back on.
   `CLAUDE.md` files are never touched.
5. **Rules.** kmem's core rules come with the kmem package. The user's own
   rules (if any) become local rules. Quill Desktop's "Install Quill's default
   kmem rules" option is gone (removed in 0.1.28); Quill Desktop's built-in
   prompt rules (plan format etc.) stay in Quill Desktop, separate from both.
6. **Switch.** Hooks added, memory mode set to kmem. Running sessions keep
   going unchanged; sessions started or resumed afterwards get kmem briefings,
   tasks, topics and handoffs. Nothing restarts. The kmem button, Topics tab and
   kmem assistant appear.
7. **Undo.** Settings → Memory: "Switch back to Claude memory" restores the
   backup (settings.json hooks, preferences) and sets the mode back. Imported
   facts stay in kmem (marked), so switching again doesn't duplicate them.

## Failure handling

- Any step fails: stop, restore the backup, show the error and the backup
  folder; nothing half-switched.
- Import is idempotent (duplicates skipped), so a retry is safe.
- kmem removed later: Quill Desktop falls back to Claude memory automatically
  (the 0.1.28 detection), hooks left by the switch are shown with "Remove hooks".

## Tests

Fake `kmem` binary with the import JSON; temp HOME with sample `MEMORY.md`
(placeholders only); checks for preview contents, backup files, settings.json
hooks merge (keeps the user's other hooks), mode switch, running session
untouched, undo restore, failure rollback.

## Decisions (user 2026-11-24)

1. One kmem topic per project (default: the project folder name, editable in
   the preview).
2. Claude Code's auto memory is turned off automatically after the import;
   Undo turns it back on.
