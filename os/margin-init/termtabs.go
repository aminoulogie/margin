package main

import (
	"image"

	"margin-init/ui"
)

// drawTermTabs draws the tab bar above the shell, on the terminal's black, and makes it the
// page for taps there (the terminal has no other page). Caller holds drawMu.
func (c *console) drawTermTabs() {
	img := image.NewRGBA(image.Rect(0, 0, c.s.W, tabsH))
	ui.Fill(img, img.Rect, consoleBG)
	p := &page{img: img, header: tabsH}
	dark := apDark
	setPalette(true) // the terminal is dark whatever the system is
	c.booksTabs(p, "tab:terminal")
	setPalette(dark)
	c.termBar, c.page = p, p
	c.s.blitRGBA(img, 0, c.barH)
}
