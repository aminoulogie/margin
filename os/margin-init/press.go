package main

import (
	"image"
	"image/color"
	"time"
)

// A finger on a button shows it pressed, before the finger lifts, as iOS does: on this
// processor a screen can take a moment to draw, and a tap that shows nothing feels missed.
// It's drawn on the screen only (the page stays as it is) and goes when the finger moves
// into a scroll, holds, or lifts. A button with a shape of its own (a capsule, a card, a
// cover) dims, its surroundings untouched; a row or a word on the page's background gets a
// soft highlight behind it.

type pressState struct {
	on  bool
	scr image.Rectangle // where it's shown on the screen
	src image.Rectangle // the same pixels in the page
}

// pressDelay: a touch that starts a scroll moves within this, and shows nothing pressed.
const pressDelay = 70 * time.Millisecond

// hitRect is hit, with the button's rectangle.
func (p *page) hitRect(x, y int) (string, image.Rectangle) {
	pt := image.Point{x, y}
	for _, btns := range p.rowBtns {
		for _, b := range btns {
			if pt.In(b.r) {
				return b.id, b.r
			}
		}
	}
	for _, b := range p.buttons {
		if pt.In(b.r) {
			return b.id, b.r
		}
	}
	return "", image.Rectangle{}
}

// onScreen maps page rect r, found under a finger at screen row y, to where it shows on the
// screen (clipped to the part of the view it's in: the fixed header, the fixed footer, or
// the scrolling page between), and the page pixels shown there.
func (c *console) onScreen(r image.Rectangle, y int) (scr, src image.Rectangle) {
	vy := y - c.barH
	vh, hh, fh, ih := c.viewH(), c.page.header, c.page.footer, c.page.img.Rect.Dy()
	tall := ih > vh
	var area image.Rectangle // in view coordinates
	shift := 0               // view y = page y + shift
	switch {
	case tall && fh > 0 && vy >= vh-fh:
		area, shift = image.Rect(0, vh-fh, c.s.W, vh), vh-ih
	case tall && vy < hh:
		area = image.Rect(0, 0, c.s.W, hh)
	case tall:
		area, shift = image.Rect(0, hh, c.s.W, vh-fh), -c.scrollY()
	default:
		area = image.Rect(0, 0, c.s.W, min(vh, ih))
	}
	area.Max.Y = min(area.Max.Y, c.pageBottom()-c.barH)
	v := r.Add(image.Pt(0, shift)).Intersect(area)
	if v.Empty() {
		return image.Rectangle{}, image.Rectangle{}
	}
	return v.Add(image.Pt(0, c.barH)), v.Sub(image.Pt(0, shift))
}

// pressShow shows the button under a finger at (x, y) pressed. Caller holds drawMu.
func (c *console) pressShow(x, y int) {
	if c.press.on || c.page == nil || c.sc.gpuOn || c.sc.cpuOn || !c.screenOn {
		return
	}
	id, r := c.page.hitRect(x, c.pageY(y))
	if id == "" {
		return
	}
	scr, src := c.onScreen(r, y)
	if scr.Empty() || scr.Dx()*scr.Dy() > c.s.W*c.viewH()/3 {
		return // nothing to show, or a whole area rather than a button
	}
	img := image.NewRGBA(image.Rect(0, 0, src.Dx(), src.Dy()))
	copyRect(img, c.page.img, src)
	pressedLook(img)
	c.s.blitRGBA(img, scr.Min.X, scr.Min.Y)
	c.s.Flush()
	c.press = pressState{on: true, scr: scr, src: src}
}

// pressClear puts the pressed button back as the page has it; flush: show it now (else the
// next flush does). Caller holds drawMu.
func (c *console) pressClear(flush bool) {
	if !c.press.on {
		return
	}
	c.press.on = false
	if c.page == nil {
		return
	}
	c.s.blitRGBA(c.page.img.SubImage(c.press.src).(*image.RGBA), c.press.scr.Min.X, c.press.scr.Min.Y)
	if flush {
		c.s.Flush()
	}
}

// copyRect copies rect r of src to dst's origin.
func copyRect(dst, src *image.RGBA, r image.Rectangle) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(dst.Pix[dst.PixOffset(0, y-r.Min.Y):], src.Pix[src.PixOffset(r.Min.X, y):src.PixOffset(r.Max.X, y)])
	}
}

// pressedLook turns a button's pixels into its pressed look, in place.
func pressedLook(img *image.RGBA) {
	b := img.Rect
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return
	}
	at := func(x, y int) color.RGBA {
		i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
		return color.RGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], 255}
	}
	corners := []color.RGBA{at(0, 0), at(w-1, 0), at(0, h-1), at(w-1, h-1)}
	edges := []color.RGBA{at(w/2, 0), at(w/2, h-1), at(0, h/2), at(w-1, h/2)}
	uniform := true
	for _, e := range append(corners[1:], edges...) {
		uniform = uniform && e == corners[0]
	}
	if uniform {
		// A row, or words on the background: a soft rounded highlight behind it all.
		to, k := rgb(0xffffff), 0.13
		if !apDark {
			to, k = rgb(0x000000), 0.08
		}
		rad := min(20, h/2, w/2)
		ki := int(k * 256)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if outsideRound(x, y, w, h, rad) {
					continue
				}
				i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
				img.Pix[i] = uint8((int(img.Pix[i])*(256-ki) + int(to.R)*ki) >> 8)
				img.Pix[i+1] = uint8((int(img.Pix[i+1])*(256-ki) + int(to.G)*ki) >> 8)
				img.Pix[i+2] = uint8((int(img.Pix[i+2])*(256-ki) + int(to.B)*ki) >> 8)
			}
		}
		return
	}
	// A shape of its own: it dims, what's around it (the corners' colours) stays.
	m := 184 // 72%
	if !apDark {
		m = 210 // 82%
	}
	for y := 0; y < h; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):][:4*w]
	px:
		for i := 0; i < len(row); i += 4 {
			for _, k := range corners {
				if row[i] == k.R && row[i+1] == k.G && row[i+2] == k.B {
					continue px
				}
			}
			row[i] = uint8(int(row[i]) * m >> 8)
			row[i+1] = uint8(int(row[i+1]) * m >> 8)
			row[i+2] = uint8(int(row[i+2]) * m >> 8)
		}
	}
}

// outsideRound: (x, y) is outside a w x h rectangle's corners rounded by rad.
func outsideRound(x, y, w, h, rad int) bool {
	cx, cy := -1, -1
	switch {
	case x < rad:
		cx = rad
	case x >= w-rad:
		cx = w - rad - 1
	}
	switch {
	case y < rad:
		cy = rad
	case y >= h-rad:
		cy = h - rad - 1
	}
	if cx < 0 || cy < 0 {
		return false
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy > rad*rad
}
