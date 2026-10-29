package main

import (
	"image"
	"image/color"
	"math"
)

// ringSet draws Soma's rings exactly as its RingSet.tsx does on the iPhone: n concentric
// rings in a px square at (x, y), each w = px/(2n+1.2+0.28(n-1)) wide with 0.14w between,
// its track in its own colour at 24%, the share reached filled with a top-left to
// bottom-right gradient from → to, a round leading cap lifted by a shadow, and Soma's arrow
// (→ » ↑ +) at the top of each. Past 100% the ring goes round again. Antialiased by
// coverage, one pixel at a time.
type ringSpec struct {
	f        float64 // share of the goal, 0 upwards
	from, to color.RGBA
}

// big: the arrows and the cap's shadow, which Soma only draws on rings 48-50 CSS pixels and up.
func ringSet(img *image.RGBA, x, y, px int, rings []ringSpec, mute, big bool) {
	n := len(rings)
	if n == 0 || px < 8 {
		return
	}
	fpx := float64(px)
	w := fpx / (float64(n)*2 + 1.2 + float64(n-1)*0.28)
	gap := w * 0.14
	c0 := fpx / 2
	half := w / 2
	type ringGeo struct {
		r, f, rest float64
		laps       int
		ex, ey     float64 // the leading cap's centre
	}
	geo := make([]ringGeo, n)
	for i, rs := range rings {
		r := c0 - half - float64(i)*(w+gap)
		f := max(0, rs.f)
		laps := int(math.Floor(f))
		rest := f - float64(laps)
		end := rest
		if rest == 0 && f > 0 {
			end = 1
		}
		a := end*2*math.Pi - math.Pi/2
		geo[i] = ringGeo{r: r, f: f, rest: rest, laps: laps, ex: c0 + r*math.Cos(a), ey: c0 + r*math.Sin(a)}
	}
	cover := func(d float64) float64 { return min(max(d+0.5, 0), 1) } // distance inside an edge → coverage
	b := image.Rect(x, y, x+px, y+px).Intersect(img.Rect)
	for py := b.Min.Y; py < b.Max.Y; py++ {
		for qx := b.Min.X; qx < b.Max.X; qx++ {
			lx, ly := float64(qx-x)+0.5, float64(py-y)+0.5
			dx, dy := lx-c0, ly-c0
			d := math.Hypot(dx, dy)
			// Clockwise from the top, 0 to 1 (worked out only for a pixel on a ring).
			t := -1.0
			for i, g := range geo {
				if g.r <= half {
					continue
				}
				// Nothing of this ring reaches a pixel this far off its circle: the band, the
				// cap and the start all lie within half a width of it (the cheap test first).
				if math.Abs(d-g.r) >= half+0.5 {
					continue
				}
				if t < 0 {
					t = math.Atan2(dx, -dy) / (2 * math.Pi)
					if t < 0 {
						t++
					}
				}
				rs := rings[i]
				band := cover(half - math.Abs(d-g.r))
				// The leading cap: round, past the end of the arc.
				capd := math.Hypot(lx-g.ex, ly-g.ey)
				capc, startc := 0.0, 0.0
				if g.f > 0 {
					capc = cover(half - capd)
					startc = cover(half - math.Hypot(lx-c0, ly-(c0-g.r))) // the round start, at the top
				}
				if band <= 0 && capc <= 0 && startc <= 0 {
					continue
				}
				o := img.PixOffset(qx, py)
				bg := color.RGBA{img.Pix[o], img.Pix[o+1], img.Pix[o+2], 255}
				grad := blend(rs.from, rs.to, min(max((lx+ly)/(2*fpx), 0), 1))
				var col color.RGBA
				var a float64
				filled := g.f > 0 && (g.laps >= 1 || t <= g.rest || (g.rest == 0 && g.f > 0))
				switch {
				case capc > 0 && g.f > 0.04:
					// Under the cap, a shadow (stdDeviation ~0.18w, 55% black), then the cap.
					sh := 0.0
					if big {
						sh = math.Exp(-math.Pow(max(capd-half, 0)/(w*0.18+0.6), 2)) * 0.55
					}
					base := blend(bg, rgb(0x000000), sh)
					if filled && band > 0 {
						base = blend(base, grad, band)
					}
					col, a = blend(base, rs.to, capc), 1
				case filled || startc > 0:
					col, a = grad, max(band, startc)
				default:
					col, a = blend(bg, rs.from, 0.24), band
				}
				if mute && rs.f < 1 {
					a *= 0.3
				}
				c := blend(bg, col, a)
				img.Pix[o], img.Pix[o+1], img.Pix[o+2] = c.R, c.G, c.B
			}
		}
	}
	if !big {
		return
	}
	// The arrows, over everything: at the top of each ring, in black at 78%.
	for i, g := range geo {
		if g.r <= half {
			continue
		}
		ax, ay := x+int(c0), y+int(c0-g.r)
		s := int(w * 0.7 / 2)
		sw := max(2, int(w*0.7*0.16))
		under := color.RGBA{img.Pix[img.PixOffset(min(ax, img.Rect.Max.X-1), min(max(ay, 0), img.Rect.Max.Y-1))],
			img.Pix[img.PixOffset(min(ax, img.Rect.Max.X-1), min(max(ay, 0), img.Rect.Max.Y-1))+1],
			img.Pix[img.PixOffset(min(ax, img.Rect.Max.X-1), min(max(ay, 0), img.Rect.Max.Y-1))+2], 255}
		ink := blend(under, rgb(0x000000), 0.78)
		seg := func(x0, y0, x1, y1 float64) {
			line(img, ax+int(x0*float64(s)), ay+int(y0*float64(s)), ax+int(x1*float64(s)), ay+int(y1*float64(s)), sw, ink)
		}
		switch i {
		case 0: // →
			seg(-1, 0, 1, 0)
			seg(0.3, -0.7, 1, 0)
			seg(0.3, 0.7, 1, 0)
		case 1: // »
			seg(-0.55, -0.62, 0.05, 0)
			seg(-0.55, 0.62, 0.05, 0)
			seg(0.3, -0.62, 0.9, 0)
			seg(0.3, 0.62, 0.9, 0)
		case 2: // ↑
			seg(0, 1, 0, -1)
			seg(-0.7, -0.3, 0, -1)
			seg(0.7, -0.3, 0, -1)
		default: // +
			seg(-0.8, 0, 0.8, 0)
			seg(0, -0.8, 0, 0.8)
		}
	}
}

// somaRingSpecs is the day's four rings as specs.
func somaRingSpecs(date string) []ringSpec {
	var out []ringSpec
	for _, r := range somaRingsFor(date) {
		f := 0.0
		if r.Goal > 0 {
			f = r.Value / r.Goal
		}
		out = append(out, ringSpec{f, rgb(r.From), rgb(r.To)})
	}
	return out
}
