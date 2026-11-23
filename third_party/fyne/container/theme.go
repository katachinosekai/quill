//go:build go1.24

package container

import (
	"reflect"
	"weak"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/cache"
	intTheme "fyne.io/fyne/v2/internal/theme"
	"fyne.io/fyne/v2/widget"
)

// ThemeOverride is a container where the child widgets are themed by the specified theme.
// Containers will be traversed and all child widgets will reflect the theme in this container.
// This should be used sparingly to avoid a jarring user experience.
//
// Since: 2.5
type ThemeOverride struct {
	widget.BaseWidget

	Content fyne.CanvasObject
	Theme   fyne.Theme

	holder *fyne.Container

	mobile  bool
	applied fyne.Theme // Theme at the last apply (Quill patch)
}

// NewThemeOverride provides a container where the child widgets are themed by the specified theme.
// Containers will be traversed and all child widgets will reflect the theme in this container.
// This should be used sparingly to avoid a jarring user experience.
//
// If the content `obj` of this theme override is a container and items are later added to the container or any
// sub-containers ensure that you call `Refresh()` on this `ThemeOverride` to ensure the new items match the theme.
//
// Since: 2.5
func NewThemeOverride(obj fyne.CanvasObject, th fyne.Theme) *ThemeOverride {
	t := &ThemeOverride{Content: obj, Theme: th, holder: NewStack(obj)}
	t.ExtendBaseWidget(t)

	t.apply()
	obj.Refresh() // required as the widgets passed in could have been initially rendered with default theme
	return t
}

func (t *ThemeOverride) CreateRenderer() fyne.WidgetRenderer {
	t.apply()

	return &overrideRenderer{parent: t, objs: []fyne.CanvasObject{t.holder}}
}

func (t *ThemeOverride) Refresh() {
	if t.holder.Objects[0] != t.Content {
		t.holder.Objects[0] = t.Content
		t.holder.Refresh()
	}

	t.apply()
	t.Content.Refresh()
	t.BaseWidget.Refresh()
}

// apply themes the content. The theme caches are cleared only when the
// override's Theme was replaced since the last apply (Quill patch,
// 2026-11-23); before, every apply of every override cleared them, see
// overrideWidget in internal/cache/theme.go.
func (t *ThemeOverride) apply() {
	if t.applied != nil && !sameTheme(t.applied, t.Theme) {
		cache.ResetThemeCaches()
	}
	t.applied = t.Theme
	cache.OverrideTheme(t.Content, addFeatures(t.Theme, t))
}

// sameTheme reports whether a and b are the same theme value; values that
// cannot be compared count as different.
func sameTheme(a, b fyne.Theme) (same bool) {
	if reflect.TypeOf(a) != reflect.TypeOf(b) || !reflect.TypeOf(a).Comparable() {
		return false
	}
	defer func() {
		if recover() != nil { // comparable type holding an uncomparable value
			same = false
		}
	}()
	return a == b
}

// SetDeviceIsMobile allows a ThemeOverride container to shape the contained widgets as a mobile device.
// This will impact containers such as AppTabs and DocTabs, and more in the future, to display a layout
// that would automatically be used for a mobile device runtime.
//
// Since: 2.6
func (t *ThemeOverride) SetDeviceIsMobile(on bool) {
	t.mobile = on
	t.Refresh()
}

// featureTheme is the theme stored in the theme cache for the content's
// objects. It holds the override weakly (Quill patch, 2026-11-23): a strong
// pointer kept the override, and with it all its content, alive from the
// cache, so the cache's weak keys could never be collected.
type featureTheme struct {
	fyne.Theme

	over weak.Pointer[ThemeOverride]
}

func addFeatures(th fyne.Theme, o *ThemeOverride) fyne.Theme {
	return &featureTheme{Theme: th, over: weak.Make(o)}
}

func (f *featureTheme) Feature(n intTheme.FeatureName) any {
	if n == intTheme.FeatureNameDeviceIsMobile {
		if o := f.over.Value(); o != nil {
			return o.mobile
		}
		return false
	}

	return nil
}

type overrideRenderer struct {
	parent *ThemeOverride

	objs []fyne.CanvasObject
}

func (r *overrideRenderer) Destroy() {
}

func (r *overrideRenderer) Layout(s fyne.Size) {
	intTheme.PushRenderingTheme(r.parent.Theme)
	defer intTheme.PopRenderingTheme()

	r.parent.holder.Resize(s)
}

func (r *overrideRenderer) MinSize() fyne.Size {
	intTheme.PushRenderingTheme(r.parent.Theme)
	defer intTheme.PopRenderingTheme()

	return r.parent.Content.MinSize()
}

func (r *overrideRenderer) Objects() []fyne.CanvasObject {
	return r.objs
}

func (r *overrideRenderer) Refresh() {
}
