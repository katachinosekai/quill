package chat

import "testing"

func TestAsksApprovalIgnoresQuotedWords(t *testing.T) {
	for text, want := range map[string]bool{
		"1. Do it.\n\n**Do you approve steps 1–4?**": true,
		"Plan above.\n\nApprove this plan?":          true,
		"**Should I do the plan-card fix next (an either/or question gets option buttons instead of \"Approve plan & run all\"), or one of the missing panel options?**": false,
		"Which one: “Approve plan” or “Reply”?": false,
		"Should the `approve` card stay?":       false,
		"OK to run these (all read-only)?":      true,
		"Which did you mean: option 1 (Quill's paired PCs), option 2, or both? Once you answer, I'll write the full plan for approval.": false,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksApproval(); got != want {
			t.Errorf("AsksApproval(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestAsksQuestion(t *testing.T) {
	for text, want := range map[string]bool{
		"Which did you mean: A, B, or both? Once you answer, I'll plan it.": true,
		"**Approve?**": true,
		"Done.":        false,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksQuestion(); got != want {
			t.Errorf("AsksQuestion(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestProposalsGetTheCard(t *testing.T) {
	for text, want := range map[string]bool{
		"**Should I ship ELTF 1.3.7 now (Workshop upload, Steam restart, pzserver deploy, and one join check)?**":                                                              true,
		"Done.\n\n**Should I adapt ELTF's ALifeSquares to A-Life 1.5.3 next? I'd keep its client-side join queue and drop the server part A-Life now covers, as ELTF 1.3.7.**": true,
		"**Do you want me to add the Windows exe to the release?**":                                                                                                            true,
		"Would you like me to start the release prep?":                                                                                                                         true,
		"**Should I do the plan-card fix next, or one of the missing panel options?**":                                                                                         false,
		"**Which panels should I build: all of them, the first four, or a set you name?**":                                                                                     false,
		"Which did you mean: option 1 (Quill's paired PCs), option 2, or both? Once you answer, I'll write the full plan for approval.":                                        false,
		"Plan:\n1. a\n2. b\n\n**Do you approve steps 1–3? Your approval also covers the earlier question only if you say which option you want.**":                             true,
		"**Approve this plan? I'll start with step 1.**":                                                                                                                       true,
		"**Anything else? I can also approve the release later.**":                                                                                                             false,
		"Plan:\n1. a\n2. b\n\nQuestions:\n1. Do you approve steps 1–6? Yes (recommended) or no?\n2. Animation style: fade in each finished paragraph plus smooth reveal of the last one (recommended), typing effect character by character, or fade only?": true,
		"**Plan**\n1. x\n2. y\n\n1. Should \"Animate new text\" start on (recommended) or off?\n2. Do you approve steps 1–7?":                                                                                                                               true,
		"Questions:\n1. Which colour?\n2. What size?": false,
		"Should I? Hmm.":             true,
		"I wonder what to do. Done.": false,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksApproval(); got != want {
			t.Errorf("AsksApproval(%q) = %v, want %v", text, got, want)
		}
	}
}

// tf2db21: "Can I run X? Yes (recommended) or no?" asks for a go-ahead even
// without "steps N" or "run this" in it.
func TestYesNoProposalIsApprovalAsk(t *testing.T) {
	for text, want := range map[string]bool{
		"Reply.\n\n**1. A or B?**\n**2. Can I run `tool find x`? Yes (recommended) or no?**": true,
		"Reply.\n\nCan I run the read-only lookup? Yes (recommended) or no?":                 true,
		"Reply.\n\nCan I run the lookup?\nYes (recommended) or no?":                          true,
		"Reply.\n\nCan I run the lookup? Blue or red?":                                       false,
		"Reply.\n\nWhich lookup should I run? Yes or no?":                                    false,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksApproval(); got != want {
			t.Errorf("AsksApproval(%q) = %v, want %v", text, got, want)
		}
	}
}

// Options on the line after their question (user 2026-11-22: no plan card for
// "1. Can I run steps 1–6?\nYes (recommended) or no?").
func TestApprovalWithOptionsOnNextLine(t *testing.T) {
	for text, want := range map[string]bool{
		"Plan.\n\n**1. Can I run steps 1–6?**\nYes (recommended) or no?":                                                                           true,
		"Plan.\n\n1. Can I run steps 1–5? Yes (recommended) or no?":                                                                                true,
		"Plan.\n\nCan I run steps 1–3? Yes (recommended) or no?":                                                                                   true,
		"Plan.\n\n1. Where should it live?\nInside the window (recommended) or a linked session?":                                                  false,
		"Plan.\n\n1. Can B carry out its plan?\nApprove as is (recommended), approve with changes, or stop?":                                       true,
		"Plan.\n\n1. Where should it live?\nInside the window (recommended) or a linked session\n2. Can I run steps 1–2?\nYes (recommended) or no": true,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksApproval(); got != want {
			t.Errorf("AsksApproval(%q) = %v, want %v", text, got, want)
		}
		if !c.AsksQuestion() {
			t.Errorf("AsksQuestion(%q) = false", text)
		}
	}
	if got := JoinOptionLines("1. Can I run steps 1–6?\nYes (recommended) or no"); got != "1. Can I run steps 1–6? Yes (recommended) or no?" {
		t.Fatalf("joined: %q", got)
	}
	if got := JoinOptionLines("Done.\nThis or that is fine.\n2. Next?\n3. Or not?"); got != "Done.\nThis or that is fine.\n2. Next?\n3. Or not?" {
		t.Fatalf("only an unnumbered options line right under a question joins: %q", got)
	}
}

// A closing "<action>? Yes or no?" question proposes work and gets the
// approval card; one that only asks for information does not.
func TestActionYesNoIsApprovalAsk(t *testing.T) {
	for text, want := range map[string]bool{
		"Done.\n\n**Fix 2 and 3 (the stale label and the 409)? Yes or no?**":               true,
		"Done.\n\n**Fix 2 and 3 (the stale label and the 409)? Yes (recommended) or no?**": true,
		"Done.\n\nRelease Quill Android 0.9.36 now? Yes or no?":                            true,
		"Done.\n\n**Release Quill Android 0.9.36 now? Yes (recommended) or no?**":          true,
		"Done.\n\n**Merge it? Yes or no?**":                                                true,
		"Done.\n\nMerge fix/gate-dialog-stale into master? Yes or no?":                     true,
		"Done.\n\nPush the branch? Yes, or no?":                                            true,
		"Done.\n\nDelete the old worktree? Yes (recommended) or no?":                       true,
		"Done.\n\nRestart Quill Desktop now? Yes or no?":                                   true,
		"Done.\n\nCommit and tag v1.2.3? Yes or no?":                                       true,
		"Done.\n\nOkay to apply the patch? Yes or no?":                                     true,
		"Done.\n\nGo ahead with the rebase? Yes or no?":                                    true,
		"Plan.\n\n1. Where?\n2. **Fix 2 and 3 (details)? Yes or no?**":                     true,
		"Found it. Fix 2 and 3? Yes or no?":                                                true,
		"Fix the title (is it \"a? b?\") now? Yes or no?":                                  true,
		"Done.\n\nDid the kmem Assistant show its replies? Yes, no, or not checked yet?":   false,
		"Done.\n\nIs that placement OK? Yes or no?":                                        false,
		"Done.\n\nDoes the card look right? Yes or no?":                                    false,
		"Done.\n\nHas the build finished? Yes or no?":                                      false,
		"Done.\n\nShow me the log? Yes or no?":                                             false,
		"Done.\n\nStop the session? Yes or no?":                                            false,
		"Done.\n\nWhich fix should I merge? Yes or no?":                                    false,
		"Done.\n\nWas the release published? Yes or no?":                                   false,
		"Done.\n\nThe fix needs a merge. Is it merged? Yes or no?":                         false,
		"Done.\n\nFix 2 and 3? Blue or red?":                                               false,
		"Done.\n\nFix 2 and 3? Yes, no, or not sure yet?":                                  false,
		"Done.\n\nMerged it. Yes or no?":                                                   false,
		"Done.\n\nIs the merge fix 2 and 3? Yes or no?":                                    false,
		"Done.\n\nWill the release run tonight? Yes or no?":                                false,
		"Done.\n\nAre the tests run on every push? Yes or no?":                             false,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksApproval(); got != want {
			t.Errorf("AsksApproval(%q) = %v, want %v", text, got, want)
		}
	}
	if ClosingApproval("Plan.\n\n1. Where?\n2. **Merge it? Yes or no?**") == "" {
		t.Error("ClosingApproval misses the numbered action question")
	}
	if ClosingApproval("Plan.\n\n1. Where?\n2. **Is that placement OK? Yes or no?**") != "" {
		t.Error("ClosingApproval takes an information question")
	}
}

func TestProposesWork(t *testing.T) {
	for q, want := range map[string]bool{
		"Merge it": true, "Can I run steps 1-3": true, "Fix 2 and 3 (x)": true, "Do you approve steps 1-3": true,
		"Did it show its replies": false, "Is that placement OK": false, "Which one": false, "": false,
	} {
		if got := ProposesWork(q); got != want {
			t.Errorf("ProposesWork(%q) = %v, want %v", q, got, want)
		}
	}
}

// t225b3e (2026-11-23): plans that ask for the user's yes without a closing
// question got no card, so a plain "yes" didn't approve them (paraphrased
// from the session's replies).
func TestAsksApprovalRequestsYes(t *testing.T) {
	for text, want := range map[string]bool{
		"That works with the desktop's password prompt.\n\n**Plan (needs your yes):**\n1. Write the script to a file.\n2. Run it with `pkexec` in the background.\n3. Check the output.\n4. Record it in kmem.": true,
		"Same plan, in order:\n1. Write the script.\n2. Then run it.\n\nReply **yes** to run it.":                                                                                                               true,
		"Plan (awaiting your approval):\n1. Edit a.go.\n   (keeps the API)\n2. Run the tests.":                                                                                                                  true,
		"Say yes and I'll start.": true,
		// Zomboid 2026-11-23: the ask after another sentence on its line.
		"Plan:\n1. Build it locally.\n2. Check memory.\n\nThe author reports (t1) stay for later. Reply **yes** to run it.": true,
		"Plan:\n1. Edit a.go.\n2. Run the tests.\n\nReply **Approved.** to run it.":                                         true,
		"I ran it. Reply with the file name to continue.":                                                                   false,
		"**Plan (needs your yes):**\n1. Write the script.\n\nI'll check the logs first.":                                    false,
		"Plan:\n1. Write the script.\n2. Run it.":                                                                           false,
		"Reply with the file name to continue.":                                                                             false,
		"Steps done:\n1. Wrote the script.\n2. Ran it.":                                                                     false,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksApproval(); got != want {
			t.Errorf("AsksApproval(%q) = %v, want %v", text, got, want)
		}
	}
}

// Plan format spec v1 (docs/plan-format.md): the closing bold question, with
// or without numbered choice questions after it, asks for approval.
func TestPlanFormatV1AsksApproval(t *testing.T) {
	plan := "Plan:\n1. Edit `readonly.go`: add `findmnt`, so mount checks need no plan.\n2. Start a background subagent that runs `go test ./...` and reports failures.\n\n**Can I run steps 1–2? Yes (recommended) or no?**"
	for _, text := range []string{plan, plan + "\n1. Commit message style: short (recommended) or detailed?"} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if !c.AsksApproval() {
			t.Errorf("AsksApproval(%q) = false", text)
		}
	}
}
