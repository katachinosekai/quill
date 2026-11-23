package test

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/cache"
	intdriver "fyne.io/fyne/v2/internal/driver"
)

// ThemeOverrideEntries returns the number of objects the theme cache holds
// an override scope for (Quill patch, for leak tests).
func ThemeOverrideEntries() int {
	return cache.OverrideEntries()
}

// CleanCaches runs the cache clean of a desktop paint loop as if `after` had
// passed since the last frame, with the visible objects of c drawn in that
// frame: renderers (and their theme scopes) of everything else expire, as
// they would in a running app (Quill patch, for leak tests).
func CleanCaches(c fyne.Canvas, after time.Duration) {
	cache.CleanAt(time.Now().Add(after), func() {
		touch := func(o fyne.CanvasObject, _, _ fyne.Position, _ fyne.Size) bool {
			if w, ok := o.(fyne.Widget); ok {
				cache.Renderer(w)
			}
			return false
		}
		if c.Content() != nil {
			intdriver.WalkVisibleObjectTree(c.Content(), touch, nil)
		}
		for _, o := range c.Overlays().List() {
			intdriver.WalkVisibleObjectTree(o, touch, nil)
		}
	})
}
