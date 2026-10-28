package main

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"time"

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
	c.redrawAll()
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

// drawPin paints the keypad under the status bar. Caller holds drawMu.
func (c *console) drawPin() {
	f := apple()
	w, h := c.s.W, c.s.H-c.barH
	img := canvas(w, h)
	for y := 0; y < h; y++ {
		ui.Fill(img, image.Rect(0, y, w, y+1), blend(rgb(0x1c2a44), rgb(0x000000), min(1, float64(y)/float64(h)*1.3)))
	}
	white := rgb(0xffffff)
	apTextCenter(img, f.title, w/2, 330, white, c.pinTitle())
	for i := 0; i < pinLen; i++ {
		cx := w/2 - (pinLen-1)*40 + i*80
		ui.Circle(img, cx, 450, 18, white)
		if i >= len(c.pinEntry) {
			ui.Circle(img, cx, 450, 15, blend(rgb(0x1c2a44), rgb(0x000000), 0.3))
		}
	}
	if c.pinMsg != "" {
		apTextCenter(img, f.callout, w/2, 560, rgb(0xff453a), c.pinMsg)
	}
	digits := textFace("inter-semibold", fonts.InterSemiBold, true, 84)
	for _, k := range c.pinKeys() {
		cx, cy := (k.r.Min.X+k.r.Max.X)/2, (k.r.Min.Y+k.r.Max.Y)/2
		switch k.key {
		case "":
		case "del":
			label := "Delete"
			if c.pinEntry == "" {
				label = "Cancel"
			}
			apTextCenter(img, f.headline, cx, cy, white, label)
		default:
			ui.Circle(img, cx, cy, k.r.Dx()/2, rgb(0x3a3a3c))
			apTextCenter(img, digits, cx, cy, white, k.key)
		}
	}
	c.s.blitRGBA(img, 0, c.barH)
	c.s.Flush()
}

// pinTouch takes a tap on the keypad. Caller holds drawMu.
func (c *console) pinTouch(p TouchPoint) {
	if !p.Up {
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
			c.drawPin()
			return
		}
		c.pinEntry += k.key
		c.pinMsg = ""
		c.drawPin()
		if len(c.pinEntry) == pinLen {
			time.Sleep(120 * time.Millisecond) // the fourth dot shows before it's checked
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
