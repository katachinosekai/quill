package painter

import (
	"bytes"
	"fyne.io/fyne/v2"
	"testing"

	"github.com/go-text/typesetting/font"

	"fyne.io/fyne/v2/theme"
)

// Quill patch: a font resource is parsed once, however many theme scopes
// (ThemeOverrides) ask for it.
func TestLoadMeasureFontParsesOnce(t *testing.T) {
	ClearFontCache()
	a := loadMeasureFont(theme.DefaultTextFont())
	b := loadMeasureFont(theme.DefaultTextFont())
	if a == nil || a != b {
		t.Fatal("the same font resource gives the same parsed face")
	}
	if c := loadMeasureFont(theme.DefaultTextBoldFont()); c == a {
		t.Fatal("a different font is parsed on its own")
	}
}

func BenchmarkLoadMeasureFontEmoji(b *testing.B) {
	emoji := theme.DefaultEmojiFont()
	if emoji == nil {
		b.Skip("built without the emoji font")
	}
	ClearFontCache()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		loadMeasureFont(emoji)
	}
}

// The cost per scope before the patch: a full parse every time.
func BenchmarkParseTTFEmojiUncached(b *testing.B) {
	emoji := theme.DefaultEmojiFont()
	if emoji == nil {
		b.Skip("built without the emoji font")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := font.ParseTTF(bytes.NewReader(emoji.Content())); err != nil {
			b.Fatal(err)
		}
	}
}

// Quill patch: the locale font is looked up on the first glyph the bundled
// fonts lack, not when the font map is built.
func TestLangFontLookedUpLazily(t *testing.T) {
	ClearFontCache()
	faces := lookupFaces(theme.DefaultTextFont(), theme.DefaultTextFont(), nil, "sans-serif", fyne.TextStyle{})
	if !faces.lazyLang || len(faces.faces) != 1 {
		t.Fatalf("lazyLang %v, %d faces", faces.lazyLang, len(faces.faces))
	}
	if f := faces.ResolveFace('a'); f != faces.faces[0] || !faces.lazyLang {
		t.Fatal("a bundled glyph must not trigger the lookup")
	}
	faces.ResolveFace(0x6F22) // a missing glyph
	if faces.lazyLang {
		t.Fatal("a missing glyph triggers the lookup")
	}
}
