package main

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// calDrag moves a finger across the screen from (x, y) by (dx, dy) in steps, a few ms apart.
func calDrag(c *console, x, y, dx, dy int) {
	c.pageTouch(TouchPoint{Down: true, X: x, Y: y})
	for k := 1; k <= 10; k++ {
		c.pageTouch(TouchPoint{Moved: true, X: x + dx*k/10, Y: y + dy*k/10})
	}
	c.pageTouch(TouchPoint{Up: true, X: x + dx, Y: y + dy})
}

// The week calendar runs on without end: swipes move it by whole days (a little = one day, more
// = more days), the hours turn round past midnight, and what a frame shows is the page's own
// picture once it rests.
func TestWeekCalendarEndless(t *testing.T) {
	c := workAt(t, "planner")
	cv := c.page.cal
	if cv == nil {
		t.Fatal("the planner's week has no calendar")
	}
	from := calDate(cv.From)
	gs, _ := c.calShown(cv.Grid)
	x, y := gs.Min.X+300, gs.Min.Y+200

	calDrag(c, x, y, -60, 0) // a little: one day on
	if got := calDate(c.page.cal.From); !got.Equal(from.AddDate(0, 0, 1)) {
		t.Fatalf("a little swipe: from %s to %s, want one day", somaDate(from), c.page.cal.From)
	}
	if c.somaUI.weekFrom != c.page.cal.From {
		t.Fatalf("the planner didn't keep where the week is: %q", c.somaUI.weekFrom)
	}
	calDrag(c, x, y, -2*calColW-50, 0) // more: three days on (2.4 columns rounds to 2... plus the slop)
	got := int(calDate(c.page.cal.From).Sub(from).Hours()/24 + 0.5)
	if got < 3 || got > 4 {
		t.Fatalf("a longer swipe went %d days on from the start", got)
	}
	calDrag(c, x, y, 6*calColW+30, 0) // back, past the week before: no end
	if !calDate(c.page.cal.From).Before(from) {
		t.Fatalf("swiping back stopped at %s", c.page.cal.From)
	}

	// Hours: up past midnight, round the clock.
	top := c.page.cal.Top
	calDrag(c, x, y, 0, -700)
	if c.page.cal.Top == top {
		t.Fatal("the hours didn't move")
	}
	for range 4 {
		calDrag(c, x, y, 0, -700)
	}
	if want := (top + 5*(700-scrollSlop)) % calDayH; abs(c.page.cal.Top-want) > 2 {
		t.Fatalf("hours at %d, want %d (round the clock)", c.page.cal.Top, want)
	}
	// The buttons follow: a half hour shown on the screen adds there.
	m := (c.page.cal.Top*60/calPxH + 60) / 30 * 30 % (24 * 60)
	id := fmt.Sprintf("wk:slotadd:%s:%d", c.page.cal.From, m)
	if _, ok := findBtn(c, id); !ok {
		t.Fatalf("no %s after scrolling the hours", id)
	}

	// The screen the frames left is the page's own picture.
	shown := bytes.Clone(c.s.buf)
	c.blitPage()
	if !bytes.Equal(shown, c.s.buf) {
		t.Fatal("the calendar at rest differs from the page's picture")
	}
	// Drawn again from scratch, where it was left.
	c.showPage()
	if c.page.cal.From != c.somaUI.weekFrom || c.page.cal.Top != c.somaUI.weekTop-1 {
		t.Fatalf("drawn again at %s/%d, left at %s/%d", c.page.cal.From, c.page.cal.Top, c.somaUI.weekFrom, c.somaUI.weekTop-1)
	}
	shot(t, c, "planner-week-endless")
	// Today goes back to this week.
	tapBtn(t, c, "wk:plannav:0")
	if c.page.cal.From != somaDate(weekStart(dayStart(time.Now()))) {
		t.Fatalf("Today: from %s", c.page.cal.From)
	}
}

func BenchmarkWeekCalendarFrame(b *testing.B) {
	t := &testing.T{}
	c := workFixture(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	c.setMode(modeSoma)
	c.somaUI.work = "planner"
	c.showPage()
	c.calFrame(0, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.calFrame(i%400, (i*7)%calDayH)
	}
}
