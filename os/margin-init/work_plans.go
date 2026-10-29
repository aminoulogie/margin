package main

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"margin-init/ui"
)

// Plans: projects for deadline-driven personal work (a Campus France application: a fixed exam
// date, documents waiting on other people, phases, a hard deadline), next to client projects.
// A project with kind "plan" hides the client, rate and money and leads with dated steps; a
// step may carry a date (due), be a milestone (a fixed-date event, not work), belong to a
// phase, and wait on someone else (never shown as my overdue). All of it is optional fields
// on Soma's own records (Project.kind, ProjectStep.due/milestone/phase/waitingOn), edited in
// place, so a client project reads and draws exactly as before.

// --- pasting a plan ----------------------------------------------------------------------------

// planLine is one line of a pasted plan: "YYYY-MM-DD | Phase | Step label | flags", the date
// may be blank; flags: milestone, waiting:<who> (comma separated).
type planLine struct {
	due, phase, label, waiting string
	milestone                  bool
}

// parsePlan reads a pasted plan, a step a line (lines may also be split by " // ", for the
// one-line field on the tablet). Lines with no label are skipped.
func parsePlan(text string) []planLine {
	var out []planLine
	text = strings.ReplaceAll(text, "\r", "\n")
	for _, raw := range strings.FieldsFunc(strings.ReplaceAll(text, " // ", "\n"), func(r rune) bool { return r == '\n' }) {
		f := strings.Split(raw, "|")
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		var l planLine
		switch len(f) {
		case 1:
			l.label = f[0]
		case 2:
			l.due, l.label = f[0], f[1]
		default:
			l.due, l.phase, l.label = f[0], f[1], f[2]
			for _, fl := range f[3:] {
				for _, flag := range strings.Split(fl, ",") {
					flag = strings.TrimSpace(flag)
					switch low := strings.ToLower(flag); {
					case low == "milestone":
						l.milestone = true
					case strings.HasPrefix(low, "waiting:"):
						l.waiting = strings.TrimSpace(flag[len("waiting:"):])
					case low == "waiting":
						l.waiting = "someone"
					}
				}
			}
		}
		if !wkDateRE.MatchString(l.due) {
			if l.due != "" && len(f) == 2 { // "Phase | label" without a date
				l.phase = l.due
			}
			l.due = ""
		}
		if l.label != "" {
			out = append(out, l)
		}
	}
	return out
}

// stepObj is a pasted line as Soma's ProjectStep (only the fields it has).
func (l planLine) stepObj() obj {
	o := obj{"id": "ps-" + somaNewID(), "label": l.label, "done": false}
	if l.due != "" {
		o["due"] = l.due
	}
	if l.phase != "" {
		o["phase"] = l.phase
	}
	if l.milestone {
		o["milestone"] = true
	}
	if l.waiting != "" {
		o["waitingOn"] = l.waiting
	}
	return o
}

// wkPastePlan adds a pasted plan's steps to a project, in order; how many.
func wkPastePlan(pid, text string) int {
	lines := parsePlan(text)
	if len(lines) == 0 {
		return 0
	}
	wkProjectSteps(pid, true, func(steps []any) []any {
		for _, l := range lines {
			steps = append(steps, l.stepObj())
		}
		return steps
	})
	return len(lines)
}

// wkNewPlan makes a plan project (kind "plan") with a deadline and pasted steps; its id.
func wkNewPlan(name, due, text string) string {
	id := wkNewProject(name, "")
	wkSetProject(id, "kind", "plan")
	if wkDateRE.MatchString(due) {
		wkSetProject(id, "due", due)
	}
	wkPastePlan(id, text)
	return id
}

// Plans made from the Terminal (margin-init soma-plan) wait in this folder until Margin, which
// owns the records, takes them in at its next sync: header "name | due", then the plan.
func wkPlanInbox() string { return filepath.Join(somaDir, "plan-inbox") }

func wkImportPlans() {
	files, _ := filepath.Glob(filepath.Join(wkPlanInbox(), "*.txt"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		os.Remove(f)
		if err != nil {
			continue
		}
		head, body, _ := strings.Cut(string(b), "\n")
		name, due, _ := strings.Cut(head, "|")
		if name = strings.TrimSpace(name); name != "" {
			wkNewPlan(name, strings.TrimSpace(due), body)
		}
	}
}

// runSomaPlan: margin-init soma-plan "Name" YYYY-MM-DD < plan. Queues a plan project for the
// running Margin to add (from the Clipboard, inside Alpine).
func runSomaPlan(args []string) int {
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		somaDir = "/proc/1/root" + marginHome + "/soma"
	}
	if len(args) < 1 {
		fmt.Println(`usage: margin-init soma-plan "Name" [YYYY-MM-DD] < plan.txt`)
		return 2
	}
	due := ""
	if len(args) > 1 {
		due = args[1]
	}
	b, _ := io.ReadAll(os.Stdin)
	lines := parsePlan(string(b))
	if len(lines) == 0 {
		fmt.Println("no steps read: one a line, YYYY-MM-DD | Phase | Step | flags")
		return 1
	}
	os.MkdirAll(wkPlanInbox(), 0o700)
	f := filepath.Join(wkPlanInbox(), fmt.Sprintf("%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(f, []byte(args[0]+" | "+due+"\n"+string(b)), 0o600); err != nil {
		fmt.Println("couldn't queue it:", err)
		return 1
	}
	for _, l := range lines {
		fl := ""
		if l.milestone {
			fl += " [milestone]"
		}
		if l.waiting != "" {
			fl += " [waiting: " + l.waiting + "]"
		}
		fmt.Printf("%-10s  %-10s  %s%s\n", l.due, l.phase, l.label, fl)
	}
	fmt.Printf("%q (plan, due %s): %d steps queued. Margin adds it within two minutes, or at once when you open a Soma page.\n", args[0], due, len(lines))
	return 0
}

// --- dated steps, for the planner, the calendar and the overview ---------------------------------

type wkDatedStep struct {
	p wkProject
	s wkStep
}

// wkStepsDue are the undone dated steps of active projects due from..to (dates, to included);
// overdue: also those of mine past their date before from.
func wkStepsDue(projects []wkProject, from, to string, overdue bool) []wkDatedStep {
	var out []wkDatedStep
	for _, p := range projects {
		if p.Status != "active" {
			continue
		}
		for _, s := range p.Steps {
			if s.Done || s.Due == "" || s.Due > to {
				continue
			}
			if s.Due >= from || overdue && s.overdue(from) {
				out = append(out, wkDatedStep{p, s})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].s.Due < out[j].s.Due })
	return out
}

// stepState is how a dated step stands: its words and colour.
func stepState(s wkStep, now time.Time) (string, color.RGBA) {
	today := somaDate(now)
	switch {
	case s.Done:
		return "done", wkFaint
	case s.WaitingOn != "":
		return "Waiting · " + s.WaitingOn, wkAmber
	case s.overdue(today):
		d, _ := time.ParseInLocation("2006-01-02", s.Due, displayZone)
		n := int(dayStart(now).Sub(d).Hours()/24 + 0.5)
		return fmt.Sprintf("%d %s late", n, map[bool]string{true: "day", false: "days"}[n == 1]), wkRed
	case s.Milestone && s.Due != "":
		return wkCountdown("", s.Due, now)[1:], wkPurple
	case s.Due == today:
		return "today", wkAmber
	}
	return "", wkMuted
}

// datedStepRows are dated steps to tick (the box ticks the step itself) or open (the rest).
func (k *wk) datedStepRows(list []wkDatedStep, ry int, now time.Time, showDate bool) int {
	for _, d := range list {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		cy := ry + 42
		k.checkbox(188, cy, d.s.Done)
		k.btn("wk:step:"+d.p.ID+":"+d.s.ID, k.R(wkX0, ry, 236, ry+83))
		col := wkColour(d.p, d.p.order)
		if d.s.Milestone {
			k.icon("flag", 248, cy, 20, col)
		} else {
			ui.RoundRect(k.img, k.R(241, cy-7, 255, cy+7), 3, col)
		}
		k.struck(273, cy-2, d.s.Label, d.s.Done, 560)
		k.text(wkFont("regular", 17), 273, cy+24, wkFaint, clip(wkFont("regular", 17), d.p.Name+phaseNote(d.s), 560))
		st, sc := stepState(d.s, now)
		if showDate {
			st = strings.TrimPrefix(wkDate(d.s.Due)+" · "+st, " · ")
			st = strings.TrimSuffix(st, " · ")
		}
		k.textR(wkFont("regular", 19), 1100, cy+7, sc, st)
		k.btn("wk:sheet-open:step:"+d.p.ID+":"+d.s.ID, k.R(236, ry, wkX1, ry+83))
		ry += 83
	}
	return ry
}

func phaseNote(s wkStep) string {
	if s.Phase != "" {
		return " · " + s.Phase
	}
	return ""
}

// dueThisWeek is the overview's list of plan steps due in the next seven days (and mine late).
func (k *wk) dueThisWeek(projects []wkProject, y int, now time.Time) int {
	var plans []wkProject
	for _, p := range projects {
		if p.plan() {
			plans = append(plans, p)
		}
	}
	today := somaDate(now)
	list := wkStepsDue(plans, today, somaDate(now.AddDate(0, 0, 6)), true)
	if len(list) == 0 {
		return y
	}
	cr := image.Rect(wkX0, y, wkX1, y+92+83*len(list))
	k.card(cr)
	e := k.cardHead(wkX0+28, y+53, "calendar-clock", "Due this week", "")
	k.text(wkFont("regular", 19), e+14, y+53, wkFaint, "· from your plans")
	k.datedStepRows(list, y+92, now, true)
	return cr.Max.Y + 30
}

// --- a plan's page ---------------------------------------------------------------------------------

// planGroups are a plan's steps by phase, in order of first appearance; with no phases, by the
// week of their date (undated last).
func planGroups(p wkProject) (names []string, groups map[string][]wkStep) {
	groups = map[string][]wkStep{}
	phased := false
	for _, s := range p.Steps {
		if s.Phase != "" {
			phased = true
		}
	}
	for _, s := range p.Steps {
		g := s.Phase
		if !phased {
			g = "No date"
			if t, err := time.ParseInLocation("2006-01-02", s.Due, displayZone); err == nil {
				g = "Week of " + weekStart(t).Format("2 Jan")
			}
		} else if g == "" {
			g = "Other steps"
		}
		if _, ok := groups[g]; !ok {
			names = append(names, g)
		}
		groups[g] = append(groups[g], s)
	}
	if !phased { // weeks in date order, the undated last
		key := func(g string) string {
			if g == "No date" {
				return "~"
			}
			return groups[g][0].Due
		}
		sort.SliceStable(names, func(i, j int) bool { return key(names[i]) < key(names[j]) })
	}
	return names, groups
}

// planSteps is a plan's steps grouped by phase: date, milestone flag, overdue in red, waiting
// in amber; tick the box, open the rest.
func (k *wk) planSteps(p wkProject, y int, now time.Time) int {
	names, groups := planGroups(p)
	h := 92 + 84*len(p.Steps) + 56*len(names)
	sc := image.Rect(wkX0, y, wkX1, y+max(h, 176))
	k.card(sc)
	left := len(p.Steps) - p.done()
	e := k.cardHead(wkX0+28, y+53, "list-checks", "Plan", "")
	k.text(wkFont("regular", 19), e+14, y+53, wkFaint, fmt.Sprintf("%d of %d done · %d left", p.done(), len(p.Steps), left))
	x := k.outline("wk:type-open:step:"+p.ID, "plus", "Add step", 1136, y+22, 50)
	k.outline("wk:type-open:pplan:"+p.ID, "file-text", "Paste plan", x-12, y+22, 50)
	ry := y + 92
	col := wkColour(p, p.order)
	for _, g := range names {
		steps := groups[g]
		done := 0
		for _, s := range steps {
			if s.Done {
				done++
			}
		}
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 18), 190, ry+36, col, strings.ToUpper(g))
		k.textR(wkFont("regular", 18), 1093, ry+36, wkFaint, fmt.Sprintf("%d/%d", done, len(steps)))
		ry += 56
		for _, s := range steps {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			cy := ry + 42
			k.checkbox(188, cy, s.Done)
			k.btn("wk:step:"+p.ID+":"+s.ID, k.R(wkX0, ry, 236, ry+84))
			lx := 240
			if s.Milestone {
				k.icon("flag", 252, cy, 22, wkPurple)
				lx = 274
			} else if s.WaitingOn != "" && !s.Done {
				k.icon("hourglass", 252, cy, 22, wkAmber)
				lx = 274
			}
			k.struck(lx, cy+8, s.Label, s.Done, 560)
			when := ""
			if s.Due != "" {
				when = wkDate(s.Due)
			}
			st, stc := stepState(s, now)
			if st != "" && st != "done" {
				k.textR(wkFont("regular", 17), 1093, cy+24, stc, clip(wkFont("regular", 17), st, 300))
				k.textR(wkFont("semi", 19), 1093, cy-2, map[bool]color.RGBA{true: wkRed, false: wkText2}[stc == wkRed], when)
			} else {
				k.textR(wkFont("regular", 19), 1093, cy+7, wkMuted, when)
			}
			k.icon("ellipsis", 1120, cy, 24, wkFaint)
			k.btn("wk:sheet-open:step:"+p.ID+":"+s.ID, k.R(236, ry, wkX1, ry+84))
			ry += 84
		}
	}
	if len(p.Steps) == 0 {
		k.emptyNote(190, ry+50, "No steps yet: Paste plan takes one a line, date | phase | step | flags.")
	}
	return sc.Max.Y
}

// planHero is the top of a plan's page: progress, how it stands, what's next, the deadline.
func (k *wk) planHero(p wkProject, now time.Time) {
	col := wkColour(p, p.order)
	hero := image.Rect(wkX0, 312, wkX1, 648)
	k.cardIn(hero, blend(wkCard, col, 0.08))
	k.arc(285, 435, 84, 18, p.progress(), col, rgb(0x262833))
	k.textC(wkFont("bold", 38), 285, 445, wkFG, fmt.Sprintf("%.0f%%", p.progress()*100))
	k.textC(wkFont("regular", 17), 285, 470, wkMuted, fmt.Sprintf("%d of %d steps", p.done(), len(p.Steps)))
	st, scol := statusOf(p, now)
	bx := 408
	pill := func(id, text string, c color.RGBA) {
		f := wkFont("semi", 18)
		w := ui.TextWidth(f, text) + 30
		r := k.R(bx, 350, bx+w, 386)
		ui.RoundRect(k.img, r, 18, blend(wkCard, c, 0.18))
		apText(k.img, f, r.Min.X+15, r.Min.Y+25, c, text)
		k.btn(id, r.Inset(-6))
		bx += w + 12
	}
	pill("wk:sheet-open:pstatus:"+p.ID, st, scol)
	pill("wk:sheet-open:proj:"+p.ID, "Plan", wkPurple)
	switch wkPace(p, now) {
	case "behind":
		pill("wk:tab:1", "Behind", wkRed)
	case "ahead":
		pill("wk:tab:1", "On track", wkGreen)
	case "overdue":
		pill("wk:tab:1", "Past the date", wkRed)
	}
	wait := 0
	for _, s := range p.Steps {
		if !s.Done && s.WaitingOn != "" {
			wait++
		}
	}
	next, due := "Nothing dated", "No date"
	if s, ok := p.nextDated(false); ok {
		next = wkCountdown("", s.Due, now)[1:]
	}
	if p.Due != "" {
		due = strings.TrimSpace(wkCountdown("", p.Due, now))
	}
	stats := []struct {
		l, v, id string
		c        color.RGBA
	}{{"Next", next, "wk:tab:1", wkFG}, {"Overdue", fmt.Sprint(p.overdueSteps(now)), "wk:tab:1", wkRed}, {"Waiting", fmt.Sprint(wait), "wk:tab:1", wkAmber}, {"Deadline", due, "wk:pdue:" + p.ID, wkFG}}
	if stats[1].v == "0" {
		stats[1].c = wkFG
	}
	if stats[2].v == "0" {
		stats[2].c = wkFG
	}
	for i, s := range stats {
		x := 408 + i*186
		k.text(wkFont("regular", 19), x, 428, wkFaint, s.l)
		k.text(wkFont("bold", 26), x, 464, s.c, clip(wkFont("bold", 26), s.v, 176))
		k.btn(s.id, k.R(x-8, 400, x+178, 480))
	}
	ln := ""
	if m, ok := p.nextDated(true); ok {
		ln = wkCountdown(m.Label, m.Due, now)
	}
	if p.Due != "" {
		due, _ := time.Parse("2006-01-02", p.Due)
		ln += " · deadline " + due.Format("Mon 2 Jan 2006")
	}
	k.text(wkFont("regular", 19), 408, 514, wkFaint, clip(wkFont("regular", 19), strings.TrimPrefix(ln, " · "), 730))
	x := k.outline("wk:type-open:step:"+p.ID, "plus", "Add step", 1133, 556, 62)
	x = k.outline("wk:type-open:pplan:"+p.ID, "file-text", "Paste plan", x-12, 556, 62)
	k.outline("wk:type-open:pnote:"+p.ID, "sticky-note", "Note", x-12, 556, 62)
}

// --- the step's fields, and the project's kind ------------------------------------------------------

func wkSetStepField(pid, sid, key string, v any) {
	wkProjectSteps(pid, false, func(steps []any) []any {
		if i := stepIndex(steps, sid); i >= 0 {
			so := steps[i].(obj)
			if v == nil || v == "" || v == false {
				delete(so, key)
			} else {
				so[key] = v
			}
		}
		return steps
	})
}

func wkStepOf(pid, sid string) (wkStep, wkProject, bool) {
	p, ok := wkProjectByID(pid)
	if !ok {
		return wkStep{}, p, false
	}
	for _, s := range p.Steps {
		if s.ID == sid {
			return s, p, true
		}
	}
	return wkStep{}, p, false
}

func init() {
	wkActions["sdue"] = func(c *console, arg string) { // <pid>:<sid>
		pid, sid, _ := strings.Cut(arg, ":")
		s, _, _ := wkStepOf(pid, sid)
		c.wkPickDate("sdue:"+arg, s.Due, "Date for "+s.Label)
	}
	wkDateSetters["sdue"] = func(c *console, arg, date string) {
		pid, sid, _ := strings.Cut(arg, ":")
		wkSetStepField(pid, sid, "due", date)
	}
	wkActions["smile"] = func(c *console, arg string) {
		pid, sid, _ := strings.Cut(arg, ":")
		s, _, _ := wkStepOf(pid, sid)
		wkSetStepField(pid, sid, "milestone", !s.Milestone)
		c.somaUI.keep = true
	}
	wkPrompts["sphase"] = func(arg string) (string, string) {
		pid, sid, _ := strings.Cut(arg, ":")
		s, _, _ := wkStepOf(pid, sid)
		return "Phase, e.g. Documents (empty for none)", s.Phase
	}
	wkTypers["sphase"] = func(c *console, arg, text string) {
		pid, sid, _ := strings.Cut(arg, ":")
		wkSetStepField(pid, sid, "phase", strings.TrimSpace(text))
	}
	wkPrompts["swait"] = func(arg string) (string, string) {
		pid, sid, _ := strings.Cut(arg, ":")
		s, _, _ := wkStepOf(pid, sid)
		return "Waiting on whom, e.g. translator (empty: not waiting)", s.WaitingOn
	}
	wkTypers["swait"] = func(c *console, arg, text string) {
		pid, sid, _ := strings.Cut(arg, ":")
		wkSetStepField(pid, sid, "waitingOn", strings.TrimSpace(text))
	}
	wkActions["pkind"] = func(c *console, arg string) { // <id>:client|plan
		id, kind, _ := strings.Cut(arg, ":")
		if kind == "plan" {
			wkSetProject(id, "kind", "plan")
		} else {
			wkSetProject(id, "kind", nil) // absent = client, as Soma reads it
		}
		c.somaUI.keep = true
	}
	wkPrompts["pplan"] = func(string) (string, string) {
		return "date | phase | step | flags, steps split by //", ""
	}
	wkTypers["pplan"] = func(c *console, pid, text string) {
		if n := wkPastePlan(pid, text); n > 0 {
			c.toastMsg(fmt.Sprintf("%d steps added", n))
		} else {
			c.toastMsg("No steps read: date | phase | step | flags")
		}
	}
	wkActions["newplan"] = func(c *console, _ string) { c.wkType("newplan:", "The plan's name, e.g. Campus France", "") }
	wkTypers["newplan"] = func(c *console, _, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		id := wkNewProject(text, "")
		wkSetProject(id, "kind", "plan")
		c.wkGo("project", "")
		c.somaUI.workProj = id
		if c.somaUI.workSel == nil {
			c.somaUI.workSel = map[string]string{}
		}
		c.somaUI.workSel["projectswk:pk"] = "1" // back on Projects: the plans
	}
}
