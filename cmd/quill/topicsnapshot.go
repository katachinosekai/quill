package main

import (
	"fmt"
	"strings"
	"time"
)

// Topic snapshot (docs/design/topic-tabs.md §3.8a, kmem 1.13.0): one
// snapshot per topic, stored as the handoff named like the topic. Quill
// Desktop only reads it (Topics tab, phone GET /phone/topics) and words the
// end-check "snapshot" blocker; sessions save it themselves. Everything here
// is gated on kmem's features; older kmem keeps today's behaviour.

const (
	topicSnapshotFeature = "topic-snapshot"
	topicTitleFeature    = "topic-title"
	// snapshotOldAge: kmem's fixed threshold for an "old" snapshot (not a
	// policy TTL).
	snapshotOldAge = 14 * 24 * time.Hour
)

// snapQuestion: an open question waiting on the user (`handoff get --json`
// "snapshot.questions[]").
type snapQuestion struct {
	QID         string   `json:"qid"`
	Text        string   `json:"text"`
	Options     []string `json:"options"`
	Recommended string   `json:"recommended"`
	Asked       string   `json:"asked"`
	Answered    string   `json:"answered"`
	ID          string   `json:"id"`
}

// snapJob: a job in flight. Running is nil when the job has no pid (it stays
// until the next snapshot).
type snapJob struct {
	What    string `json:"what"`
	PID     int    `json:"pid"`
	Log     string `json:"log"`
	Running *bool  `json:"running"`
	ID      string `json:"id"`
}

// topicSnapshot: `kmem handoff get T --json` "snapshot" (also `context
// --topic T --json` topic.snapshot; its "answered" differs, so it is not read).
type topicSnapshot struct {
	Name       string         `json:"name"`
	Saved      string         `json:"saved"`
	By         string         `json:"by"`
	AgeH       float64        `json:"age_h"`
	Old        bool           `json:"old"`
	Questions  []snapQuestion `json:"questions"`
	Jobs       []snapJob      `json:"jobs"`
	Hypotheses []string       `json:"hypotheses"`
	States     []string       `json:"states"`
}

func (s *topicSnapshot) savedAt() time.Time {
	t, _ := time.Parse(time.RFC3339, s.Saved)
	return t
}

// isOld: kmem's "old" flag, or older than the fixed 14 days by its time.
func (s *topicSnapshot) isOld(now time.Time) bool {
	if s.Old {
		return true
	}
	t := s.savedAt()
	return !t.IsZero() && now.Sub(t) > snapshotOldAge
}

// readSnapshots reads the snapshot of every topic in names that has a
// handoff of the same name (converted old handoffs are skipped). Background
// goroutine only; js is kmemJSON or its shared variant.
func readSnapshots(js func(any, ...string) bool, handoffs []kmemHandoffRow, names map[string]bool) map[string]*topicSnapshot {
	out := map[string]*topicSnapshot{}
	for _, h := range handoffs {
		if !names[h.Name] || h.ConvertedTo != "" {
			continue
		}
		var r struct {
			Snapshot *topicSnapshot `json:"snapshot"`
		}
		if js(&r, "handoff", "get", h.Name) && r.Snapshot != nil {
			out[h.Name] = r.Snapshot
		}
	}
	return out
}

// questionText: one open question with its options and the recommended one.
func questionText(q snapQuestion) string {
	t := q.Text
	if q.QID != "" {
		t = q.QID + ": " + t
	}
	if len(q.Options) > 0 {
		t += " (options: " + strings.Join(q.Options, "; ")
		if q.Recommended != "" {
			t += "; recommended: " + q.Recommended
		}
		t += ")"
	} else if q.Recommended != "" {
		t += " (recommended: " + q.Recommended + ")"
	}
	return t
}

func jobText(j snapJob) string {
	t := firstNonEmpty(j.What, "job")
	var meta []string
	if j.PID > 0 {
		meta = append(meta, fmt.Sprintf("pid %d", j.PID))
	}
	switch {
	case j.Running == nil:
		meta = append(meta, "no pid: kept until the next snapshot")
	case *j.Running:
		meta = append(meta, "running")
	default:
		meta = append(meta, "ended")
	}
	if j.Log != "" {
		meta = append(meta, "log "+j.Log)
	}
	return t + " (" + strings.Join(meta, ", ") + ")"
}

// snapshotHead: "Snapshot saved 3h ago by alpha", with the old marker.
func snapshotHead(s *topicSnapshot, now time.Time) string {
	h := "Snapshot saved " + firstNonEmpty(agoAt(s.savedAt(), now), "at an unknown time")
	if s.By != "" {
		h += " by " + s.By
	}
	if s.isOld(now) {
		h += " · old (over 14 days)"
	}
	return h
}

// snapshotLines: the Topics tab's lines for one snapshot: the head, open
// questions, jobs, states and hypotheses.
func snapshotLines(s *topicSnapshot, now time.Time) []string {
	lines := []string{snapshotHead(s, now)}
	for _, q := range s.Questions {
		lines = append(lines, "Open question "+questionText(q))
	}
	for _, j := range s.Jobs {
		lines = append(lines, "Job: "+jobText(j))
	}
	for _, st := range s.States {
		lines = append(lines, "State: "+oneLine(st, 600))
	}
	for _, hy := range s.Hypotheses {
		lines = append(lines, "Hypothesis: "+oneLine(hy, 600))
	}
	return lines
}

// agoAt is ago with a given now ("" for a zero time).
func agoAt(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
