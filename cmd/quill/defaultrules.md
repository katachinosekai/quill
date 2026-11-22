# Shared rules for every Claude session (kmem)
Quill Desktop's default rules for people who run several Claude sessions on the same projects. Every session reads them with `kmem rules`; `kmem brief` says when they changed. Edit them freely: keep one rule per numbered line so sessions can cite them (`kmem rules 3`).

## Memory
1. Start every session, and again after /clear, with `kmem brief`.
2. Keep project notes in kmem, not in files: one fact per `kmem add`; an outdated fact gets `--replaces` or `kmem retire`. Never edit kmem's database, backups or logs by hand.
3. Re-read a fact right before you replace it, and keep the change small; other sessions may have changed it.
4. Every subagent prompt carries a `kmem context "<task>"` briefing (its first line starts with `KMEM-BRIEF:`).

## Tasks and handoffs
5. Claim a kmem task before working on it, and end it with `kmem task done` or `kmem task release`. A live session's claim is never taken.
6. Before /clear or a session swap, save a handoff at a safe point with `kmem handoff save`; the next session starts with `kmem brief` and `kmem handoff get`.
7. Renamed or restarted? Tell the other sessions the new name.

## Shared folders
8. In a git repo other sessions also work in, `git add` only your own paths; never stash, reset, checkout, clean or rewrite over other sessions' work.
9. Before rebuilding a shared build output (a package, a jar, a zip others use), tell the other sessions; never build the same thing at the same time.
10. No real personal data (email addresses, keys, tokens, home paths, session ids) in tests, samples or fixtures: use placeholders.

## Guards and upkeep
11. kmem-guard is a speed bump against mistakes, not enforcement: follow the rules where nothing checks, never work around a block, and record false positives.
12. Wrong or sensitive facts and tasks go to the recycle bin (`kmem trash <id> "reason"`); only the user empties it.
13. `kmem maintain` (daily) and `kmem audit` (weekly) run on their own; tell the user about audit problems and never repair history by hand.
