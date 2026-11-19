package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

var (
	colBackground = color.NRGBA{0x26, 0x26, 0x24, 0xff}
	colSidebar    = color.NRGBA{0x1f, 0x1f, 0x1d, 0xff}
	colInput      = color.NRGBA{0x30, 0x30, 0x2e, 0xff}
	colBubble     = color.NRGBA{0x14, 0x14, 0x13, 0xff}
	colAgent      = color.NRGBA{0x2c, 0x2b, 0x29, 0xff}
	colCard       = color.NRGBA{0x2f, 0x2f, 0x2c, 0xff}
	colAccent     = color.NRGBA{0xd9, 0x77, 0x57, 0xff}
	colMuted      = color.NRGBA{0x9c, 0x9a, 0x92, 0xff}
	colText       = color.NRGBA{0xec, 0xea, 0xe3, 0xff}
	colAdd        = color.NRGBA{0x1f, 0x3a, 0x26, 0xff}
	colDel        = color.NRGBA{0x45, 0x22, 0x22, 0xff}
	colAddFG      = color.NRGBA{0x9f, 0xe0, 0xa8, 0xff}
	colDelFG      = color.NRGBA{0xf0, 0xa0, 0xa0, 0xff}
	colRunning    = color.NRGBA{0x5b, 0xa8, 0xf5, 0xff}
	colWaiting    = color.NRGBA{0xf2, 0xb3, 0x3d, 0xff}
	colIdle       = color.NRGBA{0x6c, 0xc0, 0x6c, 0xff}
	colError      = color.NRGBA{0xe5, 0x5b, 0x5b, 0xff}
	colTerminal   = color.NRGBA{0xa0, 0x7c, 0xe8, 0xff}
)

type deskTheme struct{}

func (deskTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameInputBackground:
		return colInput
	case theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground:
		return colCard
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return colAccent
	case theme.ColorNameForeground:
		return colText
	case theme.ColorNamePlaceHolder:
		return colMuted
	case theme.ColorNameSelection:
		return color.NRGBA{0xd9, 0x77, 0x57, 0x55}
	case theme.ColorNameInputBorder:
		return color.NRGBA{0x44, 0x44, 0x40, 0xff}
	case theme.ColorNameSeparator:
		return color.NRGBA{0x3a, 0x3a, 0x37, 0xff}
	case theme.ColorNameShadow:
		return color.Transparent
	case theme.ColorNameHover:
		return color.NRGBA{0xff, 0xff, 0xff, 0x12}
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (deskTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (deskTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

type compactTheme struct{ deskTheme }

func (t compactTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameCaptionText:
		return 10
	case theme.SizeNamePadding, theme.SizeNameInnerPadding:
		return 2
	case theme.SizeNameLineSpacing:
		return 2
	}
	return t.deskTheme.Size(n)
}

type smallTheme struct{ deskTheme }

func (t smallTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 12
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameLineSpacing:
		return 3
	case theme.SizeNamePadding:
		return 3
	}
	return t.deskTheme.Size(n)
}

type msgTheme struct{ deskTheme }

func (t msgTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 16
	case theme.SizeNameLineSpacing:
		return 7
	}
	return t.deskTheme.Size(n)
}

func (deskTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText, theme.SizeNameCaptionText:
		return 13
	case theme.SizeNameLineSpacing:
		return 5
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 8
	}
	return theme.DefaultTheme().Size(n)
}
