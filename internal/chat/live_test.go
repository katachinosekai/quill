package chat

import (
	"testing"

	"github.com/katachinosekai/quill/internal/protocol"
)

func TestLiveSnapshotContinuesReplyAndApproval(t *testing.T) {
	c := New()
	c.AddUser("Earlier message")
	stream := &protocol.Event{
		Type:         "stream_event",
		APIMessageID: "reply",
		Event: &protocol.StreamEvent{
			Type:         "content_block_start",
			ContentBlock: &protocol.Block{Type: "text"},
		},
	}
	reply := c.Apply(stream)[0]
	stream.Event = &protocol.StreamEvent{
		Type:  "content_block_delta",
		Delta: &protocol.StreamDelta{Type: "text_delta", Text: "First part"},
	}
	c.Apply(stream)
	c.Apply(&protocol.Event{
		Type:      "control_request",
		RequestID: "approval",
		Request:   &protocol.ControlRequest{Subtype: "can_use_tool", ToolName: "Bash"},
	})
	lastSeq := c.Items[len(c.Items)-1].Seq

	data, err := c.MarshalLive()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreLive(data)
	if err != nil {
		t.Fatal(err)
	}

	stream.Event.Delta.Text = " and second part"
	updated := restored.Apply(stream)
	if len(updated) != 1 || updated[0] != restored.Items[1] ||
		updated[0].Seq != reply.Seq || updated[0].Text != "First part and second part" ||
		len(restored.Items) != len(c.Items) {
		t.Fatalf("the streaming reply did not continue in place: %+v", restored.Items)
	}
	approval := restored.Permission("approval")
	restored.Apply(&protocol.Event{Type: "control_cancel_request", RequestID: "approval"})
	if approval == nil || approval != restored.Items[2] || approval.PermState != PermCancelled {
		t.Fatalf("the restored approval was not cancelled in place: %+v", approval)
	}
	if next := restored.AddUser("Next message"); next.Seq != lastSeq+1 {
		t.Fatalf("next item sequence = %d, want %d", next.Seq, lastSeq+1)
	}
}
