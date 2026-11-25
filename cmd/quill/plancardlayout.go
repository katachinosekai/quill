package main

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (v *chatView) planApprovalFrame(parts []fyne.CanvasObject, pending bool) fyne.CanvasObject {
	border := colAccent
	if !pending {
		border = color.NRGBA{0x44, 0x44, 0x40, 0xff}
	}
	return v.planCardFrame(parts, border)
}

func (v *chatView) planCardFrame(parts []fyne.CanvasObject, border color.Color) fyne.CanvasObject {
	background := canvas.NewRectangle(colCard)
	background.CornerRadius = 10
	background.StrokeColor = border
	background.StrokeWidth = 1.5

	body := stackBlocks(parts...)
	frame := container.NewStack(background, container.NewPadded(body))
	fit := container.New(planCardLayout{body: body, center: v.d.center || v.cardOnly}, frame)
	return stackBlocks(padTop(4), fit, padTop(4))
}

type planCardLayout struct {
	body   *fyne.Container
	center bool
}

func (l planCardLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (l planCardLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	frame := objects[0]
	width := min(size.Width, max(frame.MinSize().Width, planContentWidth(l.body)+2*theme.Padding()))
	left := float32(0)
	if l.center {
		left = (size.Width - width) / 2
	}
	frame.Move(fyne.NewPos(left, 0))
	frame.Resize(fyne.NewSize(width, size.Height))
}

// Wrapped text reports a minimum that can be much narrower than the text.
// Use its natural width, plus the space the form controls need before wrapping.
func planContentWidth(obj fyne.CanvasObject) float32 {
	if !obj.Visible() {
		return 0
	}
	width := obj.MinSize().Width
	switch o := obj.(type) {
	case *widget.Label:
		th := o.Theme()
		size := th.Size(firstNonEmptySize(o.SizeName))
		for _, line := range strings.Split(o.Text, "\n") {
			width = max(width, fyne.MeasureText(line, size, o.TextStyle).Width+2*th.Size(theme.SizeNameInnerPadding))
		}
	case *widget.RichText:
		th := o.Theme()
		width = max(width, planRichWidth(o.Segments, th)+2*th.Size(theme.SizeNameInnerPadding))
	case *container.ThemeOverride:
		width = max(width, planContentWidth(o.Content))
	case *fyne.Container:
		if row, ok := o.Layout.(*flowRow); ok {
			var rowWidth float32
			for _, child := range o.Objects {
				if child.Visible() {
					if rowWidth > 0 {
						rowWidth += row.gap
					}
					rowWidth += child.MinSize().Width
				}
			}
			return max(width, rowWidth)
		}
		for _, child := range o.Objects {
			width = max(width, planContentWidth(child))
		}
	}
	return width
}

func firstNonEmptySize(size fyne.ThemeSizeName) fyne.ThemeSizeName {
	if size == "" {
		return theme.SizeNameText
	}
	return size
}

func planRichWidth(segments []widget.RichTextSegment, th fyne.Theme) float32 {
	var widest, line float32
	for _, seg := range segments {
		switch s := seg.(type) {
		case *widget.ParagraphSegment:
			widest = max(widest, line, planRichWidth(s.Texts, th))
			line = 0
		case *widget.ListSegment:
			widest = max(widest, line, planRichWidth(s.Items, th)+2*th.Size(theme.SizeNameInnerPadding))
			line = 0
		default:
			size, style := th.Size(theme.SizeNameText), fyne.TextStyle{}
			if text, ok := seg.(*widget.TextSegment); ok {
				size, style = th.Size(firstNonEmptySize(text.Style.SizeName)), text.Style.TextStyle
			}
			for i, text := range strings.Split(seg.Textual(), "\n") {
				if i > 0 {
					widest, line = max(widest, line), 0
				}
				line += fyne.MeasureText(text, size, style).Width
			}
			if !seg.Inline() {
				widest, line = max(widest, line), 0
			}
		}
	}
	return max(widest, line)
}
