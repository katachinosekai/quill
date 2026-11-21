package driver

import (
	"fyne.io/fyne/v2"
	intdriver "fyne.io/fyne/v2/internal/driver"
)

// WalkVisibleObjectTree exposes the internal walker so apps can find the drawn objects of a widget tree.
func WalkVisibleObjectTree(obj fyne.CanvasObject, beforeChildren func(fyne.CanvasObject, fyne.Position, fyne.Position, fyne.Size) bool, afterChildren func(fyne.CanvasObject, fyne.Position, fyne.CanvasObject)) bool {
	return intdriver.WalkVisibleObjectTree(obj, beforeChildren, afterChildren)
}
