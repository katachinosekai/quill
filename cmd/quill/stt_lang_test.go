package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

func codes(ls []language) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.code)
	}
	return out
}

func TestLanguageOptionsAutoFirst(t *testing.T) {
	if languages[0].code != autoLang || languages[1].code != "en" {
		t.Fatalf("order starts %v", codes(languages[:3]))
	}
	seen := map[string]bool{}
	for _, l := range languages {
		if seen[l.code] {
			t.Fatalf("duplicate %s", l.code)
		}
		seen[l.code] = true
		if l.code != autoLang && l.native == "" {
			t.Fatalf("%s has no native name", l.code)
		}
	}
	if langChip(autoLang) != "Auto" || langChip("de") != "DE" {
		t.Fatal("chip text")
	}
}

func TestLanguageLabelsAndNativeSearch(t *testing.T) {
	label := func(code string) string {
		for _, l := range languages {
			if l.code == code {
				return languageLabel(l)
			}
		}
		return ""
	}
	if !strings.HasPrefix(label("de"), "German — Deutsch") || !strings.HasPrefix(label("ja"), "Japanese — 日本語") {
		t.Fatalf("labels %q %q", label("de"), label("ja"))
	}
	if strings.Contains(label("en"), "—") {
		t.Fatalf("English needs no native name: %q", label("en"))
	}
	for q, want := range map[string]string{"日本語": "ja", "deutsch": "de", "DEUTSCH": "de", "german": "de", "español": "es", "中文": "zh", "ja": "ja"} {
		got := codes(filterLanguages(q))
		found := false
		for _, c := range got {
			found = found || c == want
		}
		if !found {
			t.Fatalf("search %q -> %v, want %s", q, got, want)
		}
	}
	if len(filterLanguages("")) != len(languages) || len(filterLanguages("zzzzqq")) != 0 {
		t.Fatal("empty query keeps all, nonsense keeps none")
	}
}

func TestVoiceDefaultsAndModelChoice(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		sp := h.d.stt
		if sp.lang() != autoLang || sp.modelSize() != "base" {
			t.Fatalf("defaults: %q %q", sp.lang(), sp.modelSize())
		}
		// Auto never uses the English-only model; sizes pick the multilingual file.
		for size, file := range map[string]string{"base": "ggml-base-q5_1.bin", "small": "ggml-small-q5_1.bin", "medium": "ggml-medium-q5_0.bin"} {
			if modelFile(autoLang, size) != file || modelFile("de", size) != file {
				t.Fatalf("%s: %s", size, modelFile(autoLang, size))
			}
			if !strings.Contains(modelFile("en", size), ".en-") {
				t.Fatalf("English %s: %s", size, modelFile("en", size))
			}
		}
		if modelFile("de", "bogus") != "ggml-base-q5_1.bin" {
			t.Fatal("unknown size falls back to base")
		}
		sp.setModelSize("small")
		if sp.modelSize() != "small" || h.d.app.Preferences().String(prefSTTModel) != "small" {
			t.Fatal("model size not persisted")
		}
		sp.setLang("de")
		sp.setLang(autoLang)
		if sp.lang() != autoLang {
			t.Fatal("back to auto")
		}
		sp.setLangPref("fr")
		if h.d.app.Preferences().Bool(prefSTTOff) {
			t.Fatal("picking a language in Settings must not flip the switch")
		}
		sp.setLang("fr")
		if sp.lang() != "fr" {
			t.Fatal("an explicit pick is kept")
		}
	})
}

func TestVoiceSettingsSection(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if h.d.stt.btn == nil {
			t.Fatal("the mic button stays")
		}
		var texts []string
		deepWalk(h.d.voiceSettings(), func(o fyne.CanvasObject) {
			switch w := o.(type) {
			case *widget.Check:
				texts = append(texts, w.Text)
			case *widget.Button:
				texts = append(texts, w.Text)
			case *widget.Select:
				texts = append(texts, w.Selected)
			}
		})
		all := strings.Join(texts, "|")
		if !strings.Contains(all, "Voice input") || !strings.Contains(all, "Auto-detect") || !strings.Contains(all, "Base (57 MB)") {
			t.Fatalf("voice settings: %q", all)
		}
	})
}
