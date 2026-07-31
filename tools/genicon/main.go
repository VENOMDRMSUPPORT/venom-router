// Command genicon regenerates internal/tray/assets/venom.ico: an
// upward-pointing green triangle on a dark rounded square (the owner's
// approved tray icon, design 2026-07-31). Deterministic pure-Go rendering
// with 4x4 supersampled coverage antialiasing; entries are PNG-compressed.
//
// Usage (from the repo root):
//
//	go run ./tools/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

var sizes = []int{16, 20, 24, 32, 48, 64}

var (
	bg  = color.NRGBA{R: 0x1F, G: 0x21, B: 0x24, A: 0xFF} // dark square
	tri = color.NRGBA{R: 0x2E, G: 0xE6, B: 0xA8, A: 0xFF} // green triangle
)

func main() {
	out := "internal/tray/assets/venom.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := run(out); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "genicon:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	var pngs [][]byte
	for _, n := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(n)); err != nil {
			return err
		}
		pngs = append(pngs, buf.Bytes())
	}
	return os.WriteFile(out, encodeICO(pngs), 0o644)
}

// render draws one n×n frame. Geometry (fractions of n): rounded-square
// corner radius 0.18; triangle apex (0.50, 0.26), base (0.26, 0.72) to
// (0.74, 0.72) — matching the approved screenshot's proportions.
func render(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	fn := float64(n)
	ax, ay := 0.50*fn, 0.26*fn
	bx, by := 0.26*fn, 0.72*fn
	cx, cy := 0.74*fn, 0.72*fn
	r := 0.18 * fn
	const ss = 4 // supersamples per axis
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var sqHits, triHits int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					if inRoundedSquare(px, py, fn, r) {
						sqHits++
						if inTriangle(px, py, ax, ay, bx, by, cx, cy) {
							triHits++
						}
					}
				}
			}
			img.SetNRGBA(x, y, blend(sqHits, triHits, ss*ss))
		}
	}
	return img
}

// blend composes transparent -> background -> triangle by sample coverage.
func blend(sqHits, triHits, total int) color.NRGBA {
	if sqHits == 0 {
		return color.NRGBA{}
	}
	fSq := float64(sqHits) / float64(total)
	fTri := float64(triHits) / float64(total)
	mix := func(b, t uint8) uint8 {
		bgPart := (fSq - fTri) * float64(b)
		triPart := fTri * float64(t)
		return uint8((bgPart + triPart) / fSq)
	}
	return color.NRGBA{
		R: mix(bg.R, tri.R),
		G: mix(bg.G, tri.G),
		B: mix(bg.B, tri.B),
		A: uint8(fSq * 255),
	}
}

func inRoundedSquare(px, py, n, r float64) bool {
	if px < 0 || py < 0 || px > n || py > n {
		return false
	}
	// Inside the cross formed by the two inset rectangles?
	if (px >= r && px <= n-r) || (py >= r && py <= n-r) {
		return true
	}
	// Corner regions: within radius of the nearest corner circle center.
	ccx, ccy := r, r
	if px > n-r {
		ccx = n - r
	}
	if py > n-r {
		ccy = n - r
	}
	dx, dy := px-ccx, py-ccy
	return dx*dx+dy*dy <= r*r
}

func inTriangle(px, py, ax, ay, bx, by, cx, cy float64) bool {
	side := func(x1, y1, x2, y2 float64) float64 {
		return (x2-x1)*(py-y1) - (y2-y1)*(px-x1)
	}
	d1 := side(ax, ay, bx, by)
	d2 := side(bx, by, cx, cy)
	d3 := side(cx, cy, ax, ay)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

// encodeICO wraps PNG blobs in an ICO container: ICONDIR (6 bytes) +
// one 16-byte ICONDIRENTRY per image + concatenated payloads.
func encodeICO(pngs [][]byte) []byte {
	var buf bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&buf, le, uint16(0)) // reserved
	_ = binary.Write(&buf, le, uint16(1)) // type: icon
	_ = binary.Write(&buf, le, uint16(len(pngs)))
	offset := 6 + 16*len(pngs)
	for i, p := range pngs {
		n := sizes[i]
		buf.WriteByte(byte(n)) // width (256 would be 0; we stay below)
		buf.WriteByte(byte(n)) // height
		buf.WriteByte(0)       // palette colors
		buf.WriteByte(0)       // reserved
		_ = binary.Write(&buf, le, uint16(1))  // color planes
		_ = binary.Write(&buf, le, uint16(32)) // bits per pixel
		_ = binary.Write(&buf, le, uint32(len(p)))
		_ = binary.Write(&buf, le, uint32(offset))
		offset += len(p)
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes()
}
