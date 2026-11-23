//go:build !ci && !test

package painter

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// With real system fonts: CJK and emoji glyphs still resolve through the
// lazy fallback (skipped where no installed font has them).
func TestSystemFallbackFindsCJKAndEmoji(t *testing.T) {
	ClearFontCache()
	faces := lookupFaces(theme.DefaultTextFont(), theme.DefaultTextFont(), nil, "sans-serif", fyne.TextStyle{})
	for _, r := range []rune{0x6F22, 0x3042, 0x1F600} {
		f := faces.ResolveFace(r)
		if _, ok := f.NominalGlyph(r); !ok {
			t.Skipf("no installed font has U+%X", r)
		}
	}
}
