package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	"margin-init/ui"
)

// The Work tab's Overview (Overview.tsx): the greeting, today against the day's target, the
// week's hours, value and projects, hours by project for the week, what needs attention, and
// the deadlines.

var wkPalette = []color.RGBA{wkBlue, wkGreen, wkPurple, wkYellow, wkAmber, rgb(0x4cc9f0), wkRed}

// wkColour is a project's colour: its own, or one from the palette by its place.
func wkColour(p wkProject, i int) color.RGBA {
	return somaColour(p.Color, wkPalette[i%len(wkPalette)])
}

// wkTargets: hours a day and a week, from Soma's settings (8 and 40 when unset).
func wkTargets() (day, week float64) {
	day, week = num(jsonValue(soma.get("settings.workDayHours"))), num(jsonValue(soma.get("settings.workWeekHours")))
	if day <= 0 {
		day = 8
	}
	if week <= 0 {
		week = 40
	}
	return
}

func greeting(now time.Time) string {
	switch h := now.In(displayZone).Hour(); {
	case h < 12:
		return "Good morning"
	case h < 18:
		return "Good afternoon"
	}
	return "Good evening"
}

// hoursLabel is "7.4h"-style: one decimal.
func hoursLabel(d time.Duration) string { return fmt.Sprintf("%.1f", d.Hours()) }

func (k *wk) overview() int {
	now := time.Now().In(displayZone)
	today := dayStart(now)
	mon := weekStart(now)
	projects := wkProjects()
	entries := wkEntries()
	todos := somaTodos()
	dayT, weekT := wkTargets()
	_, week := now.ISOWeek()

	// What's left this week: timed to-dos (meetings) and open to-dos.
	meetings, left := 0, 0
	sun := mon.AddDate(0, 0, 7).Format("2006-01-02")
	td := today.Format("2006-01-02")
	for _, t := range todos {
		if t.Done || t.Cleared {
			continue
		}
		d := t.Date
		if t.Due != "" {
			d = t.Due
		}
		if d >= mon.Format("2006-01-02") && d < sun || t.Scope == "week" {
			left++
			if t.SlotDate >= td && t.SlotDate < sun {
				meetings++
			}
		}
	}
	sub := fmt.Sprintf("Week %d · %d tasks left this week", week, left)
	if meetings > 0 {
		sub = fmt.Sprintf("Week %d · %d meetings and %d tasks left this week", week, meetings, left)
	}
	k.header([]string{wkUser() + " · Freelance", "Overview"}, greeting(now)+", "+strings.Fields(wkUser() + " x")[0], sub, color.RGBA{})
	x := k.primary("wk:create", "plus", "Create", wkX1)
	k.square("wk:bell", "bell-dot", x-13)
	k.tabs("wk:tab", []string{"Overview", "Results", "Clients", "Activity"}, nil, k.sel("tab", 0))
	rng := k.sel("chip", 0)
	k.chips("wk:chip", 308, []string{"This week", "Last week", "This month"}, []string{"calendar"}, rng)
	k.outline("wk:customize", "layout-grid", "Customize", wkX1, 308, 53)

	// Today against the target.
	hero := image.Rect(wkX0, 388, wkX1, 614)
	k.cardIn(hero, rgb(0x15171a))
	hr := hero.Sub(image.Pt(0, k.oy)).Inset(2)
	for x := hr.Min.X; x < hr.Min.X+520; x++ { // the lime light from the ring's side
		t := 1 - float64(x-hr.Min.X)/520
		for y := hr.Min.Y + 18; y < hr.Max.Y-18; y++ {
			glass(k.img, image.Rect(x, y, x+1, y+1), 0, wkLime, 0.05*t*t)
		}
	}
	todayH := wkSum(wkHours(entries, today, today.AddDate(0, 0, 1), now))
	k.arc(280, 501, 80, 18, todayH.Hours()/dayT, wkLime, rgb(0x2a2d24))
	k.textC(wkFont("bold", 34), 280, 507, wkFG, hoursLabel(todayH)+"h")
	k.textC(wkFont("medium", 16), 280, 532, wkMuted, fmt.Sprintf("of %gh today", dayT))
	k.text(wkFont("semi", 19), 398, 444, wkMuted, strings.ToUpper("Today · "+now.Format("Monday")))
	rest := dayT*60 - todayH.Minutes()
	head := "Target reached"
	switch {
	case rest > 90:
		head = fmt.Sprintf("%.1f hours to your target", rest/60)
	case rest > 0:
		head = fmt.Sprintf("%.0f minutes to your target", rest)
	}
	k.text(wkFont("bold", 31), 398, 490, wkFG, head)
	val, _, _ := wkValue(entries, projects, today, today.AddDate(0, 0, 1), now)
	doneT, allT := 0, 0
	var next *somaTodo
	for i, t := range todos {
		if t.Cleared {
			continue
		}
		if t.Date == td || t.Due == td || t.SlotDate == td {
			allT++
			if t.Done {
				doneT++
			}
		}
		if !t.Done && t.SlotDate != "" {
			at := t.SlotDate + fmt.Sprintf(" %04d", t.SlotStart)
			if at > now.Format("2006-01-02 1504") && (next == nil || at < next.SlotDate+fmt.Sprintf(" %04d", next.SlotStart)) {
				next = &todos[i]
			}
		}
	}
	stats := []struct{ label, value, after string }{
		{"Billable", wkMoney(val), ""}, {"Tasks", fmt.Sprintf("%d / %d", doneT, allT), ""}, {"Steps", "—", ""}, {"Next", "Nothing timed", ""},
	}
	if next != nil {
		stats[3].value = clip(wkFont("bold", 25), strings.Fields(next.Text + " x")[0], 150) + " " + clockOf(float64(next.SlotStart)/60)
		if next.SlotDate != td {
			if d, err := time.Parse("2006-01-02", next.SlotDate); err == nil {
				stats[3].after = d.Format("Mon")
			}
		}
	}
	sx := 398
	for _, s := range stats {
		k.text(wkFont("regular", 18), sx, 538, wkFaint, s.label)
		e := k.text(wkFont("bold", 25), sx, 572, wkFG, s.value)
		if s.after != "" {
			e = k.text(wkFont("regular", 17), e+6, 572, wkFaint, s.after)
		}
		sx = max(e, sx+ui.TextWidth(wkFont("regular", 18), s.label)) + 32
	}

	// The week's figures, against the same days last week.
	from, to := mon, mon.AddDate(0, 0, 7)
	switch rng {
	case 1:
		from, to = mon.AddDate(0, 0, -7), mon
	case 2:
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
		to = from.AddDate(0, 1, 0)
	}
	span := to.Sub(from)
	elapsed := min(now.Sub(from), span)
	wkH := wkSum(wkHours(entries, from, to, now))
	prevH := wkSum(wkHours(entries, from.Add(-span), from.Add(-span).Add(elapsed), now))
	v, _, _ := wkValue(entries, projects, from, to, now)
	pv, _, _ := wkValue(entries, projects, from.Add(-span), from.Add(-span).Add(elapsed), now)
	var daily, dailyV []float64
	for d := from; d.Before(to) && !d.After(now); d = d.AddDate(0, 0, 1) {
		h := wkSum(wkHours(entries, d, d.AddDate(0, 0, 1), now))
		daily = append(daily, h.Hours())
		dv, _, _ := wkValue(entries, projects, d, d.AddDate(0, 0, 1), now)
		dailyV = append(dailyV, dv)
	}
	cw := (wkX1 - wkX0 - 40) / 3
	target := weekT
	if rng == 2 {
		target = weekT * span.Hours() / 24 / 7
	}
	k.kpi(image.Rect(wkX0, 644, wkX0+cw, 817), "clock", "Hours this week", hoursLabel(wkH), "h",
		pctChange(prevH.Hours(), wkH.Hours()), wkH >= prevH, fmt.Sprintf("of %.0f h", target), daily, wkLime, wkFG)
	k.kpi(image.Rect(wkX0+cw+20, 644, wkX0+2*cw+20, 817), "pound-sterling", "Billable value", wkMoney(v), "",
		pctChange(pv, v), v >= pv, "vs last wk", dailyV, wkLime, wkFG)
	active, overdue := 0, 0
	for _, p := range projects {
		if p.Status == "active" {
			active++
			if d, ok := p.daysLeft(now); ok && d < 0 {
				overdue++
			}
		}
	}
	k.kpi(image.Rect(wkX0+2*cw+40, 644, wkX1, 817), "folder-kanban", "Active projects", fmt.Sprint(active), "",
		math.NaN(), true, fmt.Sprintf("%d overdue", overdue), []float64{1, 1}, wkBlue, wkFG)

	// Today's rings, as Soma draws them on the phone: calories, protein, water, burned.
	k.ringsCard(image.Rect(wkX0, 847, wkX1, 1147), somaToday())
	k.oy -= 320 // everything below moves down under it

	// Hours by project, a bar a day, stacked by project, with what's planned on the days ahead.
	ch := image.Rect(wkX0, 847, wkX1, 1344)
	k.card(ch)
	k.cardHead(wkX0+28, 900, "chart-column", "Hours by project", fmt.Sprintf("· week %d", week))
	seg := image.Rect(820, 868, 1136, 918)
	ui.RoundRect(k.img, seg.Sub(image.Pt(0, k.oy)), 12, rgb(0x101117))
	segSel := k.sel("seg", 0)
	for i, l := range []string{"Week", "Month", "Quarter"} {
		r := image.Rect(seg.Min.X+4+i*103, seg.Min.Y+4, seg.Min.X+4+(i+1)*103, seg.Max.Y-4)
		col := wkText2
		if i == segSel {
			ui.RoundRect(k.img, r.Sub(image.Pt(0, k.oy)), 10, rgb(0x1f212b))
			col = wkFG
		}
		k.textC(wkFont("semi", 20), (r.Min.X+r.Max.X)/2, r.Min.Y+28, col, l)
		k.btn(fmt.Sprintf("wk:seg:%d", i), r.Sub(image.Pt(0, k.oy)))
	}
	idx := map[string]int{}
	for _, p := range projects {
		idx[p.ID] = p.order
	}
	base, top := 1188, 992
	maxH := 10.0
	type stack struct {
		parts   []float64
		cols    []color.RGBA
		planned float64
	}
	days := make([]stack, 7)
	used := map[string]bool{}
	for i := range 7 {
		d := mon.AddDate(0, 0, i)
		hrs := wkHours(entries, d, d.AddDate(0, 0, 1), now)
		ids := make([]string, 0, len(hrs))
		for id := range hrs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(a, b int) bool { return idx[ids[a]] < idx[ids[b]] })
		tot := 0.0
		for _, id := range ids {
			h := hrs[id].Hours()
			if h < 0.05 {
				continue
			}
			p, _ := wkProjectByID(id)
			days[i].parts = append(days[i].parts, h)
			days[i].cols = append(days[i].cols, wkColour(p, idx[id]))
			used[id] = true
			tot += h
		}
		ds := d.Format("2006-01-02")
		if d.After(today) {
			for _, t := range todos {
				if t.SlotDate == ds && !t.Done {
					days[i].planned += float64(t.SlotMins) / 60
				}
			}
		}
		maxH = max(maxH, math.Ceil((tot+days[i].planned)/2)*2)
	}
	for i := 0; i <= 5; i++ {
		y := base - i*(base-top)/5
		k.textR(wkFont("regular", 17), 218, y+6, wkFaint, fmt.Sprintf("%.0fh", maxH*float64(i)/5))
		if i == 0 {
			ui.Fill(k.img, k.R(232, y, 1136, y+2), wkLine)
		}
	}
	step := (1136 - 232) / 7
	for i, st := range days {
		cx := 232 + step*i + step/2
		d := mon.AddDate(0, 0, i)
		isToday := d.Equal(today)
		if isToday {
			ui.RoundRect(k.img, k.R(cx-46, top-16, cx+46, base+4), 14, rgb(0x14151b))
		}
		y := base
		tot := 0.0
		for j, h := range st.parts {
			ph := int(h / maxH * float64(base-top))
			r := k.R(cx-32, y-ph+2, cx+33, y)
			if j == len(st.parts)-1 {
				ui.RoundRect(k.img, r, 6, st.cols[j])
				ui.Fill(k.img, image.Rect(r.Min.X, r.Min.Y+6, r.Max.X, r.Max.Y), st.cols[j])
			} else {
				ui.Fill(k.img, r, st.cols[j])
			}
			y -= ph
			tot += h
		}
		if st.planned > 0 {
			ph := int(st.planned / maxH * float64(base-top))
			ui.RoundRect(k.img, k.R(cx-32, y-ph, cx+33, y), 6, rgb(0x24252c))
			y -= ph
		}
		col, f := wkFaint, wkFont("regular", 19)
		if isToday {
			col, f = wkFG, wkFont("semi", 19)
			lbl := fmt.Sprintf("%.1f h", tot)
			bw := ui.TextWidth(wkFont("semi", 18), lbl) + 36
			b := k.R(cx-bw/2, y-56, cx+bw/2, y-18)
			ui.RoundRect(k.img, b, 10, rgb(0xf5f6f8))
			apTextCenter(k.img, wkFont("semi", 18), cx, (b.Min.Y+b.Max.Y)/2, wkInk, lbl)
		}
		k.textC(f, cx, 1219, col, d.Format("Mon"))
	}
	// The legend: the projects with time this week, then what's planned.
	lx, ly := wkX0+28, 1264
	byOrder := append([]wkProject(nil), projects...)
	sort.SliceStable(byOrder, func(a, b int) bool { return byOrder[a].order < byOrder[b].order })
	for _, p := range byOrder {
		if !used[p.ID] {
			continue
		}
		i := p.order
		lw := 26 + ui.TextWidth(wkFont("regular", 20), p.Name) + 22
		if lx+lw > wkX1-20 {
			lx, ly = wkX0+28, ly+47
		}
		ui.RoundRect(k.img, k.R(lx, ly-14, lx+16, ly+2), 4, wkColour(p, i))
		k.text(wkFont("regular", 20), lx+26, ly+2, wkText2, p.Name)
		lx += lw
	}
	if lx+140 > wkX1-20 {
		lx, ly = wkX0+28, ly+47
	}
	ui.RoundRect(k.img, k.R(lx, ly-14, lx+16, ly+2), 4, rgb(0x2a2b33))
	k.text(wkFont("regular", 20), lx+26, ly+2, wkText2, "Planned")

	// Needs attention, and the deadlines.
	y0 := 1377
	items := wkAttention(projects, todos, now)
	rowsH := 95 * max(len(items[:min(len(items), 4)]), 1)
	na := image.Rect(wkX0, y0, 706, y0+90+rowsH)
	k.card(na)
	e := k.cardHead(wkX0+28, y0+48, "triangle-alert", "Needs attention", "")
	k.text(wkFont("regular", 19), e+14, y0+48, wkFaint, fmt.Sprint(len(items)))
	k.outline("wk:attn", "", "See all", 678, y0+18, 48)
	ui.Fill(k.img, k.R(wkX0+2, y0+88, 704, y0+90), wkLine)
	ry := y0 + 90
	for i, it := range items {
		if i == 4 {
			break
		}
		cy := ry + 47
		tile := k.R(188, cy-26, 240, cy+26)
		ui.RoundRect(k.img, tile, 12, blend(wkCard, it.col, 0.14))
		lucideIcon(k.img, it.icon, 214, k.Y(cy), 26, it.col)
		k.text(wkFont("semi", 22), 259, cy-5, wkFG, clip(wkFont("semi", 22), it.title, 300))
		k.text(wkFont("regular", 19), 259, cy+23, wkFaint, clip(wkFont("regular", 19), it.detail, 300))
		k.badge(678, cy, it.badge, it.col)
		if it.projectID != "" {
			k.btn("wk:proj:"+it.projectID, k.R(wkX0, ry, 706, ry+95))
		}
		ry += 95
		if i < min(len(items), 4)-1 {
			ui.Fill(k.img, k.R(wkX0+2, ry, 704, ry+2), wkLine)
		}
	}
	if len(items) == 0 {
		k.text(wkFont("regular", 20), 190, ry+54, wkMuted, "Nothing late, nothing stuck.")
	}
	var dl []wkProject
	for _, p := range projects {
		if _, ok := p.daysLeft(now); ok && p.Status == "active" {
			dl = append(dl, p)
		}
	}
	sort.SliceStable(dl, func(i, j int) bool { return dl[i].Due < dl[j].Due })
	dh := 80 + 100*max(min(len(dl), 4), 1)
	dc := image.Rect(726, y0, wkX1, y0+dh)
	k.card(dc)
	k.cardHead(754, y0+40, "calendar-clock", "Deadlines", "")
	ui.Fill(k.img, k.R(728, y0+70, wkX1-2, y0+72), wkLine)
	ry = y0 + 72
	for i, p := range dl {
		if i == 4 {
			break
		}
		d, _ := p.daysLeft(now)
		ui.RoundRect(k.img, k.R(753, ry+40, 768, ry+55), 4, wkColour(p, idx[p.ID]))
		k.text(wkFont("semi", 22), 787, ry+40, wkFG, clip(wkFont("semi", 22), p.Name, 280))
		due, _ := time.Parse("2006-01-02", p.Due)
		k.text(wkFont("regular", 19), 787, ry+69, wkFaint, due.Format("Mon 2 Jan"))
		n, unit, col := fmt.Sprint(d), "days", wkFG
		if d < 0 {
			n, unit, col = fmt.Sprint(-d), "late", wkRed
		}
		k.textR(wkFont("semi", 23), 1136, ry+40, col, n)
		k.textR(wkFont("regular", 18), 1136, ry+69, wkFaint, unit)
		k.btn("wk:proj:"+p.ID, k.R(726, ry, wkX1, ry+100))
		ry += 100
		if i < min(len(dl), 4)-1 {
			ui.Fill(k.img, k.R(728, ry, wkX1-2, ry+2), wkLine)
		}
	}
	if len(dl) == 0 {
		k.text(wkFont("regular", 20), 754, ry+54, wkMuted, "No deadlines set.")
	}
	return max(na.Max.Y, dc.Max.Y) + 30
}

type wkAttn struct {
	icon, title, detail, badge string
	col                        color.RGBA
	projectID                  string
	days                       int
}

// wkAttention is what needs looking at, worst first (metrics.ts attention).
func wkAttention(projects []wkProject, todos []somaTodo, now time.Time) []wkAttn {
	var out []wkAttn
	client := map[string]string{}
	for _, p := range projects {
		client[p.ID] = p.Client
		if p.Status != "active" {
			continue
		}
		d, ok := p.daysLeft(now)
		who := p.Client
		if who == "" {
			who = "No client"
		}
		next := ""
		for _, s := range p.Steps {
			if !s.Done {
				next = s.Label
				break
			}
		}
		title := p.Name
		if next != "" {
			title = p.Name + ": " + next
		}
		switch {
		case ok && d < 0:
			out = append(out, wkAttn{"alarm-clock", title, fmt.Sprintf("%s · %d days late", who, -d), "Overdue", wkRed, p.ID, d})
		case p.stale(now):
			out = append(out, wkAttn{"history", title, who + " · nothing ticked for 2 weeks", "Stale", wkAmber, p.ID, 50})
		case ok && d <= 3:
			due, _ := time.Parse("2006-01-02", p.Due)
			out = append(out, wkAttn{"flag", title, who, "Due " + due.Format("Mon"), wkAmber, p.ID, d})
		}
	}
	for _, f := range wkFindings() {
		if f.closed() || (f.Severity != "critical" && f.Severity != "high" && !(f.Due != "" && f.Due < somaToday())) {
			continue
		}
		detail := strings.TrimSpace(f.Area + " · " + strings.Title(f.Severity))
		out = append(out, wkAttn{"shield", f.Title, strings.TrimPrefix(detail, "· "), "Finding", rgb(0xf06a8a), f.ProjectID, -1})
	}
	td := somaToday()
	for _, t := range todos {
		if !t.Done && !t.Cleared && t.Due != "" && t.Due < td {
			out = append(out, wkAttn{"flag", t.Text, "To-do, due " + t.Due, "Late", wkAmber, "", 0})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].days < out[j].days })
	return out
}

// ringsCard is the day's four rings with each one's figure beside them.
func (k *wk) ringsCard(r image.Rectangle, date string) {
	k.card(r)
	k.cardHead(r.Min.X+28, r.Min.Y+46, "target", "Today's rings", "")
	ringSet(k.img, r.Min.X+40, k.Y(r.Min.Y+70), 210, somaRingSpecs(date), false, true)
	x, y := r.Min.X+300, r.Min.Y+100
	for i, rg := range somaRingsFor(date) {
		col := rgb(rg.To)
		cx := x + (i%2)*350
		cy := y + (i/2)*100
		k.text(wkFont("semi", 18), cx, cy, wkMuted, strings.ToUpper(rg.Label))
		e := k.text(wkFont("bold", 34), cx, cy+42, col, fmt.Sprintf("%.0f", rg.Value))
		e = k.text(wkFont("semi", 22), e, cy+42, wkMuted, fmt.Sprintf("/%.0f", rg.Goal))
		k.text(wkFont("semi", 16), e+6, cy+42, col, strings.ToUpper(rg.Unit))
		k.hBar(k.R(cx, cy+58, cx+300, cy+64), rg.Value/max(rg.Goal, 1), col)
	}
}
