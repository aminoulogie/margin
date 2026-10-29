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
		lucideIcon(k.img, "check", x+17, k.Y(cy), 22, wkOnLime)
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
	ui.Circle(k.img, lx, ly, 4, blend(wkCard, wkLime, 0.2))
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
		k.boxBtn("wk:go:projects", 829, 546, 1130, 609, "timer", "Start", wkLime, wkOnLime)
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
		ui.Fill(k.img, k.R(cx-56, 948, cx+38, ts.Max.Y-2), blend(wkCard, wkLime, 0.04))
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
