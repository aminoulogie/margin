package main

import (
	"strings"
	"testing"
	"time"
)

// waitSync waits for the sync the last edit started to end (what the page says of it, and so
// the page's key, holds still then).
func waitSync(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		soma.mu.Lock()
		busy := soma.running
		soma.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the sync never ended")
		}
	}
}

// A sheet is laid over the page as it was: opening it, changing what's in it and closing it
// don't draw the page again (each took seconds on the tablet), the page under it doesn't move,
// and what's deleted from it is gone at once.
func TestSheetsOverKeptPage(t *testing.T) {
	c := workFixture(t)
	soma.sync()
	drawMu.Lock()
	defer drawMu.Unlock()
	c.setMode(modeSoma)
	c.somaUI.work = "time"
	c.somaUI.workSel = map[string]string{"timewk:tab": "1"} // Entries
	c.showPage()
	base := c.page
	view, _ := c.workKey()
	var entry string
	for _, b := range base.buttons {
		if strings.HasPrefix(b.id, "wk:sheet-open:entry:e") {
			entry = b.id
			break
		}
	}
	if entry == "" {
		t.Fatal("no entry on Time > Entries")
	}
	id := strings.TrimPrefix(entry, "wk:sheet-open:entry:")
	kept := func() *page {
		if e := wkPageCache.ofView(view); e != nil {
			return e.p
		}
		return nil
	}

	// Opened: over the page as it was, dimmed; the page isn't drawn again.
	tapBtn(t, c, entry)
	if !strings.HasPrefix(c.somaUI.sheet, "entry:") {
		t.Fatalf("the entry's sheet didn't open: %q", c.somaUI.sheet)
	}
	if kept() != base {
		t.Fatal("opening a sheet drew the page under it again")
	}
	if c.page.img.Rect.Dy() != c.viewH() || c.page.header != 0 {
		t.Fatal("the sheet should lie over the screen as it was, a page that doesn't scroll")
	}
	x, y := wkX0+6, tabsH+20 // in the fixed header, outside the sheet
	under, over := base.img.RGBAAt(x, y), c.page.img.RGBAAt(x, y)
	if dim := uint8(int(under.G) * (256 - 153) >> 8); over.G != dim {
		t.Fatalf("outside the sheet: %v, the page dimmed would be %d", over, dim)
	}

	// Changed in the sheet: the sheet shows it at once; the page under it waits for a quiet
	// moment to be drawn again.
	tapBtn(t, c, "wk:eset:"+id+":billable:0")
	if b, ok := somaObj("timeEntries#" + id)["billable"].(bool); !ok || b {
		t.Fatal("the entry isn't internal now")
	}
	if !strings.HasPrefix(c.somaUI.sheet, "entry:") {
		t.Fatal("the sheet closed after a change in it")
	}
	if kept() != base {
		t.Fatal("the page under the sheet was drawn again at once")
	}
	if !c.wkRefreshOn {
		t.Fatal("no fresh drawing of the page under the sheet is waiting")
	}
	waitSync(t)
	c.wkFresh = true // the quiet moment (wkRefreshLater)
	c.showPage()
	c.wkFresh = false
	fresh := kept()
	if fresh == nil || fresh == base {
		t.Fatal("the page under the sheet wasn't drawn fresh")
	}

	// Closed: the page drawn fresh is shown as it is.
	tapBtn(t, c, "wk:sheet:close")
	if c.somaUI.sheet != "" || c.page != fresh {
		t.Fatal("closing the sheet drew the page again")
	}

	// Under a sheet nothing moves: a drag isn't a scroll, nor a tap.
	tapBtn(t, c, entry)
	sy := c.scrollY()
	x0, y0 := 600, c.barH+c.viewH()/2
	c.pageTouch(TouchPoint{Down: true, X: x0, Y: y0})
	c.pageTouch(TouchPoint{Moved: true, X: x0, Y: y0 - 300})
	c.pageTouch(TouchPoint{Up: true, X: x0, Y: y0 - 300})
	if c.scrollY() != sy || !strings.HasPrefix(c.somaUI.sheet, "entry:") {
		t.Fatalf("a drag under the sheet scrolled (%d to %d) or closed it (%q)", sy, c.scrollY(), c.somaUI.sheet)
	}

	// Deleted from the sheet: gone at once, with Undo, which puts it back.
	tapBtn(t, c, "wk:edel:"+id)
	if c.somaUI.sheet != "" || somaObj("timeEntries#"+id) != nil {
		t.Fatal("the entry wasn't deleted, or its sheet stayed")
	}
	if _, ok := findBtn(c, entry); ok {
		t.Fatal("the deleted entry is still on the page")
	}
	tapBtn(t, c, "wk:undo")
	if somaObj("timeEntries#"+id) == nil {
		t.Fatal("Undo didn't bring the entry back")
	}
	if _, ok := findBtn(c, entry); !ok {
		t.Fatal("the entry brought back isn't on the page")
	}
	waitSync(t)
}
