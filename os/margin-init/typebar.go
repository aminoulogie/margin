package main

import (
	"image"
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
	}
	return "", false
}

// drawTypeBar draws the bar onto the screen (the caller flushes). Caller holds drawMu.
func (c *console) drawTypeBar() {
	text, ok := c.pageTyping()
	if !ok || !c.skb.visible || !c.screenOn {
		return
	}
	f := apple()
	y0 := c.skb.y0 - typeBarH
	img := image.NewRGBA(image.Rect(0, 0, c.s.W, typeBarH))
	ui.Fill(img, img.Rect, apBG)
	r := image.Rect(pageGutter, 14, c.s.W-pageGutter, typeBarH-14)
	ui.RoundRect(img, r, r.Dy()/2, apCard2)
	text = withCursor(text, c.fieldBack)
	for text != "" && ui.TextWidth(f.body, text) > r.Dx()-60 { // the end, where the typing is
		text = string([]rune(text)[1:])
	}
	apText(img, f.body, r.Min.X+30, r.Min.Y+56, apLabel, text)
	c.s.blitRGBA(img, 0, y0)
}

// fieldTyped shows a key typed into a page's field now and the page itself after a pause.
// Caller holds drawMu.
func (c *console) fieldTyped() {
	if _, ok := c.pageTyping(); !ok {
		c.showPage() // done: the page as it is now
		return
	}
	c.drawTypeBar()
	c.s.Flush()
	c.typeGen++
	gen := c.typeGen
	time.AfterFunc(typeBarPause, func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if gen == c.typeGen && c.screenOn {
			c.showPage()
		}
	})
}
