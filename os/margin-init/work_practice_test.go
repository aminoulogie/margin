package main

import (
	"strings"
	"testing"
	"time"
)

// A daily practice runs from today until the day before its milestone, shows on the planner,
// the calendar and Habits, and a ticked day is logged as internal time on the project.
func TestDailyPractice(t *testing.T) {
	c := workAt(t, "projects")
	today := time.Now().In(displayZone)
	d := func(n int) string { return somaDate(today.AddDate(0, 0, n)) }
	pid := wkNewPlan("Campus France", d(30), d(5)+" | TCF | TCF exam | milestone")
	prid := wkAddPractice(pid, "TCF practice")
	p, _ := wkProjectByID(pid)
	if len(p.Practices) != 1 || p.Practices[0].Until != p.Steps[0].ID {
		t.Fatalf("practice %+v", p.Practices)
	}
	pr := p.Practices[0]
	if !p.practiceOn(pr, d(0)) || !p.practiceOn(pr, d(4)) || p.practiceOn(pr, d(5)) || p.practiceOn(pr, d(-1)) {
		t.Errorf("runs from today to the day before the exam")
	}
	todos := wkPracticeTodos([]wkProject{p}, d(0), d(9))
	if len(todos) != 5 || todos[0].SlotStart != 18*60 || todos[0].SlotMins != 30 || todoTickID(todos[0]) != "wk:ptick:"+pid+":"+prid+":"+d(0) {
		t.Errorf("planner blocks %+v", todos)
	}
	wkTickPractice(pid, prid, d(0))
	p, _ = wkProjectByID(pid)
	eid := p.Practices[0].Entries[d(0)]
	var logged *wkEntry
	for _, e := range wkEntries() {
		if e.ID == eid {
			logged = &e
		}
	}
	if !p.Practices[0].Done[d(0)] || logged == nil || logged.Billable || logged.dur(today) != 30*time.Minute {
		t.Fatalf("ticking didn't log 30 internal minutes: %+v", logged)
	}
	wkTickPractice(pid, prid, d(0))
	p, _ = wkProjectByID(pid)
	if p.Practices[0].Done[d(0)] || somaObj("timeEntries#"+eid) != nil {
		t.Errorf("unticking should take the time back out")
	}
	// Drawn everywhere, every button handled; the switch shows plans.
	for _, pg := range []string{"planner", "calendar", "habits", "project", "projects"} {
		c.somaUI.work, c.somaUI.workProj, c.somaUI.sheet = pg, pid, ""
		c.showPage()
		seen := false
		for _, b := range c.page.buttons {
			if strings.HasPrefix(b.id, "wk:") && !wkHandled(b.id) {
				t.Errorf("dead button %s on %s", b.id, pg)
			}
			seen = seen || strings.Contains(b.id, prid) || pg == "projects"
		}
		if !seen {
			t.Errorf("the practice isn't on %s", pg)
		}
	}
	shot(t, c, "practice-projects")
	tapBtn(t, c, "wk:pkindsw")
	if _, ok := findBtn(c, "wk:proj:"+pid); !ok {
		t.Errorf("the Plans side of the switch doesn't list the plan")
	}
	tapBtn(t, c, "wk:pkindsw")
	if _, ok := findBtn(c, "wk:proj:"+pid); ok {
		t.Errorf("the Work side lists the plan")
	}
	c.openSheet("practice:" + pid + ":" + prid)
	c.showPage()
	for _, b := range c.page.buttons {
		if strings.HasPrefix(b.id, "wk:") && !wkHandled(b.id) {
			t.Errorf("dead button %s on the practice sheet", b.id)
		}
	}
	tapBtn(t, c, "wk:pracset:"+pid+":"+prid+":start:15")
	if p, _ := wkProjectByID(pid); p.Practices[0].Start != 18*60+15 {
		t.Errorf("time stepper: %d", p.Practices[0].Start)
	}
}
