package main

import (
	"fmt"
	"image"
	"image/draw"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"margin-init/fonts"
	"margin-init/ui"
)

// The lock screen, as a Kindle's sleep cover meets iPadOS's lock screen: when the screen
// wakes it shows the time, the date and the book being read (its cover, title and how far
// along), on its cover's colour. A tap opens Margin where it was.
//
// And while the screen is off, the tablet saves power: the processor's governor goes to
// powersave and Wi-Fi to power-save mode, both put back on wake. (Real suspend is unreliable
// on this Intel MID kernel, so the system stays up, idle.)

// drawLock paints the lock screen under the status bar. Caller holds drawMu.
func (c *console) drawLock() {
	c.endGPUScroll()
	covers.mu.Lock()
	covers.loaded = func(url string) {
		drawMu.Lock()
		c.coverLoaded(url)
		drawMu.Unlock()
	}
	covers.mu.Unlock()
	f := apple()
	w, h := c.s.W, c.s.H-c.barH
	img := canvas(w, h)
	c.shelf = findBooks()
	reading := c.readingNow()

	// The book's colour, fading into black.
	top := rgb(0x1c1c1e)
	var it *storeItem
	if len(reading) > 0 {
		b := c.shelf[reading[0]]
		it = &storeItem{key: b.path, title: b.title, author: b.author, cover: b.cover}
		if cv := covers.get(b.cover, 120, 180); cv != nil {
			top = coverTint(cv, b.cover)
		} else {
			top = blend(placeholderColour(b.path), rgb(0x000000), 0.35)
		}
	}
	for y := 0; y < h; y++ {
		ui.Fill(img, image.Rect(0, y, w, y+1), blend(top, rgb(0x000000), min(1, float64(y)/float64(h)*1.4)))
	}
	photo := false
	if c.cfg.Wallpaper != "" { // a photo from Settings > Wallpaper, a little darker so the time reads
		if wp := covers.get(wallURL(c.cfg.Wallpaper, w, c.s.H), w, h); wp != nil {
			draw.Draw(img, img.Rect, wp, wp.Rect.Min, draw.Src)
			for i := 0; i < len(img.Pix); i += 4 {
				// In int: a byte times 3 overflows and turned the photo's colours to noise.
				img.Pix[i] = byte(int(img.Pix[i]) * 3 / 4)
				img.Pix[i+1] = byte(int(img.Pix[i+1]) * 3 / 4)
				img.Pix[i+2] = byte(int(img.Pix[i+2]) * 3 / 4)
			}
			top, photo = coverTint(wp, c.cfg.Wallpaper), true
		}
	}

	now := time.Now().In(displayZone)
	white := rgb(0xffffff)
	apTextCenter(img, f.headline, w/2, 110, blend(white, top, 0.15), now.Format("Monday 2 January"))
	apTextCenter(img, textFace("inter-semibold", fonts.InterSemiBold, true, 190), w/2, 270, white, now.Format("15:04"))

	// Widgets, as on iOS: today's habits from Soma, and the book.
	glassCard := func(r image.Rectangle) { glass(img, r, 36, white, 0.16) }
	wy := 430
	if soma.linked() { // Soma's rings, as its Today glance: the rings and the four numbers
		r := image.Rect(w/2-470, wy, w/2-15, wy+190)
		if it == nil {
			r = image.Rect(w/2-230, wy, w/2+230, wy+190)
		}
		glassCard(r)
		ringSet(img, r.Min.X+20, r.Min.Y+20, 150, somaRingSpecs(somaToday()), false, true)
		sx, sy := r.Min.X+190, r.Min.Y+44
		for _, rg := range somaRingsFor(somaToday()) {
			v := fmt.Sprintf("%.0f", rg.Value)
			apText(img, f.captionBold, sx, sy, rgb(rg.From), v)
			apText(img, f.caption, sx+ui.TextWidth(f.captionBold, v), sy, blend(white, top, 0.4), fmt.Sprintf("/%.0f %s", rg.Goal, rg.Unit))
			sy += 40
		}
	}
	if it != nil {
		r := image.Rect(w/2+15, wy, w/2+470, wy+190)
		if !soma.linked() {
			r = image.Rect(w/2-230, wy, w/2+230, wy+190)
		}
		glassCard(r)
		cr := image.Rect(r.Min.X+24, r.Min.Y+20, r.Min.X+24+100, r.Min.Y+170)
		c.drawCover(img, cr, it)
		tx := cr.Max.X + 24
		apText(img, f.headline, tx, r.Min.Y+70, white, clip(f.headline, it.title, r.Max.X-tx-20))
		pct := c.lib.Progress[it.key].Pct
		apText(img, f.caption, tx, r.Min.Y+112, blend(white, top, 0.3), fmt.Sprintf("%d%% read", pct))
		bar := image.Rect(tx, r.Min.Y+140, r.Max.X-28, r.Min.Y+148)
		ui.RoundRect(img, bar, 4, blend(white, top, 0.7))
		if pct > 0 {
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(bar.Dx()*pct/100, 8), bar.Max.Y), 4, white)
		}
	}

	if it != nil && !photo {
		// The cover itself, large, under the widgets: the book's own picture is the wallpaper.
		cr := image.Rect((w-520)/2, 720, (w+520)/2, 720+780)
		shadow(img, cr, 12, 0.35)
		c.drawCover(img, cr, it)
	} else {
		apTextCenter(img, f.serifLarge, w/2, 900, white, "Margin")
		apTextCenter(img, f.callout, w/2, 980, rgb(0x8d8d93), "Books")
	}
	hint := "Tap to open"
	if c.cfg.PinHash != "" {
		hint = "Tap to unlock"
	}
	apTextCenter(img, f.callout, w/2, h-90, rgb(0x8d8d93), hint)
	ui.RoundRect(img, image.Rect(w/2-90, h-30, w/2+90, h-22), 4, rgb(0x8d8d93)) // iOS's home bar

	c.s.blitRGBA(img, 0, c.barH)
	c.s.Flush()
}

// unlock leaves the lock screen for where Margin was. Caller holds drawMu.
func (c *console) unlock() {
	c.locked = false
	c.lockBlur = nil
	// Back to the page as it was left, at once: it's still drawn, so it's shown again rather
	// than drawn again (a Soma page takes the Atom a second or more).
	if c.page != nil && c.xapp == nil && c.mode != modeTerminal && c.mode != modeReader && c.pinMode == "" {
		c.endGPUScroll()
		clear(c.s.buf)
		c.s.markRows(0, c.s.fbH-1)
		c.drawBar()
		c.blitPage()
		c.drawToast()
		if _, ok := c.pageTyping(); ok || (c.mode == modeStore && c.store.typing) {
			c.skb.draw()
		}
		c.s.Flush()
		return
	}
	c.redrawAll()
}

// --- power saving while the screen is off --------------------------------------------------

var (
	powerMu        sync.Mutex // one switch at a time
	savedGovernors = map[string]string{}
)

// powerSave(true) slows the processor and puts Wi-Fi in power-save mode; false undoes it.
// Best effort: runs on its own, never holding drawMu.
func powerSave(on bool) {
	if _, err := os.Stat("/system/bin/linker"); err != nil {
		return // not on the tablet (tests, the PC): leave this machine's CPU alone
	}
	go func() {
		powerMu.Lock()
		defer powerMu.Unlock()
		govs, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor")
		for _, g := range govs {
			if on {
				if cur, err := os.ReadFile(g); err == nil {
					savedGovernors[g] = strings.TrimSpace(string(cur))
				}
				os.WriteFile(g, []byte("powersave"), 0o644)
			} else if old := savedGovernors[g]; old != "" {
				os.WriteFile(g, []byte(old), 0o644)
			}
		}
		state := "off"
		if on {
			state = "on"
		}
		if err := runInAlpine("/usr/sbin/iw", "dev", "wlan0", "set", "power_save", state); err != nil {
			log.Printf("wifi power save %s: %v", state, err)
		}
		log.Printf("power save %s (%d cpu governors)", state, len(govs))
	}()
}
