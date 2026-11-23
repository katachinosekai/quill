package folders

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"time"
)

const LegacyPhoneName = "Paired phone (before per-phone keys)"

type Phone struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	KeyHash  string `json:"keyHash"`
	Created  int64  `json:"created"`
	LastSeen int64  `json:"lastSeen"`
	Control  bool   `json:"control"`
	// Kind is set by the device's first hello: "pc" (Quill Desktop) or
	// "phone"; "" = not known yet.
	Kind string `json:"kind,omitempty"`
	// Lock overrides whether the device must unlock with the PC password
	// (default: every device except phones).
	Lock *bool `json:"lock,omitempty"`
	// Files: "Let this device open files" (phones only, off by default):
	// GET /phone/session/{id}/file serves this device.
	Files bool `json:"files,omitempty"`
}

// NeedsUnlock: every paired device, phones and devices that never said hello
// included, must unlock with the password unless its Lock switch is off
// (user 2026-11-21: "Every device, phones too").
func (p Phone) NeedsUnlock() bool {
	if p.Lock != nil {
		return *p.Lock
	}
	return true
}

func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func newPhoneID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewPhone(name string, now time.Time) (Phone, string) {
	key := NewKey()
	return Phone{ID: newPhoneID(), Name: name, KeyHash: HashKey(key), Created: now.UnixMilli(), Control: true}, key
}

func LegacyPhone(key string, now time.Time) Phone {
	return Phone{ID: newPhoneID(), Name: LegacyPhoneName, KeyHash: HashKey(key), Created: now.UnixMilli(), LastSeen: now.UnixMilli(), Control: true}
}

func ParsePhones(s string) []Phone {
	var out []Phone
	if json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

func PhonesJSON(p []Phone) string {
	if p == nil {
		p = []Phone{}
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func matchPhone(phones []Phone, key string) string {
	if key == "" {
		return ""
	}
	h := []byte(HashKey(key))
	id := ""
	for _, p := range phones {
		if subtle.ConstantTimeCompare(h, []byte(p.KeyHash)) == 1 {
			id = p.ID
		}
	}
	return id
}

func (s *Server) SetPhones(phones []Phone) {
	s.mu.Lock()
	s.phones = append([]Phone(nil), phones...)
	if s.idem != nil {
		s.idem.keep(phones)
	}
	revoked := s.revoked
	s.revoked = make(chan struct{})
	s.mu.Unlock()
	close(revoked)
}

func (s *Server) SetSeen(fn func(id string)) {
	s.mu.Lock()
	s.seen = fn
	s.mu.Unlock()
}

func (s *Server) phoneOK(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.phones {
		if p.ID == id {
			return true
		}
	}
	return false
}
