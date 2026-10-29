package main

import "testing"

func TestHabitsHeatAndSteps(t *testing.T) {
	c := workAt(t, "habits")
	somaPut("habits#teeth", obj{"id": "teeth", "name": "Teeth routine", "color": "#5b8cff", "history": obj{},
		"steps": []any{obj{"id": "a", "name": "Brush 1", "target": 1}, obj{"id": "b", "name": "Brush 2", "target": 1},
			obj{"id": "c", "name": "Brush 3", "target": 1}, obj{"id": "d", "name": "Floss", "target": 1},
			obj{"id": "e", "name": "Mouthwash rinse", "target": 1}, obj{"id": "f", "name": "Tongue scrub", "target": 2}}})
	c.showPage()
	if !hasBtn(c, "wk:hstep:teeth:f") || !hasBtn(c, "wk:htick:teeth") {
		t.Fatal("every step and Done should be on the row")
	}
	shot(t, c, "habits-heat")
	tapBtn(t, c, "wk:htick:teeth") // Done: the whole checklist
	h := somaHabit{ID: "teeth", o: somaObj("habits#teeth")}
	for _, s := range h.steps() {
		if h.stepCount(somaToday(), s.ID) < s.Target {
			t.Fatalf("Done left %s at %d", s.Name, h.stepCount(somaToday(), s.ID))
		}
	}
	shot(t, c, "habits-done")
}

// A page's picture is kept on the tablet, and shown the first time the page opens after a
// start, before it's drawn.
func TestPagePicturesAfterRestart(t *testing.T) {
	c := workAt(t, "reports")
	key := c.snapKey()
	snapWrites.Wait()
	pic := readSnap(snapDir()+"/"+key+".raw", c.s.W, c.viewH())
	if pic == nil {
		t.Fatal("no picture kept for Reports")
	}
	for y := 0; y < 400; y++ { // its top is the page's
		if string(pic.Pix[y*pic.Stride:][:4*c.s.W]) != string(c.page.img.Pix[c.page.img.PixOffset(0, y):][:4*c.s.W]) {
			t.Fatalf("picture row %d isn't the page's", y)
		}
	}
	// As after a restart: nothing in memory, nothing shown yet.
	snaps.Lock()
	snaps.shown = nil
	snaps.Unlock()
	wkPageCache.drop()
	c.somaUI.work = "overview"
	c.showPage()
	c.somaUI.work = "reports"
	snaps.Lock()
	first := !snaps.shown[key]
	snaps.Unlock()
	if !first {
		t.Fatal("Reports counted as shown before it opened")
	}
	c.showPage()
	snaps.Lock()
	shown := snaps.shown[key]
	snaps.Unlock()
	if !shown {
		t.Fatal("the picture should have been shown on the first opening")
	}
}
