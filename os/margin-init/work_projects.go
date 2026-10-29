package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"
)

// Projects (ProjectsPage.tsx): every project as a list, a board by status, a timeline of
// their deadlines, and the finished ones; sorted and filtered by client or by what's typed.
// A project opens on its own page: its overview, steps, time, notes and findings, each one
// editable, as the PC's side panel is.

// wkProjFilter is how the list is shown: sorted by what (and which way), one client's, a
// search.
type wkProjFilter struct {
	sort   string // "due" (unset), "name", "client", "status", "priority", "progress", "activity", "hours"
	desc   bool
	client string
	query  string
}

var wkSortKeys = []struct{ id, label, icon string }{
	{"due", "Due date", "calendar-clock"}, {"name", "Name", "list"}, {"client", "Client", "building-2"},
	{"status", "Status", "circle-check"}, {"priority", "Priority", "flag"}, {"progress", "Progress", "chart-pie"},
	{"activity", "Last moved", "history"}, {"hours", "Hours logged", "clock"},
}

// wkClients is every client the projects (and shifts) name, A to Z.
func wkClients() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range wkProjects() {
		if c := strings.TrimSpace(p.Client); c != "" && !seen[c] {
			out, seen[c] = append(out, c), true
		}
	}
	sort.Strings(out)
	return out
}

// sortProjects orders the list the way the filter says (projects.ts sortProjects for "due").
func sortProjects(list []wkProject, f wkProjFilter, entries []wkEntry, now time.Time) {
	prio := map[string]int{"high": 0, "medium": 1, "low": 2}
	rank := map[string]int{"active": 0, "paused": 1, "done": 2}
	val := func(p wkProject) (float64, string) {
		switch f.sort {
		case "name":
			return 0, strings.ToLower(p.Name)
		case "client":
			c := strings.ToLower(p.Client)
			if c == "" {
				c = "~"
			}
			return 0, c
		case "status":
			return float64(rank[p.Status]), ""
		case "priority":
			if v, ok := prio[p.Priority]; ok {
				return float64(v), ""
			}
			return 3, ""
		case "progress":
			return p.progress(), ""
		case "activity":
			t := p.Touched
			if t.IsZero() {
				t = p.Created
			}
			return -float64(t.UnixMilli()), ""
		case "hours":
			return -projectHours(entries, p.ID, now).Hours(), ""
		}
		if d, ok := p.daysLeft(now); ok {
			return float64(d), ""
		}
		return 99999, ""
	}
	sort.SliceStable(list, func(i, j int) bool {
		if f.sort == "" || f.sort == "due" { // active first, then the nearest deadline
			if ri, rj := rank[list[i].Status], rank[list[j].Status]; ri != rj {
				return ri < rj != f.desc
			}
		}
		a, as := val(list[i])
		b, bs := val(list[j])
		if as != bs {
			return as < bs != f.desc
		}
		if a != b {
			return a < b != f.desc
		}
		return false
	})
}

func (k *wk) projects() int {
	now := time.Now().In(displayZone)
	su := &k.c.somaUI
	f := su.pf
	all := wkProjects()
	entries := wkEntries()
	clients := wkClients()
	q := time.Date(now.Year(), time.Month((int(now.Month())-1)/3*3+1), 1, 0, 0, 0, 0, displayZone)
	qh := wkSum(wkHours(entries, q, now.AddDate(0, 0, 1), now))
	k.header([]string{wkUser() + " · Freelance", "Projects"}, "Projects",
		fmt.Sprintf("%d projects · %d clients · %.0f h this quarter", len(all), len(clients), qh.Hours()), color.RGBA{})
	x := k.primary("wk:newproject", "plus", "New project", wkX1)
	x = k.outline("wk:newplan", "calendar-clock", "New plan", x-13, 103, 63)
	x = k.kindSwitch(x - 13)
	x = k.square("wk:psearch", "search", x-13)
	x = k.square("wk:sheet-open:sort", "arrow-up-down", x-13)
	if f.query != "" { // what's searched, beside the search; × clears it
		fq := wkFont("medium", 21)
		lbl := "“" + clip(fq, f.query, 200) + "”"
		w := ui.TextWidth(fq, lbl) + 80
		r := k.R(x-13-w, 108, x-13, 161)
		ui.RoundRect(k.img, r, 14, blend(wkCard, wkLime, 0.25))
		apText(k.img, fq, r.Min.X+20, (r.Min.Y+r.Max.Y)/2+8, wkFG, lbl)
		lucideIcon(k.img, "x", r.Max.X-30, (r.Min.Y+r.Max.Y)/2, 22, wkFG)
		k.btn("wk:pquery:", r)
	}
	tab := k.sel("tab", 0)
	nDone := 0
	for _, p := range all {
		if p.Status == "done" {
			nDone++
		}
	}
	k.tabs("wk:tab", []string{"All projects", "Board", "Timeline", "Archive"}, []string{"", "", "", fmt.Sprint(nDone)}, tab)

	// The filters: status chips (the list), the client, what's typed.
	var shown []wkProject
	plans := k.sel("pk", 0) == 1
	for _, p := range all {
		if p.plan() != plans { // the switch: client work, or plans
			continue
		}
		if f.client != "" && strings.TrimSpace(p.Client) != f.client {
			continue
		}
		if f.query != "" {
			ql := strings.ToLower(f.query)
			if !strings.Contains(strings.ToLower(p.Name), ql) && !strings.Contains(strings.ToLower(p.Client), ql) {
				continue
			}
		}
		shown = append(shown, p)
	}
	sortProjects(shown, f, entries, now)
	n := map[string]int{}
	for _, p := range shown {
		st, _ := statusOf(p, now)
		n[p.Status]++
		if st == "Overdue" {
			n["overdue"]++
		}
	}
	chip := k.sel("chip", 0)
	if tab == 0 {
		k.chips("wk:chip", 308, []string{fmt.Sprintf("All %d", len(shown)), fmt.Sprintf("Active %d", n["active"]),
			fmt.Sprintf("Overdue %d", n["overdue"]), fmt.Sprintf("Paused %d", n["paused"]), fmt.Sprintf("Done %d", n["done"])}, nil, chip)
	}
	cl := "All clients"
	if f.client != "" {
		cl = f.client
	}
	k.outline("wk:sheet-open:clients", "building-2", clip(wkFont("medium", 21), cl, 200)+"    ", wkX1, 308, 53)
	k.icon("chevron-down", wkX1-32, 334, 22, wkText2)

	switch tab {
	case 1:
		return k.projectBoard(shown, now)
	case 2:
		return k.projectTimeline(shown, entries, now)
	case 3:
		return k.projectArchive(shown, entries, now)
	}

	// The figures.
	drifting, prog, nAct := 0, 0.0, 0
	overdueName := ""
	for _, p := range shown {
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
	box := k.row4(386)
	late := "none late"
	if overdueName != "" {
		late = overdueName
	}
	k.small(box(0), "folder-open", "Active", fmt.Sprint(nAct), "", fmt.Sprintf("%d drifting", drifting), wkFG)
	k.small(box(1), "alarm-clock", "Overdue", fmt.Sprint(n["overdue"]), "", late, wkFG)
	k.small(box(2), "clock", "This month", fmt.Sprintf("%.0f", wkSum(mh).Hours()), "h", fmt.Sprintf("across %d", len(mh)), wkFG)
	avg := 0.0
	if nAct > 0 {
		avg = prog / float64(nAct) * 100
	}
	k.small(box(3), "chart-pie", "Avg progress", fmt.Sprintf("%.0f%%", avg), "", "active projects", wkFG)

	// The projects.
	run, running := wkRunning(entries)
	y := 572
	count := 0
	for _, p := range shown {
		st, _ := statusOf(p, now)
		switch {
		case chip == 1 && p.Status != "active", chip == 2 && st != "Overdue", chip == 3 && p.Status != "paused", chip == 4 && p.Status != "done":
			continue
		}
		k.projectCard(p, y, entries, running && run.ProjectID == p.ID, now)
		y += 173
		count++
	}
	if count == 0 {
		msg := "No projects yet: New project adds one."
		if len(all) > 0 {
			msg = "Nothing matches these filters."
		}
		k.emptyNote(wkX0+20, y+40, msg)
		y += 100
	}
	if f.sort != "" && f.sort != "due" || f.desc {
		for _, sk := range wkSortKeys {
			if sk.id == f.sort || (f.sort == "" && sk.id == "due") {
				dir := "↑"
				if f.desc {
					dir = "↓"
				}
				k.textR(wkFont("regular", 18), wkX1, 566-14, wkFaint, "Sorted by "+strings.ToLower(sk.label)+" "+dir)
			}
		}
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

// projectCard is a project's row in the list: its colour, name and state, client and steps,
// progress, deadline, the last weeks' hours.
func (k *wk) projectCard(p wkProject, y int, entries []wkEntry, timing bool, now time.Time) {
	st, scol := statusOf(p, now)
	col := wkColour(p, p.order)
	cr := image.Rect(wkX0, y, wkX1, y+153)
	k.card(cr)
	if timing { // the one the timer runs on: an accent rim
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
	if p.plan() {
		sub = "Plan · " + sub
	} else if p.Client != "" {
		sub = p.Client + " · " + sub
	}
	// The next dated thing and its countdown ("TCF exam in 11 days").
	if s, ok := p.nextDated(false); ok {
		sub = wkCountdown(s.Label, s.Due, now) + " · " + sub
		if m, ok := p.nextDated(true); ok && m.ID != s.ID {
			sub = wkCountdown(m.Label, m.Due, now) + " · " + sub
		}
	}
	k.text(wkFont("regular", 20), 217, y+83, wkFaint, clip(wkFont("regular", 20), sub, 680))
	k.hBar(k.R(217, y+113, 490, y+121), p.progress(), col)
	k.text(wkFont("bold", 21), 505, y+122, wkFG, fmt.Sprintf("%.0f%%", p.progress()*100))
	k.textR(wkFont("regular", 18), 916, y+55, wkFaint, "Due")
	if d, ok := p.daysLeft(now); ok {
		due, _ := time.Parse("2006-01-02", p.effDue())
		dc, sc, note := wkFG, wkFaint, fmt.Sprintf("%d days", d)
		switch {
		case p.Status == "done":
			note = "finished"
		case d < 0 && p.overdue(now):
			dc, sc, note = wkRed, wkRed, fmt.Sprintf("%d days late", -d)
		case d < 0:
			dc, sc, note = wkAmber, wkAmber, fmt.Sprintf("%d days ago", -d)
		case d == 0:
			dc, sc, note = wkAmber, wkAmber, "today"
		case d <= 3:
			dc, sc = wkAmber, wkAmber
		}
		k.textR(wkFont("semi", 22), 916, y+84, dc, due.Format("Mon 2 Jan"))
		k.textR(wkFont("regular", 19), 916, y+113, sc, note)
	} else {
		k.textR(wkFont("semi", 22), 916, y+92, wkFG, "No date")
	}
	if p.plan() { // no hours: what's late and what waits on others
		if n := p.overdueSteps(now); n > 0 {
			k.textR(wkFont("semi", 20), 1086, y+60, wkRed, fmt.Sprintf("%d overdue", n))
		}
		wait := 0
		for _, s := range p.Steps {
			if !s.Done && s.WaitingOn != "" {
				wait++
			}
		}
		if wait > 0 {
			k.textR(wkFont("semi", 20), 1086, y+108, wkAmber, fmt.Sprintf("%d waiting", wait))
		}
	} else {
		k.spark(k.R(958, y+30, 1086, y+66), weeklyHours(entries, p.ID, 8, now), col)
		h := projectHours(entries, p.ID, now)
		k.textR(wkFont("regular", 20), 1086, y+108, wkFaint, "h")
		k.textR(wkFont("bold", 22), 1066, y+108, wkFG, fmt.Sprintf("%.1f", h.Hours()))
	}
	k.icon("chevron-right", 1122, y+72, 22, wkMuted)
	k.btn("wk:proj:"+p.ID, k.R(wkX0, y, wkX1, y+153))
}

// --- the board ---------------------------------------------------------------------------------

var wkStatuses = []struct{ id, label string }{{"active", "Active"}, {"paused", "Paused"}, {"done", "Done"}}

func statusCol(s string) color.RGBA {
	switch s {
	case "paused":
		return wkAmber
	case "done":
		return wkBlue
	}
	return wkGreen
}

func (k *wk) projectBoard(list []wkProject, now time.Time) int {
	cw := (wkX1 - wkX0 - 40) / 3
	bottom := 0
	for i, st := range wkStatuses {
		x0 := wkX0 + i*(cw+20)
		var col []wkProject
		for _, p := range list {
			if p.Status == st.id {
				col = append(col, p)
			}
		}
		h := 92 + 144*max(len(col), 1) + 70
		cr := image.Rect(x0, 386, x0+cw, 386+h)
		k.cardIn(cr, rgb(0x121318))
		bx := k.badge(x0+24+ui.TextWidth(wkFont("semi", 18), st.label)+28, 428, st.label, statusCol(st.id))
		k.text(wkFont("semi", 20), bx+ui.TextWidth(wkFont("semi", 18), st.label)+44, 435, wkFaint, fmt.Sprint(len(col)))
		y := 470
		for _, p := range col {
			t := image.Rect(x0+14, y, x0+cw-14, y+132)
			k.card(t)
			pc := wkColour(p, p.order)
			ui.RoundRect(k.img, k.R(t.Min.X+20, y+28, t.Min.X+36, y+44), 5, pc)
			f := wkFont("semi", 21)
			k.text(f, t.Min.X+48, y+44, wkFG, clip(f, p.Name, t.Dx()-130))
			if p.Client != "" {
				k.text(wkFont("regular", 18), t.Min.X+20, y+76, wkFaint, clip(wkFont("regular", 18), p.Client, t.Dx()-40))
			}
			k.hBar(k.R(t.Min.X+20, y+100, t.Min.X+110, y+106), p.progress(), pc)
			k.text(wkFont("semi", 17), t.Min.X+122, y+108, wkText2, fmt.Sprintf("%d/%d", p.done(), len(p.Steps)))
			if d, ok := p.daysLeft(now); ok {
				due, _ := time.Parse("2006-01-02", p.effDue())
				dc := wkText2
				if d < 0 && p.Status != "done" {
					dc = wkRed
				} else if d <= 3 && p.Status != "done" {
					dc = wkAmber
				}
				k.textR(wkFont("medium", 17), t.Max.X-20, y+108, dc, due.Format("2 Jan"))
			}
			k.btn("wk:proj:"+p.ID, k.R(t.Min.X, t.Min.Y, t.Max.X-64, t.Max.Y))
			// Move it to another column.
			mr := k.R(t.Max.X-62, y+12, t.Max.X-12, y+62)
			ui.RoundRect(k.img, mr, 12, rgb(0x22242d))
			lucideIcon(k.img, "arrow-left-right", (mr.Min.X+mr.Max.X)/2, (mr.Min.Y+mr.Max.Y)/2, 22, wkText2)
			k.btn("wk:sheet-open:pstatus:"+p.ID, mr.Inset(-6))
			y += 144
		}
		if len(col) == 0 {
			k.textC(wkFont("regular", 19), x0+cw/2, y+70, wkFaint, "Nothing here")
			y += 144
		}
		// Add a project straight into this column.
		ar := k.R(x0+14, y+4, x0+cw-14, y+58)
		ui.RoundRect(k.img, ar, 12, rgb(0x191a21))
		lucideIcon(k.img, "plus", ar.Min.X+34, (ar.Min.Y+ar.Max.Y)/2, 22, wkMuted)
		apText(k.img, wkFont("medium", 19), ar.Min.X+56, (ar.Min.Y+ar.Max.Y)/2+7, wkMuted, "Add project")
		k.btn("wk:newproject:"+st.id, ar)
		bottom = max(bottom, cr.Max.Y)
	}
	k.text(wkFont("regular", 18), wkX0+4, bottom+40, wkFaint, "Tap ⇄ on a card to move it to another column, or hold a card for everything about it.")
	return bottom + 60
}

// --- the timeline ------------------------------------------------------------------------------

func (k *wk) projectTimeline(list []wkProject, entries []wkEntry, now time.Time) int {
	off := k.sel("week", 0)
	const weeks = 12
	from := weekStart(now).AddDate(0, 0, 7*(4*off-2))
	to := from.AddDate(0, 0, 7*weeks)
	x0, x1 := 470, 1136
	dayW := float64(x1-x0) / float64(weeks*7)
	px := func(t time.Time) int { return x0 + int(t.Sub(from).Hours()/24*dayW) }

	// The projects that are on during these weeks: from when they started to their deadline.
	type bar struct {
		p          wkProject
		start, end time.Time
		open       bool // no deadline: shown running on
	}
	var bars []bar
	for _, p := range list {
		start := p.Created
		for _, e := range entries {
			if e.ProjectID == p.ID && (start.IsZero() || e.Start.Before(start)) {
				start = e.Start
			}
		}
		if start.IsZero() {
			start = now
		}
		b := bar{p: p, start: dayStart(start)}
		if due, err := time.ParseInLocation("2006-01-02", p.Due, displayZone); err == nil {
			b.end = due.AddDate(0, 0, 1)
		} else {
			b.end, b.open = dayStart(now).AddDate(0, 0, 14), true
			if p.Status == "done" && !p.Touched.IsZero() {
				b.end, b.open = dayStart(p.Touched).AddDate(0, 0, 1), false
			}
		}
		if b.end.Before(from) || !b.start.Before(to) {
			continue
		}
		bars = append(bars, b)
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].start.Before(bars[j].start) })
	rowH := 86
	h := 170 + rowH*max(len(bars), 1) + 30
	cr := image.Rect(wkX0, 386, wkX1, 386+h)
	k.card(cr)
	k.swipe("ptimeline:", cr)
	// The months and weeks along the top, a line a week, today in red.
	k.cardHead(wkX0+28, 432, "calendar-range", from.Format("2 Jan")+" – "+to.AddDate(0, 0, -1).Format("2 Jan 2006"), "")
	nav := func(id, icon string, x int) {
		r := k.R(x, 404, x+51, 453)
		ui.RoundRect(k.img, r, 12, rgb(0x101117))
		lucideIcon(k.img, icon, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, 22, wkFG)
		k.btn(id, r)
	}
	nav(fmt.Sprintf("wk:week:%d", off-1), "chevron-left", 1022)
	nav(fmt.Sprintf("wk:week:%d", off+1), "chevron-right", 1085)
	if off != 0 {
		k.outline("wk:week:0", "", "Today", 1008, 404, 49)
	}
	top, bot := 510, 386+h-24
	for w := 0; w <= weeks; w++ {
		d := from.AddDate(0, 0, 7*w)
		x := px(d)
		ui.Fill(k.img, k.R(x, top, x+1, bot), rgb(0x1d1e25))
		if w < weeks {
			if w == 0 || d.Day() <= 7 { // the month over its first week
				k.text(wkFont("semi", 16), x+6, top-34, wkText2, d.Format("Jan"))
			}
			k.text(wkFont("regular", 16), x+6, top-10, wkFaint, d.Format("2"))
		}
	}
	if t := dayStart(now); !t.Before(from) && t.Before(to) {
		x := px(t) + int(dayW/2)
		ui.Fill(k.img, k.R(x-1, top-4, x+2, bot), wkRed)
		ui.Circle(k.img, x, k.Y(top-4), 6, wkRed)
	}
	y := top + 10
	for _, b := range bars {
		p := b.p
		pc := wkColour(p, p.order)
		ui.RoundRect(k.img, k.R(188, y+22, 202, y+36), 4, pc)
		k.text(wkFont("semi", 20), 214, y+35, wkFG, clip(wkFont("semi", 20), p.Name, 240))
		sub := p.Client
		if st, _ := statusOf(p, now); st != "Active" {
			sub = strings.TrimPrefix(sub+" · "+st, " · ")
		}
		k.text(wkFont("regular", 17), 188, y+62, wkFaint, clip(wkFont("regular", 17), sub, 266))
		bx0, bx1 := max(px(b.start), x0), min(px(b.end), x1)
		if bx1-bx0 < 12 {
			bx1 = bx0 + 12
		}
		br := k.R(bx0, y+18, bx1, y+60)
		fill := blend(wkCard, pc, 0.35)
		if p.Status == "done" {
			fill = blend(wkCard, pc, 0.2)
		}
		ui.RoundRect(k.img, br, 10, fill)
		// Done so far, as a share of the bar.
		if pr := p.progress(); pr > 0 {
			fx := br.Min.X + int(float64(px(b.end)-px(b.start))*pr) - (px(b.start) - bx0)
			if fx > br.Min.X+4 {
				ui.RoundRect(k.img, image.Rect(br.Min.X, br.Min.Y, min(fx, br.Max.X), br.Max.Y), 10, pc)
			}
		}
		if b.open { // no deadline: the end fades
			for x := br.Max.X - 40; x < br.Max.X; x++ {
				t := float64(x-(br.Max.X-40)) / 40
				blendRect(k.img, image.Rect(x, br.Min.Y, x+1, br.Max.Y), wkCard, t*0.9)
			}
		} else if p.Status == "active" && p.overdue(now) {
			ui.Fill(k.img, image.Rect(br.Max.X-4, br.Min.Y, br.Max.X, br.Max.Y), wkRed)
		}
		lbl := fmt.Sprintf("%.0f%%", p.progress()*100)
		if br.Dx() > 70 {
			apText(k.img, wkFont("semi", 17), br.Min.X+12, (br.Min.Y+br.Max.Y)/2+6, accentInk(pc), lbl)
		}
		k.btn("wk:proj:"+p.ID, k.R(wkX0, y, wkX1, y+rowH))
		y += rowH
	}
	if len(bars) == 0 {
		k.emptyNote(188, y+50, "No projects in these weeks. Swipe or use the arrows to look further.")
	}
	k.text(wkFont("regular", 18), wkX0+4, cr.Max.Y+40, wkFaint, "From when each project started to its deadline; the filled part is the steps done. Swipe for other weeks.")
	return cr.Max.Y + 60
}

func init() {
	wkSwipes["ptimeline"] = func(c *console, _ string, dir int) {
		su := &c.somaUI
		if su.workSel == nil {
			su.workSel = map[string]string{}
		}
		n, _ := strconv.Atoi(su.workSel[su.work+"wk:week"])
		su.workSel[su.work+"wk:week"] = fmt.Sprint(n + dir)
	}
}

// --- the archive ---------------------------------------------------------------------------------

func (k *wk) projectArchive(list []wkProject, entries []wkEntry, now time.Time) int {
	var done []wkProject
	for _, p := range list {
		if p.Status == "done" {
			done = append(done, p)
		}
	}
	sort.SliceStable(done, func(i, j int) bool { return done[i].Touched.After(done[j].Touched) })
	y := 386
	cr := image.Rect(wkX0, y, wkX1, y+96+110*max(len(done), 1))
	k.card(cr)
	k.cardHead(wkX0+28, y+48, "archive", "Finished projects", fmt.Sprintf("· %d", len(done)))
	ry := y + 80
	for _, p := range done {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		pc := wkColour(p, p.order)
		ui.RoundRect(k.img, k.R(188, ry+30, 204, ry+46), 5, pc)
		k.text(wkFont("semi", 23), 220, ry+46, wkFG, clip(wkFont("semi", 23), p.Name, 520))
		h := projectHours(entries, p.ID, now)
		sub := fmt.Sprintf("%d steps · %.1f h", len(p.Steps), h.Hours())
		if p.Rate > 0 {
			sub += " · " + wkMoney(h.Hours()*p.Rate)
		}
		if !p.Touched.IsZero() {
			sub = "finished " + p.Touched.In(displayZone).Format("2 Jan 2006") + " · " + sub
		}
		if p.Client != "" {
			sub = p.Client + " · " + sub
		}
		k.text(wkFont("regular", 19), 188, ry+80, wkFaint, clip(wkFont("regular", 19), sub, 700))
		k.btn("wk:proj:"+p.ID, k.R(wkX0, ry, 940, ry+110))
		k.outline("wk:prestore:"+p.ID, "archive-restore", "Restore", 1060, ry+28, 54)
		k.outline("wk:pdelete:"+p.ID, "trash-2", "", 1136, ry+28, 54)
		ry += 110
	}
	if len(done) == 0 {
		k.emptyNote(188, ry+56, "Projects marked Done land here, out of the way. Restore brings one back.")
	}
	return cr.Max.Y + 20
}

// --- a project ------------------------------------------------------------------------------------

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
	if sub == "" {
		sub = "No client"
	}
	if p.plan() {
		sub = "Plan"
	}
	if p.Note != "" {
		sub += " · " + strings.Join(strings.Fields(p.Note), " ")
	}
	k.header([]string{wkUser() + " · Freelance", "Projects ›"}, clip(wkFont("bold", 42), p.Name, 620), clip(wkFont("regular", 20), sub, 700), col)
	k.btn("wk:type-open:prename:"+p.ID, k.R(wkX0, 110, 800, 166))
	x := k.square("wk:sheet-open:proj:"+p.ID, "ellipsis", wkX1)
	k.square("wk:back", "chevron-left", x-13)
	var pf []wkFinding
	for _, f := range wkFindings() {
		if f.ProjectID == p.ID && !f.closed() {
			pf = append(pf, f)
		}
	}
	tab := k.sel("tab", 0)
	k.tabs("wk:tab", []string{"Overview", "Steps", "Time", "Notes", "Findings"},
		[]string{"", fmt.Sprint(len(p.Steps)), "", fmt.Sprint(len(p.Log)), fmt.Sprint(len(pf))}, tab)
	if p.plan() { // dated steps first; no money
		switch tab {
		case 0:
			k.planHero(p, now)
			return k.practiceCard(p, k.planSteps(p, 674, now)+24, now)
		case 1:
			return k.planSteps(p, 312, now)
		}
	}
	switch tab {
	case 1:
		return k.stepList(p, 312, true, now)
	case 2:
		return k.projectTime(p, entries, now)
	case 3:
		return k.projectNotes(p, now)
	case 4:
		return k.projectFindings(p)
	}

	// The summary.
	hero := image.Rect(wkX0, 312, wkX1, 648)
	k.cardIn(hero, blend(wkCard, col, 0.08))
	k.arc(285, 435, 84, 18, p.progress(), col, rgb(0x262833))
	k.textC(wkFont("bold", 38), 285, 445, wkFG, fmt.Sprintf("%.0f%%", p.progress()*100))
	k.textC(wkFont("regular", 17), 285, 470, wkMuted, fmt.Sprintf("%d of %d steps", p.done(), len(p.Steps)))
	st, scol := statusOf(p, now)
	bx := 408
	pillL := func(id, text string, c color.RGBA) {
		f := wkFont("semi", 18)
		w := ui.TextWidth(f, text) + 30
		r := k.R(bx, 350, bx+w, 386)
		ui.RoundRect(k.img, r, 18, blend(wkCard, c, 0.18))
		apText(k.img, f, r.Min.X+15, r.Min.Y+25, c, text)
		k.btn(id, r.Inset(-6))
		bx += w + 12
	}
	pillL("wk:sheet-open:pstatus:"+p.ID, st, scol)
	if p.Rate > 0 {
		pillL("wk:type-open:prate:"+p.ID, "Billable · "+wkMoney(p.Rate)+"/h", wkBlue)
	} else {
		pillL("wk:type-open:prate:"+p.ID, "No rate", wkMuted)
	}
	if p.Priority != "" {
		pillL("wk:sheet-open:proj:"+p.ID, strings.ToUpper(p.Priority[:1])+p.Priority[1:]+" priority", map[string]color.RGBA{"high": rgb(0xf06a8a), "medium": wkAmber, "low": wkBlue}[p.Priority])
	} else {
		pillL("wk:sheet-open:proj:"+p.ID, "No priority", wkMuted)
	}
	logged := projectHours(entries, p.ID, now)
	stats := []struct{ l, v, id string }{{"Logged", fmt.Sprintf("%.1f h", logged.Hours()), "wk:tab:2"}, {"Value", wkMoney(logged.Hours() * p.Rate), "wk:type-open:prate:" + p.ID}, {"Due", "No date", "wk:pdue:" + p.ID}}
	d, has := p.daysLeft(now)
	if has {
		stats[2].v = fmt.Sprintf("%d days", d)
		switch {
		case d < 0:
			stats[2].v = fmt.Sprintf("%d days late", -d)
		case d == 0:
			stats[2].v = "Today"
		}
	}
	for i, s := range stats {
		k.text(wkFont("regular", 19), 408+i*248, 428, wkFaint, s.l)
		k.text(wkFont("bold", 34), 408+i*248, 468, wkFG, s.v)
		k.btn(s.id, k.R(400+i*248, 400, 640+i*248, 480))
	}
	ln := ""
	if !p.Created.IsZero() {
		ln = "Started " + p.Created.In(displayZone).Format("2 Jan")
	}
	if has {
		due, _ := time.Parse("2006-01-02", p.effDue())
		ln += " · " + due.Format("Mon 2 Jan") + " deadline"
		if !p.Created.IsZero() && p.progress() > 0 && p.progress() < 1 {
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
		k.boxBtn("wk:start:"+p.ID, 191, 556, 531, 618, "timer", "Start timer", wkLime, wkOnLime)
	}
	k.boxBtn("wk:preport:"+p.ID, 549, 556, 891, 618, "file-text", "Client report", color.RGBA{}, wkFG)
	k.boxBtn("wk:type-open:pnote:"+p.ID, 909, 556, 1048, 618, "sticky-note", "Note", color.RGBA{}, wkFG)
	k.boxBtn("wk:sheet-open:proj:"+p.ID, 1066, 556, 1133, 618, "ellipsis", "", color.RGBA{}, wkFG)

	y := k.stepList(p, 674, false, now)
	if len(p.Practices) > 0 { // (a client project without any looks as it did)
		y = k.practiceCard(p, y+24, now)
	}

	// Hours against plan, and the latest time.
	y += 24
	k.card(image.Rect(wkX0, y, 676, y+400))
	k.cardHead(wkX0+28, y+44, "trending-up", "Burn-up", "· hours, plan vs actual")
	k.burnUp(image.Rect(236, y+95, 634, y+257), p, entries, now)
	ui.RoundRect(k.img, k.R(188, y+299, 204, y+315), 4, wkLime)
	k.text(wkFont("regular", 20), 213, y+315, wkText2, "Actual")
	ui.RoundRect(k.img, k.R(290, y+299, 306, y+315), 4, wkBlue)
	k.text(wkFont("regular", 20), 316, y+315, wkText2, "Planned")
	k.card(image.Rect(696, y, wkX1, y+400))
	k.cardHead(724, y+44, "timer", "Recent time", "")
	k.outline("wk:tab:2", "", "All", 1136, y+18, 48)
	ui.Fill(k.img, k.R(698, y+76, wkX1-2, y+78), wkLine)
	var mine []wkEntry
	for i := len(entries) - 1; i >= 0 && len(mine) < 3; i-- {
		if entries[i].ProjectID == p.ID {
			mine = append(mine, entries[i])
		}
	}
	ry := y + 78
	for i, en := range mine {
		k.entryRow(en, p, 696, ry, wkX1, now, false)
		ry += 96
		if i < len(mine)-1 {
			ui.Fill(k.img, k.R(698, ry, wkX1-2, ry+2), wkLine)
		}
	}
	if len(mine) == 0 {
		k.emptyNote(724, ry+54, "No time on it yet.")
	}
	return y + 400
}

// entryRow is a time entry: what, when, how long; a tap edits it.
func (k *wk) entryRow(en wkEntry, p wkProject, x0, ry, x1 int, now time.Time, wide bool) {
	cy := ry + 48
	ui.RoundRect(k.img, k.R(x0+28, cy-26, x0+80, cy+26), 12, blend(wkCard, wkBlue, 0.16))
	lucideIcon(k.img, "timer", x0+54, k.Y(cy), 24, wkBlue)
	note := en.Note
	if note == "" {
		note = p.Name
	}
	tw := x1 - x0 - 230
	k.text(wkFont("semi", 22), x0+98, cy-4, wkFG, clip(wkFont("semi", 22), note, tw))
	when := en.Start.In(displayZone).Format("Mon 2")
	if dayStart(en.Start).Equal(dayStart(now)) {
		when = "Today"
	}
	end := "now"
	if !en.End.IsZero() {
		end = en.End.In(displayZone).Format("15:04")
	}
	sub := fmt.Sprintf("%s · %s – %s", when, en.Start.In(displayZone).Format("15:04"), end)
	if wide && !en.Billable {
		sub += " · internal"
	}
	k.text(wkFont("regular", 19), x0+98, cy+24, wkFaint, sub)
	dd := en.dur(now)
	k.textR(wkFont("bold", 23), x1-28, cy+8, wkFG, fmt.Sprintf("%d:%02d", int(dd.Hours()), int(dd.Minutes())%60))
	k.btn("wk:sheet-open:entry:"+en.ID, k.R(x0, ry, x1, ry+96))
}

// stepList is a project's steps: tick them, tap one for what can be done with it, add one.
func (k *wk) stepList(p wkProject, y int, all bool, now time.Time) int {
	left := 0
	for _, s := range p.Steps {
		if !s.Done {
			left++
		}
	}
	title := "Next steps"
	if all {
		title = "Steps"
	}
	sc := image.Rect(wkX0, y, wkX1, y+92+84*max(len(p.Steps), 1))
	k.card(sc)
	e := k.cardHead(wkX0+28, y+53, "list-checks", title, "")
	k.text(wkFont("regular", 19), e+14, y+53, wkFaint, fmt.Sprintf("%d of %d done · %d left", p.done(), len(p.Steps), left))
	k.outline("wk:type-open:step:"+p.ID, "plus", "Add step", 1136, y+22, 50)
	ry := y + 92
	open, isOpen := wkOpenStepWork(p.ID)
	for _, s := range p.Steps {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		cy := ry + 42
		k.checkbox(188, cy, s.Done)
		k.btn("wk:step:"+p.ID+":"+s.ID, k.R(wkX0, ry, 236, ry+84))
		k.struck(240, cy+8, s.Label, s.Done, 700)
		when := ""
		switch {
		case isOpen && open == s.ID:
			when = "working…"
		case s.Done && !s.At.IsZero():
			when = s.At.In(displayZone).Format("2 Jan")
			if dayStart(s.At).Equal(dayStart(now)) {
				when = "Today"
			}
		case s.WorkMs > 0:
			when = fmt.Sprintf("%.1f h", float64(s.WorkMs)/3.6e6)
		}
		if d, n := s.itemsDone(); n > 0 && !s.Done {
			when = strings.TrimSpace(fmt.Sprintf("%s  %d/%d", when, d, n))
		}
		col := wkMuted
		if when == "working…" {
			col = wkGreen
		}
		k.textR(wkFont("regular", 20), 1093, cy+7, col, when)
		k.icon("ellipsis", 1120, cy, 24, wkFaint)
		k.btn("wk:sheet-open:step:"+p.ID+":"+s.ID, k.R(236, ry, wkX1, ry+84))
		ry += 84
	}
	if len(p.Steps) == 0 {
		k.emptyNote(190, ry+50, "No steps yet: Add step breaks the work into pieces you can tick.")
	}
	return sc.Max.Y
}

// wkOpenStepWork is the step being worked on with ▶ in a project (step-work.ts), if any.
func wkOpenStepWork(pid string) (string, bool) {
	o := somaObj("projects#" + pid)
	steps, _ := o["steps"].([]any)
	for _, s := range steps {
		so, _ := s.(obj)
		work, _ := so["work"].([]any)
		for _, w := range work {
			if wo, ok := w.(obj); ok && num(wo["end"]) == 0 && num(wo["start"]) > 0 {
				return str(so["id"]), true
			}
		}
	}
	return "", false
}

// projectTime is the Time tab: the timer, the totals, every entry (tap to change one).
func (k *wk) projectTime(p wkProject, entries []wkEntry, now time.Time) int {
	run, running := wkRunning(entries)
	var mine []wkEntry
	var total, week, bill time.Duration
	mon := weekStart(now)
	for _, e := range entries {
		if e.ProjectID != p.ID {
			continue
		}
		mine = append(mine, e)
		d := e.dur(now)
		total += d
		if !e.Start.Before(mon) {
			week += d
		}
		if e.Billable {
			bill += d
		}
	}
	box := k.row4(312)
	k.small(box(0), "clock", "Logged", fmt.Sprintf("%.1f", total.Hours()), "h", fmt.Sprintf("%d entries", len(mine)), wkFG)
	k.small(box(1), "calendar", "This week", fmt.Sprintf("%.1f", week.Hours()), "h", "since Monday", wkFG)
	k.small(box(2), "pound-sterling", "Billable", wkMoney(bill.Hours()*p.Rate), "", fmt.Sprintf("for %.1f h", bill.Hours()), wkLime)
	rate := "Not set"
	if p.Rate > 0 {
		rate = wkMoney(p.Rate)
	}
	k.small(box(3), "circle-dollar-sign", "Rate / hour", rate, "", "tap to change", wkFG)
	k.btn("wk:type-open:prate:"+p.ID, k.R(box(3).Min.X, box(3).Min.Y, box(3).Max.X, box(3).Max.Y))
	y := 496
	if running && run.ProjectID == p.ID {
		dd := run.dur(now)
		k.boxBtn("wk:stop", wkX0, y, 640, y+66, "square", fmt.Sprintf("Stop timer · %02d:%02d:%02d", int(dd.Hours()), int(dd.Minutes())%60, int(dd.Seconds())%60), wkRed, rgb(0xffffff))
	} else {
		k.boxBtn("wk:start:"+p.ID, wkX0, y, 640, y+66, "play", "Start timer", wkLime, wkOnLime)
	}
	k.boxBtn("wk:sheet-open:addtime:"+p.ID, 660, y, wkX1, y+66, "plus", "Add time by hand", color.RGBA{}, wkFG)
	y += 92
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].Start.After(mine[j].Start) })
	if len(mine) > 60 {
		mine = mine[:60]
	}
	lc := image.Rect(wkX0, y, wkX1, y+80+96*max(len(mine), 1))
	k.card(lc)
	k.cardHead(wkX0+28, y+48, "list", "Entries", "· newest first, tap one to change it")
	ry := y + 80
	for _, en := range mine {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.entryRow(en, p, wkX0, ry, wkX1, now, true)
		if !en.Billable {
			k.badge(960, ry+48, "Internal", wkMuted)
		}
		ry += 96
	}
	if len(mine) == 0 {
		k.emptyNote(188, ry+54, "No time yet. Start the timer, or add time you've already spent.")
	}
	return lc.Max.Y
}

// projectNotes is the brief and the running notes.
func (k *wk) projectNotes(p wkProject, now time.Time) int {
	y := 312
	f := wkFont("regular", 21)
	brief := wkWrap(f, strings.Join(strings.Fields(p.Note), " "), 900, 6)
	bc := image.Rect(wkX0, y, wkX1, y+110+32*max(len(brief), 1))
	k.card(bc)
	k.cardHead(wkX0+28, y+48, "file-text", "Brief", "· scope, contacts, links")
	k.outline("wk:type-open:pbrief:"+p.ID, "pencil", "Edit", 1136, y+18, 50)
	ty := y + 100
	for _, l := range brief {
		k.text(f, 188, ty, wkText2, l)
		ty += 32
	}
	if len(brief) == 0 {
		k.emptyNote(188, ty, "No brief yet.")
	}
	y = bc.Max.Y + 24
	nc := image.Rect(wkX0, y, wkX1, y+90+120*max(len(p.Log), 1))
	k.card(nc)
	k.cardHead(wkX0+28, y+48, "sticky-note", "Notes", fmt.Sprintf("· %d, newest first", len(p.Log)))
	k.outline("wk:type-open:pnote:"+p.ID, "plus", "Add note", 1136, y+18, 50)
	ry := y + 86
	for _, n := range p.Log {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		meta := n.At.In(displayZone).Format("Mon 2 Jan 2006")
		if n.Source != "" {
			meta += " · " + n.Source
		}
		k.text(wkFont("semi", 18), 188, ry+36, wkFaint, clip(wkFont("semi", 18), meta, 880))
		lines := wkWrap(f, n.Text, 880, 2)
		for i, l := range lines {
			col := wkFG
			if strings.HasPrefix(n.Text, "Done: ") {
				col = wkMuted
			}
			k.text(f, 188, ry+70+i*30, col, l)
		}
		if n.Source == "" {
			k.icon("ellipsis", 1120, ry+60, 24, wkFaint)
			k.btn("wk:sheet-open:pnote:"+p.ID+":"+n.ID, k.R(wkX0, ry, wkX1, ry+120))
		}
		ry += 120
	}
	if len(p.Log) == 0 {
		k.emptyNote(188, ry+50, "What was done and how it went. Work log activities file themselves here too.")
	}
	return nc.Max.Y
}

// projectFindings is a project's findings, open ones first.
func (k *wk) projectFindings(p wkProject) int {
	var list []wkFinding
	for _, f := range wkFindings() {
		if f.ProjectID == p.ID {
			list = append(list, f)
		}
	}
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].closed() != list[j].closed() {
			return !list[i].closed()
		}
		return rank[list[i].Severity] < rank[list[j].Severity]
	})
	y := 312
	k.boxBtn("wk:type-open:pfinding:"+p.ID, wkX0, y, 520, y+64, "plus", "Log a finding", wkLime, wkOnLime)
	y += 90
	return k.findingList(list, map[string]string{p.ID: p.Name}, y)
}

// findingList draws findings as rows; a tap opens what can be changed.
func (k *wk) findingList(list []wkFinding, name map[string]string, y int) int {
	lc := image.Rect(wkX0, y, wkX1, y+20+96*max(len(list), 1))
	k.card(lc)
	ry := y + 10
	for i, f := range list {
		if i > 0 {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		}
		cy := ry + 48
		col := sevCol[f.Severity]
		if col.A == 0 {
			col = wkMuted
		}
		ui.RoundRect(k.img, k.R(188, cy-26, 240, cy+26), 12, blend(wkCard, col, 0.16))
		lucideIcon(k.img, "shield-alert", 214, k.Y(cy), 26, col)
		tc := wkFG
		if f.closed() {
			tc = wkMuted
		}
		k.text(wkFont("semi", 22), 259, cy-4, tc, clip(wkFont("semi", 22), f.Title, 560))
		sub := strings.Trim(name[f.ProjectID]+" · "+f.Area, " ·")
		if f.Due != "" {
			sub = strings.TrimPrefix(sub+" · fix by "+dayLabel(f.Due), " · ")
		}
		k.text(wkFont("regular", 19), 259, cy+24, wkFaint, clip(wkFont("regular", 19), sub, 560))
		st := map[string]string{"open": "Open", "in-progress": "In progress", "resolved": "Resolved", "accepted": "Accepted"}[f.Status]
		if st == "" {
			st = "Open"
		}
		x := k.badge(1136, cy, st, map[bool]color.RGBA{true: wkGreen, false: wkText2}[f.closed()])
		sev := f.Severity
		if sev == "" {
			sev = "medium"
		}
		k.badge(x-12, cy, strings.ToUpper(sev[:1])+sev[1:], col)
		k.btn("wk:sheet-open:finding:"+f.ID, k.R(wkX0, ry, wkX1, ry+96))
		ry += 96
	}
	if len(list) == 0 {
		k.emptyNote(188, ry+56, "No findings here.")
	}
	return lc.Max.Y
}

// --- sheets, actions and fields ----------------------------------------------------------------------

func wkTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func init() {
	// Opening a sheet or a field from a button: "wk:sheet-open:<spec>", "wk:type-open:<field>".
	wkActions["sheet-open"] = func(c *console, spec string) { c.openSheet(spec) }
	wkActions["type-open"] = func(c *console, field string) {
		name, arg, _ := strings.Cut(field, ":")
		prompt, initial := wkFieldPrompt(name, arg)
		c.wkType(field, prompt, initial)
	}

	wkActions["newproject"] = func(c *console, status string) {
		c.wkType("newproject:"+status, "New project's name", "")
	}
	wkTypers["newproject"] = func(c *console, status, text string) {
		if text == "" {
			return
		}
		id := wkNewProject(text, c.somaUI.pf.client)
		if status != "" && status != "active" {
			wkSetProject(id, "status", status)
		}
		c.wkGo("project", "")
		c.somaUI.workProj = id
		c.setScroll(modeSoma, 0)
		c.toastMsg("Project added")
	}
	wkActions["psearch"] = func(c *console, _ string) {
		c.wkType("psearch:", "Find a project or a client", c.somaUI.pf.query)
	}
	wkTypers["psearch"] = func(c *console, _, text string) {
		c.somaUI.pf.query = text
		c.setScroll(modeSoma, 0)
	}
	wkActions["pquery"] = func(c *console, _ string) { c.somaUI.pf.query = "" }

	// Sorting.
	wkSheets["sort"] = func(k *wk, _ string) *wkSheet {
		f := k.c.somaUI.pf
		sh := &wkSheet{title: "Sort projects", sub: "Tap the chosen one again to turn the order round"}
		for _, s := range wkSortKeys {
			on := f.sort == s.id || (f.sort == "" && s.id == "due")
			d := ""
			if on {
				d = map[bool]string{false: "↑ first to last", true: "↓ last to first"}[f.desc]
			}
			sh.rows = append(sh.rows, wkRow{id: "wk:psort:" + s.id, label: s.label, icon: s.icon, detail: d, sel: on})
		}
		return sh
	}
	wkActions["psort"] = func(c *console, key string) {
		f := &c.somaUI.pf
		cur := f.sort
		if cur == "" {
			cur = "due"
		}
		if cur == key {
			f.desc = !f.desc
		} else {
			f.sort, f.desc = key, false
		}
		c.somaUI.toTop = true
	}

	// The client filter.
	wkSheets["clients"] = func(k *wk, _ string) *wkSheet {
		f := k.c.somaUI.pf
		n := map[string]int{}
		for _, p := range wkProjects() {
			n[strings.TrimSpace(p.Client)]++
		}
		sh := &wkSheet{title: "Show projects for"}
		sh.rows = append(sh.rows, wkRow{id: "wk:pclient:", label: "All clients", icon: "layout-grid", sel: f.client == ""})
		for _, cl := range wkClients() {
			sh.rows = append(sh.rows, wkRow{id: "wk:pclient:" + cl, label: cl, icon: "building-2", detail: fmt.Sprint(n[cl]), sel: f.client == cl})
		}
		return sh
	}
	wkActions["pclient"] = func(c *console, cl string) {
		c.somaUI.pf.client = cl
		c.somaUI.toTop = true
	}

	// Everything about a project.
	wkSheets["proj"] = func(k *wk, id string) *wkSheet {
		p, ok := wkProjectByID(id)
		if !ok {
			return nil
		}
		sh := &wkSheet{title: p.Name, sub: fmt.Sprintf("%d of %d steps · started %s", p.done(), len(p.Steps), p.Created.In(displayZone).Format("2 Jan 2006"))}
		if k.c.somaUI.work != "project" || k.c.somaUI.workProj != id {
			sh.rows = append(sh.rows, wkRow{id: "wk:proj:" + id, label: "Open project", icon: "folder-open"})
		}
		client := p.Client
		if client == "" {
			client = "None"
		}
		due := "No date"
		if p.Due != "" {
			due = wkDate(p.Due)
		}
		rate := "Not set"
		if p.Rate > 0 {
			rate = wkMoney(p.Rate) + " / h"
		}
		var st []wkOpt
		for _, s := range wkStatuses {
			st = append(st, wkOpt{id: "wk:pset:" + id + ":status:" + s.id, label: s.label, sel: p.Status == s.id, col: statusCol(s.id)})
		}
		var pr []wkOpt
		for _, v := range []string{"high", "medium", "low"} {
			pr = append(pr, wkOpt{id: "wk:pset:" + id + ":priority:" + v, label: wkTitle(v), sel: p.Priority == v})
		}
		var sw []wkOpt
		for _, hex := range wkProjectColours {
			sw = append(sw, wkOpt{id: "wk:pset:" + id + ":color:" + hex, col: somaColour(hex, wkBlue), sel: strings.EqualFold(p.Color, hex)})
		}
		sh.rows = append(sh.rows,
			wkRow{id: "wk:type-open:prename:" + id, label: "Name", icon: "pencil", detail: p.Name},
			wkRow{kind: "seg", label: "Kind", opts: []wkOpt{{id: "wk:pkind:" + id + ":client", label: "Client work", sel: !p.plan()}, {id: "wk:pkind:" + id + ":plan", label: "Plan", sel: p.plan()}}})
		if p.plan() {
			sh.rows = append(sh.rows,
				wkRow{kind: "seg", label: "Status", opts: st},
				wkRow{id: "wk:pdue:" + id, label: "Deadline", icon: "calendar-clock", detail: due},
				wkRow{id: "wk:type-open:pplan:" + id, label: "Paste plan", icon: "file-text", detail: "date | phase | step | flags"},
				wkRow{id: "wk:padd:" + id, label: "Add daily practice", icon: "repeat", detail: "every day until a date"},
				wkRow{id: "wk:type-open:pbrief:" + id, label: "Brief", icon: "file-text", detail: clip(wkFont("regular", 21), p.Note, 280)},
				wkRow{kind: "swatches", opts: sw},
				wkRow{id: "wk:pdelete:" + id, label: "Delete project", icon: "trash-2", danger: true})
			return sh
		}
		sh.rows = append(sh.rows,
			wkRow{id: "wk:sheet-open:pclientof:" + id, label: "Client", icon: "building-2", detail: client},
			wkRow{kind: "seg", label: "Status", opts: st},
			wkRow{kind: "seg", label: "Priority", opts: pr},
			wkRow{id: "wk:pdue:" + id, label: "Deadline", icon: "calendar-clock", detail: due},
			wkRow{id: "wk:type-open:prate:" + id, label: "Rate per hour", icon: "circle-dollar-sign", detail: rate},
			wkRow{id: "wk:type-open:pbrief:" + id, label: "Brief", icon: "file-text", detail: clip(wkFont("regular", 21), p.Note, 280)},
			wkRow{kind: "swatches", opts: sw},
			wkRow{id: "wk:padd:" + id, label: "Add daily practice", icon: "repeat", detail: "every day until a date"},
			wkRow{id: "wk:pdelete:" + id, label: "Delete project", icon: "trash-2", danger: true},
		)
		return sh
	}
	wkActions["pset"] = func(c *console, arg string) { // <id>:<key>:<value>
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 {
			return
		}
		id, key, v := parts[0], parts[1], parts[2]
		if key == "priority" {
			if p, ok := wkProjectByID(id); ok && p.Priority == v {
				v = "" // the chosen one again: none
			}
		}
		wkSetProject(id, key, v)
		c.somaUI.keep = true
	}
	wkSheets["pstatus"] = func(k *wk, id string) *wkSheet {
		p, ok := wkProjectByID(id)
		if !ok {
			return nil
		}
		sh := &wkSheet{title: "Move " + p.Name + " to"}
		for _, s := range wkStatuses {
			sh.rows = append(sh.rows, wkRow{id: "wk:pmove:" + id + ":" + s.id, label: s.label, col: statusCol(s.id), sel: p.Status == s.id})
		}
		return sh
	}
	wkActions["pmove"] = func(c *console, arg string) {
		id, st, _ := strings.Cut(arg, ":")
		wkSetProject(id, "status", st)
		if st == "done" {
			wkSetProject(id, "touchedAt", nowMs())
		}
	}
	wkActions["prestore"] = func(c *console, id string) {
		wkSetProject(id, "status", "active")
		c.toastMsg("Back in Active")
	}
	wkActions["pdelete"] = func(c *console, id string) {
		o, at := somaRemove("projects", id)
		if o == nil {
			return
		}
		if c.somaUI.work == "project" && c.somaUI.workProj == id {
			c.wkGo("projects", "")
		}
		c.offerUndo("Deleted “"+clip(wkFont("semi", 22), str(o["name"]), 300)+"”", func() { somaRestore("projects", o, at) })
	}
	wkActions["pdue"] = func(c *console, id string) {
		p, _ := wkProjectByID(id)
		c.wkPickDate("pdue:"+id, p.Due, "Deadline for "+p.Name)
	}
	wkDateSetters["pdue"] = func(c *console, id, date string) { wkSetProject(id, "due", date) }
	wkActions["preport"] = func(c *console, id string) {
		p, _ := wkProjectByID(id)
		c.wkGo("reports", "")
		clients := wkReportClients()
		for i, cl := range clients {
			if cl == p.Client {
				if c.somaUI.workSel == nil {
					c.somaUI.workSel = map[string]string{}
				}
				c.somaUI.workSel["reportswk:client"] = fmt.Sprint(i)
			}
		}
	}

	// A project's client: one of the clients, a new one, or none.
	wkSheets["pclientof"] = func(k *wk, id string) *wkSheet {
		p, ok := wkProjectByID(id)
		if !ok {
			return nil
		}
		sh := &wkSheet{title: "Client of " + p.Name}
		for _, cl := range wkClients() {
			sh.rows = append(sh.rows, wkRow{id: "wk:psetclient:" + id + ":" + cl, label: cl, icon: "building-2", sel: p.Client == cl})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:type-open:pnewclient:" + id, label: "New client…", icon: "plus"},
			wkRow{id: "wk:psetclient:" + id + ":", label: "No client", icon: "x", sel: p.Client == ""})
		return sh
	}
	wkActions["psetclient"] = func(c *console, arg string) {
		id, cl, _ := strings.Cut(arg, ":")
		wkSetProject(id, "client", cl)
	}

	// A step.
	wkSheets["step"] = func(k *wk, arg string) *wkSheet {
		pid, sid, _ := strings.Cut(arg, ":")
		p, ok := wkProjectByID(pid)
		if !ok {
			return nil
		}
		for i, s := range p.Steps {
			if s.ID != sid {
				continue
			}
			sh := &wkSheet{title: s.Label, sub: fmt.Sprintf("Step %d of %d · %s", i+1, len(p.Steps), p.Name)}
			tick := wkRow{id: "wk:step:" + pid + ":" + sid, label: "Mark as done", icon: "circle-check", col: wkGreen}
			if s.Done {
				tick = wkRow{id: "wk:step:" + pid + ":" + sid, label: "Mark as not done", icon: "rotate-ccw"}
			}
			date, phase, wait := "No date", "None", "No one"
			if s.Due != "" {
				date = wkDate(s.Due)
			}
			if s.Phase != "" {
				phase = s.Phase
			}
			if s.WaitingOn != "" {
				wait = s.WaitingOn
			}
			sh.rows = append(sh.rows, tick, wkRow{id: "wk:type-open:steprename:" + arg, label: "Rename", icon: "pencil"},
				wkRow{id: "wk:sdue:" + arg, label: "Date", icon: "calendar", detail: date},
				wkRow{id: "wk:smile:" + arg, label: "Milestone (a fixed date, not work)", icon: "flag", sel: s.Milestone},
				wkRow{id: "wk:type-open:sphase:" + arg, label: "Phase", icon: "list-checks", detail: phase},
				wkRow{id: "wk:type-open:swait:" + arg, label: "Waiting on", icon: "hourglass", detail: wait})
			after := "Nothing"
			for _, b := range p.Steps {
				if b.ID == s.After {
					after = b.Label
				}
			}
			sh.rows = append(sh.rows, wkRow{id: "wk:sheet-open:safter:" + arg, label: "Comes after", icon: "arrow-right", detail: after})
			anyDone := false
			for _, it := range s.Items {
				sh.rows = append(sh.rows, wkRow{id: "wk:sitem:" + arg + ":" + it.ID, label: it.Text, icon: map[bool]string{true: "circle-check", false: "circle"}[it.Done], sel: it.Done})
				anyDone = anyDone || it.Done
			}
			sh.rows = append(sh.rows, wkRow{id: "wk:type-open:sitemadd:" + arg, label: "Add to its checklist", icon: "plus"})
			if anyDone {
				sh.rows = append(sh.rows, wkRow{id: "wk:sitemclear:" + arg, label: "Remove ticked items", icon: "trash-2"})
			}
			if open, isOpen := wkOpenStepWork(pid); isOpen && open == sid {
				sh.rows = append(sh.rows, wkRow{id: "wk:stepwork:" + arg, label: "Stop working on it", icon: "square", col: wkRed})
			} else if !s.Done {
				sh.rows = append(sh.rows, wkRow{id: "wk:steptimer:" + arg, label: "Start the timer on it", icon: "play", col: wkLime})
			}
			if i > 0 {
				sh.rows = append(sh.rows, wkRow{id: "wk:stepmove:" + arg + ":-1", label: "Move up", icon: "arrow-up"})
			}
			if i < len(p.Steps)-1 {
				sh.rows = append(sh.rows, wkRow{id: "wk:stepmove:" + arg + ":1", label: "Move down", icon: "arrow-down"})
			}
			sh.rows = append(sh.rows, wkRow{id: "wk:stepdel:" + arg, label: "Delete step", icon: "trash-2", danger: true})
			return sh
		}
		return nil
	}
	wkActions["stepmove"] = func(c *console, arg string) {
		parts := strings.Split(arg, ":")
		if len(parts) == 3 {
			d, _ := strconv.Atoi(parts[2])
			wkMoveStep(parts[0], parts[1], d)
			c.somaUI.keep = true
		}
	}
	wkActions["stepdel"] = func(c *console, arg string) {
		pid, sid, _ := strings.Cut(arg, ":")
		before := somaObj("projects#" + pid)
		wkRemoveStep(pid, sid)
		c.offerUndo("Step deleted", func() {
			if before != nil {
				somaPut("projects#"+pid, before)
			}
		})
	}
	wkActions["steptimer"] = func(c *console, arg string) {
		pid, sid, _ := strings.Cut(arg, ":")
		p, _ := wkProjectByID(pid)
		label := ""
		for _, s := range p.Steps {
			if s.ID == sid {
				label = s.Label
			}
		}
		wkStartTimerOn(pid, label, sid)
	}
	wkActions["stepwork"] = func(c *console, arg string) { wkStopStepWork() }

	// A note in the log.
	wkSheets["pnote"] = func(k *wk, arg string) *wkSheet {
		pid, nid, _ := strings.Cut(arg, ":")
		p, _ := wkProjectByID(pid)
		for _, n := range p.Log {
			if n.ID == nid {
				return &wkSheet{title: "Note", sub: n.At.In(displayZone).Format("Monday 2 January 2006"), rows: []wkRow{
					{kind: "text", detail: n.Text},
					{id: "wk:pnotedel:" + arg, label: "Delete note", icon: "trash-2", danger: true},
				}}
			}
		}
		return nil
	}
	wkActions["pnotedel"] = func(c *console, arg string) {
		pid, nid, _ := strings.Cut(arg, ":")
		before := somaObj("projects#" + pid)
		wkRemoveProjectNote(pid, nid)
		c.offerUndo("Note deleted", func() { somaPut("projects#"+pid, before) })
	}

	// A time entry.
	wkSheets["entry"] = func(k *wk, id string) *wkSheet {
		var en wkEntry
		found := false
		for _, e := range wkEntries() {
			if e.ID == id {
				en, found = e, true
			}
		}
		if !found {
			return nil
		}
		p, _ := wkProjectByID(en.ProjectID)
		now := time.Now()
		note := en.Note
		if note == "" {
			note = "No note"
		}
		d := en.dur(now)
		sh := &wkSheet{title: fmt.Sprintf("%d:%02d on %s", int(d.Hours()), int(d.Minutes())%60, p.Name),
			sub: en.Start.In(displayZone).Format("Monday 2 January")}
		end := "running"
		if !en.End.IsZero() {
			end = en.End.In(displayZone).Format("15:04")
		}
		sh.rows = append(sh.rows,
			wkRow{id: "wk:type-open:enote:" + id, label: "Note", icon: "pencil", detail: note},
			wkRow{id: "wk:sheet-open:eproj:" + id, label: "Project", icon: "folder-kanban", detail: p.Name},
			wkRow{kind: "seg", opts: []wkOpt{{id: "wk:eset:" + id + ":billable:1", label: "Billable", sel: en.Billable}, {id: "wk:eset:" + id + ":billable:0", label: "Internal", sel: !en.Billable}}},
			wkRow{kind: "stepper", id: "wk:type-open:etime:" + id + "|start", label: "Started", detail: en.Start.In(displayZone).Format("15:04"), minus: "wk:eset:" + id + ":start:-15", plus: "wk:eset:" + id + ":start:15"},
		)
		if en.End.IsZero() {
			sh.rows = append(sh.rows, wkRow{id: "wk:stop", label: "Stop it now", icon: "square", col: wkRed})
		} else {
			sh.rows = append(sh.rows, wkRow{kind: "stepper", id: "wk:type-open:etime:" + id + "|end", label: "Ended", detail: end, minus: "wk:eset:" + id + ":end:-15", plus: "wk:eset:" + id + ":end:15"})
		}
		sh.rows = append(sh.rows,
			wkRow{id: "wk:edate:" + id, label: "Day", icon: "calendar", detail: wkDate(somaDate(en.Start))},
			wkRow{id: "wk:edel:" + id, label: "Delete entry", icon: "trash-2", danger: true})
		return sh
	}
	wkActions["eset"] = func(c *console, arg string) { // <id>:<what>:<value>
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 {
			return
		}
		id, what, v := parts[0], parts[1], parts[2]
		c.somaUI.keep = true
		somaPatch("timeEntries", id, func(o obj) {
			switch what {
			case "billable":
				if v == "1" {
					delete(o, "billable")
				} else {
					o["billable"] = false
				}
			case "start", "end":
				m, _ := strconv.Atoi(v)
				t := num(o[what]) + float64(m)*60000
				if what == "start" && num(o["end"]) > 0 {
					t = min(t, num(o["end"])-60000)
				}
				if what == "end" {
					t = max(t, num(o["start"])+60000)
					t = min(t, nowMs())
				}
				o[what] = t
			}
		})
	}
	wkActions["edel"] = func(c *console, id string) {
		o, at := somaRemove("timeEntries", id)
		if o != nil {
			c.offerUndo("Entry deleted", func() { somaRestore("timeEntries", o, at) })
		}
	}
	wkActions["edate"] = func(c *console, id string) {
		o := somaObj("timeEntries#" + id)
		c.wkPickDate("edate:"+id, somaDate(msTime(o["start"])), "Move the entry to")
	}
	wkDateSetters["edate"] = func(c *console, id, date string) {
		if date == "" {
			return
		}
		somaPatch("timeEntries", id, func(o obj) {
			st := msTime(o["start"]).In(displayZone)
			d, err := time.ParseInLocation("2006-01-02", date, displayZone)
			if err != nil {
				return
			}
			shift := float64(d.Sub(dayStart(st)).Milliseconds())
			o["start"] = num(o["start"]) + shift
			if num(o["end"]) > 0 {
				o["end"] = num(o["end"]) + shift
			}
		})
	}
	wkSheets["eproj"] = func(k *wk, id string) *wkSheet {
		o := somaObj("timeEntries#" + id)
		sh := &wkSheet{title: "Which project"}
		for _, p := range wkProjects() {
			if p.Status == "done" && p.ID != str(o["projectId"]) {
				continue
			}
			sh.rows = append(sh.rows, wkRow{id: "wk:eproj:" + id + ":" + p.ID, label: p.Name, col: wkColour(p, p.order), detail: p.Client, sel: p.ID == str(o["projectId"])})
		}
		return sh
	}
	wkActions["eproj"] = func(c *console, arg string) {
		id, pid, _ := strings.Cut(arg, ":")
		somaPatch("timeEntries", id, func(o obj) { o["projectId"] = pid })
	}
	wkSheets["addtime"] = func(k *wk, pid string) *wkSheet {
		p, ok := wkProjectByID(pid)
		if !ok {
			return nil
		}
		sh := &wkSheet{title: "Add time to " + p.Name, sub: "Time you've spent, ending now"}
		for _, m := range []int{15, 30, 45, 60, 90, 120, 180, 240} {
			l := fmt.Sprintf("%d minutes", m)
			if m >= 60 {
				l = strings.TrimSuffix(fmt.Sprintf("%g hours", float64(m)/60), "")
				if m == 60 {
					l = "1 hour"
				}
			}
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:addtime:%s:%d", pid, m), label: l, icon: "clock"})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:type-open:addtime:" + pid, label: "Other…", icon: "pencil", detail: "1:30, or 9:00-10:30"})
		return sh
	}
	wkActions["addtime"] = func(c *console, arg string) {
		pid, m, _ := strings.Cut(arg, ":")
		mins, _ := strconv.Atoi(m)
		end := time.Now()
		wkAddEntry(pid, end.Add(-time.Duration(mins)*time.Minute), end, "")
		c.toastMsg(fmt.Sprintf("%d:%02d added", mins/60, mins%60))
	}

	// A finding.
	wkSheets["finding"] = func(k *wk, id string) *wkSheet {
		var f wkFinding
		ok := false
		for _, x := range wkFindings() {
			if x.ID == id {
				f, ok = x, true
			}
		}
		if !ok {
			return nil
		}
		p, _ := wkProjectByID(f.ProjectID)
		sh := &wkSheet{title: f.Title, sub: p.Name}
		var st, sv []wkOpt
		for _, s := range []struct{ id, l string }{{"open", "Open"}, {"in-progress", "In progress"}, {"resolved", "Resolved"}, {"accepted", "Accepted"}} {
			st = append(st, wkOpt{id: "wk:fset:" + id + ":status:" + s.id, label: s.l, sel: f.Status == s.id || (f.Status == "" && s.id == "open")})
		}
		for _, s := range []string{"critical", "high", "medium", "low"} {
			sv = append(sv, wkOpt{id: "wk:fset:" + id + ":severity:" + s, label: wkTitle(s), sel: f.Severity == s, col: sevCol[s]})
		}
		due := "No date"
		if f.Due != "" {
			due = wkDate(f.Due)
		}
		area := f.Area
		if area == "" {
			area = "—"
		}
		sh.rows = append(sh.rows,
			wkRow{kind: "seg", label: "Status", opts: st},
			wkRow{kind: "seg", label: "Severity", opts: sv},
			wkRow{id: "wk:type-open:frename:" + id, label: "Title", icon: "pencil", detail: f.Title},
			wkRow{id: "wk:type-open:farea:" + id, label: "Where", icon: "file", detail: area},
			wkRow{id: "wk:fdue:" + id, label: "Fix by", icon: "calendar-clock", detail: due},
			wkRow{id: "wk:sheet-open:fproj:" + id, label: "Project", icon: "folder-kanban", detail: p.Name},
			wkRow{id: "wk:fdel:" + id, label: "Delete finding", icon: "trash-2", danger: true},
		)
		return sh
	}
	wkActions["fset"] = func(c *console, arg string) {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 {
			return
		}
		if parts[1] == "status" {
			wkSetFindingStatus(parts[0], parts[2])
		} else {
			somaPatch("findings", parts[0], func(o obj) { o[parts[1]] = parts[2] })
		}
		c.somaUI.keep = true
	}
	wkActions["fdue"] = func(c *console, id string) {
		o := somaObj("findings#" + id)
		c.wkPickDate("fdue:"+id, str(o["due"]), "Fix by")
	}
	wkDateSetters["fdue"] = func(c *console, id, date string) {
		somaPatch("findings", id, func(o obj) {
			if date == "" {
				delete(o, "due")
			} else {
				o["due"] = date
			}
		})
	}
	wkActions["fdel"] = func(c *console, id string) {
		o, at := somaRemove("findings", id)
		if o != nil {
			c.offerUndo("Finding deleted", func() { somaRestore("findings", o, at) })
		}
	}
	wkSheets["fproj"] = func(k *wk, id string) *wkSheet {
		o := somaObj("findings#" + id)
		sh := &wkSheet{title: "Which project"}
		for _, p := range wkProjects() {
			sh.rows = append(sh.rows, wkRow{id: "wk:fproj:" + id + ":" + p.ID, label: p.Name, col: wkColour(p, p.order), sel: p.ID == str(o["projectId"])})
		}
		return sh
	}
	wkActions["fproj"] = func(c *console, arg string) {
		id, pid, _ := strings.Cut(arg, ":")
		somaPatch("findings", id, func(o obj) { o["projectId"] = pid })
	}

	// Typed fields.
	wkTypers["prename"] = func(c *console, id, text string) {
		if text != "" {
			wkSetProject(id, "name", text)
		}
	}
	wkTypers["pbrief"] = func(c *console, id, text string) { wkSetProject(id, "note", text) }
	wkTypers["prate"] = func(c *console, id, text string) {
		v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(text), ",", "."), 64)
		if err != nil || v <= 0 {
			wkSetProject(id, "rate", nil)
			return
		}
		wkSetProject(id, "rate", v)
	}
	wkTypers["pnote"] = func(c *console, id, text string) { wkAddProjectNote(id, text) }
	wkTypers["pnewclient"] = func(c *console, id, text string) {
		wkSetProject(id, "client", strings.TrimSpace(text))
		c.sheetPop() // past the list of clients, back to the project
	}
	wkTypers["step"] = func(c *console, id, text string) {
		if text == "" {
			return
		}
		somaAddStep(id, text)
		c.wkType("step:"+id, "Next step (or hide the keyboard)", "") // keep adding, as Enter does on the PC
	}
	wkTypers["steprename"] = func(c *console, arg, text string) {
		pid, sid, _ := strings.Cut(arg, ":")
		wkRenameStep(pid, sid, text)
	}
	wkTypers["enote"] = func(c *console, id, text string) {
		somaPatch("timeEntries", id, func(o obj) {
			if text == "" {
				delete(o, "note")
			} else {
				o["note"] = text
			}
		})
	}
	wkTypers["addtime"] = func(c *console, pid, text string) {
		start, end, ok := parseSpan(text, time.Now().In(displayZone))
		if !ok {
			c.toastMsg("Type 1:30 for an hour and a half, or 9:00-10:30")
			return
		}
		wkAddEntry(pid, start, end, "")
		c.toastMsg("Time added")
	}
	wkTypers["pfinding"] = func(c *console, pid, text string) { wkNewFinding(pid, text, "medium") }
	wkTypers["frename"] = func(c *console, id, text string) {
		if text != "" {
			somaPatch("findings", id, func(o obj) { o["title"] = text })
		}
	}
	wkTypers["farea"] = func(c *console, id, text string) {
		somaPatch("findings", id, func(o obj) {
			if text == "" {
				delete(o, "area")
			} else {
				o["area"] = text
			}
		})
	}

	// Holding a project's row or card: everything about it.
	wkLongPress["proj"] = func(c *console, id string) { c.openSheet("proj:" + id) }
}

// wkFieldPrompt is what a field asks for and what it starts with.
func wkFieldPrompt(name, arg string) (string, string) {
	switch name {
	case "prename":
		p, _ := wkProjectByID(arg)
		return "The project's name", p.Name
	case "pbrief":
		p, _ := wkProjectByID(arg)
		return "Scope, contacts, links…", p.Note
	case "prate":
		p, _ := wkProjectByID(arg)
		if p.Rate > 0 {
			return "Rate per hour (empty for none)", strconv.FormatFloat(p.Rate, 'f', -1, 64)
		}
		return "Rate per hour, e.g. 35", ""
	case "pnote":
		return "A note for the project", ""
	case "pnewclient":
		return "The client's name", ""
	case "step":
		return "A step, e.g. Send rev B", ""
	case "steprename":
		pid, sid, _ := strings.Cut(arg, ":")
		p, _ := wkProjectByID(pid)
		for _, s := range p.Steps {
			if s.ID == sid {
				return "The step", s.Label
			}
		}
	case "enote":
		return "What it was for", str(somaObj("timeEntries#" + arg)["note"])
	case "addtime":
		return "1:30 (ending now) or 9:00-10:30 (today)", ""
	case "pfinding":
		return "What was found, e.g. Lap length wrong on B12", ""
	case "frename":
		return "The finding", str(somaObj("findings#" + arg)["title"])
	case "farea":
		return "Where: a sheet, a grid line…", str(somaObj("findings#" + arg)["area"])
	}
	if f := wkPrompts[name]; f != nil {
		return f(arg)
	}
	return "", ""
}

// wkPrompts are the other pages' fields' prompts and first values.
var wkPrompts = map[string]func(arg string) (string, string){}

// wkLongPress handles a finger held on "wk:<name>:<arg>".
var wkLongPress = map[string]func(c *console, arg string){}

// parseSpan reads time typed by hand: "1:30" or "90m" or "1.5h" ending now, or "9:00-10:30" today.
func parseSpan(s string, now time.Time) (time.Time, time.Time, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	clock := func(t string) (int, bool) {
		h, m, ok := strings.Cut(t, ":")
		if !ok {
			h, m, ok = strings.Cut(t, "h")
		}
		hh, err := strconv.Atoi(h)
		if err != nil || hh < 0 || hh > 24 {
			return 0, false
		}
		mm := 0
		if ok && m != "" {
			if mm, err = strconv.Atoi(m); err != nil || mm < 0 || mm > 59 {
				return 0, false
			}
		}
		return hh*60 + mm, true
	}
	if a, b, ok := strings.Cut(s, "-"); ok {
		m0, ok0 := clock(a)
		m1, ok1 := clock(b)
		if !ok0 || !ok1 || m1 <= m0 {
			return time.Time{}, time.Time{}, false
		}
		d := dayStart(now)
		return d.Add(time.Duration(m0) * time.Minute), d.Add(time.Duration(m1) * time.Minute), true
	}
	mins := 0
	switch {
	case strings.HasSuffix(s, "m") && !strings.Contains(s, ":"):
		n, err := strconv.Atoi(strings.TrimSuffix(s, "m"))
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		mins = n
	case strings.HasSuffix(s, "h"):
		f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSuffix(s, "h"), ",", "."), 64)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		mins = int(math.Round(f * 60))
	default:
		m, ok := clock(s)
		if !ok {
			return time.Time{}, time.Time{}, false
		}
		mins = m
	}
	if mins <= 0 || mins > 12*60 {
		return time.Time{}, time.Time{}, false
	}
	return now.Add(-time.Duration(mins) * time.Minute), now, true
}

// wkStartTimerOn starts the timer on a project, for a step (startTimer with a note and step).
func wkStartTimerOn(pid, note, stepID string) {
	wkStopTimer()
	id := "te-" + somaNewID()
	o := obj{"id": id, "projectId": pid, "start": nowMs()}
	if note != "" {
		o["note"] = note
	}
	if stepID != "" {
		o["stepId"] = stepID
	}
	somaAdd("timeEntries", id, o)
}

// wkStopStepWork ends the step being worked on with ▶ (stopStepWork).
func wkStopStepWork() {
	for _, p := range wkProjects() {
		sid, ok := wkOpenStepWork(p.ID)
		if !ok {
			continue
		}
		wkProjectSteps(p.ID, false, func(steps []any) []any {
			if i := stepIndex(steps, sid); i >= 0 {
				work, _ := steps[i].(obj)["work"].([]any)
				for _, w := range work {
					if wo, ok := w.(obj); ok && num(wo["end"]) == 0 {
						wo["end"] = nowMs()
					}
				}
			}
			return steps
		})
	}
}
