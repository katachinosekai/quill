package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

func TestPeerMessageExpiryAndReceipts(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.ui(func() {
		d := h.d
		s := d.createSession(h.proj, "default", "default")
		d.org.Titles[s.id] = "Peer PC"
		send := d.routePeerMessage(operatorMessageRequest{To: "Peer PC", Text: "old wording", from: "Quill"}, now)
		if send.Status != "queued" || send.From != "Quill" || !send.ExpiresAt.Equal(now.Add(72*time.Hour)) {
			t.Fatalf("send: %+v", send)
		}
		replace := d.routePeerMessage(operatorMessageRequest{Op: "replace", ID: send.ID, Text: "new wording", TTL: 2 * time.Hour}, now)
		if replace.Status != "queued" || replace.ID == send.ID || !replace.ExpiresAt.Equal(now.Add(2*time.Hour)) {
			t.Fatalf("replace: %+v", replace)
		}
		status := func(id string, at time.Time) operatorMessageReceipt {
			return d.routePeerMessage(operatorMessageRequest{Op: "status", ID: id}, at)
		}
		if r := status(send.ID, now); r.Status != "replaced" || r.ReplacementID != replace.ID {
			t.Fatalf("old receipt: %+v", r)
		}
		d.peerMessages = nil // reload the same preferences
		if r := status(replace.ID, now.Add(time.Hour)); r.Status != "queued" || !r.ExpiresAt.Equal(replace.ExpiresAt) {
			t.Fatalf("reloaded receipt: %+v", r)
		}
		if r := status(replace.ID, replace.ExpiresAt); r.Status != "expired" {
			t.Fatalf("expiry boundary: %+v", r)
		}
		if r := status(replace.ID, replace.ExpiresAt.Add(peerReceiptTTL)); r.Error == "" {
			t.Fatal("expired receipt was retained past the retention period")
		}
		send = d.routePeerMessage(operatorMessageRequest{To: "Peer PC", Text: "withdraw me"}, now)
		if r := d.routePeerMessage(operatorMessageRequest{Op: "withdraw", ID: send.ID}, now); r.Status != "withdrawn" {
			t.Fatalf("withdraw: %+v", r)
		}
		saved := d.app.Preferences().String(pendingPeerMessagesKey)
		for _, body := range []string{"old wording", "new wording", "withdraw me"} {
			if strings.Contains(saved, body) {
				t.Errorf("body retained after leaving pending storage: %q", body)
			}
		}
	})
}

func TestPeerMessageMigratesLegacyOperatorQueues(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.ui(func() {
		d := h.d
		prefs := d.app.Preferences()
		prefs.SetString("operatorInbox."+roleQuill, `[{"id":"old-quill","text":"hello"}]`)
		prefs.SetString("operatorInbox."+roleKmem, `[{"id":"old-kmem","text":"check in"}]`)
		for range 2 {
			d.peerMessages = nil
			store := d.loadPeerMessages(now)
			if len(store.Pending) != 2 || store.Pending[0].To != quillOpName ||
				store.Pending[1].To != kmemAssistName || !store.Pending[1].ExpiresAt.Equal(now.Add(peerMessageTTL)) {
				t.Fatalf("migrated messages: %+v", store.Pending)
			}
		}
		if prefs.String("operatorInbox."+roleQuill) != "" || prefs.String("operatorInbox."+roleKmem) != "" {
			t.Fatal("old queues still contain message bodies")
		}
	})
}

func TestPeerMessageWaitsForNamedRecipient(t *testing.T) {
	h := newHarness(t)
	var target *deskSession
	var first operatorMessageReceipt
	h.ui(func() {
		d := h.d
		target = d.createSession(h.proj, "default", "default")
		d.infos[target.id] = transcript.Info{ID: target.id, Title: "Pc", Named: true}
		first = d.routeOperatorMessage(operatorMessageRequest{To: "PC", Text: "Approved.", from: "quill"})
		if first.Status != "queued" || target.running() {
			t.Fatalf("message to stopped session: %+v", first)
		}
		other := d.createSession(h.proj, "default", "default")
		d.org.Titles[other.id] = "pc"
		if r := d.routeOperatorMessage(operatorMessageRequest{To: "Pc", Text: "wrong recipient"}); !strings.HasPrefix(r.Error, "ambiguous session name") {
			t.Fatalf("ambiguous recipient: %+v", r)
		}
		delete(d.org.Titles, other.id)
		d.peerMessages = nil
		d.sendTo(target, "user start")
	})
	h.eventually("queued peer message delivered when the session runs", func() bool {
		return strings.Contains(lastUser(target), first.ID) && !target.conv.Busy
	})
	h.ui(func() {
		d := h.d
		raw := lastUser(target)
		if !notFromUser(raw) || !strings.Contains(raw, `"from":"quill"`) || !strings.Contains(raw, `"text":"Approved."`) {
			t.Fatalf("peer delivery lost its origin: %q", raw)
		}
		target.planWait = true
		second := d.routeOperatorMessage(operatorMessageRequest{To: "pc", Text: "still waiting"})
		if second.Status != "queued" || strings.Contains(lastUser(target), second.ID) {
			t.Fatalf("peer message interrupted a plan: %+v", second)
		}
		target.planWait = false
		route := d.routeOperatorMessage(operatorMessageRequest{Op: "withdraw", ID: second.ID})
		if route.Status != "withdrawn" {
			t.Fatalf("withdraw: %+v", route)
		}
		third := d.routeOperatorMessage(operatorMessageRequest{To: "pc", Text: "available now"})
		if third.Status != "submitted" || !strings.Contains(lastUser(target), third.ID) {
			t.Fatalf("idle delivery: %+v", third)
		}
	})
	h.eventually("recipient is idle again", func() bool { return !target.conv.Busy && !target.inTurn })
	h.ui(func() {
		d := h.d
		target.planWait = true
		waiting := d.routeOperatorMessage(operatorMessageRequest{To: "pc", Text: "deliver after the card closes"})
		target.planWait = false
		d.pollPeerMessages(time.Now())
		if waiting.Status != "queued" || !strings.Contains(lastUser(target), waiting.ID) {
			t.Fatalf("poll did not pick up delivery after the plan closed: %+v", waiting)
		}
	})
}

func TestPeerMessagePrefersSoleLiveMatchAndCanTargetID(t *testing.T) {
	h := newHarness(t)
	var live, older *deskSession
	h.ui(func() {
		d := h.d
		live = d.createSession(h.proj, "default", "default")
		older = d.createSession(h.proj, "default", "default")
		d.org.Titles[live.id], d.org.Titles[older.id] = "Pc", "PC"
		if err := d.ensureProcess(live); err != nil {
			t.Fatal(err)
		}
		if target, problem := d.peerRecipient("pc"); problem != "" || target != live {
			t.Fatalf("sole live match was not preferred: %q", problem)
		}
		if err := d.ensureProcess(older); err != nil {
			t.Fatal(err)
		}
		if target, problem := d.peerRecipient("pc"); target != nil ||
			!strings.Contains(problem, "id:"+live.id) || !strings.Contains(problem, "id:"+older.id) {
			t.Fatalf("live collision did not offer both IDs: %q", problem)
		}
		msg := d.routeOperatorMessage(operatorMessageRequest{To: "id:" + older.id, Text: "Only this tab"})
		if msg.Error != "" {
			t.Fatalf("explicit recipient: %+v", msg)
		}
	})
	h.eventually("only the selected live session receives the message", func() bool {
		return strings.Contains(lastUser(older), "Only this tab") && !strings.Contains(lastUser(live), "Only this tab")
	})
}

func TestPeerMessageCLIControls(t *testing.T) {
	for _, test := range []struct {
		args    []string
		op      string
		wantTTL time.Duration
		valid   bool
	}{
		{[]string{"--ttl", "2h", "pc", "message"}, "", 2 * time.Hour, true},
		{[]string{"--replace", "--ttl", "168h", "id", "replacement"}, "replace", 7 * 24 * time.Hour, true},
		{[]string{"--withdraw", "id"}, "withdraw", 0, true},
		{[]string{"--status", "id"}, "status", 0, true},
		{[]string{"--ttl", "0s", "pc", "message"}, "", 0, false},
		{[]string{"--replace", "id", ""}, "replace", 0, false},
	} {
		r, valid := operatorMessageArgs(test.args)
		if valid != test.valid || r.Op != test.op || r.TTL != test.wantTTL {
			t.Errorf("%v: %+v, valid %v", test.args, r, valid)
		}
	}
	r := operatorMessageReceipt{ID: "id", To: "pc", Status: "queued"}
	b, err := json.Marshal(r)
	if err != nil || strings.Contains(string(b), `"at"`) {
		t.Fatalf("pending receipt includes an unset timestamp: %s (%v)", b, err)
	}
}

func TestPendingPeerInboxPreservesWork(t *testing.T) {
	for _, work := range []string{"slow", "write", "Wait for command approval"} {
		t.Run(work, func(t *testing.T) {
			h := newHarness(t)
			d := h.d
			codex := work == "Wait for command approval"
			if codex {
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("QUILL_CODEX_BIN", exe)
				t.Setenv("QUILL_HANDOFF_FAKE", "1")
				d.cfg.CodexRoot = t.TempDir()
			}
			var s *deskSession
			h.ui(func() {
				s = d.addSessionWithBackend(h.proj, "default", "default", true, codex)
				d.open(s.id)
				d.org.Titles[s.id] = "Target"
				d.sendTo(s, work)
			})
			h.eventually("provider is working or waiting for permission", func() bool {
				if work != "slow" {
					return len(s.conv.PendingPermissions()) == 1
				}
				for _, it := range s.conv.Items {
					if it.Streaming && it.Text != "" {
						return true
					}
				}
				return false
			})
			var id string
			h.ui(func() {
				d.input.SetText("Unsent draft")
				var card *chat.Item
				if work != "slow" {
					card = s.conv.PendingPermissions()[0]
				}
				const body = "Approved. This concerns tomorrow."
				send := func() operatorMessageReceipt {
					return d.routeOperatorMessage(operatorMessageRequest{To: "target", from: "Peer", Text: body})
				}
				check := func() {
					t.Helper()
					label := "Peer message from Peer · received · waiting for " + s.engineName() + "\n" + body
					if len(d.chat.queueBox.Objects) != 0 || peerRowIndex(t, d.chat, label) < 0 {
						t.Fatal("expected one pending peer in chat and an empty queue footer")
					}
					if d.input.Text != "Unsent draft" || lastUser(s) != work || !s.conv.Busy || !s.inTurn || s.pendingRevision != nil || s.runAll {
						t.Fatal("arrival changed work or its draft")
					}
					if card != nil && (card.PermState != chat.PermPending || len(s.conv.PendingPermissions()) != 1 || s.conv.PendingPermissions()[0] != card) {
						t.Fatal("arrival changed the pending approval")
					}
				}
				r := send()
				if r.Status != "queued" {
					t.Fatal(r)
				}
				check()
				d.peerMessages = nil
				d.chat.show(s)
				d.pollPeerMessages(time.Now())
				check()
				if result := d.routeOperatorMessage(operatorMessageRequest{Op: "withdraw", ID: r.ID}); result.Status != "withdrawn" || len(d.chat.pendingPeers) != 0 {
					t.Fatal("withdrawal left context in the inbox")
				}
				r = send()
				id = r.ID
				check()
				if work == "slow" {
					d.interrupt(s)
				} else if !codex {
					d.answer(s, card, protocol.Deny)
				}
			})
			if !codex {
				h.eventually("peer delivered automatically at idle", func() bool {
					return strings.Contains(lastUser(s), id) && !s.conv.Busy
				})
				h.ui(func() {
					if peerRowIndex(t, d.chat, "Peer message from Peer · submitted\nApproved. This concerns tomorrow.") < 0 || len(d.chat.pendingPeers) != 0 || d.routeOperatorMessage(operatorMessageRequest{Op: "status", ID: id}).Status != "submitted" {
						t.Fatal("delivery left a stale pending inbox")
					}
				})
			}
		})
	}
}

func TestPeerMessageReconcilesInterruptedSubmission(t *testing.T) {
	h := newHarness(t)
	d := h.d
	var s *deskSession
	h.ui(func() {
		s = d.createSession(h.proj, "default", "default")
		d.org.Titles[s.id] = "Target"
		d.sendTo(s, "start")
	})
	h.eventually("recipient is idle", func() bool { return !s.inTurn && !s.conv.Busy && !s.proc.Busy() })
	h.ui(func() {
		s.planWait = true
		r := d.routeOperatorMessage(operatorMessageRequest{To: "target", from: "Peer", Text: "note"})
		if r.Status != "queued" {
			t.Fatal(r)
		}
		store := d.loadPeerMessages(time.Now())
		msg := store.Pending[0]
		store.Submitting = map[string]string{msg.ID: s.id}
		d.savePeerMessages()
		d.peerMessages = nil
		s.planWait = false
		d.pollPeerMessages(time.Now())
		status := func() string {
			return d.routeOperatorMessage(operatorMessageRequest{Op: "status", ID: r.ID}).Status
		}
		if lastUser(s) != "start" || status() != "unconfirmed" {
			t.Fatal("ambiguous submission was replayed")
		}
		s.conv.AddUser(msg.envelope())
		n := len(s.conv.Items)
		d.pollPeerMessages(time.Now())
		if len(s.conv.Items) != n || status() != "submitted" {
			t.Fatal("restored transcript did not reconcile the submission")
		}
	})
}

func peerRowIndex(t *testing.T, v *chatView, label string) int {
	t.Helper()
	pos := -1
	for i, row := range v.box.Objects {
		deepWalk(row, func(obj fyne.CanvasObject) {
			if l, ok := obj.(*widget.Label); ok && l.Text == label {
				if pos != -1 {
					t.Fatalf("duplicate timeline row %q", label)
				}
				pos = i
			}
		})
	}
	return pos
}

func TestPeerTimelineKeepsReceiptOrder(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		s := d.createSession(h.proj, "default", "default")
		d.org.Titles[s.id] = "Target"
		d.open(s.id)
		before := s.conv.AddUser("before")
		d.chat.update([]*chat.Item{before})
		now := time.Now()
		r := d.routePeerMessage(operatorMessageRequest{To: "target", from: "Peer", Text: "context", TTL: time.Hour}, now)
		if r.Status != "queued" {
			t.Fatal(r)
		}
		after := s.conv.AddUser("after")
		after.Time = now.Add(time.Minute)
		d.chat.update([]*chat.Item{after})
		check := func(status string) {
			t.Helper()
			i := peerRowIndex(t, d.chat, "Peer message from Peer · "+status+"\ncontext")
			if i != 1 || d.chat.box.Objects[0] != d.chat.views[before.Seq].holder || d.chat.box.Objects[2] != d.chat.views[after.Seq].holder {
				t.Fatalf("peer at %d, expected it between adjacent turns", i)
			}
		}
		check("received · waiting for " + s.engineName())
		d.peerMessages = nil
		d.chat.show(s)
		check("received · waiting for " + s.engineName())
		it := s.conv.AddUser(d.peerMessages.Pending[0].envelope())
		d.chat.update([]*chat.Item{it})
		check("submitted")
		if len(d.chat.pendingPeers) != 0 || len(d.chat.box.Objects) != 3 {
			t.Fatal("pending and submitted rows overlapped")
		}
		d.chat.show(s)
		check("submitted")
		exp := d.routePeerMessage(operatorMessageRequest{To: "target", from: "Peer", Text: "expires", TTL: time.Minute}, now)
		d.cleanPeerMessages(exp.ExpiresAt)
		if len(d.chat.pendingPeers) != 0 || len(d.chat.box.Objects) != 3 {
			t.Fatal("expiry left a peer row in the timeline")
		}
	})
}

func TestPeerTimelineAppearsInOperatorChat(t *testing.T) {
	for _, role := range []string{roleCodexQuill, roleCodexKmem} {
		t.Run(role, func(t *testing.T) {
			h := kmemBoxHarness(t)
			h.ui(func() {
				d := h.d
				s := d.addSessionWithBackend(h.proj, "default", "default", false, true)
				d.markHiddenAs(s, role)
				definition := codexQuillRole
				want := quillOpName
				if role == roleCodexKmem {
					definition = codexKmemRole
					want = kmemAssistName
				}
				a := d.codexAssistant(definition)
				a.s, a.state = s, asLive
				approval := s.conv.AddPlan()
				a.view.show(s)
				if role == roleCodexKmem {
					d.app.Preferences().SetBool("kmemAgentCodex", true)
					d.showKmem()
				} else {
					d.app.Preferences().SetBool("quillAgentCodex", true)
					d.openAssistBox(roleCodexQuill)
				}
				receipt := d.routeOperatorMessage(operatorMessageRequest{To: want, from: "Peer", Text: "Fixture context"})
				if receipt.Status != "queued" {
					t.Fatal(receipt)
				}
				label := "Peer message from Peer · received · waiting for Codex\nFixture context"
				if peerRowIndex(t, a.view, label) < 0 || len(a.view.queueBox.Objects) != 0 {
					t.Fatal("peer message should appear in the operator timeline only")
				}
				if approval.PermState != chat.PermPending || len(s.conv.PendingPermissions()) != 1 {
					t.Fatal("peer arrival changed the operator approval")
				}
				d.peerMessages = nil
				a.view.show(s)
				if peerRowIndex(t, a.view, label) < 0 || len(a.view.queueBox.Objects) != 0 {
					t.Fatal("the operator peer row was lost on redraw")
				}
			})
		})
	}
}
