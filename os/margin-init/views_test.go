package main

import (
	"bytes"
	"testing"
	"time"
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

// A Soma view kept while its records change elsewhere (an edit, a sync from the phone) is drawn
// again in the background, so opening it shows it as it is now, at once.
func TestViewsReadyInBackground(t *testing.T) {
	c := workFixture(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	views.Lock()
	views.off = false
	views.Unlock()
	t.Cleanup(func() { views.off = true })

	c.setMode(modeSoma)
	c.somaUI.work = "planner"
	c.showPage()
	pk := c.viewKey()
	c.somaUI.work = "habits"
	c.showPage()
	before := somaObj("todos#t4")["done"]
	somaToggleTodo("t4") // a change the planner shows
	if somaObj("todos#t4")["done"] == before {
		t.Fatal("the to-do didn't change")
	}
	views.Lock()
	old := views.m[pk]
	views.Unlock()
	if old == nil || !old.stale() {
		t.Fatal("the planner kept should be out of date now")
	}
	c.lastInput = time.Now().Add(-time.Minute)
	if !c.viewPrepTick() {
		t.Fatal("nothing was made ready")
	}
	views.Lock()
	ready := views.m[pk]
	views.Unlock()
	if ready == old || ready.stale() || ready.screen == nil {
		t.Fatal("the planner wasn't drawn again")
	}
	if c.somaUI.work != "habits" || c.viewKey() == pk {
		t.Fatal("drawing in the background changed what's on screen")
	}
	for i := 0; c.viewPrepTick(); i++ { // the others kept (Overview) too, one at a time
		if i > 5 {
			t.Fatal("views keep being drawn again")
		}
	}
	// Opened: what was made ready, the same as drawing it now.
	c.somaUI.work = "planner"
	c.showPage()
	if c.page != ready.p {
		t.Fatal("the planner should open from what was made ready")
	}
	shown := append([]byte(nil), c.s.buf...)
	c.viewFresh = true
	c.showPage()
	c.viewFresh = false
	// The same, but for the running timer's band at the bottom (its seconds move on; it's
	// redrawn by itself each second). Native columns are the screen's rows.
	st, foot := c.s.stride, c.s.H-c.page.footer
	for y := 0; y*st < len(shown); y++ {
		for x := 0; x < foot && x < st/4; x++ {
			i := y*st + 4*x
			if !bytes.Equal(shown[i:i+4], c.s.buf[i:i+4]) {
				t.Fatalf("what was made ready differs from the planner drawn now (native %d,%d)", x, y)
			}
		}
	}
}
