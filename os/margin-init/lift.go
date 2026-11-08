package main

import (
	"image"
	"time"

	xdraw "golang.org/x/image/draw"
)

// Holding a finger on something lifts it, as on the iPad: it grows a little and casts a
// shadow before what the long press does (a menu, a sheet, a to-do picked up to drag) takes
// over (2026-11-08, user: "long pressing ... should evoke a small animation, add these
// everywhere"). Drawn on the screen only: just the item's corner of it, a few frames.

const (
	liftGrow = 0.06 // how much bigger it gets
	durLift  = 150 * time.Millisecond
)

// liftAt lifts the button under the finger at (x, y). Caller holds drawMu.
func (c *console) liftAt(x, y int) {
	if !c.animOK() || c.page == nil || c.sc.gpuOn {
		return
	}
	id, r := c.page.hitRect(x, c.pageYAt(x, y))
	if id == "" {
		return
	}
	scr, src := c.onScreen(r, y)
	if scr.Empty() || scr.Dx() < 8 || scr.Dy() < 8 || scr.Dx()*scr.Dy() > c.s.W*c.viewH()/4 {
		return // nothing to show, or a whole area rather than an item
	}
	item := image.NewRGBA(image.Rect(0, 0, src.Dx(), src.Dy()))
	copyRect(item, c.page.img, src)
	pad := int(float64(max(scr.Dx(), scr.Dy()))*liftGrow/2) + 20
	area := scr.Inset(-pad).Intersect(image.Rect(0, c.barH, c.s.W, c.pageBottom()))
	if area.Empty() {
		return
	}
	under := c.s.grab(area) // what the screen shows there (edgeback.go)
	frame := image.NewRGBA(area)
	rad := min(16, scr.Dy()/4)
	show := func(t float64) {
		copy(frame.Pix, under.Pix)
		k := 1 + liftGrow*t
		w, h := int(float64(scr.Dx())*k+0.5), int(float64(scr.Dy())*k+0.5)
		cx, cy := (scr.Min.X+scr.Max.X)/2, (scr.Min.Y+scr.Max.Y)/2
		to := image.Rect(cx-w/2, cy-h/2-int(4*t), cx-w/2+w, cy-h/2-int(4*t)+h)
		shadow(frame, to, rad, 0.4*t)
		xdraw.ApproxBiLinear.Scale(frame, to, item, item.Rect, xdraw.Src, nil)
		c.s.blitRGBA(frame, area.Min.X, area.Min.Y)
	}
	c.frames(durLift, show)
	show(1) // it stays lifted until what the press opens is drawn
	c.s.Flush()
}
