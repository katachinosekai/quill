package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

// stubKmemEnv sets the KMEM_SESSION sessions inherit.
func stubKmemEnv(t *testing.T, v string) {
	t.Helper()
	old := sessionKmemEnv
	sessionKmemEnv = func() string { return v }
	t.Cleanup(func() { sessionKmemEnv = old })
}

var (
	splitOn   = [][]string{{markSplit}, {markOwn, "alpha"}}
	splitPZ   = [][]string{{markSplit}, {markOwn, "alpha"}, {markPZ}}
	className = map[gateClass]string{classRead: "read", classNoPlan: "no-plan", classGated: "gated"}
)

func TestGateSplitClassifier(t *testing.T) {
	stubKmemEnv(t, "")
	R, N, G := classRead, classNoPlan, classGated
	for _, c := range []struct {
		cmd   string
		want  gateClass
		extra [][]string
	}{
		// kmem bookkeeping under the session's own name
		{`kmem --as alpha task checkpoint t1 "step 2 done"`, N, nil},
		{`kmem --as=alpha add proj/status "text" --why "w" --replaces f1`, N, nil},
		{`kmem --as alpha task add "title" --prio 3 --topic proj --after t2`, N, nil},
		{`kmem --as alpha task claim t1`, N, nil},
		{`kmem task note t1 "a note" --as alpha`, N, nil},
		{`kmem --as alpha task release t1`, N, nil},
		{`kmem --as alpha handoff save proj --state "s" --pointer "p" --same`, N, nil},
		{`KMEM_SESSION=alpha kmem task checkpoint t1 x`, N, nil},
		{`kmem --as alpha task checkpoint t1 x 2>&1 | tail -1`, N, nil},
		{`kmem --as alpha task checkpoint t1 x; kmem --as alpha get proj`, N, nil},
		{`timeout 20 kmem --as alpha task checkpoint t1 x`, N, nil},
		// other name, no name, --force and friends
		{`kmem --as beta task checkpoint t1 x`, G, nil},
		{`KMEM_SESSION=beta kmem task checkpoint t1 x`, G, nil},
		{`KMEM_SESSION=alpha kmem --as beta task checkpoint t1 x`, G, nil},
		{`kmem task checkpoint t1 x`, G, nil}, // no --as, no KMEM_SESSION
		{`kmem --as alpha task checkpoint t1 x --force`, G, nil},
		{`kmem --as alpha task claim t1 -f`, G, nil},
		{`kmem --as alpha add proj/x text --pin`, G, nil},
		{`kmem --as alpha add proj/x text --retires f1`, G, nil},
		{`kmem --as alpha handoff save proj --transfer beta`, G, nil},
		{`kmem --as alpha task checkpoint t1 -- --force`, G, nil},
		// still need a plan
		{`kmem --as alpha task done t1 "result"`, G, nil},
		{`kmem --as alpha task transfer t1 beta`, G, nil},
		{`kmem --as alpha task block t1 t2`, G, nil},
		{`kmem --as alpha retire f1 "reason"`, G, nil},
		{`kmem --as alpha trash f1 "why"`, G, nil},
		{`kmem --as alpha trash empty`, G, nil},
		{`kmem --as alpha bin empty`, G, nil},
		{`kmem --as alpha pin f1`, G, nil},
		{`kmem --as alpha handoff rename a b`, G, nil},
		{`kmem --as alpha rules sync`, G, nil},
		{`kmem --as beta brief`, G, nil},
		// chains, pipes, substitution, redirects
		{`kmem --as alpha task checkpoint t1 x && git push`, G, nil},
		{`kmem --as alpha task checkpoint t1 x | sh`, G, nil},
		{`kmem --as alpha task checkpoint t1 "$(cat ~/.ssh/id_ed25519)"`, G, nil},
		{"kmem --as alpha task checkpoint t1 `id`", G, nil},
		{`kmem --as alpha task checkpoint t1 x > notes.md`, G, nil},
		{`kmem --as alpha task checkpoint $T x`, G, nil},
		{`kmem --as alpha get proj >> notes.md`, G, nil},
		{`sudo kmem --as alpha task checkpoint t1 x`, G, nil},
		{`/opt/x/kmem --as alpha task checkpoint t1 x`, G, nil},
		// reads the split adds
		{`git rev-list --count HEAD`, R, nil},
		{`git tag -l 'v0.1.*'`, R, nil},
		{`git tag --list --sort=-v:refname`, R, nil},
		{`git tag`, R, nil},
		{`git tag --contains abc123`, R, nil},
		{`git branch --list 'feat/*'`, R, nil},
		{`git branch --merged master`, R, nil},
		{`git -C repo remote -v`, R, nil},
		{`git ls-tree -r HEAD`, R, nil},
		{`git ls-remote --tags origin`, R, nil},
		{`git cat-file -p HEAD`, R, nil},
		{`git cat-file -e abc123 2>/dev/null && echo yes`, R, nil},
		{`git describe --tags`, R, nil},
		{`git worktree list --porcelain`, R, nil},
		{`git config --get user.name`, R, nil},
		{`git config get remote.origin.url`, R, nil},
		{`timeout 10 git status --short`, R, nil},
		{`timeout -k 5 30s git log -1`, R, nil},
		{`pzlock status`, R, nil},
		{`pzlock check alpha`, R, nil},
		{`gh api repos/o/r/pulls`, R, nil},
		{`gh api -X GET repos/o/r/pulls`, R, nil},
		{`kmem --as alpha sessions --live`, R, nil},
		{`kmem howto show build`, R, nil},
		// gated look-alikes
		{`git tag v1`, G, nil},
		{`git tag -d v1`, G, nil},
		{`git tag -l -d v1`, G, nil},
		{`git tag -a v1 -m x`, G, nil},
		{`git branch newname`, G, nil},
		{`git branch -D old`, G, nil},
		{`git branch --set-upstream-to=o/x`, G, nil},
		{`git ls-remote --upload-pack=evil origin`, G, nil},
		{`git ls-remote -u evil origin`, G, nil},
		{`git ls-remote ext::sh`, G, nil},
		{`git cat-file --textconv HEAD:f`, G, nil},
		{`git cat-file --batch`, G, nil},
		{`git worktree add ../x`, G, nil},
		{`git worktree remove ../x`, G, nil},
		{`git config user.name x`, G, nil},
		{`git config --unset user.name`, G, nil},
		{`git config --get x --add y`, G, nil},
		{`git -c core.pager=x log`, G, nil},
		{`timeout 5 git push`, G, nil},
		{`timeout 5 rm -rf x`, G, nil},
		{`timeout 5 sudo ls`, G, nil},
		{`timeout 5 X=1 ls`, G, nil},
		{`timeout $N git status`, G, nil},
		{`timeout git status`, G, nil},
		{`pzlock acquire alpha`, G, nil},
		{`pzlock release alpha`, G, nil},
		{`pzlock clear`, G, nil},
		{`pzlock check`, G, nil},
		{`gh api -X POST repos/o/r/issues`, G, nil},
		{`gh api --method DELETE repos/o/r/git/refs/x`, G, nil},
		{`gh api repos/o/r/issues -f title=x`, G, nil},
		{`gh api repos/o/r/issues -F title=x`, G, nil},
		{`gh api --input body.json repos/o/r/issues`, G, nil},
		{`gh api --hostname evil.example repos/o/r`, G, nil},
		{`git log $(rm -rf x)`, G, nil},
		{`git rev-list HEAD > revs.txt`, G, nil},
		// Zomboid: left to kmem-guard only when its pz-lock is fail-closed
		{`pz_launch`, G, nil},
		{`pz_launch`, N, splitPZ},
		{`~/games/pz/projectzomboid.sh -nosteam`, N, splitPZ},
		{`pkill -f projectzomboid`, N, splitPZ},
		{`killall -9 ProjectZomboid64`, N, splitPZ},
		{`pkill -f projectzomboid`, G, nil},
		{`pkill -f 'projectzomboid|firefox'`, G, splitPZ},
		{`pkill firefox`, G, splitPZ},
		{`pkill -u someone projectzomboid`, G, splitPZ},
		{`sudo pkill -f projectzomboid`, G, splitPZ},
		{`pzlock acquire alpha`, G, splitPZ},
	} {
		extra := splitOn
		if c.extra != nil {
			extra = c.extra
		}
		if got := commandClass(c.cmd, extra); got != c.want {
			t.Errorf("commandClass(%q) = %s, want %s", c.cmd, className[got], className[c.want])
		}
	}
}

// No --as: kmem acts under KMEM_SESSION of the session's environment.
func TestGateSplitSessionEnv(t *testing.T) {
	stubKmemEnv(t, "alpha")
	if got := commandClass(`kmem task checkpoint t1 x`, splitOn); got != classNoPlan {
		t.Fatalf("env names the session: %s", className[got])
	}
	stubKmemEnv(t, "beta")
	if got := commandClass(`kmem task checkpoint t1 x`, splitOn); got != classGated {
		t.Fatalf("env names another session: %s", className[got])
	}
	if got := commandClass(`kmem task checkpoint t1 x`, [][]string{{markSplit}}); got != classGated {
		t.Fatalf("no own name known: %s", className[got])
	}
}

// Switch off: what the gate did before the split.
func TestGateSplitOff(t *testing.T) {
	stubKmemEnv(t, "alpha")
	off := [][]string{{markOwn, "alpha"}, {markPZ}} // markers without markSplit count for nothing
	for _, cmd := range []string{
		`kmem --as alpha task checkpoint t1 x`, `KMEM_SESSION=alpha kmem get proj`, `kmem task checkpoint t1 x`,
		`git rev-list HEAD`, `git tag -l`, `git config --get user.name`, `git worktree list`, `timeout 5 git status`,
		`pzlock status`, `pzlock check alpha`, `pz_launch`, `pkill -f projectzomboid`, `kmem sessions`,
	} {
		if got := commandClass(cmd, off); got != classGated {
			t.Errorf("off: commandClass(%q) = %s", cmd, className[got])
		}
	}
	for _, cmd := range []string{`git status`, `git branch -a`, `git remote -v`, `git describe`, `kmem get proj`, `gh api repos/o/r`} {
		if got := commandClass(cmd, off); got != classRead {
			t.Errorf("off: commandClass(%q) = %s, still a read", cmd, className[got])
		}
	}
}

// A bare `kmem` line (or any kmem write) in the user's Read-only commands
// lets no kmem write through, switch on or off.
func TestUserKmemLineIsVerbChecked(t *testing.T) {
	stubKmemEnv(t, "")
	user := userReadOnlyExtra("kmem\nkmem retire\nkmem trash empty\nkmem --as alpha task checkpoint\nkmem get\nkmem --as beta brief\npzlock acquire\n\x00split")
	for _, e := range user {
		if e[0] == "kmem" && (len(e) == 1 || e[1] == "retire" || e[1] == "trash" || e[len(e)-1] == "checkpoint") {
			t.Fatalf("kept a kmem write line: %v", e)
		}
		if e[0] == markSplit {
			t.Fatal("a marker can't come from the user's text")
		}
	}
	for _, split := range []bool{false, true} {
		extra := append([][]string{}, user...)
		if split {
			extra = append(extra, []string{markSplit})
		}
		for _, cmd := range []string{`kmem retire f1 "r"`, `kmem trash empty`, `kmem --as alpha task checkpoint t1 x`, `kmem task done t1 r`} {
			if commandClass(cmd, extra) != classGated {
				t.Errorf("split=%v: %q passes through a user kmem line", split, cmd)
			}
		}
		if !readOnlyCommand(`kmem --as beta brief`, extra) || !readOnlyCommand(`pzlock acquire x`, extra) {
			t.Errorf("split=%v: the user's read lines still count", split)
		}
	}
	// Quill's own save window keeps its wildcard.
	if !readOnlyCommand(`kmem --as alpha handoff save alpha --state s`, kmemSaveExtra) {
		t.Fatal("kmemSaveExtra still lets the save through")
	}
}

const (
	kbAgent = `{"type":"assistant","message":{"id":"msg_kb","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_kbagent","name":"Agent","input":{"description":"review","prompt":"review the diff","run_in_background":true}}]},"parent_tool_use_id":null,"session_id":"s1"}`
	kbBash  = `{"type":"assistant","message":{"id":"msg_kb","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_kbbash","name":"Bash","input":{"command":"kmem --as alpha task checkpoint t1 \"started review\""}}]},"parent_tool_use_id":null,"session_id":"s1"}`
	kbReq   = `{"type":"control_request","request_id":"rkb","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"kmem --as alpha task checkpoint t1 \"started review\""},"tool_use_id":"toolu_kbbash"}}`
	kbStart = `{"type":"system","subtype":"task_started","task_id":"agentKb1","tool_use_id":"toolu_kbagent","description":"review","task_type":"local_agent","is_backgrounded":true}`
)

// Point 3 (user 2026-11-24): a kmem checkpoint next to an Agent call in one
// reply no longer stops the subagent; with the switch off it does, as before.
func TestBookkeepingKeepsItsBatchAgent(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[on], func(t *testing.T) {
			stubKmemEnv(t, "")
			h := newHarness(t)
			stopped := fakeStops(t)
			s := gatedSession(t, h)
			h.ui(func() {
				h.d.app.Preferences().SetBool(prefGateSplit, on)
				setOwnKmem(h, s)
				userEvent(h, s, "review it", nil)
				feed(t, h, s, kbAgent)
				feed(t, h, s, kbBash)
				feed(t, h, s, kbStart)
				e := gateRequest(t, h, s, kbReq)
				if on {
					if e.PermState != chat.PermAutoAllowed || len(*stopped) != 0 || len(s.refusedAgents) != 0 || s.planWait {
						t.Fatalf("on: checkpoint allowed, agent kept: %v %v %v", e.PermState, *stopped, s.refusedAgents)
					}
				} else if e.PermState != chat.PermPlanAsked || len(*stopped) != 1 {
					t.Fatalf("off: as before, refused and the agent stopped: %v %v", e.PermState, *stopped)
				}
			})
		})
	}
}

// A genuinely refused call still refuses its batch, kmem bookkeeping in it
// too (no-plan isn't read-only), and still stops the batch's subagent.
func TestRefusedBatchRefusesBookkeeping(t *testing.T) {
	stubKmemEnv(t, "")
	h := newHarness(t)
	stopped := fakeStops(t)
	s := gatedSession(t, h)
	h.ui(func() {
		setOwnKmem(h, s)
		userEvent(h, s, "go", nil)
		feed(t, h, s, kbAgent)
		feed(t, h, s, `{"type":"assistant","message":{"id":"msg_kb","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_kbpush","name":"Bash","input":{"command":"git push"}}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		feed(t, h, s, kbBash)
		feed(t, h, s, kbStart)
		if e := gateRequest(t, h, s, `{"type":"control_request","request_id":"rkp","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"git push"},"tool_use_id":"toolu_kbpush"}}`); e.PermState != chat.PermPlanAsked {
			t.Fatalf("the push is refused: %v", e.PermState)
		}
		if len(*stopped) != 1 {
			t.Fatalf("the batch's subagent is stopped: %v", *stopped)
		}
		if e := gateRequest(t, h, s, kbReq); e.PermState != chat.PermPlanAsked {
			t.Fatalf("bookkeeping in a refused batch is refused too: %v", e.PermState)
		}
	})
}

// The pz-lock-closed feature (cached `kmem version --json` features) adds
// the Zomboid marker; without it Quill's gate stays on Zomboid.
func TestPZLockClosedFeature(t *testing.T) {
	stubKmemEnv(t, "")
	h := newHarness(t)
	s := gatedSession(t, h)
	h.ui(func() {
		kill := `{"type":"control_request","request_id":"rz%d","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"pkill -f projectzomboid"},"tool_use_id":"toolu_z%d"}}`
		if e := gateRequest(t, h, s, fmt.Sprintf(kill, 1, 1)); e.PermState != chat.PermPlanAsked {
			t.Fatalf("no feature: gated: %v", e.PermState)
		}
		s.planWait = false
		v := &h.d.kmemVer
		v.mu.Lock()
		v.api, v.features, v.at = kmemAPI, []string{"sessions", pzLockClosed}, time.Now()
		v.mu.Unlock()
		if e := gateRequest(t, h, s, fmt.Sprintf(kill, 2, 2)); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("pz-lock-closed: left to kmem-guard: %v", e.PermState)
		}
		h.d.app.Preferences().SetBool(prefGateSplit, false)
		if e := gateRequest(t, h, s, fmt.Sprintf(kill, 3, 3)); e.PermState != chat.PermPlanAsked {
			t.Fatalf("switch off: gated again: %v", e.PermState)
		}
	})
}

func setOwnKmem(h *harness, s *deskSession) {
	if h.d.org.Kmem == nil {
		h.d.org.Kmem = map[string]string{}
	}
	h.d.org.Kmem[s.id] = "alpha"
}
