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

// The rest of the Work tab: the Work log (WorkLogPage.tsx) and Findings (FindingsPage.tsx),
// and Personal's pages as the workstation hosts them: Health (HealthPage.tsx), Training,
// Nutrition, Mind and Stats, in the same cards.

// bars is a bar chart of vals in r (screen y), a dashed goal line, every nth day labelled.
func (k *wk) bars(r image.Rectangle, vals []float64, col color.RGBA, goal float64, labels []string, every int, unit string) {
	hi := max(goal*1.15, 1)
	for _, v := range vals {
		hi = max(hi, v*1.05)
	}
	st := niceStep(hi / 3)
	hi = st * math.Ceil(hi/st)
	for i := 0; i*int(st*1000) <= int(hi*1000); i++ {
		v := st * float64(i)
		y := r.Max.Y - int(v/hi*float64(r.Dy()))
		k.textR(wkFont("regular", 16), r.Min.X-10, y+6, wkFaint, fmtAxis(v))
		ui.Fill(k.img, k.R(r.Min.X, y, r.Max.X, y+1), rgb(0x1a1b21))
	}
	n := len(vals)
	if n == 0 {
		return
	}
	bw := r.Dx() / n
	for i, v := range vals {
		x := r.Min.X + i*bw
		if v > 0 {
			h := int(v / hi * float64(r.Dy()))
			ui.RoundRect(k.img, k.R(x+bw/6, r.Max.Y-h, x+bw-bw/6, r.Max.Y), min(4, bw/4), col)
		}
		if every > 0 && i%every == 0 && i < len(labels) {
			k.textC(wkFont("regular", 16), x+bw/2, r.Max.Y+26, wkFaint, labels[i])
		}
	}
	if goal > 0 {
		gy := k.Y(r.Max.Y - int(goal/hi*float64(r.Dy())))
		for x := r.Min.X; x < r.Max.X; x += 14 {
			ui.Fill(k.img, image.Rect(x, gy-1, min(x+8, r.Max.X), gy+1), wkText2)
		}
		k.text(wkFont("regular", 16), r.Max.X-60, r.Max.Y-int(goal/hi*float64(r.Dy()))-8, wkText2, "Goal")
	}
	_ = unit
}

func fmtAxis(v float64) string {
	if v >= 1000 {
		return fmtThousands(int(v))
	}
	if v == math.Trunc(v) {
		return fmt.Sprint(int(v))
	}
	return fmt.Sprintf("%.1f", v)
}

// small is a figure card: a label, the value, a faint line under it.
func (k *wk) small(r image.Rectangle, icon, label, value, unit, foot string, valCol color.RGBA) {
	k.card(r)
	x := r.Min.X + 24
	if icon != "" {
		k.icon(icon, x+10, r.Min.Y+34, 24, wkMuted)
		x += 32
	}
	k.text(wkFont("medium", 20), x, r.Min.Y+42, wkText2, label)
	e := k.text(wkFont("bold", 36), r.Min.X+24, r.Min.Y+92, valCol, value)
	if unit != "" {
		k.text(wkFont("medium", 21), e+4, r.Min.Y+92, wkMuted, unit)
	}
	k.text(wkFont("regular", 18), r.Min.X+24, r.Min.Y+130, wkFaint, clip(wkFont("regular", 18), foot, r.Dx()-40))
}

// row4 is four figure cards in a row at y.
func (k *wk) row4(y int) func(i int) image.Rectangle {
	cw := (wkX1 - wkX0 - 60) / 4
	return func(i int) image.Rectangle { return image.Rect(wkX0+i*(cw+20), y, wkX0+i*(cw+20)+cw, y+160) }
}

func lastDays(n int) []string {
	now := time.Now()
	out := make([]string, n)
	for i := range n {
		out[i] = somaDate(now.AddDate(0, 0, -(n - 1 - i)))
	}
	return out
}

func dayLabel(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.Format("Jan 2")
}

// --- the work log ---------------------------------------------------------------------------------

func (k *wk) worklog() int {
	now := time.Now().In(displayZone)
	off := k.sel("week", 0)
	mon := weekStart(now).AddDate(0, 0, 7*off)
	end := mon.AddDate(0, 0, 7)
	rates, _ := jsonValue(soma.get("settings.clientRates")).(map[string]any)
	var shifts []somaShift
	for _, s := range somaShifts() {
		if !s.Start.Before(mon) && s.Start.Before(end) {
			shifts = append(shifts, s)
		}
	}
	// Worked, breaks and standby (the gaps between a client's shifts in a day) this week.
	type cl struct{ worked, standby time.Duration }
	byClient := map[string]*cl{}
	days := map[string][]somaShift{}
	var worked, breaks time.Duration
	for _, s := range shifts {
		e := s.End
		if e.IsZero() {
			e = now
		}
		worked += s.Worked
		breaks += max(0, e.Sub(s.Start)-s.Worked)
		days[s.Client+"|"+somaDate(s.Start)] = append(days[s.Client+"|"+somaDate(s.Start)], s)
		if byClient[s.Client] == nil {
			byClient[s.Client] = &cl{}
		}
		byClient[s.Client].worked += s.Worked
	}
	var standby time.Duration
	for key, ss := range days {
		first, last := ss[0].Start, time.Time{}
		var w time.Duration
		for _, s := range ss {
			e := s.End
			if e.IsZero() {
				e = now
			}
			if s.Start.Before(first) {
				first = s.Start
			}
			if e.After(last) {
				last = e
			}
			w += e.Sub(s.Start)
		}
		sb := max(0, last.Sub(first)-w)
		standby += sb
		byClient[strings.SplitN(key, "|", 2)[0]].standby += sb
	}
	owed := 0.0
	for c, v := range byClient {
		owed += (v.worked.Hours() + v.standby.Hours()*0.5) * num(rates[c])
	}
	hm := func(d time.Duration) string {
		if d < time.Hour {
			return fmt.Sprintf("%dm", int(d.Minutes()))
		}
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	k.header([]string{wkUser() + " · Freelance", "Work log"}, "Work log", mon.Format("Jan 2")+" – "+end.AddDate(0, 0, -1).Format("Jan 2, 2006"), color.RGBA{})
	x := k.primary("so:type:clockin", "plus", "Add a shift", wkX1)
	x = k.square(fmt.Sprintf("wk:week:%d", off+1), "chevron-right", x-13)
	k.square(fmt.Sprintf("wk:week:%d", off-1), "chevron-left", x-13)
	k.tabs("wk:tab", []string{"This week", "Shifts", "Clients"}, nil, k.sel("tab", 0))
	box := k.row4(312)
	k.small(box(0), "clock", "Worked", hm(worked), "", fmt.Sprintf("%d shifts this week", len(shifts)), wkFG)
	k.small(box(1), "coffee", "Breaks", hm(breaks), "", "not counted as worked", wkFG)
	k.small(box(2), "hourglass", "Standby", hm(standby), "", "gaps between shifts, paid at half", wkFG)
	k.small(box(3), "pound-sterling", "Owed", wkMoney(owed), "", "at each client's rate", wkLime)

	// Clocked in or not.
	cur := (*somaShift)(nil)
	for i, s := range shifts {
		if s.End.IsZero() {
			cur = &shifts[i]
		}
	}
	cr := image.Rect(wkX0, 496, wkX1, 616)
	k.cardIn(cr, rgb(0x16181d))
	if cur != nil {
		ui.Circle(k.img, 196, k.Y(546), 8, wkGreen)
		k.text(wkFont("semi", 22), 216, 553, wkFG, "Clocked in · "+cur.Client)
		k.text(wkFont("regular", 19), 216, 584, wkFaint, "since "+cur.Start.In(displayZone).Format("15:04")+" · "+hm(cur.Worked)+" worked")
		k.boxBtn("wk:clockout", 900, 526, 1136, 586, "square", "Clock out", wkRed, rgb(0xffffff))
	} else {
		k.text(wkFont("semi", 22), 188, 553, wkFG, "Not clocked in")
		k.text(wkFont("regular", 19), 188, 584, wkFaint, "Type the client you're working for and start the shift.")
		k.boxBtn("so:type:clockin", 900, 526, 1136, 586, "timer", "Clock in", wkLime, wkInk)
	}

	// The shifts, and the clients.
	y := 640
	sh := image.Rect(wkX0, y, 690, y+96+84*max(len(shifts), 1))
	k.card(sh)
	k.cardHead(wkX0+28, y+45, "briefcase", "Shifts", "")
	ry := y + 76
	for _, s := range shifts {
		ui.Fill(k.img, k.R(wkX0+2, ry, 688, ry+2), wkLine)
		e := s.End
		end := "now"
		if !e.IsZero() {
			end = e.In(displayZone).Format("15:04")
		}
		k.text(wkFont("semi", 21), 188, ry+38, wkFG, clip(wkFont("semi", 21), s.Client, 300))
		k.text(wkFont("regular", 18), 188, ry+66, wkFaint, s.Start.In(displayZone).Format("Mon 2 · 15:04")+" – "+end)
		k.textR(wkFont("bold", 22), 660, ry+50, wkFG, hm(s.Worked))
		ry += 84
	}
	if len(shifts) == 0 {
		k.text(wkFont("regular", 20), 188, ry+52, wkMuted, "No shifts this week.")
	}
	var clients []string
	for c := range byClient {
		clients = append(clients, c)
	}
	sort.Strings(clients)
	bc := image.Rect(710, y, wkX1, y+96+84*max(len(clients), 1))
	k.card(bc)
	k.cardHead(738, y+45, "building-2", "By client", "")
	k.textR(wkFont("regular", 17), 1136, y+45, wkFaint, "rate per hour")
	ry = y + 76
	for _, c := range clients {
		ui.Fill(k.img, k.R(712, ry, wkX1-2, ry+2), wkLine)
		v := byClient[c]
		k.text(wkFont("semi", 21), 738, ry+38, wkFG, clip(wkFont("semi", 21), c, 230))
		k.text(wkFont("regular", 18), 738, ry+66, wkFaint, hm(v.worked)+" worked · "+hm(v.standby)+" standby")
		r := num(rates[c])
		if r > 0 {
			k.textR(wkFont("bold", 21), 1136, ry+38, wkFG, wkMoney((v.worked.Hours()+v.standby.Hours()*0.5)*r))
			k.textR(wkFont("regular", 17), 1136, ry+66, wkFaint, wkMoney(r)+"/h")
		} else {
			k.textR(wkFont("regular", 18), 1136, ry+50, wkFaint, "no rate")
		}
		ry += 84
	}
	if len(clients) == 0 {
		k.text(wkFont("regular", 20), 738, ry+52, wkMuted, "Clients appear once you log a shift.")
	}
	return max(sh.Max.Y, bc.Max.Y)
}

// wkClockIn starts a shift for a client (clockIn); wkClockOut ends the open one.
func wkClockIn(client string) {
	client = strings.TrimSpace(client)
	if client == "" {
		return
	}
	wkClockOut()
	id := "sh-" + somaNewID()
	somaPut("shifts#"+id, obj{"id": id, "client": client, "start": float64(time.Now().UnixMilli()), "breaks": []any{}, "activities": []any{}})
	var order []string
	for _, o := range somaList("shifts") {
		order = append(order, str(o["id"]))
	}
	somaPut("shifts@order", order)
}

func wkClockOut() {
	for _, o := range somaList("shifts") {
		if num(o["end"]) == 0 && num(o["start"]) > 0 {
			o["end"] = float64(time.Now().UnixMilli())
			somaPut("shifts#"+str(o["id"]), o)
		}
	}
}

// --- findings -------------------------------------------------------------------------------------

var sevCol = map[string]color.RGBA{"critical": rgb(0xf0566a), "high": wkAmber, "medium": wkYellow, "low": wkBlue}

func (k *wk) findingsPage() int {
	now := time.Now().In(displayZone)
	all := wkFindings()
	projects := wkProjects()
	name := map[string]string{}
	for _, p := range projects {
		name[p.ID] = p.Name
	}
	td := somaToday()
	open := map[string]int{}
	late := 0
	for _, f := range all {
		if !f.closed() {
			open[f.Severity]++
			if f.Due != "" && f.Due < td {
				late++
			}
		}
	}
	k.header([]string{wkUser() + " · Freelance", "Findings"}, "Findings", "What review turned up, until it's fixed or accepted", color.RGBA{})
	k.primary("so:type:finding", "plus", "Finding", wkX1)
	chip := k.sel("chip", 0)
	k.tabs("wk:tab", []string{"All projects", "By project", "By severity"}, nil, k.sel("tab", 0))
	k.chips("wk:chip", 308, []string{"Open", "Closed", "All"}, nil, chip)
	box := k.row4(386)
	for i, s := range []string{"critical", "high", "medium"} {
		k.small(box(i), "shield", strings.ToUpper(s[:1])+s[1:], fmt.Sprint(open[s]), "", "open", sevCol[s])
	}
	k.small(box(3), "alarm-clock", "Overdue fixes", fmt.Sprint(late), "", "past their due date", map[bool]color.RGBA{true: wkRed, false: wkFG}[late > 0])
	var list []wkFinding
	for _, f := range all {
		if chip == 0 && f.closed() || chip == 1 && !f.closed() {
			continue
		}
		list = append(list, f)
	}
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(list, func(i, j int) bool { return rank[list[i].Severity] < rank[list[j].Severity] })
	y := 572
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
		k.text(wkFont("semi", 22), 259, cy-4, wkFG, clip(wkFont("semi", 22), f.Title, 560))
		sub := strings.Trim(name[f.ProjectID]+" · "+f.Area, " ·")
		if f.Due != "" {
			sub += " · fix by " + dayLabel(f.Due)
		}
		k.text(wkFont("regular", 19), 259, cy+24, wkFaint, clip(wkFont("regular", 19), sub, 560))
		st := map[string]string{"open": "Open", "in-progress": "In progress", "resolved": "Resolved", "accepted": "Accepted"}[f.Status]
		if st == "" {
			st = "Open"
		}
		x := k.badge(1136, cy, st, map[bool]color.RGBA{true: wkGreen, false: wkText2}[f.closed()])
		k.badge(x-12, cy, strings.ToUpper(f.Severity[:min(1, len(f.Severity))])+f.Severity[min(1, len(f.Severity)):], col)
		k.btn("wk:finding:"+f.ID, k.R(wkX0, ry, wkX1, ry+96))
		ry += 96
	}
	if len(list) == 0 {
		k.text(wkFont("regular", 20), 188, ry+56, wkMuted, "No findings here. Add one: it attaches to your newest active project.")
	}
	_ = now
	return lc.Max.Y
}

// wkFindingNext moves a finding along: open → in progress → resolved → open.
func wkFindingNext(id string) {
	o := somaObj("findings#" + id)
	if o == nil {
		return
	}
	next := map[string]string{"open": "in-progress", "in-progress": "resolved", "resolved": "open", "accepted": "open"}[str(o["status"])]
	if next == "" {
		next = "in-progress"
	}
	o["status"] = next
	if next == "resolved" {
		o["closedAt"] = float64(time.Now().UnixMilli())
	} else {
		delete(o, "closedAt")
	}
	somaPut("findings#"+id, o)
}

// wkAddFinding logs a finding on the first active project, medium until changed on the PC.
func wkAddFinding(title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	pid := ""
	for _, p := range wkProjects() {
		if p.Status == "active" {
			pid = p.ID
			break
		}
	}
	id := "fd-" + somaNewID()
	somaPut("findings#"+id, obj{"id": id, "projectId": pid, "title": title, "severity": "medium", "status": "open", "createdAt": float64(time.Now().UnixMilli())})
	var order []string
	for _, o := range somaList("findings") {
		order = append(order, str(o["id"]))
	}
	somaPut("findings@order", order)
}

// --- health ---------------------------------------------------------------------------------------

func sleepHours(date string) float64 {
	sl, _ := somaObj("nutrition/" + date)["sleep"].(obj)
	return num(sl["hours"])
}

func bodyWeight(date string) float64 { return num(somaObj("nutrition/" + date)["bodyWeight"]) }

func avgOf(v []float64) (float64, int) {
	s, n := 0.0, 0
	for _, x := range v {
		if x > 0 {
			s += x
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return s / float64(n), n
}

func (k *wk) health() int {
	rng := []int{14, 28, 90}[k.sel("chip", 1)]
	days := lastDays(rng)
	var cals, prot, sleep, kg []float64
	goalC, goalP := 0.0, 0.0
	for _, d := range days {
		n := somaNutrition(d)
		cals, prot = append(cals, n.Cals), append(prot, n.P)
		goalC, goalP = max(goalC, n.GoalCals), max(goalP, n.GoalP)
		sleep, kg = append(sleep, sleepHours(d)), append(kg, bodyWeight(d))
	}
	sessions := 0
	for _, d := range days {
		if somaTraining(d) != nil {
			sessions++
		}
	}
	k.header([]string{"Personal", "Health"}, "Health", "Food, sleep, weight and training, side by side", color.RGBA{})
	k.chips("wk:chip", 236, []string{"14 days", "28 days", "90 days"}, nil, k.sel("chip", 1))
	box := k.row4(312)
	ac, nc := avgOf(cals)
	ap, _ := avgOf(prot)
	as, _ := avgOf(sleep)
	k.small(box(0), "utensils", "Calories / day", fmtThousands(int(ac)), "", fmt.Sprintf("goal %s · %d of %d logged", fmtThousands(int(goalC)), nc, rng), wkFG)
	k.small(box(1), "target", "Protein / day", fmt.Sprintf("%.0f", ap), "g", fmt.Sprintf("goal %.0f g", goalP), wkFG)
	first, last := 0.0, 0.0
	for _, v := range kg {
		if v > 0 {
			if first == 0 {
				first = v
			}
			last = v
		}
	}
	wf := "nothing weighed"
	if last > 0 {
		wf = fmt.Sprintf("%+.1f kg over %d days", last-first, rng)
	}
	k.small(box(2), "activity", "Bodyweight", fmt.Sprintf("%.1f", last), "kg", wf, wkFG)
	k.small(box(3), "dumbbell", "Sessions", fmt.Sprint(sessions), "", fmt.Sprintf("%.1f a week", float64(sessions)/float64(rng)*7), wkFG)
	labels := make([]string, len(days))
	for i, d := range days {
		labels[i] = dayLabel(d)
	}
	every := max(len(days)/5, 1)
	chart := func(r image.Rectangle, icon, title, note string, vals []float64, col color.RGBA, goal float64) {
		k.card(r)
		k.cardHead(r.Min.X+28, r.Min.Y+44, icon, title, "")
		k.textR(wkFont("regular", 17), r.Max.X-28, r.Min.Y+44, wkFaint, note)
		k.bars(image.Rect(r.Min.X+80, r.Min.Y+80, r.Max.X-28, r.Max.Y-50), vals, col, goal, labels, every, "")
	}
	chart(image.Rect(wkX0, 496, 652, 836), "utensils", "Calories", "per day", cals, wkBlue, goalC)
	chart(image.Rect(672, 496, wkX1, 836), "target", "Protein", "grams per day", prot, wkBlue, goalP)
	// Bodyweight as a line.
	r := image.Rect(wkX0, 856, 652, 1196)
	k.card(r)
	k.cardHead(r.Min.X+28, r.Min.Y+44, "activity", "Bodyweight", "")
	k.textR(wkFont("regular", 17), r.Max.X-28, r.Min.Y+44, wkFaint, "kg")
	var pts []float64
	for _, v := range kg {
		if v > 0 {
			pts = append(pts, v)
		}
	}
	if len(pts) >= 2 {
		lo, hi := pts[0], pts[0]
		for _, v := range pts {
			lo, hi = min(lo, v), max(hi, v)
		}
		pad := max((hi-lo)*0.2, 0.3)
		lo, hi = lo-pad, hi+pad
		pr := k.R(r.Min.X+80, r.Min.Y+90, r.Max.X-40, r.Max.Y-60)
		for i := 0; i <= 2; i++ {
			v := lo + (hi-lo)*float64(i)/2
			yy := pr.Max.Y - i*pr.Dy()/2
			apTextRight(k.img, wkFont("regular", 16), pr.Min.X-10, yy+6, wkFaint, fmt.Sprintf("%.1f", v))
		}
		px := func(i int) (int, int) {
			return pr.Min.X + i*pr.Dx()/(len(pts)-1), pr.Max.Y - int((pts[i]-lo)/(hi-lo)*float64(pr.Dy()))
		}
		for i := 1; i < len(pts); i++ {
			x0, y0 := px(i - 1)
			x1, y1 := px(i)
			line(k.img, x0, y0, x1, y1, 3, wkBlue)
		}
		for i := range pts {
			x, y := px(i)
			ui.Circle(k.img, x, y, 5, wkBlue)
		}
	} else {
		k.text(wkFont("regular", 20), r.Min.X+28, r.Min.Y+170, wkMuted, "Weigh in on the phone and it shows here.")
	}
	chart(image.Rect(672, 856, wkX1, 1196), "clock", "Sleep", fmt.Sprintf("%.1f h a night", as), sleep, wkBlue, 8)
	// Sessions.
	y := 1216
	var ss []*somaSession
	for i := len(days) - 1; i >= 0 && len(ss) < 6; i-- {
		if s := somaTraining(days[i]); s != nil {
			ss = append(ss, s)
		}
	}
	tc := image.Rect(wkX0, y, wkX1, y+130+64*max(len(ss), 1))
	k.card(tc)
	k.cardHead(wkX0+28, y+44, "dumbbell", "Sessions", "")
	k.textR(wkFont("regular", 17), 1136, y+44, wkFaint, fmt.Sprintf("%d days", rng))
	hf := wkFont("semi", 16)
	k.text(hf, 188, y+100, wkFaint, "DATE")
	k.text(hf, 360, y+100, wkFaint, "SESSION")
	k.textR(hf, 880, y+100, wkFaint, "TIME")
	k.textR(hf, 980, y+100, wkFaint, "SETS")
	k.textR(hf, 1136, y+100, wkFaint, "VOLUME")
	ry := y + 116
	for _, s := range ss {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		t, _ := time.Parse("2006-01-02", s.Date)
		k.text(wkFont("regular", 20), 188, ry+40, wkBlue, t.Format("Mon, Jan 2"))
		k.text(wkFont("semi", 20), 360, ry+40, wkFG, clip(wkFont("semi", 20), s.Split, 420))
		k.textR(wkFont("regular", 20), 880, ry+40, wkText2, s.Duration)
		k.textR(wkFont("regular", 20), 980, ry+40, wkText2, fmt.Sprint(s.Sets))
		k.textR(wkFont("semi", 20), 1136, ry+40, wkFG, fmtThousands(int(s.Volume))+" kg")
		ry += 64
	}
	if len(ss) == 0 {
		k.text(wkFont("regular", 20), 188, ry+44, wkMuted, "No sessions in this range.")
	}
	return tc.Max.Y
}

// --- training -------------------------------------------------------------------------------------

type liftBest struct {
	name     string
	e1rm     float64
	kg       float64
	reps     int
	date     string
	sessions int
}

// somaLifts is each movement's best estimated one-rep max (Epley) over the history.
func somaLifts() []liftBest {
	best := map[string]*liftBest{}
	for _, key := range soma.keysWith("history/") {
		date := strings.TrimPrefix(key, "history/")
		o := somaObj(key)
		exs, _ := o["exercises"].([]any)
		for _, e := range exs {
			eo, ok := e.(obj)
			if !ok {
				continue
			}
			name := str(eo["name"])
			sets, _ := eo["sets"].([]any)
			b := best[name]
			if b == nil {
				b = &liftBest{name: name}
				best[name] = b
			}
			b.sessions++
			for _, s := range sets {
				so, ok := s.(obj)
				if !ok {
					continue
				}
				kg, reps := num(so["kg"]), int(num(so["reps"]))
				if kg <= 0 || reps <= 0 {
					continue
				}
				if e1 := kg * (1 + float64(reps)/30); e1 > b.e1rm {
					b.e1rm, b.kg, b.reps, b.date = e1, kg, reps, date
				}
			}
		}
	}
	var out []liftBest
	for _, b := range best {
		if b.e1rm > 0 {
			out = append(out, *b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].e1rm > out[j].e1rm })
	return out
}

func (k *wk) training() int {
	days := lastDays(28)
	var vol []float64
	sessions, sets := 0, 0
	var last *somaSession
	for _, d := range days {
		s := somaTraining(d)
		if s == nil {
			vol = append(vol, 0)
			continue
		}
		sessions++
		sets += s.Sets
		vol = append(vol, s.Volume)
		last = s
	}
	k.header([]string{"Personal", "Training"}, "Training", "What you lifted, and how strong each movement is", color.RGBA{})
	k.primary("so:type:workout", "plus", "Log a session", wkX1)
	k.tabs("wk:tab", []string{"Overview", "Movements", "Sessions"}, nil, k.sel("tab", 0))
	box := k.row4(312)
	tv, _ := avgOf(vol)
	k.small(box(0), "dumbbell", "Sessions", fmt.Sprint(sessions), "", "last 28 days", wkFG)
	k.small(box(1), "chart-column", "Volume / session", fmtThousands(int(tv)), "kg", "lifted, all sets", wkFG)
	k.small(box(2), "list-checks", "Sets", fmt.Sprint(sets), "", fmt.Sprintf("%.0f a session", float64(sets)/math.Max(float64(sessions), 1)), wkFG)
	lastS, lastF := "—", "nothing logged"
	if last != nil {
		lastS, lastF = dayLabel(last.Date), last.Split
	}
	k.small(box(3), "calendar", "Last session", lastS, "", lastF, wkFG)
	labels := make([]string, len(days))
	for i, d := range days {
		labels[i] = dayLabel(d)
	}
	vr := image.Rect(wkX0, 496, wkX1, 816)
	k.card(vr)
	k.cardHead(wkX0+28, 540, "chart-column", "Volume", "· kg a day, last 28 days")
	k.bars(image.Rect(wkX0+90, 580, wkX1-28, 770), vol, wkLime, 0, labels, 7, "kg")
	lifts := somaLifts()
	y := 840
	lc := image.Rect(wkX0, y, wkX1, y+96+72*max(min(len(lifts), 12), 1))
	k.card(lc)
	k.cardHead(wkX0+28, y+44, "trending-up", "Strength by movement", "· best estimated 1RM")
	ry := y + 76
	for i, l := range lifts {
		if i == 12 {
			break
		}
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 21), 188, ry+34, wkFG, clip(wkFont("semi", 21), l.name, 640))
		k.text(wkFont("regular", 17), 188, ry+60, wkFaint, fmt.Sprintf("best %g kg × %d · %s · %d sessions", l.kg, l.reps, dayLabel(l.date), l.sessions))
		k.textR(wkFont("bold", 24), 1136, ry+46, wkFG, fmt.Sprintf("%.1f kg", l.e1rm))
		ry += 72
	}
	if len(lifts) == 0 {
		k.text(wkFont("regular", 20), 188, ry+50, wkMuted, "Sessions logged on the phone show their lifts here.")
	}
	return lc.Max.Y
}

// --- nutrition ------------------------------------------------------------------------------------

func (k *wk) nutrition() int {
	date := somaToday()
	n := somaNutrition(date)
	day := somaObj("nutrition/" + date)
	k.header([]string{"Personal", "Nutrition"}, "Nutrition", time.Now().In(displayZone).Format("Monday 2 January"), color.RGBA{})
	k.primary("so:type:food", "plus", "Add food", wkX1)
	k.tabs("wk:tab", []string{"Dashboard", "Log", "Week"}, nil, k.sel("tab", 0))
	goals := somaObj("settings.goals")
	gC, gF := num(goals["carbs"]), num(goals["fat"])
	if gC == 0 {
		gC = 260
	}
	if gF == 0 {
		gF = 70
	}
	gW := num(goals["water"])
	if gW == 0 {
		gW = 3500
	}
	// Calories, and the three macros.
	cr := image.Rect(wkX0, 312, wkX1, 452)
	k.card(cr)
	k.text(wkFont("semi", 20), 188, 356, wkMuted, "CALORIES")
	k.textR(wkFont("bold", 24), 1136, 358, wkFG, fmt.Sprintf("%.0f / %.0f", n.Cals, n.GoalCals))
	k.hBar(k.R(188, 384, 1136, 400), n.Cals/max(n.GoalCals, 1), rgb(0xff375f))
	k.text(wkFont("regular", 18), 188, 432, wkFaint, fmt.Sprintf("%.0f kcal left", max(n.GoalCals-n.Cals, 0)))
	box := func(i int) image.Rectangle {
		cw := (wkX1 - wkX0 - 40) / 3
		return image.Rect(wkX0+i*(cw+20), 472, wkX0+i*(cw+20)+cw, 612)
	}
	macro := func(i int, label string, v, goal float64, col color.RGBA) {
		r := box(i)
		k.card(r)
		ui.RoundRect(k.img, k.R(r.Min.X+24, r.Min.Y+24, r.Min.X+28, r.Min.Y+46), 2, col)
		k.text(wkFont("semi", 18), r.Min.X+38, r.Min.Y+42, wkMuted, label)
		e := k.text(wkFont("bold", 32), r.Min.X+24, r.Min.Y+88, col, fmt.Sprintf("%.0f", v))
		k.text(wkFont("medium", 20), e+4, r.Min.Y+88, wkMuted, fmt.Sprintf("/%.0fg", goal))
		k.hBar(k.R(r.Min.X+24, r.Min.Y+108, r.Max.X-24, r.Min.Y+116), v/max(goal, 1), col)
	}
	macro(0, "PROTEIN", n.P, n.GoalP, rgb(0x9cf03a))
	macro(1, "CARBS", n.C, gC, wkBlue)
	macro(2, "FAT", n.F, gF, rgb(0xff5a5a))
	// Water.
	wr := image.Rect(wkX0, 632, wkX1, 812)
	k.card(wr)
	k.cardHead(wkX0+28, 676, "activity", "Water", "")
	k.textR(wkFont("bold", 22), 1136, 676, rgb(0x4cc9f0), fmt.Sprintf("%.0f / %.0f ml", n.Water, gW))
	k.hBar(k.R(188, 700, 1136, 712), n.Water/gW, rgb(0x4cc9f0))
	for i, ml := range []int{250, 500, -250} {
		x0 := 188 + i*230
		k.boxBtn(fmt.Sprintf("wk:water:%d", ml), x0, 732, x0+210, 790, "plus", fmt.Sprintf("%+d ml", ml), color.RGBA{}, wkFG)
	}
	// What was eaten today.
	items, _ := day["items"].([]any)
	y := 836
	lc := image.Rect(wkX0, y, wkX1, y+96+72*max(len(items), 1))
	k.card(lc)
	k.cardHead(wkX0+28, y+44, "utensils", "Today's food", fmt.Sprintf("· %d items", len(items)))
	ry := y + 76
	for _, it := range items {
		f, ok := it.(obj)
		if !ok {
			continue
		}
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 21), 188, ry+34, wkFG, clip(wkFont("semi", 21), str(f["name"]), 600))
		when := ""
		if t := num(f["eatenAt"]); t > 0 {
			when = time.UnixMilli(int64(t)).In(displayZone).Format("15:04") + " · "
		}
		k.text(wkFont("regular", 17), 188, ry+60, wkFaint, when+str(f["meal"]))
		k.textR(wkFont("bold", 21), 1136, ry+34, wkFG, fmt.Sprintf("%.0f kcal", num(f["cals"])))
		k.textR(wkFont("regular", 17), 1136, ry+60, wkFaint, fmt.Sprintf("%.0f g protein", num(f["p"])))
		ry += 72
	}
	if len(items) == 0 {
		k.text(wkFont("regular", 20), 188, ry+50, wkMuted, "Nothing logged today. Add food: \"Wrap 450 35\" is the name, calories, protein.")
	}
	// The week.
	y = lc.Max.Y + 20
	days := lastDays(7)
	var cals []float64
	labels := make([]string, 7)
	for i, d := range days {
		cals = append(cals, somaNutrition(d).Cals)
		t, _ := time.Parse("2006-01-02", d)
		labels[i] = t.Format("Mon")
	}
	wc := image.Rect(wkX0, y, wkX1, y+320)
	k.card(wc)
	k.cardHead(wkX0+28, y+44, "chart-column", "This week", "· calories a day")
	k.bars(image.Rect(wkX0+90, y+84, wkX1-28, y+270), cals, rgb(0xff375f), n.GoalCals, labels, 1, "")
	return wc.Max.Y
}

// --- mind -----------------------------------------------------------------------------------------

func (k *wk) mindPage() int {
	date := somaToday()
	goal := num(jsonValue(soma.get("settings.readingGoalMin")))
	if goal <= 0 {
		goal = 30
	}
	k.header([]string{"Personal", "Mind"}, "Mind", "Reading, and the notes vault", color.RGBA{})
	k.tabs("wk:tab", []string{"Reading", "Notes"}, nil, k.sel("tab", 0))
	days := lastDays(28)
	var mins []float64
	streak := 0
	for _, d := range days {
		mins = append(mins, float64(somaReading(d)))
	}
	for i := len(days) - 1; i >= 0; i-- {
		if mins[i] >= goal {
			streak++
		} else if i < len(days)-1 {
			break
		}
	}
	box := k.row4(312)
	today := float64(somaReading(date))
	k.small(box(0), "timer", "Today", fmt.Sprintf("%.0f", today), "min", fmt.Sprintf("of your %.0f-minute goal", goal), wkFG)
	am, _ := avgOf(mins)
	k.small(box(1), "chart-column", "Average", fmt.Sprintf("%.0f", am), "min", "a day you read, 28 days", wkFG)
	k.small(box(2), "flag", "Streak", fmt.Sprint(streak), "days", "at the goal or more", wkFG)
	tot := 0.0
	for _, m := range mins {
		tot += m
	}
	k.small(box(3), "history", "28 days", fmt.Sprintf("%.1f", tot/60), "h", "read in all", wkFG)
	labels := make([]string, len(days))
	for i, d := range days {
		labels[i] = dayLabel(d)
	}
	rr := image.Rect(wkX0, 496, wkX1, 816)
	k.card(rr)
	k.cardHead(wkX0+28, 540, "chart-column", "Minutes read", "· a day")
	k.bars(image.Rect(wkX0+90, 580, wkX1-28, 770), mins, wkPurple, goal, labels, 7, "")
	// The notes, newest first.
	notes := somaNotes()
	y := 840
	nc := image.Rect(wkX0, y, wkX1, y+96+72*max(min(len(notes), 10), 1))
	k.card(nc)
	k.cardHead(wkX0+28, y+44, "files", "Notes", fmt.Sprintf("· %d in the vault", len(notes)))
	ry := y + 76
	for i, nt := range notes {
		if i == 10 {
			break
		}
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 21), 188, ry+34, wkFG, clip(wkFont("semi", 21), nt.title(), 760))
		k.text(wkFont("regular", 17), 188, ry+60, wkFaint, clip(wkFont("regular", 17), strings.TrimSpace(strings.Join(strings.Fields(nt.Text), " ")), 760))
		if nt.Mtime > 0 {
			k.textR(wkFont("regular", 17), 1136, ry+34, wkFaint, time.UnixMilli(nt.Mtime).In(displayZone).Format("2 Jan"))
		}
		k.btn("so:note:"+nt.ID, k.R(wkX0, ry, wkX1, ry+72))
		ry += 72
	}
	if len(notes) == 0 {
		k.text(wkFont("regular", 20), 188, ry+50, wkMuted, "The vault fills in once the PC's Soma has it linked.")
	}
	return nc.Max.Y
}

// --- stats ----------------------------------------------------------------------------------------

func (k *wk) stats() int {
	days := lastDays(28)
	k.header([]string{"Personal", "Stats"}, "Stats", "The last four weeks, against the four before", color.RGBA{})
	prev := make([]string, 28)
	for i := range prev {
		prev[i] = somaDate(time.Now().AddDate(0, 0, -(55 - i)))
	}
	series := []struct {
		label, unit string
		get         func(d string) float64
		good        bool // higher is better
	}{
		{"Calories", "kcal", func(d string) float64 { return somaNutrition(d).Cals }, true},
		{"Protein", "g", func(d string) float64 { return somaNutrition(d).P }, true},
		{"Sleep", "h", sleepHours, true},
		{"Reading", "min", func(d string) float64 { return float64(somaReading(d)) }, true},
		{"Training volume", "kg", func(d string) float64 {
			if s := somaTraining(d); s != nil {
				return s.Volume
			}
			return 0
		}, true},
		{"Water", "ml", func(d string) float64 { return somaNutrition(d).Water }, true},
	}
	y := 312
	hs := somaHabits()
	for i, s := range series {
		var a, b []float64
		for j := range days {
			a, b = append(a, s.get(days[j])), append(b, s.get(prev[j]))
		}
		va, na := avgOf(a)
		vb, _ := avgOf(b)
		col := i % 2
		r := image.Rect(wkX0+col*512, y, wkX0+col*512+492, y+180)
		k.card(r)
		k.text(wkFont("medium", 20), r.Min.X+24, r.Min.Y+42, wkText2, s.label)
		k.textR(wkFont("regular", 17), r.Max.X-24, r.Min.Y+42, wkFaint, fmt.Sprintf("%d of 28 days", na))
		e := k.text(wkFont("bold", 36), r.Min.X+24, r.Min.Y+92, wkFG, fmtThousands(int(va+0.5)))
		k.text(wkFont("medium", 20), e+4, r.Min.Y+92, wkMuted, s.unit+" avg")
		k.delta(r.Min.X+24, r.Min.Y+130, pctChange(vb, va), va >= vb == s.good, "vs the 4 weeks before")
		k.spark(k.R(r.Max.X-150, r.Min.Y+70, r.Max.X-24, r.Min.Y+110), a, wkLime)
		if col == 1 {
			y += 200
		}
	}
	// Habits, the share of days each was done.
	hc := image.Rect(wkX0, y, wkX1, y+96+64*max(len(hs), 1))
	k.card(hc)
	k.cardHead(wkX0+28, y+44, "target", "Habits", "· share of the last 28 days done")
	ry := y + 76
	for _, h := range hs {
		done := 0
		for _, d := range days {
			if h.done(d) {
				done++
			}
		}
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 20), 188, ry+40, wkFG, clip(wkFont("semi", 20), h.Name, 360))
		k.hBar(k.R(580, ry+28, 1040, ry+38), float64(done)/28, wkLime)
		k.textR(wkFont("semi", 20), 1136, ry+40, wkFG, fmt.Sprintf("%.0f%%", float64(done)/28*100))
		ry += 64
	}
	if len(hs) == 0 {
		k.text(wkFont("regular", 20), 188, ry+48, wkMuted, "No habits yet.")
	}
	return hc.Max.Y
}

// --- settings -------------------------------------------------------------------------------------

// wkSettingRows are the settings the tablet can change, as Soma keeps them.
var wkSettingRows = []struct{ group, key, label, hint string }{
	{"You", "name", "Your name", "what Soma greets you with"},
	{"You", "currency", "Currency", "GBP, EUR, USD or DZD"},
	{"Work", "workDayHours", "Hours a day", "the day's target"},
	{"Work", "workWeekHours", "Hours a week", "the week's target"},
	{"Body", "customGoals.cals", "Calories", "kcal a day"},
	{"Body", "customGoals.protein", "Protein", "grams a day"},
	{"Body", "customGoals.water", "Water", "ml a day"},
	{"Mind", "readingGoalMin", "Reading", "minutes a day"},
}

func settingValue(key string) string {
	if k, sub, ok := strings.Cut(key, "."); ok {
		o, _ := jsonValue(soma.get("settings." + k)).(map[string]any)
		if v, ok := o[sub]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	v := jsonValue(soma.get("settings." + key))
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// wkSetSetting keeps a typed setting: numbers as numbers, in Soma's own shape.
func wkSetSetting(key, text string) {
	text = strings.TrimSpace(text)
	var v any = text
	var f float64
	if _, err := fmt.Sscan(text, &f); err == nil && key != "name" && key != "currency" {
		v = f
	}
	if c, ok := strings.CutPrefix(key, "rate:"); ok {
		rates, _ := jsonValue(soma.get("settings.clientRates")).(map[string]any)
		if rates == nil {
			rates = map[string]any{}
		}
		rates[c] = f
		somaPut("settings.clientRates", rates)
		return
	}
	if k, sub, ok := strings.Cut(key, "."); ok {
		o, _ := jsonValue(soma.get("settings." + k)).(map[string]any)
		if o == nil {
			o = map[string]any{}
		}
		o[sub] = v
		somaPut("settings."+k, o)
		return
	}
	if key == "currency" {
		v = strings.ToUpper(text)
	}
	somaPut("settings."+key, v)
}

func (k *wk) settingsPage() int {
	k.header([]string{"Soma", "Settings"}, "Settings", "Soma on this tablet: you, your targets, your clients, sync", color.RGBA{})
	su := &k.c.somaUI
	y := 236
	// The groups as cards, a row per setting; a tap types a new value.
	groups := []string{"You", "Work", "Body", "Mind"}
	for _, g := range groups {
		var items []int
		for i, r := range wkSettingRows {
			if r.group == g {
				items = append(items, i)
			}
		}
		k.text(wkFont("semi", 18), wkX0+8, y+20, wkFaint, strings.ToUpper(g))
		y += 36
		c := image.Rect(wkX0, y, wkX1, y+8+80*len(items))
		k.card(c)
		ry := y + 4
		for j, i := range items {
			r := wkSettingRows[i]
			if j > 0 {
				ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			}
			k.text(wkFont("semi", 21), 188, ry+36, wkFG, r.label)
			k.text(wkFont("regular", 17), 188, ry+62, wkFaint, r.hint)
			v := settingValue(r.key)
			if su.typing && su.field == "set:"+r.key {
				v = withCursor(su.editing, k.c.fieldBack)
			}
			if v == "" {
				v = "Not set"
			}
			k.textR(wkFont("medium", 21), 1100, ry+48, wkText2, v)
			k.icon("chevron-right", 1124, ry+40, 20, wkFaint)
			k.btn("so:type:set:"+r.key, k.R(wkX0, ry, wkX1, ry+80))
			ry += 80
		}
		y = c.Max.Y + 24
	}
	// Each client's rate.
	rates, _ := jsonValue(soma.get("settings.clientRates")).(map[string]any)
	seen := map[string]bool{}
	var clients []string
	for c := range rates {
		clients, seen[c] = append(clients, c), true
	}
	for _, p := range wkProjects() {
		if p.Client != "" && !seen[p.Client] {
			clients, seen[p.Client] = append(clients, p.Client), true
		}
	}
	sort.Strings(clients)
	k.text(wkFont("semi", 18), wkX0+8, y+20, wkFaint, "CLIENT RATES")
	y += 36
	cc := image.Rect(wkX0, y, wkX1, y+8+80*max(len(clients), 1))
	k.card(cc)
	ry := y + 4
	for j, cl := range clients {
		if j > 0 {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		}
		k.text(wkFont("semi", 21), 188, ry+48, wkFG, clip(wkFont("semi", 21), cl, 600))
		v := "Not set"
		if r := num(rates[cl]); r > 0 {
			v = wkMoney(r) + " an hour"
		}
		if su.typing && su.field == "set:rate:"+cl {
			v = withCursor(su.editing, k.c.fieldBack)
		}
		k.textR(wkFont("medium", 21), 1100, ry+48, wkText2, v)
		k.icon("chevron-right", 1124, ry+40, 20, wkFaint)
		k.btn("so:type:set:rate:"+cl, k.R(wkX0, ry, wkX1, ry+80))
		ry += 80
	}
	if len(clients) == 0 {
		k.text(wkFont("regular", 20), 188, ry+48, wkMuted, "Clients come from projects and shifts.")
	}
	y = cc.Max.Y + 24
	// Sync.
	k.text(wkFont("semi", 18), wkX0+8, y+20, wkFaint, "SYNC")
	y += 36
	sc := image.Rect(wkX0, y, wkX1, y+168)
	k.card(sc)
	k.text(wkFont("semi", 21), 188, y+50, wkFG, "Soma relay")
	k.text(wkFont("regular", 18), 188, y+80, wkFaint, clip(wkFont("regular", 18), soma.status(), 600))
	k.boxBtn("so:sync", 900, y+24, 1136, y+84, "repeat", "Sync now", wkLime, wkInk)
	k.text(wkFont("regular", 17), 188, y+140, wkFaint, "Linked with your recovery code; it never leaves the tablet.")
	return sc.Max.Y
}
