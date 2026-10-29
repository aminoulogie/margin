package main

import (
	"strings"
	"testing"
	"time"
)

// The test plan from the user's Campus France application (one line a step).
const campusFrance = `2026-10-31 | Documents | Create / open Études en France account |
2026-11-01 | Documents | Scan every document |
2026-11-02 | Documents | Translations at the sworn translator | waiting:translator
2026-11-02 | Documents | Work certificate from the design office | waiting:employer
2026-11-03 | Documents | Shortlist 12-15 programmes |
2026-11-04 | TCF | Upload TCF convocation |
2026-11-09 | TCF | TCF exam | milestone
2026-11-12 | Platform | Enter education + experience |
2026-11-14 | Platform | Final programme choices |
2026-11-15 | Platform | CV + study project |
2026-11-16 | Platform | Explanatory letter for the 2025 gap |
2026-11-20 | Letters | Upload translations |
2026-11-23 | Letters | All motivation letters |
2026-11-26 | Submit | Full proofread |
2026-11-27 | Submit | Submit, pay, book appointment | milestone
           | After | Add TCF results |
2027-03-15 | After | Campus France interview (deadline) | milestone`

func TestParsePlan(t *testing.T) {
	ls := parsePlan(campusFrance)
	if len(ls) != 17 {
		t.Fatalf("%d steps, want 17", len(ls))
	}
	if l := ls[2]; l.due != "2026-11-02" || l.phase != "Documents" || l.waiting != "translator" || l.milestone {
		t.Errorf("waiting line: %+v", l)
	}
	if l := ls[6]; l.label != "TCF exam" || !l.milestone || l.phase != "TCF" {
		t.Errorf("milestone line: %+v", l)
	}
	if l := ls[15]; l.due != "" || l.phase != "After" || l.label != "Add TCF results" {
		t.Errorf("undated line: %+v", l)
	}
	if l := ls[0]; l.label != "Create / open Études en France account" || l.waiting != "" || l.milestone {
		t.Errorf("first line: %+v", l)
	}
	// The tablet's one-line field splits steps with " // ".
	if ls := parsePlan("2026-11-01 | A | One // | B | Two | milestone"); len(ls) != 2 || ls[1].label != "Two" || !ls[1].milestone || ls[1].due != "" {
		t.Errorf("one line: %+v", ls)
	}
}

// Every new field, and any field the tablet doesn't know, survives every edit made here.
func TestPlanFieldsSurviveEdits(t *testing.T) {
	workFixture(t)
	somaAdd("projects", "pf", obj{"id": "pf", "name": "Campus France", "kind": "plan", "due": "2026-11-30", "status": "active",
		"color": "#5b8cff", "createdAt": nowMs(), "someday": "kept", "steps": []any{
			obj{"id": "s1", "label": "TCF exam", "done": false, "due": "2026-11-09", "milestone": true, "phase": "TCF", "fromThePhone": 7.0},
			obj{"id": "s2", "label": "Translations", "done": false, "due": "2026-11-02", "phase": "Documents", "waitingOn": "translator"},
		}})
	somaStepTap("pf", "s1")
	somaStepTap("pf", "s1")
	wkRenameStep("pf", "s2", "Sworn translations")
	wkMoveStep("pf", "s2", -1)
	wkSetProject("pf", "name", "Campus France 2027")
	wkSetStepField("pf", "s1", "phase", "Exam")
	wkPastePlan("pf", "2026-11-27 | Submit | Submit | milestone")
	o := somaObj("projects#pf")
	if o["kind"] != "plan" || o["someday"] != "kept" || o["due"] != "2026-11-30" {
		t.Fatalf("project fields lost: %v", o)
	}
	steps := o["steps"].([]any)
	s2, s1 := steps[0].(obj), steps[1].(obj)
	if s1["milestone"] != true || s1["due"] != "2026-11-09" || s1["phase"] != "Exam" || s1["fromThePhone"] != 7.0 {
		t.Errorf("step fields lost: %v", s1)
	}
	if s2["waitingOn"] != "translator" || s2["phase"] != "Documents" || s2["label"] != "Sworn translations" {
		t.Errorf("step fields lost: %v", s2)
	}
	p, _ := wkProjectByID("pf")
	if !p.plan() || len(p.Steps) != 3 || !p.Steps[1].Milestone || p.Steps[0].WaitingOn != "translator" || p.Steps[2].Phase != "Submit" {
		t.Errorf("read back: %+v", p)
	}
}

func TestPlanDeadlinesAndPace(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, displayZone)
	p := wkProject{Kind: "plan", Status: "active", Due: "2026-11-30", Created: now.AddDate(0, 0, -20), Steps: []wkStep{
		{ID: "a", Label: "Translations", Due: "2026-11-02", WaitingOn: "translator"}, // waiting: not mine
		{ID: "b", Label: "TCF exam", Due: "2026-11-09", Milestone: true},             // a fixed date, not work
		{ID: "c", Label: "Enter education", Due: "2026-11-12"},
	}}
	if p.overdueSteps(now) != 0 || p.overdue(now) || wkPace(p, now) == "behind" && p.overdueSteps(now) > 0 {
		t.Errorf("waiting and milestone steps counted as overdue")
	}
	if p.effDue() != "2026-11-09" { // the earliest undone step's date, not the waiting one's
		t.Errorf("effective deadline %q", p.effDue())
	}
	p.Steps = append(p.Steps, wkStep{ID: "d", Label: "Scan", Due: "2026-11-01"})
	if p.overdueSteps(now) != 1 || !p.overdue(now) || wkPace(p, now) != "behind" {
		t.Errorf("an overdue step: %d overdue, overdue %v, pace %s", p.overdueSteps(now), p.overdue(now), wkPace(p, now))
	}
	if st, _ := statusOf(p, now); st != "Overdue" {
		t.Errorf("status %q", st)
	}
	if s, ok := p.nextDated(true); !ok || s.ID != "b" {
		t.Errorf("next milestone %+v", s)
	}
	if got := wkCountdown("TCF exam", "2026-11-21", now); got != "TCF exam in 11 days" {
		t.Errorf("countdown %q", got)
	}
	// A client project with no step dates reads as before.
	c := wkProject{Status: "active", Due: "2026-11-08", Steps: []wkStep{{ID: "x", Label: "Rev B"}}}
	if d, ok := c.daysLeft(now); !ok || d != -2 || !c.overdue(now) || c.effDue() != c.Due {
		t.Errorf("client project: %d %v", d, ok)
	}
	// Sorting by deadline goes by the effective one.
	list := []wkProject{c, p}
	list[0].Due = "2026-11-20"
	sortProjects(list, wkProjFilter{}, nil, now)
	if list[0].Kind != "plan" {
		t.Errorf("the plan (a step due 1 Nov) should sort first")
	}
}

// A plan's page, its sheets, the planner, the calendar and the overview: drawn, every button
// handled, a step ticked from the planner ticks the step.
func TestPlanPages(t *testing.T) {
	c := workAt(t, "projects")
	tapBtn(t, c, "wk:newplan")
	typeIn(t, c, "Campus France")
	pid := c.somaUI.workProj
	if p, ok := wkProjectByID(pid); !ok || !p.plan() || c.somaUI.work != "project" {
		t.Fatalf("the new plan didn't open: %+v", p)
	}
	today := time.Now().In(displayZone)
	d := func(n int) string { return somaDate(today.AddDate(0, 0, n)) }
	tapBtn(t, c, "wk:type-open:pplan:"+pid)
	typeIn(t, c, d(-2)+" | Documents | Scan every document // "+d(0)+" | Documents | Translations | waiting:translator // "+
		d(2)+" | TCF | TCF exam | milestone //  | After | Add TCF results")
	p, _ := wkProjectByID(pid)
	if len(p.Steps) != 4 || p.overdueSteps(today) != 1 {
		t.Fatalf("pasted: %+v", p.Steps)
	}
	shot(t, c, "plan-page")
	dead := func(where string) {
		for _, b := range c.page.buttons {
			if strings.HasPrefix(b.id, "wk:") && !wkHandled(b.id) {
				t.Errorf("dead button %s on %s", b.id, where)
			}
		}
	}
	dead("plan page")
	tapBtn(t, c, "wk:sheet-open:step:"+pid+":"+p.Steps[2].ID)
	dead("step sheet")
	tapBtn(t, c, "wk:smile:"+pid+":"+p.Steps[2].ID) // milestone off
	if s, _, _ := wkStepOf(pid, p.Steps[2].ID); s.Milestone {
		t.Errorf("the milestone switch didn't switch")
	}
	c.somaUI.sheet = ""
	c.showPage()
	tapBtn(t, c, "wk:sheet-open:proj:"+pid)
	dead("plan sheet")
	c.somaUI.sheet = ""
	for _, pg := range []string{"projects", "planner", "calendar", "overview"} {
		c.wkGo(pg, "")
		c.showPage()
		dead(pg)
	}
	// The overview's Due this week lists the plan's steps; the planner's today ticks one.
	c.wkGo("planner", "")
	c.showPage()
	tick := "wk:step:" + pid + ":" + p.Steps[1].ID
	tapBtn(t, c, tick)
	if s, _, _ := wkStepOf(pid, p.Steps[1].ID); !s.Done {
		t.Errorf("ticking the step on the planner didn't tick it")
	}
}
