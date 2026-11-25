package chat

import (
	"encoding/json"
	"errors"
)

type liveSnapshot struct {
	Conversation *Conversation
	Seq          int
	Streams      map[string]int
	Thinks       map[string]string
}

func (c *Conversation) MarshalLive() ([]byte, error) {
	streams := make(map[string]int, len(c.streamKeys))
	for key, it := range c.streamKeys {
		if it != nil {
			streams[key] = it.Seq
		}
	}
	return json.Marshal(liveSnapshot{Conversation: c, Seq: c.seq, Streams: streams, Thinks: c.thinkKeys})
}

func RestoreLive(data []byte) (*Conversation, error) {
	snapshot := liveSnapshot{Conversation: New()}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	c := snapshot.Conversation
	if c == nil {
		return nil, errors.New("missing conversation in live snapshot")
	}
	c.seq = snapshot.Seq
	items := make(map[int]*Item, len(c.Items))
	for _, it := range c.Items {
		if it == nil {
			return nil, errors.New("missing item in live snapshot")
		}
		items[it.Seq] = it
		c.seq = max(c.seq, it.Seq)
		if it.Kind == Tool && it.ToolID != "" {
			c.byTool[it.ToolID] = it
		}
		if it.Kind == Permission && it.RequestID != "" {
			c.byRequest[it.RequestID] = it
		}
	}
	for key, seq := range snapshot.Streams {
		if it := items[seq]; it != nil {
			c.streamKeys[key] = it
		}
	}
	if snapshot.Thinks != nil {
		c.thinkKeys = snapshot.Thinks
	}
	return c, nil
}
