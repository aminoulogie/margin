package main

import "image"

// tabPress: a finger on a tab moves the selected pill to it at once, sliding over from the
// tab that was selected (as a segmented control does), before the finger lifts and the
// page is drawn: the bar already shows where the tap goes. Shown on the screen only, like
// any press. True if id is a tab of the bar on screen. Caller holds drawMu.
func (c *console) tabPress(id string) bool {
	if c.tabsOn == "" || c.page == nil {
		return false
	}
	main, sub, mi, si := c.tabLayout(c.tabsOn)
	row, from, to := main, mi, -1
	for i, t := range main {
		if t.id == id {
			to = i
		}
	}
	if to < 0 {
		row, from = sub, si
		for i, t := range sub {
			if t.id == id {
				to = i
			}
		}
	}
	if to < 0 || to == from || row[to].icon {
		return false // not a tab, or already there (the search pill gets the usual press)
	}
	hh := min(c.page.header, c.page.img.Rect.Dy())
	if hh < tabBarBot {
		return false
	}
	hb := image.NewRGBA(image.Rect(0, 0, c.s.W, hh))
	end := row[to].r
	start := end
	if from >= 0 {
		start = row[from].r
	} else { // none selected in that row: it grows from the tab's middle
		cx := (end.Min.X + end.Max.X) / 2
		start = image.Rect(cx-end.Dy()/2, end.Min.Y, cx+end.Dy()/2, end.Max.Y)
	}
	draw := func(t float64) {
		copy(hb.Pix, c.page.img.Pix[:len(hb.Pix)])
		lerp := func(a, b int) int { return a + int(float64(b-a)*t+0.5) }
		pill := image.Rect(lerp(start.Min.X, end.Min.X), end.Min.Y, lerp(start.Max.X, end.Max.X), end.Max.Y)
		mp, sp := (*image.Rectangle)(nil), (*image.Rectangle)(nil)
		bold := map[string]bool{row[to].id: t > 0.5}
		if mi >= 0 {
			r := main[mi].r
			mp, bold[main[mi].id] = &r, true
		}
		if si >= 0 {
			r := sub[si].r
			sp, bold[sub[si].id] = &r, true
		}
		if from >= 0 {
			bold[row[from].id] = t <= 0.5
		}
		if &row[0] == &main[0] {
			mp = &pill
		} else {
			sp = &pill
		}
		drawTabs(hb, main, sub, mp, sp, bold)
		c.s.blitRGBA(hb, 0, c.barH)
	}
	if c.animOK() {
		c.frames(durTab, func(t float64) { draw(t) })
	}
	draw(1)
	c.s.Flush()
	c.press = pressState{on: true, scr: image.Rect(0, c.barH, c.s.W, c.barH+hh), src: image.Rect(0, 0, c.s.W, hh)}
	return true
}
