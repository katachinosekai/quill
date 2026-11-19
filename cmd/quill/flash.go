package main

import (
	"context"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2/canvas"
)

const glowPeriod = 2400 * time.Millisecond

func needsGlow(st status) bool {
	return st == stWaiting || st == stError
}

func (d *desk) setGlow(r *canvas.Rectangle, base color.Color) {
	if d.glowBoxes == nil {
		d.glowBoxes = map[*canvas.Rectangle]color.Color{}
	}
	if base != nil {
		if _, ok := d.glowBoxes[r]; !ok {
			d.glowBoxes[r] = base
			paintGlow(r, base, 0.5)
		}
		d.glowBoxes[r] = base
		return
	}
	if _, ok := d.glowBoxes[r]; ok {
		delete(d.glowBoxes, r)
		r.FillColor, r.StrokeColor = color.Transparent, color.Transparent
		r.Refresh()
	}
}

func fade(c color.Color, a float64) color.Color {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	n.A = uint8(float64(n.A) * a)
	return n
}

func paintGlow(r *canvas.Rectangle, base color.Color, a float64) {
	r.FillColor = fade(base, 0.04+0.14*a)
	r.StrokeColor = fade(base, 0.15+0.6*a)
	r.Refresh()
}

func (d *desk) glowLoop(ctx context.Context) {
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			phase := float64(now.Sub(start)%glowPeriod) / float64(glowPeriod)
			a := 0.5 - 0.5*math.Cos(2*math.Pi*phase)
			d.do(func() {
				for r, base := range d.glowBoxes {
					paintGlow(r, base, a)
				}
			})
		}
	}
}
