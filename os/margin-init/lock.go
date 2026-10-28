package main

import (
	"fmt"
	"image"
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

	now := time.Now().In(displayZone)
	white := rgb(0xffffff)
	apTextCenter(img, f.headline, w/2, 110, blend(white, top, 0.15), now.Format("Monday 2 January"))
	apTextCenter(img, textFace("inter-semibold", fonts.InterSemiBold, true, 190), w/2, 270, white, now.Format("15:04"))

	// Widgets, as on iOS: today's habits from Soma, and the book.
	glass := func(r image.Rectangle) {
		ui.RoundRect(img, r, 36, blend(blend(top, rgb(0x000000), min(1, float64(r.Min.Y)/float64(h)*1.4)), white, 0.14))
	}
	wy := 430
	if soma.linked() {
		habits := somaHabits()
		done := 0
		for _, hb := range habits {
			if hb.done(somaToday()) {
				done++
			}
		}
		r := image.Rect(w/2-470, wy, w/2-15, wy+190)
		glass(r)
		frac := 0.0
		if len(habits) > 0 {
			frac = float64(done) / float64(len(habits))
		}
		ring(img, r.Min.X+105, r.Min.Y+95, 62, 18, frac, rgb(0x2c3a2f), rgb(0x30d158))
		apText(img, f.title, r.Min.X+200, r.Min.Y+88, white, fmt.Sprintf("%d / %d", done, len(habits)))
		apText(img, f.callout, r.Min.X+200, r.Min.Y+136, blend(white, top, 0.3), "habits today")
		if m := somaReading(somaToday()); m > 0 {
			apText(img, f.caption, r.Min.X+200, r.Min.Y+172, blend(white, top, 0.4), fmt.Sprintf("%d min read", m))
		}
	}
	if it != nil {
		r := image.Rect(w/2+15, wy, w/2+470, wy+190)
		if !soma.linked() {
			r = image.Rect(w/2-230, wy, w/2+230, wy+190)
		}
		glass(r)
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

	if it != nil {
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
