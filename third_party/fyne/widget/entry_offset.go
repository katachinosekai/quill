package widget

func (e *Entry) SetCursorTextOffset(pos int) {
	e.CursorRow, e.CursorColumn = e.rowColFromTextPos(pos)
	e.syncSelectable()
	e.Refresh()
}
