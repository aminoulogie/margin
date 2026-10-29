package main

import (
	"os"
	"testing"
	"time"
)

// How long opening each of Margin's tabs takes on this machine (the tablet is ten to fifteen
// times slower). MARGIN_SPEED=1 prints it.
func TestTabSpeed(t *testing.T) {
	if os.Getenv("MARGIN_SPEED") == "" {
		t.Skip("set MARGIN_SPEED=1")
	}
	c := workFixture(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	views.off = os.Getenv("MARGIN_NOVIEWS") != ""
	defer func() { views.off = true }()
	modes := []struct {
		name string
		m    mode
	}{{"home", modeBooksHome}, {"library", modeBooks}, {"store", modeStore}, {"words", modeWords},
		{"soma", modeSoma}, {"playbox", modePlaybox}, {"browser", modeBrowser}, {"settings", modeSettings}, {"terminal", modeTerminal}}
	for _, m := range modes {
		c.setMode(m.m) // the first time
	}
	for round := 0; round < 2; round++ {
		for _, m := range modes {
			c.setMode(modeBooksHome)
			if m.m == modeBooksHome {
				c.setMode(modeSettings)
			}
			start := time.Now()
			c.setMode(m.m)
			t.Logf("round %d %-9s %6.1f ms", round, m.name, float64(time.Since(start).Microseconds())/1000)
		}
	}
}
