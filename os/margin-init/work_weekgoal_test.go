package main

import (
	"strings"
	"testing"
	"time"
)

func TestWeekGoalAndOvernightTimer(t *testing.T) {
	c := workAt(t, "projects")
	c.openSheet("proj:p1")
	c.showPage()
	tapBtn(t, c, "wk:pgoal:p1:1")
	tapBtn(t, c, "wk:pgoal:p1:1")
	if p, _ := wkProjectByID("p1"); p.WeekGoal != 2 {
		t.Fatalf("goal %v", p.WeekGoal)
	}
	c.somaUI.sheet = ""
	for _, pg := range []string{"overview", "project"} {
		c.somaUI.work, c.somaUI.workProj = pg, "p1"
		c.showPage()
		found := false
		for _, b := range c.page.buttons {
			found = found || pg == "overview" && b.id == "wk:proj:p1" || pg == "project" && b.id == "wk:sheet-open:proj:p1"
			if strings.HasPrefix(b.id, "wk:") && !wkHandled(b.id) {
				t.Errorf("dead %s on %s", b.id, pg)
			}
		}
		if !found {
			t.Errorf("no goal on %s", pg)
		}
	}
	// A timer from yesterday: stopped at midnight, and asked about.
	y := dayStart(time.Now()).Add(-3 * time.Hour)
	somaPatch("timeEntries", "run", func(o obj) { o["start"] = float64(y.UnixMilli()) })
	c.wkOvernightCheck()
	if _, ok := wkRunning(wkEntries()); ok {
		t.Fatal("the overnight timer still runs")
	}
	if c.somaUI.sheet != "overnight:run" {
		t.Errorf("sheet %q", c.somaUI.sheet)
	}
	tapBtn(t, c, "wk:type-open:etime:run|end")
	typeIn(t, c, y.Add(90*time.Minute).In(displayZone).Format("15:04"))
	if e := msTime(somaObj("timeEntries#run")["end"]); e.Sub(y) != 90*time.Minute {
		t.Errorf("typed end: %v", e.Sub(y))
	}
}
