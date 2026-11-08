package main

import (
	"fmt"
	"image"
	"strings"
	"time"

	"margin-init/ui"
)

// Typing in a page's field shows each key at once in a bar just above the keyboard, drawn
// on its own, and redraws the page behind only when the typing pauses: a tall page takes
// longer to draw than a key takes to type.

const (
	typeBarH     = 112
	typeBarPause = 350 * time.Millisecond
)

// pageTyping is what's being typed into a page's field now, if anything.
func (c *console) pageTyping() (string, bool) {
	switch {
	case c.mode == modeSoma && c.somaUI.typing:
		return c.somaUI.editing, true
	case c.mode == modeWords && c.wui.edit:
		return c.wui.editText, true
	case c.mode == modeSettings && c.wifi.typing:
		return masked(c.wifi.pass), true
	case c.mode == modeSettings && c.apps.typing:
		return c.apps.editing, true
	case c.mode == modePlaybox && c.pb.typing:
		return c.pb.editing, true
	case c.mode == modeBrowser && c.br.typing:
		return c.br.editing, true
	}
	return "", false
}

// typeBarCache is the bar above the keyboard (or the composer) as drawn without the text
// being typed: a key then draws only the text's strip on a copy of it and sends just that,
// as the terminal draws only the cells that changed (2026-11-08, user: typing and deleting in
// Soma were slow, the screen glitched; the terminal is instant).
type typeBarCache struct {
	key  string
	img  *image.RGBA     // the whole bar, screen coordinates, without its text
	text image.Rectangle // where the text goes
}

// typeBarKey says what the bar looks like apart from its text ("" = drawn whole each time).
func (c *console) typeBarKey(text string) string {
	if c.composing() {
		cm := c.somaUI.cmp
		return fmt.Sprint("cmp", cm, strings.TrimSpace(text) != "", text == "", c.somaUI.field, somaToday(),
			c.composerCard(c.skb.y0-composerH), apDark, wkBG, wkCard, wkLime, c.skb.y0, c.s.W)
	}
	if text == "" && c.mode == modeSoma && c.somaUI.prompt != "" {
		return "" // what the field is for: drawn whole
	}
	return fmt.Sprint("bar", c.skb.y0, c.s.W, apBG, apCard2, apLabel)
}

// drawTypeBar draws the bar onto the screen (the caller flushes). Caller holds drawMu.
func (c *console) drawTypeBar() {
	text, ok := c.pageTyping()
	if !ok || !c.skb.visible || !c.screenOn {
		return
	}
	key := c.typeBarKey(text)
	if c.composing() { // a new to-do: the composer, with its day and time
		y0 := c.skb.y0 - composerH
		img := image.NewRGBA(image.Rect(0, y0, c.s.W, c.skb.y0))
		c.cmpBare = true
		c.composerLayout(img, y0)
		c.cmpBare = false
		c.tbar = typeBarCache{key, cloneAt(img), composerTitleRect(c.composerCard(y0))}
		c.composerTitle(img, c.composerCard(y0))
		c.s.blitRGBA(img, 0, y0)
		return
	}
	f := apple()
	y0 := c.skb.y0 - typeBarH
	img := image.NewRGBA(image.Rect(0, y0, c.s.W, c.skb.y0))
	ui.Fill(img, img.Rect, apBG)
	r := image.Rect(pageGutter, y0+14, c.s.W-pageGutter, c.skb.y0-14)
	ui.RoundRect(img, r, r.Dy()/2, apCard2)
	if key == "" { // what the field is for
		apText(img, f.body, r.Min.X+30, r.Min.Y+56, apLabel, "|")
		apText(img, f.body, r.Min.X+44, r.Min.Y+56, apSecondary, clip(f.body, c.somaUI.prompt, r.Dx()-80))
		c.tbar = typeBarCache{}
		c.s.blitRGBA(img, 0, y0)
		return
	}
	c.tbar = typeBarCache{key, cloneAt(img), image.Rect(r.Min.X+r.Dy()/2-8, r.Min.Y+6, r.Max.X-r.Dy()/2+8, r.Max.Y-6)}
	c.typeBarText(img, text)
	c.s.blitRGBA(img, 0, y0)
}

// typeBarText draws the text being typed into the bar's picture img.
func (c *console) typeBarText(img *image.RGBA, text string) {
	if c.composing() {
		c.composerTitle(img, c.composerCard(c.skb.y0-composerH))
		return
	}
	f := apple()
	r := image.Rect(pageGutter, c.skb.y0-typeBarH+14, c.s.W-pageGutter, c.skb.y0-14)
	text = withCursor(text, c.fieldBack)
	for text != "" && ui.TextWidth(f.body, text) > r.Dx()-60 { // the end, where the typing is
		text = string([]rune(text)[1:])
	}
	apText(img, f.body, r.Min.X+30, r.Min.Y+56, apLabel, text)
}

// drawTypedText shows what's typed now: only the text's strip of the bar, when the bar on the
// screen is the one kept; else the whole bar. Caller holds drawMu.
func (c *console) drawTypedText() {
	text, ok := c.pageTyping()
	if !ok || !c.skb.visible || !c.screenOn {
		return
	}
	tb := &c.tbar
	if tb.key == "" || tb.key != c.typeBarKey(text) {
		c.drawTypeBar()
		return
	}
	strip := image.NewRGBA(image.Rect(0, 0, tb.text.Dx(), tb.text.Dy()))
	copyRect(strip, tb.img, tb.text)
	strip.Rect = tb.text // the same pixels, in screen coordinates
	c.typeBarText(strip, text)
	c.s.blitRGBA(strip, tb.text.Min.X, tb.text.Min.Y)
}

// cloneAt is a copy of img with the same bounds.
func cloneAt(img *image.RGBA) *image.RGBA {
	out := image.NewRGBA(img.Rect)
	copy(out.Pix, img.Pix)
	return out
}

// fieldTyped shows a key typed into a page's field now and the page itself after a pause.
// The page is drawn in a way a key stops (yielding: it gives way to the finger), so typing on
// never waits for it; a new to-do's composer doesn't show on the page, so its page isn't drawn
// again until it's added. Caller holds drawMu.
func (c *console) fieldTyped() {
	if _, ok := c.pageTyping(); !ok {
		c.showPage() // done: the page as it is now
		return
	}
	c.drawTypedText()
	c.s.Flush()
	c.typeGen++
	if c.composing() {
		return
	}
	gen := c.typeGen
	var try func()
	try = func() {
		time.AfterFunc(typeBarPause, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if gen != c.typeGen || !c.screenOn {
				return
			}
			if time.Since(c.lastInput) < typeBarPause || len(c.skb.pressed) > 0 { // still typing: later
				try()
				return
			}
			tabs, pg := c.tabsOn, c.page
			if !yielding(c.showPage) { // a key came: the screen stays as it is, drawn later
				c.tabsOn, c.page = tabs, pg
				try()
			}
		})
	}
	try()
}
