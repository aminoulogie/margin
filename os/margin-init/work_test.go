package main

import (
	"fmt"
	"testing"
	"time"
)

// workFixture is a freelancer's month like the one in the Work tab's design: five projects,
// a week of time, a timer running, a finding, money in and out.
func workFixture(t *testing.T) *console {
	c, _, _ := somaTestLink(t)
	now := time.Now()
	ms := func(t time.Time) float64 { return float64(t.UnixMilli()) }
	day := func(d int) string { return now.AddDate(0, 0, d).Format("2006-01-02") }
	steps := func(n, done int, labels ...string) []any {
		var out []any
		for i := range n {
			l := fmt.Sprintf("Step %d", i+1)
			if i < len(labels) {
				l = labels[i]
			}
			out = append(out, obj{"id": fmt.Sprintf("s%d", i), "label": l, "done": i < done, "at": ms(now.AddDate(0, 0, -20+i*4))})
		}
		return out
	}
	somaPut("settings.currency", "GBP")
	projects := []obj{
		{"id": "p1", "name": "Block C rebar detailing", "client": "Northbank Ltd", "color": "#5b8cff", "status": "active", "due": day(8), "rate": 35, "priority": "high",
			"createdAt": ms(now.AddDate(0, 0, -23)), "touchedAt": ms(now), "note": "Revit structural detailing",
			"steps": steps(7, 4, "Model columns and walls, level 1 to 3", "Bar schedule for slab S1", "Detailed grid lines 1 to 3, level 2", "Detailed grid lines 4 to 7, level 2", "Shear links around openings", "Client review, rev C", "Issue final sheets and BBS")},
		{"id": "p2", "name": "Revit rebar add-in", "client": "Northbank Ltd", "color": "#a98bfa", "status": "active", "due": day(-2), "rate": 35, "createdAt": ms(now.AddDate(0, 0, -40)), "touchedAt": ms(now), "steps": steps(11, 9, "Export to IFC")},
		{"id": "p3", "name": "Slab schedules, phase 2", "client": "Harlow Structures", "color": "#3ecf8e", "status": "active", "due": day(2), "rate": 35, "createdAt": ms(now.AddDate(0, 0, -30)), "touchedAt": ms(now), "steps": steps(10, 5, "Issue rev B")},
		{"id": "p4", "name": "Isolated footing tool", "client": "Own product", "color": "#e3f25a", "status": "active", "due": day(33), "createdAt": ms(now.AddDate(0, 0, -60)), "touchedAt": ms(now), "steps": steps(13, 4)},
		{"id": "p5", "name": "Margin OS", "client": "Personal", "color": "#f0a63c", "status": "active", "createdAt": ms(now.AddDate(0, 0, -90)), "touchedAt": ms(now.AddDate(0, 0, -3)), "steps": steps(20, 11)},
	}
	var order []string
	for _, p := range projects {
		somaPut("projects#"+p["id"].(string), p)
		order = append(order, p["id"].(string))
	}
	somaPut("projects@order", order)
	// The week so far: mornings and afternoons on the projects.
	mon := weekStart(now)
	n := 0
	var eorder []string
	for d := mon; !d.After(dayStart(now)); d = d.AddDate(0, 0, 1) {
		for i, pid := range []string{"p1", "p3", "p2", "p4"} {
			start := d.Add(time.Duration(8+i*2) * time.Hour)
			end := start.Add(time.Duration(60+i*15) * time.Minute)
			if end.After(now) {
				continue
			}
			id := fmt.Sprintf("e%d", n)
			n++
			somaPut("timeEntries#"+id, obj{"id": id, "projectId": pid, "start": ms(start), "end": ms(end), "note": "Grid lines 4 to 7, level 2", "billable": pid != "p4"})
			eorder = append(eorder, id)
		}
	}
	somaPut("timeEntries#run", obj{"id": "run", "projectId": "p1", "start": ms(now.Add(-102 * time.Minute)), "note": "Grid lines 4 to 7, level 2"})
	somaPut("timeEntries@order", append(eorder, "run"))
	somaPut("findings#f1", obj{"id": "f1", "projectId": "p1", "title": "Lap length wrong on B12 bars", "severity": "high", "status": "open", "area": "Sheet S-104", "createdAt": ms(now)})
	somaPut("findings@order", []string{"f1"})
	somaPut("todos#t1", obj{"id": "t1", "text": "Reply to Northbank on lap lengths", "date": day(0), "done": true})
	somaPut("todos#t2", obj{"id": "t2", "text": "Fix lap length on B12 bars", "date": day(0), "done": false, "priority": "high"})
	somaPut("todos#t3", obj{"id": "t3", "text": "Send October invoice", "date": day(0), "done": false, "priority": "high"})
	somaPut("todos#t4", obj{"id": "t4", "text": "Call Northbank", "date": day(1), "done": false, "slot": obj{"date": day(1), "start": 540, "mins": 60}})
	somaPut("todos@order", []string{"t1", "t2", "t3", "t4"})
	for i, e := range []obj{
		{"kind": "income", "amount": 1347, "category": "Northbank Ltd"}, {"kind": "income", "amount": 1120, "category": "Harlow Structures"},
		{"kind": "spend", "amount": 420, "category": "Software & tools"}, {"kind": "spend", "amount": 380, "category": "Rent share"},
		{"kind": "spend", "amount": 290, "category": "Food"}, {"kind": "spend", "amount": 160, "category": "Transport"},
	} {
		id := fmt.Sprintf("l%d", i)
		e["id"], e["date"] = id, day(-i)
		somaPut("ledger#"+id, e)
	}
	// A month of the body: food, sleep, weight, reading and sessions every other day.
	for i := 0; i < 28; i++ {
		d := day(-i)
		somaPut("nutrition/"+d, obj{"items": []any{obj{"name": "Chicken wrap", "cals": 1800 + float64(i%5)*90, "p": 150 + float64(i%4)*8, "meal": "Lunch", "eatenAt": ms(now.AddDate(0, 0, -i))}},
			"water": 2400, "sleep": obj{"hours": 6.5 + float64(i%3)*0.5}, "bodyWeight": 79.6 - float64(i)*0.03})
		somaPut("reading/"+d, 20+i%3*10)
		if i%2 == 0 {
			somaPut("history/"+d, obj{"split": "Push A", "durationFormatted": "54:12", "totalSets": 16, "totalVol": 8000 + i*90, "timestamp": ms(now.AddDate(0, 0, -i)),
				"exercises": []any{obj{"name": "Incline pressing", "sets": []any{obj{"kg": 90 + float64(i%3)*5, "reps": 8}}}, obj{"name": "Rows", "sets": []any{obj{"kg": 70, "reps": 10}}}}})
		}
	}
	somaPut("shifts#sh1", obj{"id": "sh1", "client": "Northbank Ltd", "start": ms(dayStart(now).Add(8 * time.Hour)), "end": ms(dayStart(now).Add(12 * time.Hour)), "breaks": []any{}})
	somaPut("shifts#sh2", obj{"id": "sh2", "client": "Northbank Ltd", "start": ms(dayStart(now).Add(13 * time.Hour)), "end": ms(dayStart(now).Add(16 * time.Hour)), "breaks": []any{}})
	somaPut("shifts@order", []string{"sh1", "sh2"})
	somaPut("settings.clientRates", obj{"Northbank Ltd": 35})
	return c
}

func TestWorkPages(t *testing.T) {
	c := workFixture(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	c.setMode(modeSoma)
	c.somaUI.view = "work"
	for _, pg := range []string{"overview", "projects", "project", "time", "planner", "reports", "money", "worklog", "findings", "health", "training", "nutrition", "mind", "stats", "settings"} {
		c.somaUI.work, c.somaUI.workProj = pg, "p1"
		c.showPage()
		shot(t, c, "work-"+pg)
	}
	if !c.somaTap("wk:go:projects") || c.somaUI.work != "projects" {
		t.Fatalf("the rail didn't open Projects: %q", c.somaUI.work)
	}
}
