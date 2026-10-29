package main

import (
	"testing"
)

// Each tab, once drawn, opens again from what was kept, in memory and after a restart.
func TestViewsKept(t *testing.T) {
	c := workFixture(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	views.Lock()
	views.off = false
	views.Unlock()
	t.Cleanup(func() { views.off = true })

	tabs := []mode{modeBooksHome, modeBooks, modeStore, modeWords, modeSoma, modePlaybox, modeBrowser, modeSettings}
	for _, m := range tabs {
		c.setMode(m)
	}
	keys := map[mode]string{}
	for _, m := range tabs {
		c.setMode(m)
		keys[m] = c.viewKey()
		if keys[m] == "" {
			t.Fatalf("mode %d isn't kept", m)
		}
	}
	// Back to each: shown from memory, the same screen, no drawing.
	for _, m := range tabs {
		c.setMode(modeTerminal)
		views.Lock()
		e := views.m[keys[m]]
		views.Unlock()
		if e == nil {
			t.Fatalf("mode %d not kept", m)
		}
		c.setMode(m)
		if c.page != e.p {
			t.Fatalf("mode %d was drawn again, not shown from what was kept", m)
		}
	}

	// A sub-tab of the workstation too.
	c.setMode(modeSoma)
	c.somaUI.work = "habits"
	c.showPage()
	hk := c.viewKey()
	c.somaUI.work = "overview"
	c.showPage()
	c.somaUI.work = "habits"
	c.showPage()
	views.Lock()
	he := views.m[hk]
	views.Unlock()
	if he == nil || c.page != he.p {
		t.Fatal("going back to Habits should show what was kept")
	}
	// A change on the page itself is drawn at once.
	c.showPage()
	if c.page == he.p {
		t.Fatal("drawing the view on screen again should draw it fresh")
	}

	// After a restart: nothing in memory, the tablet's copy shows at once.
	views.writes.Wait()
	views.Lock()
	views.m, views.bytes = nil, 0
	views.Unlock()
	c.setMode(modeSettings)
	c.setMode(modeBooks)
	if c.page == nil || c.page.img.Rect.Dy() != c.viewH() || len(c.page.buttons) == 0 {
		t.Fatalf("Library after a restart: %v rows, %d buttons", c.page.img.Rect.Dy(), len(c.page.buttons))
	}
	views.Lock()
	_, inMem := views.m[c.viewOn]
	views.Unlock()
	if inMem {
		t.Fatal("Library should have come from the tablet's copy, not been drawn")
	}
}
