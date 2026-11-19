package main

import (
	"context"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2/canvas"
)

const flashPeriod = 1200 * time.Millisecond

func needsFlash(st status) bool {
	return st == stWaiting || st == stError
}

func (d *desk) setFlash(c *canvas.Circle, base color.Color, on bool) {
	if d.flashDots == nil {
		d.flashDots = map[*canvas.Circle]color.Color{}
	}
	if on {
		d.flashDots[c] = base
		return
	}
	delete(d.flashDots, c)
}

func fade(c color.Color, a float64) color.Color {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	n.A = uint8(float64(n.A) * a)
	return n
}

func (d *desk) flashLoop(ctx context.Context) {
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			phase := float64(now.Sub(start)%flashPeriod) / float64(flashPeriod)
			a := 0.2 + 0.8*(0.5+0.5*math.Cos(2*math.Pi*phase))
			d.do(func() {
				for c, base := range d.flashDots {
					c.FillColor = fade(base, a)
					c.Refresh()
				}
			})
		}
	}
}
