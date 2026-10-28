package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	"golang.org/x/image/font"

	"margin-init/ui"
)

// The Work tab's Projects, a project, Time and the Planner (ProjectsPage.tsx, TimePage.tsx,
// TasksPage.tsx), in the tablet screens' layout.

// hBar is a rounded progress bar in r (page coordinates).
func (k *wk) hBar(r image.Rectangle, frac float64, col color.RGBA) {
	ui.RoundRect(k.img, r, r.Dy()/2, rgb(0x26272f))
	if frac > 0 {
		w := max(int(float64(r.Dx())*min(frac, 1)), r.Dy())
		ui.RoundRect(k.img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+w, r.Max.Y), r.Dy()/2, col)
	}
}

// weeklyHours is the hours on a project (all of them for "") in each of the last n weeks.
func weeklyHours(entries []wkEntry, projectID string, n int, now time.Time) []float64 {
	mon := weekStart(now)
	out := make([]float64, n)
	for i := range n {
		from := mon.AddDate(0, 0, -7*(n-1-i))
		h := wkHours(entries, from, from.AddDate(0, 0, 7), now)
		if projectID == "" {
			out[i] = wkSum(h).Hours()
		} else {
			out[i] = h[projectID].Hours()
		}
	}
	return out
}

func projectHours(entries []wkEntry, id string, now time.Time) time.Duration {
	var t time.Duration
	for _, e := range entries {
		if e.ProjectID == id {
			t += e.dur(now)
		}
	}
	return t
}

// statusOf is a project's badge: its state, or late, or drifting.
func statusOf(p wkProject, now time.Time) (string, color.RGBA) {
	switch p.Status {
	case "paused":
		return "Paused", wkMuted
	case "done":
		return "Done", wkBlue
	case "archived":
		return "Archived", wkMuted
	}
	if d, ok := p.daysLeft(now); ok && d < 0 {
		return "Overdue", wkRed
	}
	if p.stale(now) || (p.Due == "" && !p.Touched.IsZero() && now.Sub(p.Touched) > 3*24*time.Hour) {
		return "Drifting", wkAmber
	}
	return "Active", wkGreen
}

// boxBtn is a full-width action button in a hero card: outlined, or filled when fill is set.
func (k *wk) boxBtn(id string, x0, y0, x1, y1 int, icon, label string, fill, fg color.RGBA) {
	r := k.R(x0, y0, x1, y1)
	if fill.A == 0 {
		ui.RoundRect(k.img, r, 14, rgb(0x2a2c35))
		ui.RoundRect(k.img, r.Inset(2), 12, rgb(0x15161c))
	} else {
		ui.RoundRect(k.img, r, 14, fill)
	}
	f := wkFont("semi", 22)
	w := 24
	if label != "" {
		w = ui.TextWidth(f, label) + 36
	}
	sx := (r.Min.X+r.Max.X)/2 - w/2
	lucideIcon(k.img, icon, sx+12, (r.Min.Y+r.Max.Y)/2, 24, fg)
	if label != "" {
		apText(k.img, f, sx+36, (r.Min.Y+r.Max.Y)/2+8, fg, label)
	}
	k.btn(id, r)
}

// checkbox is a step's or a to-do's box at x, centred on cy.
func (k *wk) checkbox(x, cy int, done bool) {
	cb := k.R(x, cy-17, x+34, cy+17)
	if done {
		ui.RoundRect(k.img, cb, 8, wkLime)
		lucideIcon(k.img, "check", x+17, k.Y(cy), 22, wkInk)
	} else {
		ui.RoundRect(k.img, cb, 8, rgb(0x3a3c46))
		ui.RoundRect(k.img, cb.Inset(2), 6, wkCard)
	}
}

// struck is a row's label, crossed out and faint when done; returns its right edge.
func (k *wk) struck(x, y int, s string, done bool, width int) int {
	f, lc := wkFont("semi", 22), wkFG
	if done {
		f, lc = wkFont("regular", 22), wkFaint
	}
	e := k.text(f, x, y, lc, clip(f, s, width))
	if done {
		ui.Fill(k.img, k.R(x, y-8, e, y-6), wkFaint)
	}
	return e
}

func (k *wk) projects() int {
	now := time.Now().In(displayZone)
	all := wkProjects()
	entries := wkEntries()
	clients := map[string]bool{}
	for _, p := range all {
		if p.Client != "" {
			clients[p.Client] = true
		}
	}
	q := time.Date(now.Year(), time.Month((int(now.Month())-1)/3*3+1), 1, 0, 0, 0, 0, displayZone)
	qh := wkSum(wkHours(entries, q, now.AddDate(0, 0, 1), now))
	k.header([]string{wkUser() + " · Freelance", "Projects"}, "Projects",
		fmt.Sprintf("%d projects · %d clients · %.0f h this quarter", len(all), len(clients), qh.Hours()), color.RGBA{})
	x := k.primary("wk:newproject", "plus", "New project", wkX1)
	k.square("wk:sort", "arrow-up-down", x-13)
	k.tabs("wk:tab", []string{"All projects", "Board", "Timeline", "Archive"}, nil, k.sel("tab", 0))

	n := map[string]int{}
	for _, p := range all {
		st, _ := statusOf(p, now)
		n[p.Status]++
		if st == "Overdue" {
			n["overdue"]++
		}
	}
	chip := k.sel("chip", 0)
	k.chips("wk:chip", 308, []string{fmt.Sprintf("All %d", len(all)), fmt.Sprintf("Active %d", n["active"]),
		fmt.Sprintf("Overdue %d", n["overdue"]), fmt.Sprintf("Paused %d", n["paused"]), fmt.Sprintf("Done %d", n["done"])}, nil, chip)
	k.outline("wk:clients", "building-2", "All clients    ", wkX1, 308, 53)
	k.icon("chevron-down", wkX1-32, 334, 22, wkText2)

	// The figures.
	drifting, prog, nAct := 0, 0.0, 0
	overdueName := ""
	for _, p := range all {
		if p.Status != "active" {
			continue
		}
		nAct++
		prog += p.progress()
		if st, _ := statusOf(p, now); st == "Drifting" {
			drifting++
		} else if st == "Overdue" && overdueName == "" {
			overdueName = p.Name
		}
	}
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
	mh := wkHours(entries, month, month.AddDate(0, 1, 0), now)
	cw := (wkX1 - wkX0 - 60) / 4
	box := func(i int) image.Rectangle { return image.Rect(wkX0+i*(cw+20), 386, wkX0+i*(cw+20)+cw, 549) }
	small := func(r image.Rectangle, icon, label, value, unit, foot string) {
		k.card(r)
		k.icon(icon, r.Min.X+34, r.Min.Y+34, 24, wkMuted)
		k.text(wkFont("medium", 20), r.Min.X+56, r.Min.Y+42, wkText2, label)
		e := k.text(wkFont("bold", 40), r.Min.X+24, r.Min.Y+96, wkFG, value)
		if unit != "" {
			k.text(wkFont("medium", 22), e+3, r.Min.Y+96, wkMuted, unit)
		}
		k.text(wkFont("regular", 19), r.Min.X+24, r.Min.Y+136, wkFaint, clip(wkFont("regular", 19), foot, r.Dx()-48))
	}
	small(box(0), "folder-open", "Active", fmt.Sprint(nAct), "", fmt.Sprintf("%d drifting", drifting))
	late := "none late"
	if overdueName != "" {
		late = overdueName
	}
	small(box(1), "alarm-clock", "Overdue", fmt.Sprint(n["overdue"]), "", late)
	small(box(2), "clock", "This month", fmt.Sprintf("%.0f", wkSum(mh).Hours()), "h", fmt.Sprintf("across %d", len(mh)))
	avg := 0.0
	if nAct > 0 {
		avg = prog / float64(nAct) * 100
	}
	small(box(3), "chart-pie", "Avg progress", fmt.Sprintf("%.0f%%", avg), "", "active projects")

	// The projects.
	run, running := wkRunning(entries)
	y := 575
	shown := 0
	for _, p := range all {
		st, scol := statusOf(p, now)
		switch {
		case chip == 1 && p.Status != "active", chip == 2 && st != "Overdue", chip == 3 && p.Status != "paused", chip == 4 && p.Status != "done":
			continue
		}
		col := wkColour(p, p.order)
		cr := image.Rect(wkX0, y, wkX1, y+153)
		k.card(cr)
		if running && run.ProjectID == p.ID { // the one the timer runs on: a lime rim
			r := cr.Sub(image.Pt(0, k.oy))
			ui.RoundRect(k.img, r, 20, blend(wkCard, wkLime, 0.5))
			ui.RoundRect(k.img, r.Inset(2), 18, rgb(0x17181d))
		}
		ui.RoundRect(k.img, k.R(187, y+20, 193, y+133), 3, col)
		f := wkFont("bold", 25)
		name := clip(f, p.Name, 330)
		e := k.text(f, 217, y+48, wkFG, name)
		k.badge(e+14+ui.TextWidth(wkFont("semi", 18), st)+28, y+40, st, scol)
		sub := fmt.Sprintf("%d of %d steps", p.done(), len(p.Steps))
		if p.Client != "" {
			sub = p.Client + " · " + sub
		}
		k.text(wkFont("regular", 20), 217, y+83, wkFaint, sub)
		k.hBar(k.R(217, y+113, 490, y+121), p.progress(), col)
		k.text(wkFont("bold", 21), 505, y+122, wkFG, fmt.Sprintf("%.0f%%", p.progress()*100))
		k.textR(wkFont("regular", 18), 916, y+55, wkFaint, "Due")
		if d, ok := p.daysLeft(now); ok {
			due, _ := time.Parse("2006-01-02", p.Due)
			dc, sc, note := wkFG, wkFaint, fmt.Sprintf("%d days", d)
			switch {
			case d < 0:
				dc, sc, note = wkRed, wkRed, fmt.Sprintf("%d days late", -d)
			case d <= 3:
				dc, sc = wkAmber, wkAmber
			}
			k.textR(wkFont("semi", 22), 916, y+84, dc, due.Format("Mon 2 Jan"))
			k.textR(wkFont("regular", 19), 916, y+113, sc, note)
		} else {
			k.textR(wkFont("semi", 22), 916, y+92, wkFG, "No date")
		}
		k.spark(k.R(958, y+30, 1086, y+66), weeklyHours(entries, p.ID, 8, now), col)
		h := projectHours(entries, p.ID, now)
		k.textR(wkFont("regular", 20), 1086, y+108, wkFaint, "h")
		k.textR(wkFont("bold", 22), 1066, y+108, wkFG, fmt.Sprintf("%.1f", h.Hours()))
		k.icon("chevron-right", 1122, y+72, 22, wkMuted)
		k.btn("wk:proj:"+p.ID, k.R(wkX0, y, wkX1, y+153))
		y += 173
		shown++
	}
	if shown == 0 {
		k.text(wkFont("regular", 21), wkX0+20, y+40, wkMuted, "No projects here. Add them in Soma on the PC or the phone.")
		y += 100
	}

	// Where the month went, and the hours week by week.
	y += 2
	split := image.Rect(wkX0, y, 652, y+330)
	k.card(split)
	k.cardHead(wkX0+28, y+43, "chart-pie", "Time split", "· "+now.Format("January"))
	type part struct {
		name string
		h    float64
		col  color.RGBA
	}
	var parts []part
	tot := 0.0
	for _, p := range all {
		if h := mh[p.ID].Hours(); h > 0 {
			parts = append(parts, part{shortName(p.Name), h, wkColour(p, p.order)})
			tot += h
		}
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].h > parts[j].h })
	var vals []float64
	var cols []color.RGBA
	for _, pt := range parts {
		vals = append(vals, pt.h)
		cols = append(cols, pt.col)
	}
	k.donut(290, y+175, 90, 26, vals, cols)
	k.textC(wkFont("bold", 30), 290, y+183, wkFG, fmt.Sprintf("%.0f h", tot))
	k.textC(wkFont("regular", 18), 290, y+207, wkMuted, now.Format("January"))
	ly := y + 92
	for i, pt := range parts {
		if i == 5 {
			break
		}
		ui.RoundRect(k.img, k.R(388, ly-14, 404, ly+2), 4, pt.col)
		k.text(wkFont("regular", 20), 415, ly, wkText2, clip(wkFont("regular", 20), pt.name, 150))
		k.textR(wkFont("semi", 20), 623, ly, wkFG, fmt.Sprintf("%.0f%%", pt.h/tot*100))
		ly += 42
	}
	hw := image.Rect(672, y, wkX1, y+330)
	k.card(hw)
	k.cardHead(700, y+43, "trending-up", "Hours per week", "")
	_, weekT := wkTargets()
	k.lineChart(image.Rect(750, y+95, 1122, y+249), weeklyHours(entries, "", 8, now), weekT/2, 707, func(i int) string {
		_, w := now.AddDate(0, 0, -7*(7-i)).ISOWeek()
		return fmt.Sprintf("W%d", w)
	})
	return y + 330
}

// lineChart is an area chart of vals in r (screen-y coordinates), a dashed line at target,
// the last point marked with its value. The axis labels start at labelX.
func (k *wk) lineChart(r image.Rectangle, vals []float64, target float64, labelX int, xlabel func(int) string) {
	hi := max(target, 10.0)
	for _, v := range vals {
		hi = max(hi, v)
	}
	hi = math.Ceil(hi/10) * 10
	for i := 0; i <= 4; i++ {
		y := r.Max.Y - i*r.Dy()/4
		k.text(wkFont("regular", 17), labelX, y+6, wkFaint, fmt.Sprintf("%.0fh", hi*float64(i)/4))
	}
	ui.Fill(k.img, k.R(r.Min.X, r.Max.Y, r.Max.X, r.Max.Y+2), wkLine)
	n := len(vals)
	if n < 2 {
		return
	}
	base := k.Y(r.Max.Y)
	pt := func(i int, v float64) (int, int) {
		return r.Min.X + i*r.Dx()/(n-1), base - int(v/hi*float64(r.Dy()))
	}
	for i := 1; i < n; i++ { // the area, lime fading down
		x0, y0 := pt(i-1, vals[i-1])
		x1, y1 := pt(i, vals[i])
		for x := x0; x < x1; x++ {
			yt := y0 + (y1-y0)*(x-x0)/max(x1-x0, 1)
			for y := yt; y < base; y++ {
				glass(k.img, image.Rect(x, y, x+1, y+1), 0, wkLime, 0.16*(1-float64(y-yt)/float64(max(base-yt, 1))))
			}
		}
	}
	if target > 0 {
		ty := base - int(target/hi*float64(r.Dy()))
		for x := r.Min.X; x < r.Max.X; x += 18 {
			ui.Fill(k.img, image.Rect(x, ty-1, min(x+10, r.Max.X), ty+2), wkBlue)
		}
	}
	for i := 1; i < n; i++ {
		x0, y0 := pt(i-1, vals[i-1])
		x1, y1 := pt(i, vals[i])
		line(k.img, x0, y0, x1, y1, 3, wkLime)
	}
	lx, ly := pt(n-1, vals[n-1])
	ui.Fill(k.img, image.Rect(lx-1, ly, lx+1, base), rgb(0x3a3b44))
	ui.Circle(k.img, lx, ly, 8, wkLime)
	ui.Circle(k.img, lx, ly, 4, rgb(0x2a2b20))
	k.bubble(lx, ly-34, fmt.Sprintf("%.0f h", vals[n-1]), r.Max.X+30)
	k.text(wkFont("regular", 17), r.Min.X, r.Max.Y+28, wkFaint, xlabel(0))
	k.textR(wkFont("regular", 17), r.Max.X, r.Max.Y+28, wkFaint, xlabel(n-1))
}

// bubble is a white label centred on (cx, cy) in page coordinates, kept left of maxX.
func (k *wk) bubble(cx, cy int, lbl string, maxX int) {
	f := wkFont("semi", 17)
	bw := ui.TextWidth(f, lbl) + 30
	bx := min(cx-bw/2, maxX-bw)
	b := image.Rect(bx, cy-17, bx+bw, cy+17)
	ui.RoundRect(k.img, b, 9, rgb(0xf5f6f8))
	apTextCenter(k.img, f, (b.Min.X+b.Max.X)/2, cy, wkInk, lbl)
}

// --- a project ----------------------------------------------------------------------------------

func (k *wk) project() int {
	now := time.Now().In(displayZone)
	p, ok := wkProjectByID(k.c.somaUI.workProj)
	if !ok {
		k.c.somaUI.work = "projects"
		return k.projects()
	}
	entries := wkEntries()
	col := wkColour(p, p.order)
	sub := p.Client
	if p.Note != "" {
		sub = strings.TrimPrefix(sub+" · "+p.Note, " · ")
	}
	k.header([]string{wkUser() + " · Freelance", "Projects ›"}, clip(wkFont("bold", 42), p.Name, 620), sub, col)
	x := k.square("wk:share", "share-2", wkX1)
	k.square("wk:back", "chevron-left", x-13)
	var pf []wkFinding
	for _, f := range wkFindings() {
		if f.ProjectID == p.ID && !f.closed() {
			pf = append(pf, f)
		}
	}
	k.tabs("wk:tab", []string{"Overview", "Steps", "Time", "Notes", "Files", "Findings"},
		[]string{"", fmt.Sprint(len(p.Steps)), "", "", "", fmt.Sprint(len(pf))}, k.sel("tab", 0))

	// The summary.
	hero := image.Rect(wkX0, 312, wkX1, 648)
	k.cardIn(hero, blend(wkCard, col, 0.08))
	k.arc(285, 435, 84, 18, p.progress(), col, rgb(0x262833))
	k.textC(wkFont("bold", 38), 285, 445, wkFG, fmt.Sprintf("%.0f%%", p.progress()*100))
	k.textC(wkFont("regular", 17), 285, 470, wkMuted, fmt.Sprintf("%d of %d steps", p.done(), len(p.Steps)))
	st, scol := statusOf(p, now)
	bx := 408
	pillL := func(text string, c color.RGBA) {
		f := wkFont("semi", 18)
		w := ui.TextWidth(f, text) + 30
		r := k.R(bx, 350, bx+w, 386)
		ui.RoundRect(k.img, r, 18, blend(wkCard, c, 0.18))
		apText(k.img, f, r.Min.X+15, r.Min.Y+25, c, text)
		bx += w + 12
	}
	pillL(st, scol)
	if p.Rate > 0 {
		pillL("Billable · "+wkMoney(p.Rate)+"/h", wkBlue)
	}
	if p.Priority != "" {
		pillL(strings.ToUpper(p.Priority[:1])+p.Priority[1:]+" priority", wkText2)
	}
	logged := projectHours(entries, p.ID, now)
	stats := []struct{ l, v string }{{"Logged", fmt.Sprintf("%.1f h", logged.Hours())}, {"Value", wkMoney(logged.Hours() * p.Rate)}, {"Due", "No date"}}
	d, has := p.daysLeft(now)
	if has {
		stats[2].v = fmt.Sprintf("%d days", d)
		if d < 0 {
			stats[2].v = fmt.Sprintf("%d days late", -d)
		}
	}
	for i, s := range stats {
		k.text(wkFont("regular", 19), 408+i*248, 428, wkFaint, s.l)
		k.text(wkFont("bold", 34), 408+i*248, 468, wkFG, s.v)
	}
	ln := ""
	if !p.Created.IsZero() {
		ln = "Started " + p.Created.In(displayZone).Format("2 Jan")
	}
	if has {
		due, _ := time.Parse("2006-01-02", p.Due)
		ln += " · " + due.Format("Mon 2 Jan") + " deadline"
		if !p.Created.IsZero() && p.progress() > 0 {
			finish := p.Created.Add(time.Duration(float64(now.Sub(p.Created)) / p.progress()))
			if finish.Before(due.Add(24 * time.Hour)) {
				ln += " · on track to finish " + finish.In(displayZone).Format("2 Jan")
			} else {
				ln += " · at this pace, " + finish.In(displayZone).Format("2 Jan")
			}
		}
	}
	k.text(wkFont("regular", 19), 408, 514, wkFaint, clip(wkFont("regular", 19), strings.TrimPrefix(ln, " · "), 730))
	run, running := wkRunning(entries)
	if running && run.ProjectID == p.ID {
		dd := run.dur(now)
		k.boxBtn("wk:stop", 191, 556, 531, 618, "square", fmt.Sprintf("Stop timer · %02d:%02d", int(dd.Hours()), int(dd.Minutes())%60), wkRed, rgb(0xffffff))
	} else {
		k.boxBtn("wk:start:"+p.ID, 191, 556, 531, 618, "timer", "Start timer", wkLime, wkInk)
	}
	k.boxBtn("wk:go:reports", 549, 556, 891, 618, "file-text", "Client report", color.RGBA{}, wkFG)
	k.boxBtn("wk:note", 909, 556, 1048, 618, "file", "Note", color.RGBA{}, wkFG)
	k.boxBtn("wk:more", 1066, 556, 1133, 618, "ellipsis", "", color.RGBA{}, wkFG)

	// The steps.
	left := 0
	for _, s := range p.Steps {
		if !s.Done {
			left++
		}
	}
	sc := image.Rect(wkX0, 674, wkX1, 674+92+84*max(len(p.Steps), 1))
	k.card(sc)
	e := k.cardHead(wkX0+28, 727, "list-checks", "Next steps", "")
	k.text(wkFont("regular", 19), e+14, 727, wkFaint, fmt.Sprintf("%d left", left))
	k.outline("wk:addstep", "plus", "Add step", 1136, 696, 50)
	y := 766
	for _, s := range p.Steps {
		ui.Fill(k.img, k.R(wkX0+2, y, wkX1-2, y+2), wkLine)
		cy := y + 42
		k.checkbox(188, cy, s.Done)
		k.struck(240, cy+8, s.Label, s.Done, 760)
		when := ""
		switch {
		case s.Done && !s.At.IsZero():
			when = s.At.In(displayZone).Format("2 Jan")
			if dayStart(s.At).Equal(dayStart(now)) {
				when = "Today"
			}
		case s.WorkMs > 0:
			when = fmt.Sprintf("%.1f h", float64(s.WorkMs)/3.6e6)
		}
		right := 1136
		if !s.Done {
			k.icon("grip-vertical", 1124, cy, 22, wkFaint)
			right = 1093
		}
		k.textR(wkFont("regular", 20), right, cy+7, wkMuted, when)
		k.btn("wk:step:"+p.ID+":"+s.ID, k.R(wkX0, y, wkX1, y+84))
		y += 84
	}
	if len(p.Steps) == 0 {
		k.text(wkFont("regular", 20), 190, y+50, wkMuted, "No steps yet.")
	}

	// Hours against plan, and the latest time.
	y = sc.Max.Y + 24
	k.card(image.Rect(wkX0, y, 676, y+400))
	k.cardHead(wkX0+28, y+44, "trending-up", "Burn-up", "· hours, plan vs actual")
	k.burnUp(image.Rect(236, y+95, 634, y+257), p, entries, now)
	ui.RoundRect(k.img, k.R(188, y+299, 204, y+315), 4, wkLime)
	k.text(wkFont("regular", 20), 213, y+315, wkText2, "Actual")
	ui.RoundRect(k.img, k.R(290, y+299, 306, y+315), 4, wkBlue)
	k.text(wkFont("regular", 20), 316, y+315, wkText2, "Planned")
	k.card(image.Rect(696, y, wkX1, y+400))
	k.cardHead(724, y+44, "timer", "Recent time", "")
	ui.Fill(k.img, k.R(698, y+76, wkX1-2, y+78), wkLine)
	var mine []wkEntry
	for i := len(entries) - 1; i >= 0 && len(mine) < 3; i-- {
		if entries[i].ProjectID == p.ID {
			mine = append(mine, entries[i])
		}
	}
	ry := y + 78
	for i, en := range mine {
		cy := ry + 48
		ui.RoundRect(k.img, k.R(724, cy-26, 776, cy+26), 12, blend(wkCard, wkBlue, 0.16))
		lucideIcon(k.img, "timer", 750, k.Y(cy), 24, wkBlue)
		note := en.Note
		if note == "" {
			note = p.Name
		}
		k.text(wkFont("semi", 22), 794, cy-4, wkFG, clip(wkFont("semi", 22), note, 270))
		when := en.Start.In(displayZone).Format("Mon 2")
		if dayStart(en.Start).Equal(dayStart(now)) {
			when = "Today"
		}
		end := "now"
		if !en.End.IsZero() {
			end = en.End.In(displayZone).Format("15:04")
		}
		k.text(wkFont("regular", 19), 794, cy+24, wkFaint, fmt.Sprintf("%s · %s – %s", when, en.Start.In(displayZone).Format("15:04"), end))
		dd := en.dur(now)
		k.textR(wkFont("bold", 23), 1136, cy+8, wkFG, fmt.Sprintf("%d:%02d", int(dd.Hours()), int(dd.Minutes())%60))
		ry += 96
		if i < len(mine)-1 {
			ui.Fill(k.img, k.R(698, ry, wkX1-2, ry+2), wkLine)
		}
	}
	if len(mine) == 0 {
		k.text(wkFont("regular", 20), 724, ry+54, wkMuted, "No time on it yet.")
	}
	return y + 400
}

// burnUp charts the hours logged on a project since it started against a straight plan to
// its deadline.
func (k *wk) burnUp(r image.Rectangle, p wkProject, entries []wkEntry, now time.Time) {
	start := p.Created
	if start.IsZero() {
		start = now.AddDate(0, 0, -30)
	}
	var mine []wkEntry
	for _, e := range entries {
		if e.ProjectID == p.ID {
			mine = append(mine, e)
			if e.Start.Before(start) {
				start = e.Start
			}
		}
	}
	days := max(int(now.Sub(start).Hours()/24)+1, 2)
	cum := make([]float64, days)
	for _, e := range mine {
		cum[min(max(int(e.Start.Sub(start).Hours()/24), 0), days-1)] += e.dur(now).Hours()
	}
	for i := 1; i < days; i++ {
		cum[i] += cum[i-1]
	}
	total := cum[days-1]
	plan := total * 1.05 // the plan: the hours so far, at an even pace from the start
	hi := math.Ceil(max(total, plan, 4)/12) * 12
	for i := 0; i <= 4; i++ {
		y := r.Max.Y - i*r.Dy()/4
		k.textR(wkFont("regular", 17), r.Min.X-12, y+6, wkFaint, fmt.Sprintf("%.0fh", hi*float64(i)/4))
	}
	base := k.Y(r.Max.Y)
	px := func(i int) int { return r.Min.X + i*r.Dx()/(days-1) }
	py := func(v float64) int { return base - int(v/hi*float64(r.Dy())) }
	for i := 1; i < days; i++ {
		for x := px(i - 1); x < px(i); x++ {
			yt := py(cum[i-1] + (cum[i]-cum[i-1])*float64(x-px(i-1))/float64(max(px(i)-px(i-1), 1)))
			for y := yt; y < base; y++ {
				glass(k.img, image.Rect(x, y, x+1, y+1), 0, wkLime, 0.14*(1-float64(y-yt)/float64(max(base-yt, 1))))
			}
		}
	}
	for x := r.Min.X; x < r.Max.X; x += 16 {
		x1 := min(x+9, r.Max.X)
		line(k.img, x, py(plan*float64(x-r.Min.X)/float64(r.Dx())), x1, py(plan*float64(x1-r.Min.X)/float64(r.Dx())), 3, wkBlue)
	}
	for i := 1; i < days; i++ {
		line(k.img, px(i-1), py(cum[i-1]), px(i), py(cum[i]), 3, wkLime)
	}
	ex, ey := px(days-1), py(total)
	ui.Circle(k.img, ex, ey, 8, wkLime)
	k.bubble(ex-20, ey-40, fmt.Sprintf("%.1f h", total), r.Max.X+20)
	k.text(wkFont("regular", 17), r.Min.X, r.Max.Y+28, wkFaint, start.In(displayZone).Format("2 Jan"))
	k.textR(wkFont("regular", 17), r.Max.X, r.Max.Y+28, wkFaint, "Today")
}

// --- time -----------------------------------------------------------------------------------------

func (k *wk) timePage() int {
	now := time.Now().In(displayZone)
	entries := wkEntries()
	projects := wkProjects()
	today := dayStart(now)
	mon := weekStart(now)
	dayT, weekT := wkTargets()
	k.header([]string{wkUser() + " · Freelance", "Time"}, "Time tracking", "Every hour, where it went and what it's worth", color.RGBA{})
	x := k.primary("wk:entry", "plus", "Entry", wkX1)
	k.square("wk:cal", "calendar", x-13)
	k.tabs("wk:tab", []string{"Timesheet", "Entries", "Rates", "Invoiced"}, nil, k.sel("tab", 0))

	// The timer.
	k.cardIn(image.Rect(wkX0, 312, wkX1, 640), rgb(0x16181d))
	run, running := wkRunning(entries)
	todayH := wkSum(wkHours(entries, today, today.AddDate(0, 0, 1), now))
	k.arc(1041, 431, 82, 16, todayH.Hours()/dayT, wkGreen, rgb(0x262833))
	k.textC(wkFont("bold", 34), 1041, 437, wkFG, hoursLabel(todayH)+"h")
	k.textC(wkFont("regular", 16), 1041, 460, wkMuted, fmt.Sprintf("of %gh today", dayT))
	if running {
		p, _ := wkProjectByID(run.ProjectID)
		ui.Circle(k.img, 201, k.Y(356), 10, blend(wkCard, wkGreen, 0.35))
		ui.Circle(k.img, 201, k.Y(356), 6, wkGreen)
		k.text(wkFont("semi", 20), 222, 363, wkGreen, "Running since "+run.Start.In(displayZone).Format("15:04"))
		d := run.dur(now)
		k.text(wkFont("bold", 92), 190, 465, wkFG, fmt.Sprintf("%02d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60))
		ui.RoundRect(k.img, k.R(193, 495, 209, 511), 4, wkColour(p, p.order))
		e := k.text(wkFont("semi", 22), 222, 511, wkFG, p.Name)
		after := ""
		if f := strings.Fields(p.Client); len(f) > 0 {
			after = " · " + f[0]
		}
		if p.Rate > 0 {
			after += " · " + wkMoney2(d.Hours()*p.Rate)
		}
		k.text(wkFont("regular", 21), e+8, 511, wkFaint, after)
	} else {
		k.text(wkFont("semi", 20), 193, 363, wkMuted, "No timer running")
		k.text(wkFont("bold", 92), 190, 465, wkFaint, "00:00:00")
		k.text(wkFont("regular", 21), 193, 511, wkFaint, "Start one from a project, or add an entry.")
	}
	k.boxBtn("wk:break", 193, 546, 494, 609, "coffee", "Take a break", color.RGBA{}, wkFG)
	k.boxBtn("wk:go:projects", 512, 546, 811, 609, "arrow-left-right", "Switch project", color.RGBA{}, wkFG)
	if running {
		k.boxBtn("wk:stop", 829, 546, 1130, 609, "square", "Stop", wkRed, rgb(0xffffff))
	} else {
		k.boxBtn("wk:go:projects", 829, 546, 1130, 609, "timer", "Start", wkLime, wkInk)
	}

	// The week's figures, against the same days last week.
	elapsed := now.Sub(mon)
	val, bill, all := wkValue(entries, projects, mon, mon.AddDate(0, 0, 7), now)
	pval, pbill, pall := wkValue(entries, projects, mon.AddDate(0, 0, -7), mon.AddDate(0, 0, -7).Add(elapsed), now)
	cw := (wkX1 - wkX0 - 60) / 4
	box := func(i int) image.Rectangle { return image.Rect(wkX0+i*(cw+20), 664, wkX0+i*(cw+20)+cw, 828) }
	k.kpi(box(0), "clock", "This week", hoursLabel(all), "h", pctChange(pall.Hours(), all.Hours()), all >= pall, fmt.Sprintf("target %.0f h", weekT), nil, wkLime, wkFG)
	share, pshare := 0.0, 0.0
	if all > 0 {
		share = bill.Hours() / all.Hours() * 100
	}
	if pall > 0 {
		pshare = pbill.Hours() / pall.Hours() * 100
	}
	k.kpi(box(1), "circle-dollar-sign", "Billable", fmt.Sprintf("%.0f%%", share), "", pctChange(pshare, share), share >= pshare, fmt.Sprintf("%.1f h", bill.Hours()), nil, wkLime, wkFG)
	k.kpi(box(2), "pound-sterling", "Value", wkMoney(val), "", pctChange(pval, val), val >= pval, "at your rates", nil, wkLime, wkFG)
	days := int(now.Sub(mon).Hours()/24) + 1
	k.kpi(box(3), "gauge", "Avg / day", fmt.Sprintf("%.1f", all.Hours()/float64(days)), "h", math.NaN(), true,
		"Mon to "+now.Format("Mon"), nil, wkLime, wkFG)

	// The timesheet.
	off := k.sel("week", 0)
	wmon := mon.AddDate(0, 0, 7*off)
	type row struct {
		p   wkProject
		day [7]float64
		tot float64
	}
	var rows []row
	var dayTot [7]float64
	grand := 0.0
	for _, p := range projects {
		r := row{p: p}
		for i := range 7 {
			d := wmon.AddDate(0, 0, i)
			r.day[i] = wkHours(entries, d, d.AddDate(0, 0, 1), now)[p.ID].Hours()
			r.tot += r.day[i]
		}
		if r.tot > 0 {
			for i := range 7 {
				dayTot[i] += r.day[i]
			}
			rows = append(rows, r)
			grand += r.tot
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].tot > rows[j].tot })
	ts := image.Rect(wkX0, 853, wkX1, 853+151+68*(len(rows)+1))
	k.card(ts)
	e := k.cardHead(wkX0+28, 908, "table", "Timesheet", "")
	k.text(wkFont("regular", 20), e+14, 908, wkFaint, "· "+wmon.Format("2 Jan")+" – "+wmon.AddDate(0, 0, 6).Format("2 Jan"))
	nav := func(id, icon string, x0 int) {
		r := k.R(x0, 876, x0+51, 925)
		ui.RoundRect(k.img, r, 12, rgb(0x101117))
		lucideIcon(k.img, icon, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, 22, wkFG)
		k.btn(id, r)
	}
	nav(fmt.Sprintf("wk:week:%d", off-1), "chevron-left", 884)
	nav(fmt.Sprintf("wk:week:%d", off+1), "chevron-right", 947)
	k.outline("wk:excel", "file-spreadsheet", "Excel", 1136, 876, 50)
	cols := []int{471, 562, 654, 745, 828, 909, 997}
	todayCol := -1
	for i := range 7 {
		if wmon.AddDate(0, 0, i).Equal(today) {
			todayCol = i
		}
	}
	if todayCol >= 0 {
		cx := cols[todayCol]
		ui.Fill(k.img, k.R(cx-56, 948, cx+38, ts.Max.Y-2), rgb(0x191b17))
	}
	hf := wkFont("semi", 17)
	k.text(hf, 187, 983, wkFaint, "PROJECT")
	for i, cx := range cols {
		c := wkFaint
		if i == todayCol {
			c = wkLime
		}
		k.textR(hf, cx+22, 983, c, strings.ToUpper(wmon.AddDate(0, 0, i).Format("Mon")))
	}
	k.textR(hf, 1136, 983, wkFaint, "TOTAL")
	y := 1004
	cell := func(v float64, cx, y int, f font.Face) {
		if v < 0.05 {
			k.textR(wkFont("regular", 20), cx+14, y, wkFaint, "–")
			return
		}
		k.textR(f, cx+22, y, wkFG, fmt.Sprintf("%.1f", v))
	}
	for _, r := range rows {
		ui.Fill(k.img, k.R(wkX0+2, y, wkX1-2, y+2), wkLine)
		ui.RoundRect(k.img, k.R(187, y+27, 203, y+43), 4, wkColour(r.p, r.p.order))
		k.text(wkFont("regular", 22), 216, y+44, wkFG, clip(wkFont("regular", 22), shortName(r.p.Name), 200))
		for i, cx := range cols {
			cell(r.day[i], cx, y+44, wkFont("regular", 22))
		}
		k.textR(wkFont("bold", 22), 1136, y+44, wkFG, fmt.Sprintf("%.1f", r.tot))
		y += 68
	}
	ui.Fill(k.img, k.R(wkX0+2, y, wkX1-2, y+2), wkLine)
	k.text(wkFont("semi", 22), 187, y+44, wkMuted, "Total")
	for i, cx := range cols {
		cell(dayTot[i], cx, y+44, wkFont("bold", 22))
	}
	k.textR(wkFont("bold", 22), 1136, y+44, wkLime, fmt.Sprintf("%.1f", grand))

	// Today's entries.
	y = ts.Max.Y + 25
	var todays []wkEntry
	var tsum time.Duration
	for i := len(entries) - 1; i >= 0; i-- {
		if dayStart(entries[i].Start).Equal(today) {
			todays = append(todays, entries[i])
			tsum += entries[i].dur(now)
		}
	}
	te := image.Rect(wkX0, y, wkX1, y+96+94*max(len(todays), 1))
	k.card(te)
	e = k.cardHead(wkX0+28, y+45, "list", "Today's entries", "")
	k.text(wkFont("regular", 20), e+14, y+45, wkFaint, fmt.Sprintf("%d · %dh %02dm", len(todays), int(tsum.Hours()), int(tsum.Minutes())%60))
	k.outline("wk:entry", "plus", "Add entry", 1136, y+21, 50)
	ry := y + 96
	for _, en := range todays {
		ui.Fill(k.img, k.R(wkX0+2, ry-2, wkX1-2, ry), wkLine)
		p, _ := wkProjectByID(en.ProjectID)
		ui.RoundRect(k.img, k.R(187, ry+16, 192, ry+78), 2, wkColour(p, p.order))
		note := en.Note
		if note == "" {
			note = p.Name
		}
		k.text(wkFont("semi", 22), 211, ry+40, wkFG, clip(wkFont("semi", 22), note, 680))
		end := "now"
		if !en.End.IsZero() {
			end = en.End.In(displayZone).Format("15:04")
		}
		k.text(wkFont("regular", 19), 211, ry+68, wkFaint, en.Start.In(displayZone).Format("15:04")+" – "+end+" · "+p.Name)
		b, bc := "Billable", wkLime
		if !en.Billable {
			b, bc = "Internal", wkMuted
		}
		k.badge(1048, ry+46, b, bc)
		d := en.dur(now)
		k.textR(wkFont("bold", 23), 1136, ry+54, wkFG, fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60))
		ry += 94
	}
	if len(todays) == 0 {
		k.text(wkFont("regular", 20), 190, ry+54, wkMuted, "Nothing logged today yet.")
	}
	return te.Max.Y
}

// shortName is a project's name to its first comma: "Slab schedules, phase 2" → "Slab schedules".
func shortName(s string) string {
	if i := strings.Index(s, ","); i > 0 {
		return s[:i]
	}
	return s
}

// --- the planner -----------------------------------------------------------------------------

func (k *wk) planner() int {
	now := time.Now().In(displayZone)
	off := k.sel("week", 0)
	mon := weekStart(now).AddDate(0, 0, 7*off)
	today := dayStart(now)
	todos := somaTodos()
	booked := 0
	tomorrow := today.AddDate(0, 0, 1).Format("2006-01-02")
	tomorrowMins := 0
	for _, t := range todos {
		if t.SlotDate >= mon.Format("2006-01-02") && t.SlotDate < mon.AddDate(0, 0, 7).Format("2006-01-02") {
			booked += t.SlotMins
		}
		if t.SlotDate == tomorrow {
			tomorrowMins += t.SlotMins
		}
	}
	free := max(0, (8*60-tomorrowMins)/120)
	k.header([]string{wkUser() + " · Freelance", "Planner"}, "Tasks & calendar", fmt.Sprintf("%.1f h booked this week · %d free blocks tomorrow", float64(booked)/60, free), color.RGBA{})
	x := k.primary("wk:task", "plus", "Task", wkX1)
	x = k.square(fmt.Sprintf("wk:week:%d", off+1), "chevron-right", x-13)
	k.square(fmt.Sprintf("wk:week:%d", off-1), "chevron-left", x-13)
	k.tabs("wk:tab", []string{"Week", "Day", "Month", "Board", "Routines"}, nil, k.sel("tab", 0))

	k.card(image.Rect(wkX0, 312, wkX1, 1298))
	x0, colW := 232, 130
	firstH, pxH := 8, 73.6
	for i := range 7 {
		d := mon.AddDate(0, 0, i)
		cx := x0 + i*colW + colW/2
		k.textC(wkFont("medium", 18), cx, 343, wkFaint, strings.ToUpper(d.Format("Mon")))
		if d.Equal(today) {
			ui.Fill(k.img, k.R(x0+i*colW+1, 452, x0+(i+1)*colW, 1284), rgb(0x14151a))
			ui.Circle(k.img, cx, k.Y(382), 28, wkLime)
			apTextCenter(k.img, wkFont("bold", 25), cx, k.Y(382), wkInk, d.Format("2"))
		} else {
			k.textC(wkFont("bold", 25), cx, 391, wkFG, d.Format("2"))
		}
		ds := d.Format("2006-01-02")
		done, all := 0, 0
		for _, t := range todos {
			if (t.Date == ds && t.Scope != "week") || t.SlotDate == ds {
				all++
				if t.Done {
					done++
				}
			}
		}
		frac, col := 0.0, wkLime
		if all > 0 {
			frac = float64(done) / float64(all)
		}
		if d.Before(today) && all > done {
			frac, col = 1, wkRed
		}
		k.hBar(k.R(cx-31, 431, cx+31, 437), frac, col)
		ui.Fill(k.img, k.R(x0+i*colW, 476, x0+i*colW+1, 1284), wkLine)
	}
	for h := 0; h <= 10; h++ {
		y := 474 + int(float64(h)*pxH)
		k.text(wkFont("regular", 17), 171, y+6, wkFaint, fmt.Sprintf("%02d:00", firstH+h))
		ui.Fill(k.img, k.R(x0, y, 1146, y+1), rgb(0x1b1c22))
	}
	for _, t := range todos {
		if t.SlotDate == "" {
			continue
		}
		d, err := time.ParseInLocation("2006-01-02", t.SlotDate, displayZone)
		if err != nil {
			continue
		}
		i := int(math.Round(d.Sub(mon).Hours() / 24))
		if i < 0 || i > 6 {
			continue
		}
		col := wkPalette[len(t.Text)%len(wkPalette)]
		y0 := 474 + int((float64(t.SlotStart)/60-float64(firstH))*pxH)
		y1 := y0 + int(float64(max(t.SlotMins, 30))/60*pxH)
		y0, y1 = max(y0, 476), min(y1, 1284)
		if y1-y0 < 30 {
			continue
		}
		r := k.R(x0+i*colW+4, y0+2, x0+(i+1)*colW-4, y1-2)
		ui.RoundRect(k.img, r, 8, blend(wkCard, col, 0.22))
		ui.RoundRect(k.img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+4, r.Max.Y), 2, col)
		ty := r.Min.Y + 24
		f := wkFont("semi", 18)
		for _, l := range wkWrap(f, t.Text, r.Dx()-24, 2) {
			if ty > r.Max.Y-8 {
				break
			}
			apText(k.img, f, r.Min.X+14, ty, wkFG, l)
			ty += 22
		}
		if ty < r.Max.Y-6 {
			apText(k.img, wkFont("regular", 16), r.Min.X+14, ty, wkMuted, clockOf(float64(t.SlotStart)/60))
		}
		k.btn("wk:todo:"+t.ID, r)
	}
	if off == 0 {
		i := int(today.Sub(mon).Hours() / 24)
		hh := float64(now.Hour()) + float64(now.Minute())/60
		if hh >= float64(firstH) && hh <= float64(firstH)+11 {
			y := k.Y(474 + int((hh-float64(firstH))*pxH))
			ui.Fill(k.img, image.Rect(x0+i*colW-4, y-1, x0+(i+1)*colW, y+2), wkRed)
			ui.Circle(k.img, x0+i*colW-4, y, 7, wkRed)
		}
	}

	// Today's list.
	ts := today.Format("2006-01-02")
	var list []somaTodo
	for _, t := range todos {
		if !t.Cleared && (t.Date == ts && t.Scope != "week" || t.SlotDate == ts || t.Due == ts) {
			list = append(list, t)
		}
	}
	done, leftMin := 0, 0
	for _, t := range list {
		if t.Done {
			done++
		} else {
			leftMin += t.SlotMins
		}
	}
	y := 1323
	tc := image.Rect(wkX0, y, wkX1, y+96+83*max(len(list), 1)+20)
	k.card(tc)
	e := k.cardHead(wkX0+28, y+54, "sun", "Today", "")
	note := fmt.Sprintf("%d of %d done", done, len(list))
	if leftMin > 0 {
		note += fmt.Sprintf(" · %dh %02dm left", leftMin/60, leftMin%60)
	}
	k.text(wkFont("regular", 20), e+14, y+54, wkFaint, note)
	k.outline("wk:planday", "wand-sparkles", "Plan my day", 1136, y+23, 50)
	ry := y + 94
	for _, t := range list {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		cy := ry + 42
		k.checkbox(188, cy, t.Done)
		ui.RoundRect(k.img, k.R(241, cy-7, 255, cy+7), 3, wkPalette[len(t.Text)%len(wkPalette)])
		k.struck(273, cy+8, t.Text, t.Done, 700)
		dur := ""
		if t.SlotMins > 0 {
			dur = fmt.Sprintf("%dm", t.SlotMins)
			if t.SlotMins%60 == 0 {
				dur = fmt.Sprintf("%dh", t.SlotMins/60)
			}
		}
		k.textR(wkFont("regular", 20), 1136, cy+7, wkMuted, dur)
		if t.Priority == "high" {
			k.icon("flag", 1065, cy, 24, rgb(0xf06a8a))
		}
		k.btn("wk:todo:"+t.ID, k.R(wkX0, ry, wkX1, ry+83))
		ry += 83
	}
	if len(list) == 0 {
		k.text(wkFont("regular", 20), 190, ry+50, wkMuted, "Nothing planned today.")
	}
	return tc.Max.Y
}

// wkWrap breaks s into at most n lines of width w, the last one clipped.
func wkWrap(f font.Face, s string, w, n int) []string {
	words := strings.Fields(s)
	var out []string
	cur := ""
	i := 0
	for ; i < len(words) && len(out) < n-1; i++ {
		try := strings.TrimSpace(cur + " " + words[i])
		if ui.TextWidth(f, try) > w && cur != "" {
			out = append(out, cur)
			cur = words[i]
			continue
		}
		cur = try
	}
	rest := strings.TrimSpace(cur + " " + strings.Join(words[i:], " "))
	if rest != "" {
		out = append(out, clip(f, rest, w))
	}
	return out
}
