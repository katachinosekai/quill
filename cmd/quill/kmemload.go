package main

import (
	"strings"
	"sync"
	"time"
)

// One window event (show, focus, tab change, the bin changing, a phone write,
// the timer) used to start several independent loads of the same kmem reads
// within ~100 ms: the panel's reload, the badge refresh and the rename scan
// each ran `kmem brief`, `handoff list`, `task list`, `rollout status` and
// `trash list` themselves (kmem task tce45f4). kmemCoalescer is the one
// loader they share: an identical read-only command that is already running
// is joined, and its answer is reused for kmemCoalesceFor; any kmem command
// that may change state (and invalidate()) drops the answers.
const kmemCoalesceFor = 400 * time.Millisecond

type kmemCoalescer struct {
	mu   sync.Mutex
	gen  int // bumped by every invalidation
	call map[string]*kmemCall
}

type kmemCall struct {
	done chan struct{}
	out  string
	err  error
	at   time.Time // when it finished; zero while running
	gen  int
}

// kmemReadOnly: the commands whose answer may be shared.
func kmemReadOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "brief", "topics", "version":
		return true
	case "handoff", "task", "trash":
		return len(args) > 1 && args[1] == "list"
	case "rollout":
		return len(args) > 1 && args[1] == "status"
	}
	return false
}

// invalidate drops the shared answers (after a write kmem could not see).
func (c *kmemCoalescer) invalidate() {
	c.mu.Lock()
	c.gen++
	c.call = nil
	c.mu.Unlock()
}

// runKmemShared runs a read-only kmem command for the window's loaders (the
// panel, the badge, the bin, the rename scan): identical calls share one run.
// Pollers that wait for a change (handoff saves) use runKmem, never this.
func (d *desk) runKmemShared(args ...string) (string, error) {
	return d.kmemCo.run(d, args)
}

func (c *kmemCoalescer) run(d *desk, args []string) (string, error) {
	if !kmemReadOnly(args) {
		return d.runKmem("kmem", args...)
	}
	key := strings.Join(args, "\x00")
	c.mu.Lock()
	if cl := c.call[key]; cl != nil && cl.gen == c.gen && (cl.at.IsZero() || time.Since(cl.at) < kmemCoalesceFor) {
		c.mu.Unlock()
		<-cl.done
		return cl.out, cl.err
	}
	cl := &kmemCall{done: make(chan struct{}), gen: c.gen}
	if c.call == nil {
		c.call = map[string]*kmemCall{}
	}
	c.call[key] = cl
	c.mu.Unlock()
	cl.out, cl.err = d.runKmemRaw("kmem", args...)
	c.mu.Lock()
	cl.at = time.Now()
	c.mu.Unlock()
	close(cl.done)
	return cl.out, cl.err
}
