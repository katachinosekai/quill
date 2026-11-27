package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestOperatorMessageCLIReceipts(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "q.sock")
	h.d.listenInstance(path)

	var out, errOut bytes.Buffer
	body := "line one\nApproved."
	if rc := operatorMessageCLI(path, []string{"kmem", body}, &out, &errOut); rc != 0 {
		t.Fatalf("CLI exit %d: %s", rc, &errOut)
	}
	var rep operatorMessageReceipt
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.To != kmemAssistName || rep.ID == "" || rep.Status != "queued" {
		t.Fatalf("queue receipt: %s (%v)", &out, err)
	}
	h.ui(func() {
		queue := h.d.loadPeerMessages(time.Now()).Pending
		if len(queue) != 1 || queue[0].ID != rep.ID || queue[0].Text != body {
			t.Fatalf("window inbox: %+v", queue)
		}
		if queue[0].To != kmemAssistName {
			t.Fatal("kmem message stored with the wrong recipient")
		}
	})
	out.Reset()
	if rc := operatorMessageCLI(path, []string{"claude-id", "test"}, &out, &errOut); rc == 0 || out.Len() != 0 {
		t.Fatalf("unknown addresses should fail: exit %d, receipt %q", rc, &out)
	}
}
