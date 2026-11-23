package main

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKmemRenamePure(t *testing.T) {
	links := map[string]string{"a": "quill", "b": "garden", "c": "", "d": "quill"}
	got := renamedLinks(links, map[string]string{"quill": "quill-desktop", "garden": "garden"})
	if want := map[string]string{"a": "quill-desktop", "d": "quill-desktop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("renamedLinks = %v, want %v", got, want)
	}
	listed := []kmemHandoff{{Name: "garden"}}
	if got := kmemRenameCandidates(links, listed, nil); !reflect.DeepEqual(got, []string{"quill"}) {
		t.Fatalf("candidates = %v: only linked names missing from the list, once", got)
	}
	if got := kmemRenameCandidates(links, listed, map[string]bool{"quill": true}); len(got) != 0 {
		t.Fatalf("skipped names are not asked: %v", got)
	}
}

// renameKmem: a fake kmem with handoffs and renames (old -> new); it never
// renames anything itself.
type renameKmem struct {
	mu      sync.Mutex
	saved   map[string]bool
	renamed map[string]string
	calls   []string
}

func (k *renameKmem) run(prog string, args ...string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	line := strings.Join(args, " ")
	k.calls = append(k.calls, line)
	switch {
	case line == "version --json":
		return fmt.Sprintf(`{"api":%d,"version":"test"}`, kmemAPI), nil
	case line == "handoff list --json":
		var hs []string
		for n := range k.saved {
			hs = append(hs, fmt.Sprintf(`{"name":%q,"items":2,"saved":%q}`, n, time.Now().Add(-time.Hour).Format(time.RFC3339)))
		}
		return fmt.Sprintf(`{"api":%d,"handoffs":[%s]}`, kmemAPI, strings.Join(hs, ",")), nil
	case strings.HasPrefix(line, "handoff resolve "):
		n := strings.Fields(line)[2]
		chain := []string{n}
		for !k.saved[n] {
			next, ok := k.renamed[n]
			if !ok {
				return "no handoff " + chain[0] + " (kmem handoff list)", fmt.Errorf("exit 1")
			}
			n = next
			chain = append(chain, n)
		}
		return fmt.Sprintf(`{"name":%q,"chain":["%s"],"api":%d}`, n, strings.Join(chain, `","`), kmemAPI), nil
	case strings.HasPrefix(line, "handoff rename"):
		panic("tests never rename kmem handoffs")
	}
	return "", nil
}

func (k *renameKmem) asked(prefix string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, c := range k.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func lastNotice(s *deskSession) string {
	if s == nil || len(s.conv.Items) == 0 {
		return ""
	}
	return s.conv.Items[len(s.conv.Items)-1].Text
}

// The user's bug (2026-11-22): `kmem handoff rename quill quill-desktop` ran
// outside Quill Desktop; the live tab must follow, so --resume quill-desktop
// opens it, and --resume quill follows the rename to it.
func TestKmemRenameFollowedAndResume(t *testing.T) {
	h := newHarness(t)
	k := &renameKmem{saved: map[string]bool{"quill-desktop": true, "garden": true}, renamed: map[string]string{"quill": "quill-desktop"}}
	h.d.kmemOff = false
	h.d.kmemRun = k.run
	var live, other *deskSession
	h.ui(func() {
		live = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[live.id] = "quill"
		h.d.org.Titles[live.id] = "quill"
		h.d.org.Projects = map[string]string{"quill": "/home/user/proj"}
		other = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[other.id] = "garden"
		h.d.open(other.id)
		h.d.reconcileKmemNames(true, nil)
	})
	h.eventually("the tab link follows the rename", func() bool {
		return h.d.org.Kmem[live.id] == "quill-desktop"
	})
	h.ui(func() {
		if h.d.org.Titles[live.id] != "quill-desktop" || h.d.org.Projects["quill-desktop"] != "/home/user/proj" || h.d.org.Kmem[other.id] != "garden" {
			t.Fatalf("title %q, projects %v, other %q", h.d.org.Titles[live.id], h.d.org.Projects, h.d.org.Kmem[other.id])
		}
		if !strings.Contains(lastNotice(live), "renamed to “quill-desktop” outside Quill Desktop") {
			t.Fatalf("the tab says it followed: %q", lastNotice(live))
		}
	})
	if k.asked("handoff resolve garden") != 0 {
		t.Fatal("a listed name is current; kmem is not asked about it")
	}

	h.ui(func() { h.d.resumeNamedFromCLI("quill-desktop") })
	h.eventually("--resume opens the tab with that current name and says so", func() bool {
		ok := h.d.current == live && hasNotice(live, "quill --resume quill-desktop: opened the tab")
		return ok
	})

	// A NEW handoff "quill" now exists: --resume quill must not open the
	// renamed tab but start a fresh session from the new handoff.
	k.mu.Lock()
	k.saved["quill"] = true
	k.mu.Unlock()
	var before int
	h.ui(func() {
		h.d.open(other.id)
		before = len(h.d.sessions)
		h.d.resumeNamedFromCLI("quill")
	})
	h.eventually("a fresh session from the new handoff, not the renamed tab", func() bool {
		c := h.d.current
		ok := len(h.d.sessions) == before+1 && c != live && c != other && h.d.org.Kmem[c.id] == "quill" &&
			hasNotice(c, "started a fresh session from the kmem handoff “quill”")
		return ok
	})
}

func TestKmemResumeFollowsRenamedName(t *testing.T) {
	h := newHarness(t)
	k := &renameKmem{saved: map[string]bool{"quill-desktop": true}, renamed: map[string]string{"quill": "quill-desktop"}}
	h.d.kmemOff = false
	h.d.kmemRun = k.run
	var live, other *deskSession
	h.ui(func() {
		live = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[live.id] = "quill"
		other = h.d.createSession(h.proj, "default", "default")
		h.d.open(other.id)
		h.d.resumeNamedFromCLI("quill")
	})
	h.eventually("--resume of the old name opens the renamed tab", func() bool {
		ok := h.d.current == live && h.d.org.Kmem[live.id] == "quill-desktop" &&
			hasNotice(live, "(kmem renamed “quill” to “quill-desktop”)")
		return ok
	})
	h.ui(func() { h.d.resumeNamedFromCLI("nope") })
	h.eventually("an unknown name is noted", func() bool {
		return strings.Contains(lastNotice(h.d.current), "no kmem handoff or live session called “nope”")
	})
	// the minute check skips names kmem had no handoff for
	h.ui(func() {
		h.d.org.Kmem[other.id] = "unsaved"
		h.d.reconcileKmemNames(true, nil)
	})
	// Asked is not applied: the scan's result reaches kmemNoHandoff on the UI
	// thread afterwards, and a minute check started before that (under load)
	// legitimately asks again. Wait for the applied result.
	h.eventually("asked once and the answer applied", func() bool {
		// eventually already holds the UI lock
		return h.d.kmemNoHandoff["unsaved"] && k.asked("handoff resolve unsaved") == 1
	})
	ran := false
	h.ui(func() { h.d.reconcileKmemNames(false, func(kmemRenameScan) { ran = true }) })
	h.eventually("the minute check ran", func() bool { return ran })
	if n := k.asked("handoff resolve unsaved"); n != 1 {
		t.Fatalf("the minute check asked about a name kmem has no handoff for again (%d)", n)
	}
}
