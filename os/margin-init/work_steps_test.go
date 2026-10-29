package main

import (
	"strings"
	"testing"
	"time"
)

// A step that comes after another is blocked (never overdue) until that one is ticked; a
// step's checklist ticks the step when all of it is done; milestones count down a week ahead.
func TestStepAfterChecklistCountdown(t *testing.T) {
	c := workAt(t, "projects")
	now := time.Now().In(displayZone)
	d := func(n int) string { return somaDate(now.AddDate(0, 0, n)) }
	pid := wkNewPlan("Campus France", d(30), d(-3)+" | Documents | Translations | // "+d(-1)+" | Letters | Upload translations // "+d(4)+" | TCF | TCF exam | milestone")
	p, _ := wkProjectByID(pid)
	a, b := p.Steps[0].ID, p.Steps[1].ID
	wkSetStepField(pid, b, "after", a)
	p, _ = wkProjectByID(pid)
	if !p.Steps[1].blocked || p.overdueSteps(now) != 1 {
		t.Fatalf("blocked %v, overdue %d (only the first is mine)", p.Steps[1].blocked, p.overdueSteps(now))
	}
	if st, _ := stepState(p, p.Steps[1], now); st != "After: Translations" {
		t.Errorf("state %q", st)
	}
	// The checklist: adding, ticking all ticks the step, unticking one unticks it.
	c.somaUI.work, c.somaUI.workProj = "project", pid
	c.showPage()
	tapBtn(t, c, "wk:sheet-open:step:"+pid+":"+a)
	for _, txt := range []string{"Diploma", "Transcript"} {
		tapBtn(t, c, "wk:type-open:sitemadd:"+pid+":"+a)
		typeIn(t, c, txt)
	}
	s, _, _ := wkStepOf(pid, a)
	if len(s.Items) != 2 {
		t.Fatalf("items %+v", s.Items)
	}
	for _, it := range s.Items {
		wkItemsTick(pid, a, it.ID)
	}
	if s, _, _ = wkStepOf(pid, a); !s.Done {
		t.Errorf("all ticked: the step should be done")
	}
	if p, _ = wkProjectByID(pid); p.Steps[1].blocked {
		t.Errorf("the step after it should be free now")
	}
	wkItemsTick(pid, a, s.Items[0].ID)
	if s, _, _ = wkStepOf(pid, a); s.Done {
		t.Errorf("one unticked: the step should be undone")
	}
	// The countdown: from 7 days before.
	if l, n, _, ok := wkMilestoneSoon(wkProjects(), d(0)); !ok || n != 4 || l != "TCF exam" {
		t.Errorf("countdown %q %d %v", l, n, ok)
	}
	if _, _, _, ok := wkMilestoneSoon(wkProjects(), d(-4)); ok {
		t.Errorf("8 days before: no countdown yet")
	}
	for _, sheet := range []string{"step:" + pid + ":" + a, "safter:" + pid + ":" + b} {
		c.somaUI.sheet = ""
		c.openSheet(sheet)
		c.showPage()
		for _, bt := range c.page.buttons {
			if strings.HasPrefix(bt.id, "wk:") && !wkHandled(bt.id) {
				t.Errorf("dead button %s on %s", bt.id, sheet)
			}
		}
	}
	c.somaUI.sheet = ""
	if c.somaUI.workSel == nil {
		c.somaUI.workSel = map[string]string{}
	}
	for _, pg := range []string{"planner", "calendar"} {
		c.somaUI.work = pg
		c.somaUI.workSel[pg+"wk:tab"] = "2"
		c.showPage()
	}
}
