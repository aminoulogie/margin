package main

import (
	"fmt"
	"image"
	"strconv"
	"strings"
	"time"
)

// Time's extras: a weekly hours goal per project (project.weekGoalH, tablet only), and a timer
// left running past midnight, stopped at midnight with a question in the morning.

// weekGoalCard is a project's hours this week against its goal (nothing when it has none).
func (k *wk) weekGoalCard(p wkProject, y int, now time.Time) int {
	if p.WeekGoal <= 0 {
		return y
	}
	mon := weekStart(now)
	h := wkHours(wkEntries(), mon, mon.AddDate(0, 0, 7), now)[p.ID].Hours()
	cr := image.Rect(wkX0, y+24, wkX1, y+24+150)
	k.card(cr)
	k.cardHead(wkX0+28, y+24+50, "target", "This week", fmt.Sprintf("· goal %g h", p.WeekGoal))
	col := wkColour(p, p.order)
	if h >= p.WeekGoal {
		col = wkGreen
	}
	k.hBar(k.R(wkX0+28, y+24+92, wkX1-220, y+24+104), min(h/p.WeekGoal, 1), col)
	left := fmt.Sprintf("%.1f h to go", p.WeekGoal-h)
	if h >= p.WeekGoal {
		left = "done for the week"
	}
	k.textR(wkFont("bold", 24), wkX1-28, y+24+104, wkFG, fmt.Sprintf("%.1f of %g h", h, p.WeekGoal))
	k.text(wkFont("regular", 18), wkX0+28, y+24+134, wkFaint, left)
	k.btn("wk:sheet-open:proj:"+p.ID, cr.Sub(image.Pt(0, k.oy)))
	return cr.Max.Y
}

// weekGoals is the Overview's list of projects with an hours goal this week.
func (k *wk) weekGoals(projects []wkProject, y int, now time.Time) int {
	var list []wkProject
	for _, p := range projects {
		if p.WeekGoal > 0 && p.Status == "active" {
			list = append(list, p)
		}
	}
	if len(list) == 0 {
		return y
	}
	mon := weekStart(now)
	hours := wkHours(wkEntries(), mon, mon.AddDate(0, 0, 7), now)
	cr := image.Rect(wkX0, y, wkX1, y+92+84*len(list))
	k.card(cr)
	k.cardHead(wkX0+28, y+53, "target", "Weekly goals", "· hours this week")
	ry := y + 92
	for _, p := range list {
		h := hours[p.ID].Hours()
		col := wkColour(p, p.order)
		if h >= p.WeekGoal {
			col = wkGreen
		}
		k.text(wkFont("semi", 21), 190, ry+38, wkFG, clip(wkFont("semi", 21), p.Name, 420))
		k.hBar(k.R(640, ry+30, 940, ry+40), min(h/p.WeekGoal, 1), col)
		k.textR(wkFont("semi", 20), 1100, ry+42, wkText2, fmt.Sprintf("%.1f / %g h", h, p.WeekGoal))
		k.btn("wk:proj:"+p.ID, k.R(wkX0, ry, wkX1, ry+84))
		ry += 84
	}
	return cr.Max.Y + 30
}

// wkOvernight is a timer stopped at midnight, whose sheet opens the next time Soma shows.
var wkOvernight string

// wkOvernightCheck stops a timer still running from an earlier day at that day's midnight.
// Caller holds drawMu.
func (c *console) wkOvernightCheck() {
	run, ok := wkRunning(wkEntries())
	if !ok || !dayStart(run.Start).Before(dayStart(time.Now())) {
		return
	}
	end := dayStart(run.Start).AddDate(0, 0, 1).Add(-time.Minute)
	somaPatch("timeEntries", run.ID, func(o obj) { o["end"] = float64(end.UnixMilli()) })
	wkOvernight = run.ID
	if c.mode == modeSoma && c.screenOn && !c.somaUI.typing {
		c.showPage()
	}
}

func init() {
	wkSheets["overnight"] = func(k *wk, id string) *wkSheet {
		o := somaObj("timeEntries#" + id)
		if o == nil {
			return nil
		}
		st := msTime(o["start"]).In(displayZone)
		return &wkSheet{title: "Your timer ran past midnight", sub: "Stopped at midnight on " + st.Format("Mon 2 Jan") + ". When did you really stop?",
			rows: []wkRow{
				{id: "wk:type-open:etime:" + id + "|end", label: "Type when I stopped", icon: "clock", detail: "started " + st.Format("15:04")},
				{id: "wk:sheet:close", label: "Midnight is right", icon: "check"},
			}}
	}
	wkActions["pgoal"] = func(c *console, arg string) { // <id>:<delta hours>
		id, d, _ := strings.Cut(arg, ":")
		n, _ := strconv.Atoi(d)
		p, _ := wkProjectByID(id)
		g := max(p.WeekGoal+float64(n), 0)
		if g == 0 {
			wkSetProject(id, "weekGoalH", nil)
		} else {
			wkSetProject(id, "weekGoalH", g)
		}
		c.somaUI.keep = true
	}
}
