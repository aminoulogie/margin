package main

import (
	"fmt"
	"image"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"
)

// Daily practice: something to do every day for a project until a date ("French test 30 min a
// day until the TCF exam"), at one time each day. It shows as a block on the planner and the
// calendar, under Habits as a temporary habit, and on the project; ticking a day logs its minutes
// as internal (not billed) time on the project. Kept on the project's record
// (project.practices, tablet only: the phone and the PC don't read it, and keep it as it is):
//   {id, label, start (minutes from midnight), mins, until (a milestone step's id, or "due"),
//    from ("YYYY-MM-DD"), done {date: true}, entries {date: time entry id}}
// It runs from its first day until the day before the milestone (it moves when the milestone
// does), or the project's deadline.

type wkPractice struct {
	ID, Label, Until, From string
	Start, Mins            int
	Done                   map[string]bool
	Entries                map[string]string
}

func readPractices(o obj) []wkPractice {
	list, _ := o["practices"].([]any)
	var out []wkPractice
	for _, it := range list {
		po, _ := it.(obj)
		if po == nil || str(po["id"]) == "" {
			continue
		}
		pr := wkPractice{ID: str(po["id"]), Label: str(po["label"]), Until: str(po["until"]), From: str(po["from"]),
			Start: int(num(po["start"])), Mins: max(int(num(po["mins"])), 5), Done: map[string]bool{}, Entries: map[string]string{}}
		if d, _ := po["done"].(obj); d != nil {
			for k, v := range d {
				if b, _ := v.(bool); b {
					pr.Done[k] = true
				}
			}
		}
		if e, _ := po["entries"].(obj); e != nil {
			for k, v := range e {
				pr.Entries[k] = str(v)
			}
		}
		if pr.Label == "" {
			pr.Label = "Practice"
		}
		out = append(out, pr)
	}
	return out
}

// practiceEnd is the date the practice stops on (not practised that day), "" for none.
func (p wkProject) practiceEnd(pr wkPractice) string {
	if pr.Until != "" && pr.Until != "due" {
		for _, s := range p.Steps {
			if s.ID == pr.Until && s.Due != "" {
				return s.Due
			}
		}
	}
	return p.Due
}

// untilLabel is what it runs until: "TCF exam (Mon 9 Nov)".
func (p wkProject) untilLabel(pr wkPractice) string {
	end := p.practiceEnd(pr)
	name := "the deadline"
	for _, s := range p.Steps {
		if s.ID == pr.Until {
			name = s.Label
		}
	}
	if end == "" {
		return "no end date yet"
	}
	return name + " (" + wkDate(end) + ")"
}

// practiceOn: the practice is to be done on date.
func (p wkProject) practiceOn(pr wkPractice, date string) bool {
	if p.Status != "active" || date < pr.From {
		return false
	}
	end := p.practiceEnd(pr)
	return end == "" || date < end
}

// practiceCount is the days it was done and the days it has run, up to today.
func (p wkProject) practiceCount(pr wkPractice, now time.Time) (done, days int) {
	today := somaDate(now)
	t, err := time.ParseInLocation("2006-01-02", pr.From, displayZone)
	if err != nil {
		return 0, 0
	}
	end := p.practiceEnd(pr)
	for d := somaDate(t); d <= today && (end == "" || d < end); d = somaDate(t) {
		days++
		if pr.Done[d] {
			done++
		}
		t = t.AddDate(0, 0, 1)
	}
	return done, days
}

func (pr wkPractice) when() string {
	return clockOf(float64(pr.Start)/60) + " · " + minsLabel(pr.Mins)
}

// wkPracticeTodos are the practice blocks from..to (dates), as timed to-dos for the planner to
// lay out (id "practice:<project>:<practice>:<date>"; a tap opens the practice, the box ticks
// the day).
func wkPracticeTodos(projects []wkProject, from, to string) []somaTodo {
	var out []somaTodo
	t, err := time.ParseInLocation("2006-01-02", from, displayZone)
	if err != nil {
		return nil
	}
	for ; somaDate(t) <= to; t = t.AddDate(0, 0, 1) {
		d := somaDate(t)
		for _, p := range projects {
			for _, pr := range p.Practices {
				if p.practiceOn(pr, d) {
					out = append(out, somaTodo{ID: "practice:" + p.ID + ":" + pr.ID + ":" + d, Text: pr.Label, Date: d, Scope: "day",
						SlotDate: d, SlotStart: pr.Start, SlotMins: pr.Mins, Done: pr.Done[d], col: wkColour(p, p.order)})
				}
			}
		}
	}
	return out
}

// todoTapID and todoTickID are a to-do's buttons: a practice block's open the practice and tick
// its day.
func todoTapID(t somaTodo) string {
	if rest, ok := strings.CutPrefix(t.ID, "practice:"); ok {
		parts := strings.SplitN(rest, ":", 3)
		if len(parts) == 3 {
			return "wk:sheet-open:practice:" + parts[0] + ":" + parts[1]
		}
	}
	return "wk:sheet-open:task:" + t.ID
}

func todoTickID(t somaTodo) string {
	if rest, ok := strings.CutPrefix(t.ID, "practice:"); ok {
		return "wk:ptick:" + rest
	}
	return "wk:todo:" + t.ID
}

func isPractice(t somaTodo) bool { return strings.HasPrefix(t.ID, "practice:") }

// --- changing it ---------------------------------------------------------------------------------

// wkEditPractice changes one practice of a project in place (other fields kept).
func wkEditPractice(pid, prid string, fn func(po obj)) {
	somaPatch("projects", pid, func(o obj) {
		list, _ := o["practices"].([]any)
		for _, it := range list {
			if po, _ := it.(obj); po != nil && str(po["id"]) == prid {
				fn(po)
			}
		}
	})
}

// wkAddPractice adds a daily practice to a project: at 18:00 for 30 minutes, until its next
// milestone (else its deadline), from today; its id.
func wkAddPractice(pid, label string) string {
	p, ok := wkProjectByID(pid)
	if !ok {
		return ""
	}
	until := "due"
	if m, ok := p.nextDated(true); ok {
		until = m.ID
	}
	id := "pr-" + somaNewID()
	po := obj{"id": id, "label": strings.TrimSpace(label), "start": 18 * 60.0, "mins": 30.0, "until": until,
		"from": somaToday(), "done": obj{}, "entries": obj{}}
	somaPatch("projects", pid, func(o obj) {
		list, _ := o["practices"].([]any)
		o["practices"] = append(list, po)
	})
	return id
}

func wkRemovePractice(pid, prid string) {
	somaPatch("projects", pid, func(o obj) {
		list, _ := o["practices"].([]any)
		var keep []any
		for _, it := range list {
			if po, _ := it.(obj); po == nil || str(po["id"]) != prid {
				keep = append(keep, it)
			}
		}
		o["practices"] = keep
	})
}

// wkTickPractice ticks (or unticks) a day of practice; a ticked day is logged as internal time
// on the project at the practice's time.
func wkTickPractice(pid, prid, date string) {
	p, ok := wkProjectByID(pid)
	if !ok {
		return
	}
	var pr wkPractice
	for _, x := range p.Practices {
		if x.ID == prid {
			pr = x
		}
	}
	if pr.ID == "" {
		return
	}
	if pr.Done[date] {
		if eid := pr.Entries[date]; eid != "" {
			somaRemove("timeEntries", eid)
		}
		wkEditPractice(pid, prid, func(po obj) {
			if d, _ := po["done"].(obj); d != nil {
				delete(d, date)
			}
			if e, _ := po["entries"].(obj); e != nil {
				delete(e, date)
			}
		})
		return
	}
	day, err := time.ParseInLocation("2006-01-02", date, displayZone)
	if err != nil {
		return
	}
	start := day.Add(time.Duration(pr.Start) * time.Minute)
	eid := wkAddEntry(pid, start, start.Add(time.Duration(pr.Mins)*time.Minute), pr.Label)
	somaPatch("timeEntries", eid, func(o obj) { o["billable"] = false })
	wkEditPractice(pid, prid, func(po obj) {
		d, _ := po["done"].(obj)
		if d == nil {
			d = obj{}
		}
		d[date] = true
		po["done"] = d
		e, _ := po["entries"].(obj)
		if e == nil {
			e = obj{}
		}
		e[date] = eid
		po["entries"] = e
	})
}

// --- drawing it ----------------------------------------------------------------------------------

type wkPracticeRow struct {
	p  wkProject
	pr wkPractice
}

// wkPracticesOn are the practices to do on date, of every active project.
func wkPracticesOn(projects []wkProject, date string) []wkPracticeRow {
	var out []wkPracticeRow
	for _, p := range projects {
		for _, pr := range p.Practices {
			if p.practiceOn(pr, date) {
				out = append(out, wkPracticeRow{p, pr})
			}
		}
	}
	return out
}

// practiceRows are practices to tick for date (the box) or open (the rest of the row).
func (k *wk) practiceRows(list []wkPracticeRow, date string, ry int, now time.Time, project bool) int {
	for _, r := range list {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		cy := ry + 42
		on := r.p.practiceOn(r.pr, date)
		if on {
			k.checkbox(188, cy, r.pr.Done[date])
			k.btn("wk:ptick:"+r.p.ID+":"+r.pr.ID+":"+date, k.R(wkX0, ry, 236, ry+84))
		} else {
			k.icon("repeat", 188, cy, 24, wkFaint)
		}
		col := wkColour(r.p, r.p.order)
		ui.RoundRect(k.img, k.R(241, cy-7, 255, cy+7), 3, col)
		k.struck(273, cy-2, r.pr.Label, on && r.pr.Done[date], 560)
		done, days := r.p.practiceCount(r.pr, now)
		sub := r.pr.when() + " · until " + r.p.untilLabel(r.pr)
		if !project {
			sub = r.p.Name + " · " + sub
		}
		k.text(wkFont("regular", 17), 273, cy+24, wkFaint, clip(wkFont("regular", 17), sub, 620))
		k.textR(wkFont("regular", 19), 1093, cy+7, wkMuted, fmt.Sprintf("%d of %d days", done, days))
		k.icon("ellipsis", 1120, cy, 24, wkFaint)
		k.btn("wk:sheet-open:practice:"+r.p.ID+":"+r.pr.ID, k.R(236, ry, wkX1, ry+84))
		ry += 84
	}
	return ry
}

// practiceCard is a project's daily practice, today's box to tick, and Add.
func (k *wk) practiceCard(p wkProject, y int, now time.Time) int {
	var list []wkPracticeRow
	for _, pr := range p.Practices {
		list = append(list, wkPracticeRow{p, pr})
	}
	cr := image.Rect(wkX0, y, wkX1, y+92+84*max(len(list), 1))
	k.card(cr)
	k.cardHead(wkX0+28, y+53, "repeat", "Daily practice", "· every day until a date")
	k.outline("wk:padd:"+p.ID, "plus", "Add", 1136, y+22, 50)
	k.practiceRows(list, somaDate(now), y+92, now, true)
	if len(list) == 0 {
		k.emptyNote(190, y+92+50, "e.g. French test practice, 30 minutes a day until the exam.")
	}
	return cr.Max.Y
}

// tempHabits is Habits' list of temporary habits (the projects' daily practice), and those
// finished.
func (k *wk) tempHabits(y int) int {
	now := time.Now().In(displayZone)
	today := somaDate(now)
	var on, done []wkPracticeRow
	for _, p := range wkProjects() {
		for _, pr := range p.Practices {
			switch end := p.practiceEnd(pr); {
			case p.practiceOn(pr, today):
				on = append(on, wkPracticeRow{p, pr})
			case end != "" && end <= today || p.Status != "active":
				done = append(done, wkPracticeRow{p, pr})
			}
		}
	}
	if len(on)+len(done) == 0 {
		return y
	}
	if len(on) > 0 {
		cr := image.Rect(wkX0, y, wkX1, y+92+84*len(on))
		k.card(cr)
		e := k.cardHead(wkX0+28, y+53, "repeat", "Temporary habits", "")
		k.text(wkFont("regular", 19), e+14, y+53, wkFaint, "· until their date, from your projects")
		k.practiceRows(on, today, y+92, now, false)
		y = cr.Max.Y + 24
	}
	if len(done) > 0 {
		cr := image.Rect(wkX0, y, wkX1, y+92+84*len(done))
		k.card(cr)
		k.cardHead(wkX0+28, y+53, "circle-check", "Finished", "")
		k.practiceRows(done, today, y+92, now, false)
		y = cr.Max.Y + 24
	}
	return y
}

// kindSwitch is Projects' Work / Plans switch: two icons and a switch between them.
func (k *wk) kindSwitch(right int) int {
	plans := k.sel("pk", 0) == 1
	r := k.R(right-196, 103, right, 166)
	ui.RoundRect(k.img, r, 14, wkRim)
	ui.RoundRect(k.img, r.Inset(2), 12, rgb(0x16171e))
	cy := (r.Min.Y + r.Max.Y) / 2
	lc, rc := wkFG, wkMuted
	if plans {
		lc, rc = wkMuted, wkFG
	}
	lucideIcon(k.img, "briefcase", r.Min.X+36, cy, 26, lc)
	lucideIcon(k.img, "calendar-clock", r.Max.X-36, cy, 26, rc)
	tr := image.Rect(r.Min.X+66, cy-16, r.Max.X-66, cy+16)
	track := rgb(0x2a2c35)
	if plans {
		track = wkPurple
	}
	ui.RoundRect(k.img, tr, 16, track)
	kx := tr.Min.X + 16
	if plans {
		kx = tr.Max.X - 16
	}
	ui.Circle(k.img, kx, cy, 13, rgb(0xffffff))
	k.btn("wk:pkindsw", r)
	return r.Min.X
}

func init() {
	wkActions["pkindsw"] = func(c *console, _ string) {
		su := &c.somaUI
		if su.workSel == nil {
			su.workSel = map[string]string{}
		}
		key := su.work + "wk:pk"
		if su.workSel[key] == "1" {
			su.workSel[key] = "0"
		} else {
			su.workSel[key] = "1"
		}
		su.toTop = true
	}
	wkActions["padd"] = func(c *console, pid string) {
		c.wkType("pracnew:"+pid, "What to practise every day, e.g. TCF practice", "")
	}
	wkTypers["pracnew"] = func(c *console, pid, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		if id := wkAddPractice(pid, text); id != "" {
			c.openSheet("practice:" + pid + ":" + id)
		}
	}
	wkActions["ptick"] = func(c *console, arg string) { // <project>:<practice>:<date>
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) == 3 {
			wkTickPractice(parts[0], parts[1], parts[2])
			c.somaUI.keep = true
		}
	}
	wkSheets["practice"] = func(k *wk, arg string) *wkSheet {
		pid, prid, _ := strings.Cut(arg, ":")
		p, ok := wkProjectByID(pid)
		if !ok {
			return nil
		}
		for _, pr := range p.Practices {
			if pr.ID != prid {
				continue
			}
			now := time.Now().In(displayZone)
			done, days := p.practiceCount(pr, now)
			sh := &wkSheet{title: pr.Label, sub: fmt.Sprintf("Every day at %s · until %s · done %d of %d days", pr.when(), p.untilLabel(pr), done, days)}
			today := somaDate(now)
			if p.practiceOn(pr, today) {
				if pr.Done[today] {
					sh.rows = append(sh.rows, wkRow{id: "wk:ptick:" + arg + ":" + today, label: "Not done today", icon: "rotate-ccw"})
				} else {
					sh.rows = append(sh.rows, wkRow{id: "wk:ptick:" + arg + ":" + today, label: "Done today", icon: "circle-check", col: wkGreen,
						detail: minsLabel(pr.Mins) + " logged"})
				}
			}
			sh.rows = append(sh.rows,
				wkRow{id: "wk:type-open:pracname:" + arg, label: "Name", icon: "pencil", detail: pr.Label},
				wkRow{kind: "stepper", id: "wk:type-open:practime:" + arg, label: "Time", detail: clockOf(float64(pr.Start) / 60),
					minus: "wk:pracset:" + arg + ":start:-15", plus: "wk:pracset:" + arg + ":start:15"},
				wkRow{kind: "stepper", label: "Minutes a day", detail: fmt.Sprint(pr.Mins),
					minus: "wk:pracset:" + arg + ":mins:-5", plus: "wk:pracset:" + arg + ":mins:5"},
				wkRow{id: "wk:sheet-open:pracuntil:" + arg, label: "Until", icon: "flag", detail: p.untilLabel(pr)},
				wkRow{id: "wk:proj:" + pid, label: "Open " + p.Name, icon: "folder-open"},
				wkRow{id: "wk:pracdel:" + arg, label: "Stop this practice", icon: "trash-2", danger: true})
			return sh
		}
		return nil
	}
	wkActions["pracset"] = func(c *console, arg string) { // <project>:<practice>:start|mins:<delta>
		parts := strings.Split(arg, ":")
		if len(parts) != 4 {
			return
		}
		d, _ := strconv.Atoi(parts[3])
		wkEditPractice(parts[0], parts[1], func(po obj) {
			switch parts[2] {
			case "start":
				po["start"] = float64((int(num(po["start"])) + d + 24*60) % (24 * 60))
			case "mins":
				po["mins"] = float64(min(max(int(num(po["mins"]))+d, 5), 240))
			}
		})
		c.somaUI.keep = true
	}
	wkPrompts["pracname"] = func(arg string) (string, string) {
		pid, prid, _ := strings.Cut(arg, ":")
		p, _ := wkProjectByID(pid)
		for _, pr := range p.Practices {
			if pr.ID == prid {
				return "What to practise", pr.Label
			}
		}
		return "What to practise", ""
	}
	wkTypers["pracname"] = func(c *console, arg, text string) {
		if text = strings.TrimSpace(text); text != "" {
			pid, prid, _ := strings.Cut(arg, ":")
			wkEditPractice(pid, prid, func(po obj) { po["label"] = text })
		}
	}
	wkPrompts["practime"] = func(string) (string, string) { return "The time each day, e.g. 18:30", "" }
	wkTypers["practime"] = func(c *console, arg, text string) {
		var h, m int
		if _, err := fmt.Sscanf(strings.ReplaceAll(strings.TrimSpace(text), ".", ":"), "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			c.toastMsg("Type a time like 18:30")
			return
		}
		pid, prid, _ := strings.Cut(arg, ":")
		wkEditPractice(pid, prid, func(po obj) { po["start"] = float64(h*60 + m) })
	}
	wkSheets["pracuntil"] = func(k *wk, arg string) *wkSheet {
		pid, prid, _ := strings.Cut(arg, ":")
		p, ok := wkProjectByID(pid)
		if !ok {
			return nil
		}
		cur := ""
		for _, pr := range p.Practices {
			if pr.ID == prid {
				cur = pr.Until
			}
		}
		sh := &wkSheet{title: "Practise until", sub: "It stops the day before (it moves when the date does)"}
		for _, s := range p.Steps {
			if s.Milestone {
				d := "no date"
				if s.Due != "" {
					d = wkDate(s.Due)
				}
				sh.rows = append(sh.rows, wkRow{id: "wk:pracuntil:" + arg + ":" + s.ID, label: s.Label, icon: "flag", col: wkPurple, detail: d, sel: cur == s.ID})
			}
		}
		d := "no date"
		if p.Due != "" {
			d = wkDate(p.Due)
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:pracuntil:" + arg + ":due", label: "The project's deadline", icon: "calendar-clock", detail: d, sel: cur == "due" || cur == ""})
		return sh
	}
	wkActions["pracuntil"] = func(c *console, arg string) { // <project>:<practice>:<step id|due>
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) == 3 {
			wkEditPractice(parts[0], parts[1], func(po obj) { po["until"] = parts[2] })
		}
	}
	wkActions["pracdel"] = func(c *console, arg string) {
		pid, prid, _ := strings.Cut(arg, ":")
		before := somaObj("projects#" + pid)
		wkRemovePractice(pid, prid)
		c.offerUndo("Practice stopped", func() {
			if before != nil {
				somaPut("projects#"+pid, before)
			}
		})
	}
}
