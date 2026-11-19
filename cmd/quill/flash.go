package main

import (
	"context"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2/canvas"
)

const glowPeriod = 2400 * time.Millisecond

type glow struct {
	base     color.Color
	strength float64
	pulse    bool
}

func glowStrength(st status) float64 {
	switch st {
	case stWaiting, stError:
		return 1
	case stIdle:
		return 0.3
	}
	return 0
}

func (d *desk) setGlow(r *canvas.Rectangle, base color.Color, strength float64) {
	if d.glowBoxes == nil {
		d.glowBoxes = map[*canvas.Rectangle]glow{}
	}
	if base != nil && strength > 0 {
		g := glow{base, strength, strength >= 1}
		if old, ok := d.glowBoxes[r]; !ok || old != g {
			paintGlow(r, g, 0.5)
		}
		d.glowBoxes[r] = g
		d.countPulses()
		return
	}
	if _, ok := d.glowBoxes[r]; ok {
		delete(d.glowBoxes, r)
		r.FillColor, r.StrokeColor = color.Transparent, color.Transparent
		r.Refresh()
		d.countPulses()
	}
}

func fade(c color.Color, a float64) color.Color {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	n.A = uint8(float64(n.A) * a)
	return n
}

func paintGlow(r *canvas.Rectangle, g glow, a float64) {
	r.FillColor = fade(g.base, (0.04+0.14*a)*g.strength)
	r.StrokeColor = fade(g.base, (0.15+0.6*a)*g.strength)
	r.Refresh()
}

func (d *desk) countPulses() {
	n := int32(0)
	for _, g := range d.glowBoxes {
		if g.pulse {
			n++
		}
	}
	d.pulsing.Store(n)
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
			if d.pulsing.Load() == 0 {
				continue
			}
			phase := float64(now.Sub(start)%glowPeriod) / float64(glowPeriod)
			a := 0.5 - 0.5*math.Cos(2*math.Pi*phase)
			d.do(func() {
				for r, g := range d.glowBoxes {
					if g.pulse {
						paintGlow(r, g, a)
					}
				}
			})
		}
	}
}
