# Overnight work 2026-11-23 (night): review list

Everything below is committed locally on `master` of Quill Desktop and Quill Android.
Nothing is pushed, built or released. The running Quill Desktop is still the old
build (d7b4eae), so none of this is visible until the next build. To undo one
item, `git revert <commit>`.

## Quill Desktop (master)

| Commit | What | Checked |
|---|---|---|
| 7607407 | "New topic…" field on the Open a topic card was hidden under the topics list; now above the list | layout test, screenshot |
| b966d15 | Topic names without Latin letters → `topic-N` instead of all landing on `session` | tests |
| 968fc70 | Layout test for the new-session name dialog | test only |
| f825f87 | Topic titles in any language: saved across restarts, same title reopens, symbols stripped before prompts; 6 scripts render | tests, screenshots |
| ee36794 | Choice buttons: "(recommended)" + accent highlight; ✓ no longer on every button before a pick (was a bug) | tests, screenshots |
| 401ee5e | Streaming reveal and fade step by whole letters (emoji, Hindi, Arabic stay intact) | tests |
| eb7852b | Long tool output / crash text cut at whole characters (no broken "?" glyphs) | tests |
| 615b272 | Autocorrect uses the system language's hunspell dictionary, else off; picker in Settings; Turkish İ | tests |
| cfe17d5, faddde7 | Dates in the user's language (goodsign/monday, 35 languages), locale sorting, search folding (ß/ss, İ/i) | tests |
| 036ac98 | Background tasks button removed from the message bar; shell jobs show in the subagent strip; Stop everything in strip/tab menus | tests, screenshot |
| b470e2a, 83a043d | kmem handoff names for non-Latin titles (`session-N` + title kept), rename preview, kmem panel search by title | tests |
| e504c58 | Voice input: Auto-detect default, base/small/medium models, native language names; "Voice: XX" button removed, settings in Settings → Voice input | tests (no model downloads, not tried in the app) |
| 23018bc, 5f1526a | Plan-first gate: more read-only commands pass (kmem --version, pdftotext -, gh api GET, command -v, loops, cd &&); then 14 bypass classes closed that two adversarial reviews (Opus, Sonnet) proved, most already on master before tonight ($'..', ${..$(..)}, >&1"path", /dev/tcp, abbreviated GNU options, PATH via for/read, awk, rg --hostname-bin, gh api other hosts, kmem --as anywhere) | ~190 test cases |
| 49ce07e, 66b44b3 | Auto-approve until turned off (see below) | tests incl. -race, two adversarial reviews |

Full suite (`go vet`, `go test -tags wayland ./...`) passed on master e504c58 and on the final auto-approve branch (= master 66b44b3).

## Quill Android (master)

| Commit | What |
|---|---|
| 9202743 | Recommended option label + highlight on choice and plan-card buttons |
| 2cebf15 | Streaming reveal by whole letters |
| 97c9566, c69af14 | Cut text without splitting emoji/surrogate pairs |
| 9892c3b | **Fix of my own mistake**: the rebase of 97c9566 committed conflict markers into ChatView.java; fixed one commit later, unit tests pass |
| d85943c | Dates/times follow the device language (getBestDateTimePattern). English output changes slightly: "Nov 21, 7:05 AM" instead of "Nov 21 7:05 AM" |

Unit tests (`testDebugUnitTest`, JDK 21) pass on d85943c. Nothing tried on a device.

## Auto-approve (merged: 49ce07e, 66b44b3)

Checkbox on the plan card: "Keep approving in this tab until I turn it off"; chip
"✓ Auto-approve on ×" by the message box; "Turn off auto-approve" in the session menu;
ends when Claude exits/restarts or Quill Desktop restarts. Reviewed against kmem
decisions and by Opus + Sonnet; fixes applied (root check much broader, off switch
ends the current approval at once, failed start turns it off, off-note reaches Claude).

**Your decisions needed**
1. Peer/trigger turns: kmem decision f3a1828 says peer messages and scheduled jobs never approve. I kept that: with auto-approve on, turns started by other sessions or triggers still need a plan. Keep, or let auto-approve cover them too?
2. The root check is deliberately strict: words like "su" in a commit message, docker beyond read verbs, `systemctl` changes, and scripts that contain such words need a plan even with auto-approve on.
3. Quill Android has no auto-approve UI yet (the phone API has the flag and an off endpoint).
4. kmem decision fdd52bd (plan card layout) should be updated for the new checkbox row.

## Not done / follow-ups

- Large language items waiting for you: UI translation (~940 Desktop / ~120 Android strings), right-to-left text on Desktop, CJK typing on Wayland (GLFW has no text-input protocol), Android RTL layout (`supportsRtl`).
- Split view still has its own tasks button (only the main bar's was removed).
- Android sorting/search folding not localised (only dates were in scope).
- kmem tasks logged tonight: t17ad60 (kmem window refreshes 2–3×), t982851 (kmem window badge), te3470e (kmem window status indicator) — the last two were relayed by the kmem-lock-audit session as your requests; not started without your word. te19825: the gate refused an approved background subagent (heredoc + go test in /tmp) even after resume — gate bug to look at.
- A reviewer marked kmem brief/rules of a throwaway name "zzother" as read while proving a bypass (harmless).
- `internal/whisper` TestLiveStreaming and two cmd/quill tests (TestCloseTabEndsAndSavesIntoInactive, TestThemeOverrideLookups) are flaky under heavy load; they pass alone.
- kmem is on 1.9.3 for this session (kmem-lock-audit rollout); topic mode stays off until kmem 1.10.0 (expected).
