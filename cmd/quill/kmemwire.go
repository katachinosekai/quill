package main

// Wiring of the kmem window's parts (docs/design/kmem-window.md §11): the
// assistant lifecycle (kmemassist.go) draws into the window's chat side
// (kmemwindow.go) and starts/stops with the window; quitting Quill Desktop
// saves and stops it first (desk.go confirmQuit).
func init() {
	kmemWinSetChat = (*kmemWindow).setChat
	kmemWinSetChatReason = (*kmemWindow).setChatReason
	kmemWinSetChatReasonWith = (*kmemWindow).setChatReasonWith
	kmemWinSetPrefill = (*kmemWindow).setPrefill
	kmemWindowOpenHook = func(d *desk, w *kmemWindow) { d.kmemAssistOpen(w) }
	kmemWindowCloseHook = func(d *desk, w *kmemWindow) { d.kmemAssistClose(w) }
}

// wireKmemQuit: called from newDesk's setup.
func (d *desk) wireKmemQuit() {
	d.kmemQuitFlow = d.kmemAssistQuit
}
