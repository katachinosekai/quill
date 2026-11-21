package account

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetChromeDefault(t *testing.T) {
	cases := []struct {
		name, in, want string
		on             bool
	}{
		{"flip", "{\n  \"a\": 1,\n  \"claudeInChromeDefaultEnabled\": true,\n  \"b\": {\n    \"claudeInChromeDefaultEnabled\": true\n  }\n}\n", "{\n  \"a\": 1,\n  \"claudeInChromeDefaultEnabled\": false,\n  \"b\": {\n    \"claudeInChromeDefaultEnabled\": true\n  }\n}\n", false},
		{"insert", "{\n  \"a\": 1\n}\n", "{\n  \"claudeInChromeDefaultEnabled\": true,\n  \"a\": 1\n}\n", true},
		{"insert empty", "{}\n", "{\n  \"claudeInChromeDefaultEnabled\": true\n}\n", true},
		{"same", "{\n  \"claudeInChromeDefaultEnabled\": true\n}\n", "{\n  \"claudeInChromeDefaultEnabled\": true\n}\n", true},
	}
	for _, c := range cases {
		p := filepath.Join(t.TempDir(), ".claude.json")
		if err := os.WriteFile(p, []byte(c.in), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := setChromeDefaultAt(p, c.on); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, _ := os.ReadFile(p)
		if string(got) != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
		if chromeDefaultAt(p) != c.on {
			t.Errorf("%s: read back %v", c.name, !c.on)
		}
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v", c.name, st.Mode().Perm())
		}
	}
	p := filepath.Join(t.TempDir(), ".claude.json")
	os.WriteFile(p, []byte("{broken"), 0o600)
	if setChromeDefaultAt(p, true) == nil {
		t.Error("broken JSON: want error")
	}
	if got, _ := os.ReadFile(p); string(got) != "{broken" {
		t.Error("broken JSON was changed")
	}
}
