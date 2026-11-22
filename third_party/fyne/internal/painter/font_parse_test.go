package painter

import (
	"bytes"
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
