package main

import (
	"image"
	"time"

	"margin-init/ui"
)

// Swiping in from the left edge goes back, as on an iPad: from a book's page in the Store to
// the Store, from a word to the word book, from Settings to Books. As the finger pulls, a
// round back button slides out from the edge under it, and turns blue once a lift would go
// back. Only that small circle is drawn while the finger moves.

const (
	edgeZone   = 40  // pixels from the left edge where a swipe can start
	edgeCommit = 150 // pulled this far (or flicked), the lift goes back
	edgeR      = 42  // the round button's radius
)

type edgeState struct {
	can   bool // the finger came down at the edge of a page with a way back
	on    bool // ...and is pulling
	ready bool // a lift now goes back
	at    image.Point
	under *image.RGBA // the screen under the button, put back when it moves
	t0    time.Time
}

// backID is the page's way back, as a button id, or "" if it has none.
func (c *console) backID() string {
	if c.page == nil {
		return ""
	}
	ids := []string{"s:back", "w:back", "wk:back", "so:notes"}
	if c.mode == modeSettings {
		ids = append(ids, "home")
	}
	for _, want := range ids {
		for _, b := range c.page.buttons {
			if b.id == want {
				return want
			}
		}
	}
	return ""
}

// edgeDown: a finger came down at (x, y); it may be the start of a swipe back. Caller holds
// drawMu.
func (c *console) edgeDown(x, y int) {
	c.edge = edgeState{can: x < edgeZone && y > c.barH && c.backID() != "", t0: time.Now()}
}

// edgeStart: the finger moved by (dx, dy) past the slop: a swipe back if it started at the
// edge and goes right. Caller holds drawMu.
func (c *console) edgeStart(dx, dy int) bool {
	if !c.edge.can || dx <= 0 || abs(dx) < 2*abs(dy) {
		c.edge.can = false
		return false
	}
	c.edge.on = true
	return true
}

// edgeMove draws the back button where the finger has pulled it. Caller holds drawMu.
func (c *console) edgeMove(x, y int) {
	e := &c.edge
	e.restore(c.s)
	pull := x
	ready := pull >= edgeCommit
	if ready && !e.ready {
		vibrate(10)
	}
	e.ready = ready
	k := min(float64(pull)/edgeCommit, 1)
	cx := -edgeR + int(float64(edgeR+60)*spring(k)) + max(pull-edgeCommit, 0)/6
	cy := min(max(y, c.barH+edgeR+8), c.s.H-edgeR-8)
	r := image.Rect(cx-edgeR-4, cy-edgeR-4, cx+edgeR+4, cy+edgeR+4).Intersect(image.Rect(0, c.barH, c.s.W, c.s.H))
	if r.Empty() {
		return
	}
	e.at, e.under = r.Min, c.s.grab(r)
	img := image.NewRGBA(r)
	copy(img.Pix, e.under.Pix)
	ring, fill, ink := apSeparator, apCard2, apLabel
	if ready {
		ring, fill, ink = apBlue, apBlue, rgb(0xffffff)
	}
	ui.Circle(img, cx, cy, edgeR+2, ring)
	ui.Circle(img, cx, cy, edgeR, fill)
	iconBack(img, cx-10, cy, ink)
	c.s.blitRGBA(img, r.Min.X, r.Min.Y)
	c.s.Flush()
}

// edgeUp: the finger lifted from a swipe back; true if it went back. Caller holds drawMu.
func (c *console) edgeUp(x int) bool {
	e := &c.edge
	e.restore(c.s)
	fast := float64(x)/time.Since(e.t0).Seconds() > 900 && x > edgeCommit/3
	back := e.ready || fast
	c.edge = edgeState{}
	if id := c.backID(); back && id != "" {
		c.tapID(id)
		return true
	}
	c.s.Flush()
	return false
}

func (e *edgeState) restore(s *Screen) {
	if e.under != nil {
		s.blitRGBA(e.under, e.at.X, e.at.Y)
		e.under = nil
	}
}

// grab copies the logical rectangle r of the screen (as drawn, not yet flushed or not).
func (s *Screen) grab(r image.Rectangle) *image.RGBA {
	r = r.Intersect(image.Rect(0, 0, s.W, s.H))
	img := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	ch := func(v uint32, f bitfield) uint8 { return uint8((v >> f.offset) & (1<<f.length - 1) << (8 - f.length)) }
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			fx, fy := s.rot.toFB(x, y, s.fbW, s.fbH)
			o := fy*s.stride + fx*s.bpp/8
			var v uint32
			if s.bpp == 32 {
				v = uint32(s.buf[o]) | uint32(s.buf[o+1])<<8 | uint32(s.buf[o+2])<<16 | uint32(s.buf[o+3])<<24
			} else {
				v = uint32(s.buf[o]) | uint32(s.buf[o+1])<<8
			}
			i := img.PixOffset(x-r.Min.X, y-r.Min.Y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = ch(v, s.red), ch(v, s.green), ch(v, s.blue), 255
		}
	}
	return img
}
