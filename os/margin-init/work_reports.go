package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"margin-init/ui"
)

// Reports (ReportsPage.tsx): a report for a client, for one project, or for everything, over
// a week, a month, a quarter or a year, any of them back in time; its sections switched on and
// off. It can be saved into the notes (which reach the PC's vault) and its value logged as
// money in.

var reportPeriods = []string{"Week", "Month", "Quarter", "Year"}
var reportSections = []string{"Summary", "Time", "Projects", "Next steps", "Findings", "Work log", "Money"}

// reportRange is the period chosen, n periods back.
func reportRange(per, back int, now time.Time) (from, to time.Time, label string) {
	switch per {
	case 0:
		from = weekStart(now).AddDate(0, 0, -7*back)
		to = from.AddDate(0, 0, 7)
		_, w := from.ISOWeek()
		label = fmt.Sprintf("Week %d, %s – %s", w, from.Format("2 Jan"), to.AddDate(0, 0, -1).Format("2 Jan 2006"))
	case 2:
		q := time.Date(now.Year(), time.Month((int(now.Month())-1)/3*3+1), 1, 0, 0, 0, 0, displayZone)
		from = q.AddDate(0, -3*back, 0)
		to = from.AddDate(0, 3, 0)
		label = fmt.Sprintf("Q%d %d", (int(from.Month())-1)/3+1, from.Year())
	case 3:
		from = time.Date(now.Year()-back, 1, 1, 0, 0, 0, 0, displayZone)
		to = from.AddDate(1, 0, 0)
		label = fmt.Sprint(from.Year())
	default:
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone).AddDate(0, -back, 0)
		to = from.AddDate(0, 1, 0)
		label = from.Format("January 2006")
	}
	return
}

type report struct {
	title, who, label, scope string
	from, to, pfrom          time.Time
	projects                 []wkProject
	standby                  time.Duration // waiting on the client, in the value at half
	hours, phours, val, pval float64
	steps                    int
	stepProjects             int
	late, items              int
	weeks                    [][2]float64 // all, billable
	weekNames                []string
	next                     []string
	findings                 []wkFinding
	shiftH                   time.Duration
	income                   float64
}

// buildReport gathers what goes in: tab 0 everything, 1 a client, 2 a project.
func buildReport(tab int, client, projectID string, per, back int) report {
	now := time.Now().In(displayZone)
	projects := wkProjects()
	entries := wkEntries()
	r := report{}
	r.from, r.to, r.label = reportRange(per, back, now)
	r.pfrom = r.from.Add(-r.to.Sub(r.from))
	switch tab {
	case 0:
		r.projects, r.title, r.scope = projects, "All clients", "everything"
	case 2:
		for _, p := range projects {
			if p.ID == projectID {
				r.projects, r.title, r.scope = []wkProject{p}, p.Name, p.Client
			}
		}
	default:
		for _, p := range projects {
			if p.Client == client {
				r.projects = append(r.projects, p)
			}
		}
		r.title, r.scope = client, client
	}
	r.who = wkUser() + " · " + strings.ToLower(firstNonEmpty(wkNoteOf(projects, client), "freelance work"))
	ids := map[string]bool{}
	for _, p := range r.projects {
		ids[p.ID] = true
	}
	v, _, a := wkValue(entries, r.projects, r.from, r.to, now)
	pv, _, pa := wkValue(entries, r.projects, r.pfrom, r.from, now)
	r.val, r.hours, r.pval, r.phours = v, a.Hours(), pv, pa.Hours()
	r.standby, _ = wkStandbyIn(r.projects, r.from, r.to, now)
	moved := map[string]bool{}
	for _, p := range r.projects {
		for _, s := range p.Steps {
			if s.Done && !s.At.Before(r.from) && s.At.Before(r.to) {
				r.steps++
				moved[p.ID] = true
			}
		}
		if _, ok := p.daysLeft(now); ok && p.Status == "active" {
			r.items++
			if p.overdue(now) {
				r.late++
			}
		}
		for _, s := range p.Steps {
			if !s.Done && len(r.next) < 6 {
				due := ""
				if p.Due != "" {
					due = " by " + wkDate(p.Due)
				}
				r.next = append(r.next, s.Label+", "+shortName(p.Name)+due)
				break
			}
		}
	}
	r.stepProjects = len(moved)
	// Hours a week across the period (at most 8 weeks, the last ones).
	w0 := weekStart(r.from)
	var weeks []time.Time
	for w := w0; w.Before(r.to); w = w.AddDate(0, 0, 7) {
		weeks = append(weeks, w)
	}
	if len(weeks) > 8 {
		weeks = weeks[len(weeks)-8:]
	}
	for _, w := range weeks {
		_, b, a := wkValue(entries, r.projects, w, w.AddDate(0, 0, 7), now)
		r.weeks = append(r.weeks, [2]float64{a.Hours(), b.Hours()})
		_, wn := w.ISOWeek()
		r.weekNames = append(r.weekNames, fmt.Sprintf("W%d", wn))
	}
	for _, f := range wkFindings() {
		if ids[f.ProjectID] && !f.closed() {
			r.findings = append(r.findings, f)
		}
	}
	clients := map[string]bool{}
	for _, p := range r.projects {
		clients[strings.ToLower(p.Client)] = true
	}
	for _, s := range somaShifts() {
		if clients[strings.ToLower(s.Client)] && !s.Start.Before(r.from) && s.Start.Before(r.to) {
			r.shiftH += s.Worked
		}
	}
	for _, l := range somaList("ledger") {
		d := str(l["date"])
		if str(l["kind"]) == "income" && clients[strings.ToLower(str(l["category"]))] && d >= r.from.Format("2006-01-02") && d < r.to.Format("2006-01-02") {
			r.income += toBase(math.Abs(num(l["amount"])), str(l["currency"]))
		}
	}
	return r
}

// markdown is the report as a note.
func (r report) markdown(off map[string]bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Report · %s · %s\n\n%s\n\n", r.title, r.label, r.who)
	if !off["Summary"] {
		fmt.Fprintf(&b, "- Hours: %.1f h\n- Standby: %.1f h (billed at half, in the value)\n- Value: %s\n- Steps done: %d\n", r.hours, r.standby.Hours(), wkMoney(r.val), r.steps)
		if r.items > 0 {
			fmt.Fprintf(&b, "- Deadlines on time: %d of %d\n", r.items-r.late, r.items)
		}
		b.WriteString("\n")
	}
	if !off["Projects"] {
		b.WriteString("## Projects\n\n| Project | Client | Done | Status |\n|---|---|---|---|\n")
		for _, p := range r.projects {
			st, _ := statusOf(p, time.Now())
			fmt.Fprintf(&b, "| %s | %s | %.0f%% | %s |\n", p.Name, p.Client, p.progress()*100, st)
		}
		b.WriteString("\n")
	}
	if !off["Next steps"] && len(r.next) > 0 {
		b.WriteString("## Next steps\n\n")
		for _, n := range r.next {
			b.WriteString("- " + n + "\n")
		}
		b.WriteString("\n")
	}
	if !off["Findings"] && len(r.findings) > 0 {
		b.WriteString("## Open findings\n\n")
		for _, f := range r.findings {
			fmt.Fprintf(&b, "- %s (%s)\n", f.Title, f.Severity)
		}
		b.WriteString("\n")
	}
	if !off["Work log"] && r.shiftH > 0 {
		fmt.Fprintf(&b, "Work log: %s on shifts.\n\n", wkHM(r.shiftH))
	}
	if !off["Money"] && r.income > 0 {
		fmt.Fprintf(&b, "Received: %s.\n", wkMoney(r.income))
	}
	return b.String()
}

func (k *wk) reports() int {
	su := &k.c.somaUI
	tab := k.sel("tab", 1)
	clients := wkReportClients()
	projects := wkProjects()
	ci := k.sel("client", 0)
	client := ""
	if len(clients) > 0 {
		client = clients[ci%len(clients)]
	}
	pid := su.reportProj
	if pid == "" && len(projects) > 0 {
		pid = projects[0].ID
	}
	k.header([]string{wkUser() + " · Freelance", "Reports"}, "Reports", "Reports from your projects, hours and money", color.RGBA{})
	// What it's about: a client, a project, or all.
	label := "All clients"
	pick := "wk:sheet-open:rclient:"
	switch tab {
	case 1:
		label = firstNonEmpty(client, "No clients")
	case 2:
		label = "No projects"
		for _, p := range projects {
			if p.ID == pid {
				label = p.Name
			}
		}
		pick = "wk:sheet-open:rproject:"
	}
	f := wkFont("medium", 22)
	w := ui.TextWidth(f, clip(f, label, 360)) + 110
	if tab != 0 && tab != 3 {
		r := k.R(wkX1-w, 103, wkX1, 166)
		ui.RoundRect(k.img, r, 14, wkRim)
		ui.RoundRect(k.img, r.Inset(2), 12, rgb(0x16171e))
		lucideIcon(k.img, map[int]string{1: "building-2", 2: "folder-kanban"}[tab], r.Min.X+36, (r.Min.Y+r.Max.Y)/2, 24, wkText2)
		apText(k.img, f, r.Min.X+56, (r.Min.Y+r.Max.Y)/2+8, wkFG, clip(f, label, 360))
		lucideIcon(k.img, "chevron-down", r.Max.X-32, (r.Min.Y+r.Max.Y)/2, 22, wkText2)
		k.btn(pick, r)
	}
	k.tabs("wk:tab", []string{"Everything", "By client", "By project", "Saved"}, nil, tab)
	if tab == 3 {
		return k.savedReports()
	}

	// What goes in.
	per := k.sel("rper", 1)
	back := su.reportBack
	rep := buildReport(tab, client, pid, per, back)
	opt := image.Rect(wkX0, 312, wkX1, 547)
	k.cardIn(opt, rgb(0x121318))
	k.segBar("wk:rper", image.Rect(188, 337, 640, 392), reportPeriods, per)
	ui.RoundRect(k.img, k.R(660, 337, 1136, 392), 12, rgb(0x0e0f13))
	k.iconBtn("wk:rback:1", "chevron-left", 690, 364, 22, rgb(0x1f212b), wkFG)
	k.textC(wkFont("semi", 20), 898, 371, wkFG, clip(wkFont("semi", 20), rep.label, 320))
	if back > 0 {
		k.iconBtn("wk:rback:-1", "chevron-right", 1106, 364, 22, rgb(0x1f212b), wkFG)
	}
	k.text(wkFont("medium", 20), 187, 444, wkMuted, "Include")
	toggle := func(x, y int, on bool, l string) int {
		f := wkFont("medium", 21)
		w := ui.TextWidth(f, l) + 74
		r := k.R(x, y, x+w, y+47)
		if on {
			ui.RoundRect(k.img, r, 12, blend(rgb(0x121318), wkLime, 0.35))
			ui.RoundRect(k.img, r.Inset(2), 10, blend(rgb(0x121318), wkLime, 0.08))
			lucideIcon(k.img, "check", r.Min.X+30, (r.Min.Y+r.Max.Y)/2, 22, wkFG)
		} else {
			ui.RoundRect(k.img, r, 12, wkRim)
			ui.RoundRect(k.img, r.Inset(2), 10, rgb(0x121318))
			lucideIcon(k.img, "plus", r.Min.X+30, (r.Min.Y+r.Max.Y)/2, 22, wkMuted)
		}
		c := wkFG
		if !on {
			c = wkMuted
		}
		apText(k.img, f, r.Min.X+52, (r.Min.Y+r.Max.Y)/2+8, c, l)
		k.btn("wk:rinc:"+l, r)
		return x + w + 14
	}
	off := su.reportOff
	tx, ty := 276, 415
	for _, l := range reportSections {
		if tx+ui.TextWidth(wkFont("medium", 21), l)+74 > wkX1-10 {
			tx, ty = 188, ty+61
		}
		tx = toggle(tx, ty, !off[l], l)
	}

	// The report, as a page.
	y := k.reportPage(rep, off, 572)
	k.boxBtn("wk:rsave", wkX0, y+20, 532, y+83, "file-down", "Save to notes", wkLime, wkOnLime)
	k.boxBtn("wk:sheet-open:rincome:", 549, y+20, 848, y+83, "arrow-down-left", "Log as paid", color.RGBA{}, wkFG)
	k.boxBtn("wk:rinvoices", 866, y+20, wkX1, y+83, "receipt", "Invoices", color.RGBA{}, wkFG)
	return y + 83
}

// reportPage draws the report on a white page; returns where it ends.
func (k *wk) reportPage(r report, off map[string]bool, y0 int) int {
	ink, grey, light := rgb(0x111111), rgb(0x6b6e75), rgb(0xf2f3f5)
	green, red := rgb(0x16a34a), rgb(0xdc2626)
	// How tall: the sections that are on.
	h := 200
	if !off["Summary"] {
		h += 150
	}
	if !off["Time"] {
		h += 200
	}
	if !off["Projects"] {
		h += 70 + 48*len(r.projects)
	}
	if !off["Next steps"] {
		h += 60 + 30*max(len(r.next), 1)
	}
	if !off["Findings"] {
		h += 60 + 30*max(len(r.findings), 1)
	}
	if !off["Work log"] || !off["Money"] {
		h += 80
	}
	k.cardIn(image.Rect(wkX0, y0, wkX1, y0+h+70), rgb(0x0a0a0d))
	pg := k.R(222, y0+35, 1101, y0+h+35)
	ui.RoundRect(k.img, pg, 8, rgb(0xffffff))
	x0, x1 := 273, 1050
	y := y0 + 95
	k.text(wkFont("semi", 15), x0, y, grey, spaced("REPORT · "+strings.ToUpper(r.title)))
	k.text(wkFont("bold", 38), x0, y+49, ink, r.label)
	ui.RoundRect(k.img, k.R(x1-47, y-15, x1, y+32), 10, ink)
	apTextCenter(k.img, wkFont("bold", 22), x1-24, k.Y(y+8), wkLime, "S")
	k.textR(wkFont("regular", 16), x1, y+56, grey, time.Now().In(displayZone).Format("2 Jan 2006"))
	k.text(wkFont("regular", 18), x0, y+83, rgb(0x4a4d55), clip(wkFont("regular", 18), r.who, 640))
	ui.Fill(k.img, k.R(x0, y+111, x1, y+114), ink)
	y += 140
	trend := func(a, b float64) (string, color.RGBA) {
		p := pctChange(a, b)
		if math.IsNaN(p) {
			return "new this period", green
		}
		arrow, c := "↑", green
		if p < 0 {
			arrow, c = "↓", red
		}
		return fmt.Sprintf("%s %.0f%% vs before", arrow, math.Abs(p)), c
	}
	if !off["Summary"] {
		stat := func(i int, l, v, foot string, fc color.RGBA) {
			sx := x0 + i*196
			ui.RoundRect(k.img, k.R(sx, y, sx+182, y+102), 6, light)
			k.text(wkFont("regular", 15), sx+16, y+27, grey, l)
			k.text(wkFont("bold", 26), sx+16, y+62, ink, v)
			k.text(wkFont("medium", 15), sx+16, y+86, fc, foot)
		}
		t1, c1 := trend(r.phours, r.hours)
		t2, c2 := trend(r.pval, r.val)
		hl := "Hours"
		if r.standby > 0 {
			hl = fmt.Sprintf("Hours · %.1f h standby", r.standby.Hours())
		}
		stat(0, hl, fmt.Sprintf("%.1f h", r.hours), t1, c1)
		stat(1, "Value", wkMoney(r.val), t2, c2)
		stat(2, "Steps done", fmt.Sprint(r.steps), fmt.Sprintf("%d projects", r.stepProjects), green)
		ontime, note := 100.0, "nothing late"
		if r.items > 0 {
			ontime = float64(r.items-r.late) / float64(r.items) * 100
		}
		if r.late > 0 {
			note = fmt.Sprintf("%d late", r.late)
		}
		stat(3, "On time", fmt.Sprintf("%.0f%%", ontime), note, map[bool]color.RGBA{true: red, false: green}[r.late > 0])
		y += 150
	}
	if !off["Time"] {
		e := k.text(wkFont("bold", 19), x0, y, ink, "Hours per week")
		k.text(wkFont("regular", 19), e+6, y, grey, "· billable dark")
		hi := 1.0
		for _, b := range r.weeks {
			hi = max(hi, b[0])
		}
		base := y + 130
		step := (x1 - x0) / max(len(r.weeks), 1)
		for i, b := range r.weeks {
			cx := x0 + step*i + step/2
			bh := int(b[0] / hi * 90)
			bb := int(b[1] / hi * 90)
			if bh > 0 {
				ui.RoundRect(k.img, k.R(cx-30, base-bh, cx+30, base), 4, rgb(0xe5e6e9))
			}
			if bb > 0 {
				ui.RoundRect(k.img, k.R(cx-30, base-bb, cx+30, base), 4, ink)
			}
			k.textC(wkFont("regular", 15), cx, base-bh-14, grey, fmt.Sprintf("%.0f h", b[0]))
			k.textC(wkFont("regular", 15), cx, base+22, rgb(0xa0a3aa), r.weekNames[i])
		}
		y += 200
	}
	if !off["Projects"] {
		k.text(wkFont("bold", 20), x0, y, ink, "Projects")
		ui.Fill(k.img, k.R(x0, y+16, x1, y+50), light)
		hf := wkFont("semi", 14)
		k.text(hf, x0+13, y+38, grey, spaced("PROJECT"))
		k.text(hf, x0+330, y+38, grey, spaced("CLIENT"))
		k.textR(hf, x0+620, y+38, grey, spaced("HOURS"))
		k.textR(hf, x0+700, y+38, grey, spaced("DONE"))
		k.textR(hf, x1-13, y+38, grey, spaced("VALUE"))
		ry := y + 51
		entries := wkEntries()
		now := time.Now()
		for _, p := range r.projects {
			cy := ry + 24
			_, _, ph := wkValue(entries, []wkProject{p}, r.from, r.to, now)
			pv, _, _ := wkValue(entries, []wkProject{p}, r.from, r.to, now)
			ui.RoundRect(k.img, k.R(x0+13, cy-6, x0+27, cy+8), 2, wkColour(p, p.order))
			k.text(wkFont("regular", 17), x0+37, cy+7, ink, clip(wkFont("regular", 17), p.Name, 280))
			k.text(wkFont("regular", 17), x0+330, cy+7, grey, clip(wkFont("regular", 17), p.Client, 190))
			k.textR(wkFont("regular", 17), x0+620, cy+7, ink, fmt.Sprintf("%.1f", ph.Hours()))
			k.textR(wkFont("regular", 17), x0+700, cy+7, ink, fmt.Sprintf("%.0f%%", p.progress()*100))
			v := "—"
			if p.Rate > 0 {
				v = wkMoney(pv)
			}
			k.textR(wkFont("semi", 17), x1-13, cy+7, ink, v)
			ry += 48
			ui.Fill(k.img, k.R(x0, ry-1, x1, ry), rgb(0xe8e9ec))
		}
		y = ry + 40
	}
	list := func(title string, items []string, empty string) {
		k.text(wkFont("bold", 20), x0, y, ink, title)
		y += 35
		if len(items) == 0 {
			k.text(wkFont("regular", 17), x0, y, grey, "• "+empty)
			y += 30
		}
		for _, s := range items {
			k.text(wkFont("regular", 17), x0, y, rgb(0x2a2d33), clip(wkFont("regular", 17), "• "+s, x1-x0))
			y += 30
		}
		y += 25
	}
	if !off["Next steps"] {
		list("Next steps", r.next, "Nothing open.")
	}
	if !off["Findings"] {
		var fs []string
		for _, f := range r.findings {
			fs = append(fs, f.Title+" ("+f.Severity+")")
		}
		list("Open findings", fs, "None open.")
	}
	var foot []string
	if !off["Work log"] {
		foot = append(foot, "Work log: "+wkHM(r.shiftH)+" on shifts")
	}
	if !off["Money"] {
		foot = append(foot, "Received: "+wkMoney(r.income))
	}
	if len(foot) > 0 {
		k.text(wkFont("regular", 18), x0, y+10, ink, strings.Join(foot, "   ·   "))
	}
	k.text(wkFont("regular", 14), x0, y0+h+10, rgb(0xa0a3aa), "Made with SOMA")
	return y0 + h + 70
}

// savedReports is the reports saved into the notes.
func (k *wk) savedReports() int {
	var list []somaNote
	for _, n := range somaNotes() {
		if strings.HasPrefix(n.title(), "Report · ") {
			list = append(list, n)
		}
	}
	y := 312
	cr := image.Rect(wkX0, y, wkX1, y+20+90*max(len(list), 1))
	k.card(cr)
	ry := y + 10
	for i, n := range list {
		if i > 0 {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		}
		k.text(wkFont("semi", 22), 188, ry+44, wkFG, clip(wkFont("semi", 22), strings.TrimPrefix(n.title(), "Report · "), 760))
		k.text(wkFont("regular", 17), 188, ry+72, wkFaint, "Saved "+time.UnixMilli(n.Mtime).In(displayZone).Format("2 Jan 2006, 15:04"))
		k.btn("wk:note:"+n.ID, k.R(wkX0, ry, wkX1, ry+90))
		ry += 90
	}
	if len(list) == 0 {
		k.emptyNote(188, ry+50, "Reports you save to notes are kept here (and in the PC's vault).")
	}
	return cr.Max.Y + 20
}

func init() {
	wkActions["rper"] = func(c *console, arg string) {
		if c.somaUI.workSel == nil {
			c.somaUI.workSel = map[string]string{}
		}
		c.somaUI.workSel["reportswk:rper"] = arg
		c.somaUI.reportBack = 0
	}
	wkActions["rback"] = func(c *console, arg string) {
		if arg == "1" {
			c.somaUI.reportBack++
		} else if c.somaUI.reportBack > 0 {
			c.somaUI.reportBack--
		}
	}
	wkActions["rinc"] = func(c *console, sec string) {
		if c.somaUI.reportOff == nil {
			c.somaUI.reportOff = map[string]bool{}
		}
		c.somaUI.reportOff[sec] = !c.somaUI.reportOff[sec]
	}
	wkSheets["rclient"] = func(k *wk, _ string) *wkSheet {
		sh := &wkSheet{title: "Report for"}
		cur := k.sel("client", 0)
		for i, cl := range wkReportClients() {
			sh.rows = append(sh.rows, wkRow{id: fmt.Sprintf("wk:client:%d", i), label: cl, icon: "building-2", sel: i == cur})
		}
		return sh
	}
	wkSheets["rproject"] = func(k *wk, _ string) *wkSheet {
		sh := &wkSheet{title: "Report for"}
		for _, p := range wkProjects() {
			sh.rows = append(sh.rows, wkRow{id: "wk:rproj:" + p.ID, label: p.Name, col: wkColour(p, p.order), detail: p.Client, sel: k.c.somaUI.reportProj == p.ID})
		}
		return sh
	}
	wkActions["rproj"] = func(c *console, id string) { c.somaUI.reportProj = id }
	current := func(c *console) report {
		k := &wk{c: c}
		clients := wkReportClients()
		client := ""
		if len(clients) > 0 {
			client = clients[k.sel("client", 0)%len(clients)]
		}
		pid := c.somaUI.reportProj
		if ps := wkProjects(); pid == "" && len(ps) > 0 {
			pid = ps[0].ID
		}
		return buildReport(k.sel("tab", 1), client, pid, k.sel("rper", 1), c.somaUI.reportBack)
	}
	wkActions["rsave"] = func(c *console, _ string) {
		r := current(c)
		title := "Report · " + r.title + " · " + r.label
		title = strings.Map(func(x rune) rune {
			if strings.ContainsRune(`\/:*?"<>|#^[]`, x) {
				return '-'
			}
			return x
		}, title)
		somaPutNote("Reports/"+title+".md", r.markdown(c.somaUI.reportOff))
		c.toastMsg("Saved to your notes")
	}
	wkSheets["rincome"] = func(k *wk, _ string) *wkSheet {
		r := current(k.c)
		who := r.title
		if len(r.projects) == 1 && r.projects[0].Client != "" {
			who = r.projects[0].Client
		}
		return &wkSheet{title: "Paid?", sub: "Logs " + wkMoney(r.val) + " in from " + who + " in Money", rows: []wkRow{
			{id: fmt.Sprintf("wk:rincome:%s|%.2f", who, r.val), label: "Yes, log it", icon: "circle-check", col: wkGreen},
		}}
	}
	wkActions["rincome"] = func(c *console, arg string) {
		who, v, _ := strings.Cut(arg, "|")
		var amt float64
		fmt.Sscan(v, &amt)
		if amt > 0 {
			wkAddLedger("income", amt, who, "Report", "")
			c.toastMsg("Logged as money in")
		}
	}
	wkActions["rinvoices"] = func(c *console, _ string) {
		c.wkGo("money", "")
		if c.somaUI.workSel == nil {
			c.somaUI.workSel = map[string]string{}
		}
		c.somaUI.workSel["moneywk:tab"] = "1"
	}
}
