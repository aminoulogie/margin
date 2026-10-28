package main

import (
	"testing"
	"time"
)

// A key typed into a page's field shows in the bar at once; the page follows after a pause.
func TestTypeBarShowsKeysAtOnce(t *testing.T) {
	c, _, _ := somaTestLink(t)
	drawMu.Lock()
	c.setMode(modeSoma)
	c.somaUI.typing, c.somaUI.field, c.skb.visible = true, "todo", true
	c.showPage()
	gen := c.pageGen
	c.somaKey([]byte("Buy milk"))
	if c.pageGen != gen {
		t.Fatalf("the page was redrawn for a key")
	}
	drawMu.Unlock()
	time.Sleep(typeBarPause + 200*time.Millisecond)
	drawMu.Lock()
	defer drawMu.Unlock()
	if c.pageGen == gen {
		t.Fatalf("the page wasn't redrawn after the pause")
	}
	shot(t, c, "typebar")
	c.somaUI.typing, c.skb.visible = false, false
}
