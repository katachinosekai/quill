package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

type fakeSpeller map[string][]string

func (f fakeSpeller) check(w string) (bool, []string) {
	s, bad := f[w]
	return !bad, s
}

func TestAutocorrectTyping(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		var sent string
		ignored := map[string]bool{}
		e := newSendEntry(func(s string) { sent = s })
		e.ac = &autocorrector{
			sp:      fakeSpeller{"teh": {"the"}, "Teh": {"The"}, "recieve": {"receive"}, "zorp": {"zap"}, "qwxz": nil},
			enabled: func() bool { return true },
			ignored: func(w string) bool { return ignored[w] },
			ignore:  func(w string) { ignored[w] = true },
		}
		e.known = func() func(string) bool { return func(w string) bool { return w == "kmemm" } }
		var hint string
		e.onFix = func(m string) { hint = m }
		w := test.NewWindow(e)
		defer w.Close()
		typed := func(s string) string {
			e.SetText("")
			test.Type(e, s)
			return e.Text
		}
		cases := map[string]string{
			"teh cat ":    "the cat ",
			"Teh end.":    "Teh end.",
			"`teh ` ":     "`teh ` ",
			"/tmp/teh ":   "/tmp/teh ",
			"zorp ":       "zorp ",
			"qwxz ":       "qwxz ",
			"x teh":       "x teh",
			"```\nteh \n": "```\nteh \n",
		}
		for in, want := range cases {
			if got := typed(in); got != want {
				t.Errorf("typing %q gave %q, want %q", in, got, want)
			}
		}
		if typed("teh ") != "the " || hint == "" {
			t.Fatal("corrects and shows the hint")
		}
		e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyBackspace})
		if e.Text != "teh " || !ignored["teh"] {
			t.Fatalf("Backspace right after a correction restores the word and learns it: %q", e.Text)
		}
		if typed("teh ") != "teh " {
			t.Fatal("a learned word is left alone")
		}
		e.SetText("")
		test.Type(e, "please recieve")
		e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		if sent != "please receive" {
			t.Fatalf("the last word is corrected on send: %q", sent)
		}
		e.ac.enabled = func() bool { return false }
		if typed("recieve ") != "recieve " {
			t.Fatal("the setting turns it off")
		}
	})
}

func TestAutocorrectWithHunspell(t *testing.T) {
	if !hunspellAvailable() {
		t.Skip("hunspell not installed")
	}
	h, err := startHunspell(acLang)
	if err != nil {
		t.Skip("no en_US dictionary: ", err)
	}
	a := &autocorrector{sp: h}
	for in, want := range map[string]string{"teh": "the", "recieve": "receive", "becuase": "because"} {
		if got, ok := a.fix(in, in, "", nil); !ok || got != want {
			t.Errorf("%s -> %q %v", in, got, ok)
		}
	}
	for _, w := range []string{"kmem", "pzlock", "the", "Fyne"} {
		if got, ok := a.fix(w, w, "", nil); ok {
			t.Errorf("%s should stay, got %q", w, got)
		}
	}
}
