package main

import (
	"testing"
	"time"
)

// holdAndDrag holds a finger on button id until it lifts, moves it to screen (tx, ty) in a few
// steps, and lets go.
func holdAndDrag(t *testing.T, c *console, id string, tx, ty int) {
	t.Helper()
	r, ok := findBtn(c, id)
	if !ok {
		t.Fatalf("no button %q", id)
	}
	x, y := screenOf(c, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
	c.pageTouch(TouchPoint{Down: true, X: x, Y: y})
	c.sc.longFired = c.pageLongPress(x, y) // what the timer does after a moment held still
	if !c.drag.on {
		t.Fatalf("holding %q didn't lift it", id)
	}
	for i := 1; i <= 6; i++ {
		c.pageTouch(TouchPoint{Moved: true, X: x + (tx-x)*i/6, Y: y + (ty-y)*i/6})
	}
	shot(t, c, "dragging-"+id[len(id)-2:])
	c.pageTouch(TouchPoint{Up: true, X: tx, Y: ty})
}

func TestDragTodo(t *testing.T) {
	c := workAt(t, "planner")
	now := time.Now().In(displayZone)
	mon := weekStart(dayStart(now))
	// Onto the week: Call Northbank, from its day at 9, to the week's first day at about 14:00.
	var col *dropZone
	for i := range c.page.drops {
		if z := &c.page.drops[i]; z.first >= 0 && z.date == mon.Format("2006-01-02") {
			col = z
		}
	}
	if col == nil {
		t.Fatal("the week has no hours to drop on")
	}
	tx := (col.r.Min.X + col.r.Max.X) / 2
	ty := col.r.Min.Y + int(float64(14*60-col.first)*col.pxMin) + 10 + c.barH // finger near the block's top
	r, _ := findBtn(c, "wk:sheet-open:task:t4")
	grab := (r.Min.Y+r.Max.Y)/2 - r.Min.Y
	holdAndDrag(t, c, "wk:sheet-open:task:t4", tx, ty+grab)
	o := somaObj("todos#t4")
	s, _ := o["slot"].(obj)
	if str(s["date"]) != mon.Format("2006-01-02") || int(num(s["start"])) < 13*60+45 || int(num(s["start"])) > 14*60+15 || int(num(s["mins"])) != 60 {
		t.Fatalf("dropped at %v", s)
	}
	shot(t, c, "planner-dragged")

	// A list row onto a day's heading: on that day's list.
	tue := mon.AddDate(0, 0, 1).Format("2006-01-02")
	for _, z := range c.page.drops {
		if z.first < 0 && z.date == tue {
			holdAndDrag(t, c, "wk:sheet-open:task:t2", (z.r.Min.X+z.r.Max.X)/2, (z.r.Min.Y+z.r.Max.Y)/2+c.barH)
		}
	}
	if d := str(somaObj("todos#t2")["date"]); d != tue {
		t.Fatalf("t2 on %s, not %s", d, tue)
	}

	// Held and let go where it was: its sheet.
	r, _ = findBtn(c, "wk:sheet-open:task:t3")
	x, y := screenOf(c, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
	c.pageTouch(TouchPoint{Down: true, X: x, Y: y})
	c.sc.longFired = c.pageLongPress(x, y)
	c.pageTouch(TouchPoint{Up: true, X: x, Y: y})
	if c.somaUI.sheet != "task:t3" {
		t.Fatalf("sheet %q", c.somaUI.sheet)
	}
}

func TestSwipeFollowsFinger(t *testing.T) {
	c := workAt(t, "planner")
	zr, ok := c.swipeZoneRect("plan:")
	if !ok {
		t.Fatal("no week to swipe")
	}
	before := c.planDay()
	x, y := screenOf(c, (zr.Min.X+zr.Max.X)/2, zr.Min.Y+400)
	c.pageTouch(TouchPoint{Down: true, X: x, Y: y})
	for i := 1; i <= 8; i++ {
		c.sc.last = time.Time{}
		c.pageTouch(TouchPoint{Moved: true, X: x - i*40, Y: y + 2})
	}
	shot(t, c, "planner-swiping")
	c.pageTouch(TouchPoint{Up: true, X: x - 320, Y: y + 2})
	if !c.planDay().After(before) {
		t.Fatalf("swiping left should show the next week: %v then %v", before, c.planDay())
	}
}
