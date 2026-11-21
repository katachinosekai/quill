package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The kmem banner shows what only the user decides (kmem rules 21 and 26):
// items waiting in the recycle bin and audit problems. It is hidden while
// there is nothing to decide. Its buttons open the kmem panel, whose Recycle
// bin tab reviews, restores and empties (emptying runs in a terminal, where
// kmem asks the user to type the confirmation).

var kmemBannerEvery = 30 * time.Minute

var briefBin = regexp.MustCompile(`(?m)^bin: (\d+) item`)

// kmemNeeds reads `kmem brief`: items in the bin and the audit problems.
func kmemNeeds(brief string) (bin int, audit string) {
	if m := briefBin.FindStringSubmatch(brief); m != nil {
		bin, _ = strconv.Atoi(m[1])
	}
	for _, l := range strings.Split(brief, "\n") {
		if strings.HasPrefix(l, "AUDIT PROBLEMS: ") {
			audit = strings.TrimSpace(strings.TrimPrefix(l, "AUDIT PROBLEMS: "))
		}
	}
	return bin, audit
}

func kmemBannerText(bin int, audit string) string {
	var parts []string
	if audit != "" {
		parts = append(parts, "kmem audit found problems: "+oneLine(audit, 140))
	}
	if bin > 0 {
		parts = append(parts, fmt.Sprintf("%d item(s) in the kmem recycle bin wait for you", bin))
	}
	return strings.Join(parts, "  ·  ")
}

func (d *desk) buildKmemBanner() fyne.CanvasObject {
	d.kmemBannerText = widget.NewLabel("")
	d.kmemBannerText.Wrapping = fyne.TextWrapWord
	d.kmemBinBtn = widget.NewButtonWithIcon("Review bin", theme.DeleteIcon(), func() { d.showKmemPanelTab(kmemTabBin) })
	d.kmemAuditBtn = widget.NewButtonWithIcon("Details", theme.InfoIcon(), func() { d.showKmemPanelTab(kmemTabOverview) })
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 8
	bgr.StrokeColor = colMuted
	bgr.StrokeWidth = 1
	d.kmemBtns = container.NewHBox(d.kmemAuditBtn, d.kmemBinBtn)
	d.kmemBanner = container.NewStack(bgr, container.NewPadded(container.NewBorder(nil, nil, nil,
		container.NewCenter(d.kmemBtns), d.kmemBannerText)))
	d.kmemBanner.Hide()
	return d.kmemBanner
}

// setKmemBanner shows or hides the banner for these needs (UI thread).
func (d *desk) setKmemBanner(bin int, audit string) {
	if d.kmemBanner == nil {
		return
	}
	text := kmemBannerText(bin, audit)
	was := d.kmemBanner.Visible()
	if text == "" {
		d.kmemBanner.Hide()
	} else {
		d.kmemBannerText.SetText(text)
		showIf(d.kmemBinBtn, bin > 0)
		showIf(d.kmemAuditBtn, audit != "")
		d.kmemBanner.Show()
		relayoutTree(d.kmemBanner)
	}
	if was != d.kmemBanner.Visible() && d.headerBox != nil {
		d.headerBox.Refresh()
		if d.mainPane != nil {
			relayout(d.mainPane)
		}
	}
}

func showIf(o fyne.CanvasObject, on bool) {
	if on {
		o.Show()
	} else {
		o.Hide()
	}
}

// refreshKmemBanner asks kmem in the background and updates the banner.
func (d *desk) refreshKmemBanner() {
	if !d.kmemAvailable() {
		return
	}
	go func() {
		var brief struct {
			Brief string `json:"brief"`
		}
		if !d.kmemJSON(&brief, "brief") {
			return
		}
		bin, audit := kmemNeeds(brief.Brief)
		d.do(func() { d.setKmemBanner(bin, audit) })
	}()
}

// watchKmemBanner: once at start, then every kmemBannerEvery until ctx ends.
func (d *desk) watchKmemBanner(ctx context.Context) {
	d.do(d.refreshKmemBanner)
	t := time.NewTicker(kmemBannerEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.do(d.refreshKmemBanner)
		}
	}
}

const (
	kmemTabOverview = 0
	kmemTabBin      = 4
)

// showKmemPanelTab opens the kmem panel on one of its tabs.
func (d *desk) showKmemPanelTab(tab int) {
	d.showKmemPanel()
	if p := d.kmemPanelNow; p != nil && p.tabs != nil && tab < len(p.tabs.Items) {
		p.tabs.SelectIndex(tab)
	}
}
