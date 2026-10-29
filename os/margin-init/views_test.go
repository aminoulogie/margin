package main

import (
	"bytes"
	"image"
	"os"
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

// A kept Soma page is drawn again only when a kind of record it read changes, and the
// tablet's copy is the whole page, the same pixels.
func TestViewsFreshUntilTheirRecordsChange(t *testing.T) {
	c := workFixture(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	views.Lock()
	views.off = false
	views.Unlock()
	t.Cleanup(func() { views.off = true })
	c.setMode(modeSoma)
	c.somaUI.work = "projects"
	c.showPage()
	key := c.viewKey()
	views.Lock()
	e := views.m[key]
	views.Unlock()
	if e == nil || !e.soma || len(e.fields) == 0 {
		t.Fatalf("Projects wasn't kept with what it read: %+v", e)
	}
	now := time.Now()
	if viewStale(e, now) {
		t.Fatal("just drawn, it's stale")
	}
	somaPut("hunger#h1", obj{"id": "h1", "level": 3})
	if viewStale(e, now) {
		t.Fatalf("a record Projects doesn't read made it stale (it read %v)", e.fields)
	}
	if !viewStale(e, now.Add(5*time.Minute)) {
		t.Fatal("five minutes on, it should be drawn again")
	}
	somaPut("projects#p9", obj{"id": "p9", "name": "New one", "status": "active"})
	if !viewStale(e, now) {
		t.Fatal("a new project should make Projects stale")
	}

	// The tablet's copy: the whole page, pixel for pixel, its buttons too.
	d := c.viewFromDisk(key)
	if d == nil {
		t.Fatal("not on the tablet")
	}
	if d.p.img.Rect != e.p.img.Rect || !bytes.Equal(d.p.img.Pix, cloneRGBA(e.p.img).Pix) {
		t.Fatalf("the tablet's copy differs: %v vs %v", d.p.img.Rect, e.p.img.Rect)
	}
	if len(d.p.buttons) == 0 || !d.disk {
		t.Fatal("the tablet's copy lost its buttons, or isn't marked to be drawn fresh")
	}
}

func TestPackRoundTrip(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 37, 21))
	for i := range img.Pix {
		if (i/4)%7 < 3 {
			img.Pix[i] = byte(i * 13)
		}
	}
	for _, im := range []*image.RGBA{img, img.SubImage(image.Rect(3, 2, 30, 19)).(*image.RGBA)} {
		b := packRGBA(nil, im)
		want := cloneRGBA(im)
		got := make([]byte, len(want.Pix))
		if err := unpackRGBA(b, got); err != nil || !bytes.Equal(got, want.Pix) {
			t.Fatalf("round trip: %v", err)
		}
		if unpackRGBA(b[:len(b)-1], got) == nil {
			t.Fatal("a damaged file should be refused")
		}
	}
}

// A page the workstation took from its own kept ones still knows what it reads.
func TestViewsKnowWhatCachedPagesRead(t *testing.T) {
	c := workFixture(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	views.Lock()
	views.off = false
	views.Unlock()
	t.Cleanup(func() { views.off = true })
	c.setMode(modeSoma)
	c.somaUI.work = "projects"
	c.showPage()
	key := c.viewKey()
	views.Lock()
	delete(views.m, key) // memory and the tablet let it go; the workstation's own copy is still there
	views.Unlock()
	os.Remove(viewPath(key))
	c.somaUI.work = "overview"
	c.showPage()
	c.somaUI.work = "projects"
	c.showPage()
	views.Lock()
	e := views.m[key]
	views.Unlock()
	if e == nil || len(e.fields) == 0 {
		t.Fatalf("kept without what it read: %+v", e)
	}
	somaPut("projects#p9", obj{"id": "p9", "name": "New one", "status": "active"})
	if !viewStale(e, time.Now()) {
		t.Fatal("a new project should make Projects stale")
	}
}
