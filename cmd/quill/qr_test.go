package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"rsc.io/qr"
)

func TestPairQRIsPixelExact(t *testing.T) {
	link := "quillpair://pair?fp=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef&h=192.168.1.20%2Chost.local&k=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&n=host&p=43210"
	code, err := qr.Encode(link, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	for _, scale := range []float32{1, 1.25, 1.5, 2} {
		px, side := qrLayout(code.Size, scale)
		img := qrImage(code, px).(*image.Gray)
		total := code.Size + 2*qrQuiet
		b := img.Bounds()
		if b.Dx() != b.Dy() || b.Dx() != total*px || side*scale != float32(total*px) {
			t.Fatalf("scale %v: square, whole modules, shown 1:1: %v px=%d side=%v", scale, b, px, side)
		}
		if px < 3 {
			t.Fatalf("scale %v: modules at least 3 px: %d", scale, px)
		}
		at := func(mx, my int) uint8 { return img.GrayAt((mx+qrQuiet)*px+px/2, (my+qrQuiet)*px+px/2).Y }
		for y := 0; y < code.Size; y++ {
			for x := 0; x < code.Size; x++ {
				want := uint8(0xff)
				if code.Black(x, y) {
					want = 0
				}
				x0, y0 := (x+qrQuiet)*px, (y+qrQuiet)*px
				for yy := y0; yy < y0+px; yy++ {
					for xx := x0; xx < x0+px; xx++ {
						if img.GrayAt(xx, yy).Y != want {
							t.Fatalf("module %d,%d is not a uniform block", x, y)
						}
					}
				}
			}
		}
		for i := 0; i < qrQuiet*px; i++ {
			if img.GrayAt(i, b.Dy()/2).Y != 0xff || img.GrayAt(b.Dx()-1-i, b.Dy()/2).Y != 0xff {
				t.Fatal("white quiet zone on both sides")
			}
		}
		if at(0, 0) != 0 || at(code.Size-1, 0) != 0 || at(0, code.Size-1) != 0 {
			t.Fatal("finder patterns in three corners")
		}
	}
	if dir := os.Getenv("DESK_SHOTS"); dir != "" {
		px, _ := qrLayout(code.Size, 1)
		f, _ := os.Create(filepath.Join("..", "..", ".shots", "pair-qr.png"))
		png.Encode(f, qrImage(code, px))
		f.Close()
	}
}
