package main

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/draw"
	"math"

	"margin-init/fonts"
	"margin-init/ui"
)

// The passcode: four digits, asked for when the lock screen is tapped (and after a restart),
// set and turned off in Settings > Display > Lock Screen. Only a salted hash is kept.
//
// pinMode is what the keypad is doing: "unlock" (behind the lock screen), "set" and
// "confirm" (a new passcode, twice), "off" (the current one, to turn it off).

const pinLen = 4

func pinHash(pin string) string {
	h := sha256.Sum256([]byte("margin-passcode:" + pin))
	return hex.EncodeToString(h[:])
}

// needLock: wake (and start) behind the lock screen.
func (c *console) needLock() bool { return !c.cfg.NoLock || c.cfg.PinHash != "" }

// startPin opens the keypad. Caller holds drawMu.
func (c *console) startPin(mode string) {
	c.pinMode, c.pinEntry, c.pinMsg = mode, "", ""
	if mode == "unlock" && c.lockBlur != nil { // made while the lock screen was up
		c.pinBlur = c.lockBlur
	} else {
		c.pinBlur = c.s.blurred(c.barH)
	}
	c.pinBG = nil
	c.redrawAll()
}

// blurred is the screen below the status bar as it is now, blurred heavily and dimmed, as
// iOS puts behind its passcode: sampled every 12 pixels, smoothed, and spread back out.
// nil when the screen is nearly black (nothing to blur).
func (s *Screen) blurred(top int) *image.RGBA {
	const step = 12
	w, h := s.W, s.H-top
	sw, sh := w/step+1, h/step+1
	small := make([]float64, sw*sh*3)
	sum := 0.0
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			fx, fy := s.rot.toFB(min(x*step, s.W-1), min(top+y*step, s.H-1), s.fbW, s.fbH)
			o := fy*s.stride + fx*(s.bpp/8)
			if s.bpp != 32 || o+3 >= len(s.buf) {
				return nil
			}
			v := uint32(s.buf[o]) | uint32(s.buf[o+1])<<8 | uint32(s.buf[o+2])<<16
			i := (y*sw + x) * 3
			small[i], small[i+1], small[i+2] = float64(v>>s.red.offset&0xff), float64(v>>s.green.offset&0xff), float64(v>>s.blue.offset&0xff)
			sum += small[i] + small[i+1] + small[i+2]
		}
	}
	if sum/float64(sw*sh*3) < 6 {
		return nil
	}
	// Three box passes each way: close to a Gaussian, about 60 screen pixels wide.
	tmp := make([]float64, len(small))
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				for c := 0; c < 3; c++ {
					a, n := 0.0, 0.0
					for d := -2; d <= 2; d++ {
						if xx := x + d; xx >= 0 && xx < sw {
							a += small[(y*sw+xx)*3+c]
							n++
						}
					}
					tmp[(y*sw+x)*3+c] = a / n
				}
			}
		}
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				for c := 0; c < 3; c++ {
					a, n := 0.0, 0.0
					for d := -2; d <= 2; d++ {
						if yy := y + d; yy >= 0 && yy < sh {
							a += tmp[(yy*sw+x)*3+c]
							n++
						}
					}
					small[(y*sw+x)*3+c] = a / n
				}
			}
		}
	}
	// Spread back out, bilinear in integers (the tablet's Atom does floats slowly), dimmed
	// to 55% as iOS does.
	sm := make([]int32, len(small))
	for i, v := range small {
		sm[i] = int32(v * 0.55 * 16)
	}
	img := canvas(w, h)
	for y := 0; y < h; y++ {
		if y%2 == 1 { // smooth enough that every other row is its neighbour's copy
			copy(img.Pix[y*img.Stride:(y+1)*img.Stride], img.Pix[(y-1)*img.Stride:])
			continue
		}
		y0 := min(y/step, sh-1)
		y1 := min(y0+1, sh-1)
		ty := int32(y % step * 256 / step)
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			x0 := min(x/step, sw-1)
			x1 := min(x0+1, sw-1)
			tx := int32(x % step * 256 / step)
			a, b, c, d := (y0*sw+x0)*3, (y0*sw+x1)*3, (y1*sw+x0)*3, (y1*sw+x1)*3
			for ch := 0; ch < 3; ch++ {
				top := sm[a+ch]*(256-tx) + sm[b+ch]*tx
				bot := sm[c+ch]*(256-tx) + sm[d+ch]*tx
				row[4*x+ch] = uint8((top*(256-ty) + bot*ty) >> 20)
			}
			row[4*x+3] = 255
		}
	}
	return img
}

func (c *console) pinTitle() string {
	switch c.pinMode {
	case "set":
		return "Enter a New Passcode"
	case "confirm":
		return "Enter It Again"
	case "off":
		return "Enter Your Passcode"
	}
	return "Enter Passcode"
}

// pinKeys are the keypad's twelve places: 1-9, then (blank) 0 ⌫.
func (c *console) pinKeys() []struct {
	r   image.Rectangle
	key string
} {
	w := c.s.W
	const d, gap = 180, 50
	x0, y0 := (w-3*d-2*gap)/2, 720
	var out []struct {
		r   image.Rectangle
		key string
	}
	for i, k := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "", "0", "del"} {
		col, row := i%3, i/3
		r := image.Rect(x0+col*(d+gap), y0+row*(d+40), x0+col*(d+gap)+d, y0+row*(d+40)+d)
		out = append(out, struct {
			r   image.Rectangle
			key string
		}{r, k})
	}
	return out
}

// pinBase is the keypad without what changes as digits are typed (the dots, Delete/Cancel),
// drawn once per title and message: a digit then repaints two small strips, not the screen.
func (c *console) pinBase() *image.RGBA {
	key := c.pinTitle() + "|" + c.pinMsg
	if c.pinBG != nil && c.pinBGKey == key {
		return c.pinBG
	}
	f := apple()
	w, h := c.s.W, c.s.H-c.barH
	img := canvas(w, h)
	if c.pinBlur != nil && c.pinBlur.Rect.Dx() == w && c.pinBlur.Rect.Dy() == h {
		copy(img.Pix, c.pinBlur.Pix)
	} else {
		for y := 0; y < h; y++ {
			ui.Fill(img, image.Rect(0, y, w, y+1), blend(rgb(0x1c2a44), rgb(0x000000), min(1, float64(y)/float64(h)*1.3)))
		}
	}
	white := rgb(0xffffff)
	apTextCenter(img, f.title, w/2, 330, white, c.pinTitle())
	if c.pinMsg != "" {
		apTextCenter(img, f.callout, w/2, 560, rgb(0xff453a), c.pinMsg)
	}
	digits := textFace("inter-semibold", fonts.InterSemiBold, true, 84)
	for _, k := range c.pinKeys() {
		if k.key == "" || k.key == "del" {
			continue
		}
		cx, cy := (k.r.Min.X+k.r.Max.X)/2, (k.r.Min.Y+k.r.Max.Y)/2
		glass(img, k.r, k.r.Dx()/2, rgb(0xffffff), 0.2) // a frosted key, the blur showing through
		apTextCenter(img, digits, cx, cy, white, k.key)
	}
	c.pinBG, c.pinBGKey = img, key
	return img
}

// pinDotsRect and pinDelRect are the strips a typed digit changes.
func (c *console) pinDotsRect() image.Rectangle {
	return image.Rect(c.s.W/2-200, 420, c.s.W/2+200, 480)
}

func (c *console) pinDelRect() image.Rectangle {
	for _, k := range c.pinKeys() {
		if k.key == "del" {
			return k.r
		}
	}
	return image.Rectangle{}
}

// drawPinStrip paints one strip over the base and puts just that on the screen.
func (c *console) drawPinStrip(r image.Rectangle, paint func(img *image.RGBA)) {
	base := c.pinBase()
	img := image.NewRGBA(r)
	draw.Draw(img, r, base, r.Min, draw.Src)
	paint(img)
	c.s.blitRGBA(img, r.Min.X, c.barH+r.Min.Y)
}

// drawPinDynamic paints the dots and Delete/Cancel. Caller flushes.
func (c *console) drawPinDynamic() {
	white := rgb(0xffffff)
	c.drawPinStrip(c.pinDotsRect(), func(img *image.RGBA) {
		for i := 0; i < pinLen; i++ {
			cx := c.s.W/2 - (pinLen-1)*40 + i*80
			if i < len(c.pinEntry) {
				ui.Circle(img, cx, 450, 18, white)
				continue
			}
			// An empty dot: a white ring, the background showing inside.
			for y := 450 - 19; y <= 450+19; y++ {
				for x := cx - 19; x <= cx+19; x++ {
					d := math.Hypot(float64(x-cx)+0.5, float64(y-450)+0.5)
					if a := min(18.5-d, d-14.5, 1); a > 0 && image.Pt(x, y).In(img.Rect) {
						o := img.PixOffset(x, y)
						for c := 0; c < 3; c++ {
							img.Pix[o+c] = uint8(float64(img.Pix[o+c])*(1-a) + 255*a)
						}
					}
				}
			}
		}
	})
	dr := c.pinDelRect()
	c.drawPinStrip(dr, func(img *image.RGBA) {
		label := "Delete"
		if c.pinEntry == "" {
			label = "Cancel"
		}
		apTextCenter(img, apple().headline, (dr.Min.X+dr.Max.X)/2, (dr.Min.Y+dr.Max.Y)/2, white, label)
	})
}

// drawPin paints the whole keypad under the status bar. Caller holds drawMu.
func (c *console) drawPin() {
	c.s.blitRGBA(c.pinBase(), 0, c.barH)
	c.drawPinDynamic()
	c.s.Flush()
}

// pinTouch takes a key the moment a finger lands on it (not when it lifts: typing a code is
// quick). Caller holds drawMu.
func (c *console) pinTouch(p TouchPoint) {
	if !p.Down {
		return
	}
	y := p.Y - c.barH
	for _, k := range c.pinKeys() {
		if !image.Pt(p.X, y).In(k.r.Inset(-10)) || k.key == "" {
			continue
		}
		if k.key == "del" {
			if c.pinEntry == "" { // cancel
				c.pinMode = ""
				c.redrawAll()
				return
			}
			c.pinEntry = c.pinEntry[:len(c.pinEntry)-1]
			c.drawPinDynamic()
			c.s.Flush()
			return
		}
		c.pinEntry += k.key
		c.drawPinDynamic()
		c.s.Flush()
		if len(c.pinEntry) == pinLen {
			c.pinDone()
		}
		return
	}
}

// pinDone checks or keeps a full passcode. Caller holds drawMu.
func (c *console) pinDone() {
	entry := c.pinEntry
	c.pinEntry = ""
	switch c.pinMode {
	case "unlock", "off":
		if pinHash(entry) != c.cfg.PinHash {
			c.pinMsg = "Wrong passcode"
			c.drawPin()
			return
		}
		if c.pinMode == "off" {
			c.cfg.PinHash = ""
			c.cfg.save()
			c.toastMsg("Passcode turned off")
		}
		c.pinMode = ""
		if c.locked {
			c.unlock()
			return
		}
	case "set":
		c.pinFirst, c.pinMode = entry, "confirm"
		c.drawPin()
		return
	case "confirm":
		if entry != c.pinFirst {
			c.pinMode, c.pinMsg = "set", "They didn't match. Try again."
			c.drawPin()
			return
		}
		c.cfg.PinHash, c.pinFirst, c.pinMode = pinHash(entry), "", ""
		c.cfg.save()
		c.toastMsg("Passcode set")
	}
	c.redrawAll()
}
