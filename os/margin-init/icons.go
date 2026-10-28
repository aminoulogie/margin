package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
)

// Soma's icons are lucide's (lucide-react 0.510, as Soma's package.json has it), drawn in
// Chromium from lucide's own paths at 96 pixels with its 2-wide stroke, white on black, ten to a
// row in icons/lucide.png, named in order in icons/lucide.txt. lucideIcon scales one down by
// area to the size asked, so the stroke stays lucide's 2/24 of the icon.

//go:embed icons/lucide.png
var lucidePNG []byte

//go:embed icons/lucide.txt
var lucideNames string

const lucideCell = 96

var lucide struct {
	once  sync.Once
	sheet *image.Gray
	index map[string]int
	mu    sync.Mutex
	sized map[[2]any]*image.Alpha
}

func lucideLoad() {
	lucide.index = map[string]int{}
	for i, n := range strings.Fields(lucideNames) {
		lucide.index[n] = i
	}
	lucide.sized = map[[2]any]*image.Alpha{}
	im, err := png.Decode(bytes.NewReader(lucidePNG))
	if err != nil {
		return
	}
	g := image.NewGray(im.Bounds())
	for y := g.Rect.Min.Y; y < g.Rect.Max.Y; y++ {
		for x := g.Rect.Min.X; x < g.Rect.Max.X; x++ {
			r, _, _, _ := im.At(x, y).RGBA()
			g.Pix[g.PixOffset(x, y)] = uint8(r >> 8)
		}
	}
	lucide.sheet = g
}

// lucideMask is the icon's coverage at size x size pixels, or nil for a name not in the sheet.
func lucideMask(name string, size int) *image.Alpha {
	lucide.once.Do(lucideLoad)
	i, ok := lucide.index[name]
	if !ok || lucide.sheet == nil || size < 4 {
		return nil
	}
	lucide.mu.Lock()
	defer lucide.mu.Unlock()
	key := [2]any{name, size}
	if m := lucide.sized[key]; m != nil {
		return m
	}
	ox, oy := (i%10)*lucideCell, (i/10)*lucideCell
	m := image.NewAlpha(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		sy0, sy1 := y*lucideCell/size, max((y+1)*lucideCell/size, y*lucideCell/size+1)
		for x := 0; x < size; x++ {
			sx0, sx1 := x*lucideCell/size, max((x+1)*lucideCell/size, x*lucideCell/size+1)
			sum, n := 0, 0
			for sy := sy0; sy < sy1; sy++ {
				row := lucide.sheet.Pix[lucide.sheet.PixOffset(ox+sx0, oy+sy):]
				for sx := 0; sx < sx1-sx0; sx++ {
					sum += int(row[sx])
					n++
				}
			}
			m.Pix[y*m.Stride+x] = uint8(sum / max(n, 1))
		}
	}
	lucide.sized[key] = m
	return m
}

// lucideIcon draws the icon centred on (cx, cy), size pixels square, in col.
func lucideIcon(img *image.RGBA, name string, cx, cy, size int, col color.RGBA) {
	m := lucideMask(name, size)
	if m == nil {
		return
	}
	x0, y0 := cx-size/2, cy-size/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			a := m.Pix[y*m.Stride+x]
			if a == 0 {
				continue
			}
			p := image.Pt(x0+x, y0+y)
			if !p.In(img.Rect) {
				continue
			}
			o := img.PixOffset(p.X, p.Y)
			t := float64(a) / 255 * float64(col.A) / 255
			img.Pix[o] = uint8(float64(img.Pix[o])*(1-t) + float64(col.R)*t + 0.5)
			img.Pix[o+1] = uint8(float64(img.Pix[o+1])*(1-t) + float64(col.G)*t + 0.5)
			img.Pix[o+2] = uint8(float64(img.Pix[o+2])*(1-t) + float64(col.B)*t + 0.5)
		}
	}
}
