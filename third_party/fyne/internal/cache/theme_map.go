//go:build go1.24

package cache

import (
	"reflect"
	"sync"
	"unsafe"
	"weak"

	"fyne.io/fyne/v2"
)

// overrideMap maps canvas objects to their theme override scope without
// keeping the objects alive (Quill patch, 2026-11-23).
//
// It used to be a plain map keyed by the objects: every text, rectangle and
// widget inside every ThemeOverride stayed in it for good (only widgets were
// removed, when their renderer was destroyed), and through the scope's theme
// the whole override with its content. Quill wraps each chat message in a
// ThemeOverride: 243,000 entries and ~140 MB after a few hours, ~0.9 MB per
// streaming redraw. Now an object is keyed by its address and held by a weak
// pointer, so it is collected once nothing else uses it; the clean task
// drops the entries of collected objects (sweep).
type overrideMap struct {
	m sync.Map // uintptr (object address) or fyne.CanvasObject (non-pointer objects) -> *overrideEntry
}

type overrideEntry struct {
	obj   weak.Pointer[byte] // pointer objects
	value fyne.CanvasObject  // non-pointer objects, held strongly as before
	scope *overrideScope
}

// objectKey returns the map key for o and its address (nil for objects that
// are not a non-empty pointer: those are keyed, and kept, by value).
func objectKey(o fyne.CanvasObject) (any, unsafe.Pointer) {
	t := reflect.TypeOf(o)
	if t == nil || t.Kind() != reflect.Pointer || t.Elem().Size() == 0 {
		return o, nil
	}
	p := reflect.ValueOf(o).UnsafePointer()
	if p == nil {
		return o, nil
	}
	return uintptr(p), p
}

func (e *overrideEntry) live(p unsafe.Pointer) bool {
	return p == nil || e.obj.Value() == (*byte)(p)
}

func (m *overrideMap) Load(o fyne.CanvasObject) (*overrideScope, bool) {
	k, p := objectKey(o)
	v, ok := m.m.Load(k)
	if !ok {
		return nil, false
	}
	e := v.(*overrideEntry)
	if !e.live(p) { // a collected object that lived at the same address
		return nil, false
	}
	return e.scope, true
}

func (m *overrideMap) Store(o fyne.CanvasObject, s *overrideScope) {
	k, p := objectKey(o)
	e := &overrideEntry{scope: s}
	if p != nil {
		e.obj = weak.Make((*byte)(p))
	} else {
		e.value = o
	}
	m.m.Store(k, e)
}

func (m *overrideMap) Delete(o fyne.CanvasObject) {
	k, _ := objectKey(o)
	m.m.Delete(k)
}

// Len returns the number of entries (collected objects not swept yet included).
func (m *overrideMap) Len() (n int) {
	m.m.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// sweep deletes the entries of collected objects and returns the cache IDs
// of the scopes still in use.
func (m *overrideMap) sweep() map[string]bool {
	live := map[string]bool{}
	m.m.Range(func(k, v any) bool {
		e := v.(*overrideEntry)
		if e.value == nil && e.obj.Value() == nil {
			m.m.CompareAndDelete(k, v)
			return true
		}
		live[e.scope.cacheID] = true
		return true
	})
	return live
}

var (
	scopeHooksMu sync.Mutex
	scopeHooks   []func(live func(cacheID string) bool)
)

// OnScopesSwept registers a hook the clean task runs after dropping the
// entries of collected objects: live reports whether a scope cache ID is
// still used, so caches keyed by scope can drop the others (Quill patch).
func OnScopesSwept(f func(live func(cacheID string) bool)) {
	scopeHooksMu.Lock()
	scopeHooks = append(scopeHooks, f)
	scopeHooksMu.Unlock()
}

// sweepOverrides drops the override entries of collected objects.
func sweepOverrides() {
	live := overrides.sweep()
	scopeHooksMu.Lock()
	hooks := scopeHooks
	scopeHooksMu.Unlock()
	for _, f := range hooks {
		f(func(id string) bool { return live[id] })
	}
}
