package main

import (
	"testing"
	"time"
)

// The tab bar is on every screen, Settings and the Terminal included; a finger on a tab
// slides the selected pill to it at once, before the lift opens it.
func TestTabBarEverywhere(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	c.cfg.Animations = true
	c.setMode(modeSettings)
	if id, _ := c.page.hitRect(600, 50); id == "" || c.tabsOn != "tab:settings" {
		t.Fatalf("Settings has no tab bar: %q %q", id, c.tabsOn)
	}
	shot(t, c, "tabs-settings")

	c.setMode(modeTerminal)
	if c.termBar == nil || c.offY < c.barH+tabsH {
		t.Fatal("the terminal has no tab bar above its shell")
	}
	shot(t, c, "tabs-terminal")
	// Touch Settings in the terminal's bar: the pill slides there before the lift.
	var x int
	main, _, _, _ := c.tabLayout("tab:terminal")
	for _, tb := range main {
		if tb.id == "tab:settings" {
			x = (tb.r.Min.X + tb.r.Max.X) / 2
		}
	}
	y := c.barH + (tabBarTop+tabBarBot)/2
	before := animFrames
	c.pageTouch(TouchPoint{Down: true, X: x, Y: y})
	if animFrames == before || c.mode != modeSettings {
		t.Fatal("touching a tab should slide its pill there and open it at once")
	}
	shot(t, c, "tabs-press")
	drawMu.Unlock()
	time.Sleep(20 * time.Millisecond)
	drawMu.Lock()
	c.pageTouch(TouchPoint{Up: true, X: x, Y: y})
	if c.mode != modeSettings {
		t.Fatalf("lifting should open Settings: mode %v", c.mode)
	}

	// Margin's pages: the pills under the bar sit clear of it, and the page starts below them.
	c.setMode(modeBooksHome)
	_, sub, _, _ := c.tabLayout("tab:home")
	if sub[0].r.Min.Y-tabBarBot < 16 || c.page.header < sub[0].r.Max.Y+10 {
		t.Fatalf("pills at %v, bar ends %d, header %d", sub[0].r, tabBarBot, c.page.header)
	}
}
