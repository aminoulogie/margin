package main

import "margin-init/ui"

// The Browser tab: the page engine is in browser_layout.go; its screen is still being
// written, so for now the tab says so.

type browserUI struct {
	typing  bool
	editing string
}

func (c *console) brKey(b []byte) {}

func (c *console) browserPage() *page {
	img := canvas(c.s.W, c.viewH())
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img, header: tabsH}
	c.booksTabs(p, "tab:browser")
	apText(img, apple().largeTitle, pageGutter, 238, apLabel, "Browser")
	apText(img, apple().callout, pageGutter, 300, apSecondary, "Coming in the next update.")
	return p
}

func (c *console) browserTap(id string) bool { return false }
