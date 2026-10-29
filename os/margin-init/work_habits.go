package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"
)

// Habits (HabitsView.tsx, habit-score.ts, habit-steps.ts): today's habits to tick (a
// checklist's steps one at a time, an amount that ramps), the week and the month as grids,
// the year as a heatmap of how each day went, and a habit's own page. As on the phone, a day
// counts when its weighted score reaches 70%, and only today can be changed.

const keepAt = 70 // KEEP_AT: a day at this score or more keeps the streak

var coefLabels = map[int]string{1: "Nice to have", 2: "Normal", 3: "Important", 4: "Very important", 5: "Essential"}

// wkHabitColours are HABIT_COLORS: a new habit takes the first one not in use.
var wkHabitColours = []string{"#34e0a1", "#4c8dff", "#ffcf4a", "#a77bff", "#ff5c8a", "#3dd6f5", "#ff9e3d", "#6e6bff", "#e879f9", "#ff6b57", "#d3fd50"}

func (h somaHabit) coef() int {
	c := int(math.Round(num(h.o["coef"])))
	if c < 1 || c > 5 {
		return 2
	}
	return c
}

func (h somaHabit) goalDays() int {
	g := int(num(h.o["goalDaysPerWeek"]))
	if g <= 0 || g > 7 {
		return 7
	}
	return g
}

// start is the first day the habit counts from (habitStart): its first mark, or since.
func (h somaHabit) start() string {
	first := str(h.o["since"])
	for _, f := range []string{"history", "amountLog", "stepLog"} {
		m, _ := h.o[f].(obj)
		for d := range m {
			if len(d) == 10 && (first == "" || d < first) {
				first = d
			}
		}
	}
	return first
}

// habitScore is habitDayScore: the share of the day's weight done (-1: nothing to score).
func habitScore(hs []somaHabit, date, today string) int {
	earned, possible := 0, 0
	for _, h := range hs {
		st := h.start()
		if st == "" {
			st = today
		}
		if date < st {
			continue
		}
		done := h.done(date)
		if h.goalDays() < 7 && !done {
			continue
		}
		possible += h.coef()
		if done {
			earned += h.coef()
		}
	}
	if possible == 0 {
		return -1
	}
	return int(math.Round(float64(earned) / float64(possible) * 100))
}

// habitStreak is days in a row at 70% or more (today counts once it gets there).
func habitStreak(hs []somaHabit, today string) int {
	d := today
	if s := habitScore(hs, today, today); s < keepAt {
		d = wkAddDays(today, -1)
	}
	n := 0
	for i := 0; i < 3650; i++ {
		if s := habitScore(hs, d, today); s < keepAt {
			break
		}
		n++
		d = wkAddDays(d, -1)
	}
	return n
}

// habitConsistency is the average score over 30 days (today only once it's kept).
func habitConsistency(hs []somaHabit, today string) int {
	sum, n := 0, 0
	if s := habitScore(hs, today, today); s >= keepAt {
		sum, n = s, 1
	}
	for i := 1; i < 30; i++ {
		if s := habitScore(hs, wkAddDays(today, -i), today); s >= 0 {
			sum += s
			n++
		}
	}
	if n == 0 {
		return -1
	}
	return int(math.Round(float64(sum) / float64(n)))
}

// bestRun is the longest run of days a habit was done.
func (h somaHabit) bestRun() int {
	hist, _ := h.o["history"].(obj)
	var days []string
	for d, v := range hist {
		if b, _ := v.(bool); b {
			days = append(days, d)
		}
	}
	sort.Strings(days)
	best, run := 0, 0
	for i, d := range days {
		if i > 0 && wkAddDays(days[i-1], 1) == d {
			run++
		} else {
			run = 1
		}
		best = max(best, run)
	}
	return best
}

type habitStep struct {
	ID, Name string
	Target   int
}

func (h somaHabit) steps() []habitStep {
	var out []habitStep
	steps, _ := h.o["steps"].([]any)
	for _, s := range steps {
		if so, ok := s.(obj); ok {
			out = append(out, habitStep{str(so["id"]), str(so["name"]), min(max(int(num(so["target"])), 1), 12)})
		}
	}
	return out
}

func (h somaHabit) stepCount(date, sid string) int {
	log, _ := h.o["stepLog"].(obj)
	day, _ := log[date].(obj)
	return int(num(day[sid]))
}

// wkToggleHabit is toggleHabit for today: a checklist all done or cleared, an amount set to
// what today asks for, else the tick.
func wkToggleHabit(id string) {
	o := somaObj("habits#" + id)
	if o == nil {
		return
	}
	h := somaHabit{ID: id, o: o}
	today := somaToday()
	if r := h.rampOf(); r != nil {
		log, _ := o["amountLog"].(obj)
		if log == nil {
			log = obj{}
		}
		hist, _ := o["history"].(obj)
		if hist == nil {
			hist = obj{}
		}
		if h.done(today) {
			delete(log, today)
			hist[today] = false
		} else {
			log[today] = r.rungOn(today, log)
			hist[today] = true
		}
		o["amountLog"], o["history"] = log, hist
		somaPut("habits#"+id, o)
		return
	}
	somaToggleHabit(id)
}

// wkBumpStep is bumpStep: one more of a step today (past its target: back to none).
func wkBumpStep(id, sid string) {
	o := somaObj("habits#" + id)
	if o == nil {
		return
	}
	h := somaHabit{ID: id, o: o}
	today := somaToday()
	target := 0
	for _, s := range h.steps() {
		if s.ID == sid {
			target = s.Target
		}
	}
	if target == 0 {
		return
	}
	n := h.stepCount(today, sid) + 1
	if n > target {
		n = 0
	}
	log, _ := o["stepLog"].(obj)
	if log == nil {
		log = obj{}
	}
	day, _ := log[today].(obj)
	if day == nil {
		day = obj{}
	}
	if n > 0 {
		day[sid] = float64(n)
	} else {
		delete(day, sid)
	}
	if len(day) > 0 {
		log[today] = day
	} else {
		delete(log, today)
	}
	o["stepLog"] = log
	complete := true
	for _, s := range h.steps() {
		if int(num(day[s.ID])) < s.Target {
			complete = false
		}
	}
	hist, _ := o["history"].(obj)
	if hist == nil {
		hist = obj{}
	}
	hist[today] = complete
	o["history"] = hist
	somaPut("habits#"+id, o)
}

func habitColour(h somaHabit) color.RGBA { return somaColour(h.Color, wkLime) }

// --- the page --------------------------------------------------------------------------------------

func (k *wk) habits() int {
	su := &k.c.somaUI
	if id, ok := strings.CutPrefix(su.workSub, "habit:"); ok {
		return k.habitPage(id)
	}
	hs := somaHabits()
	today := somaToday()
	now := time.Now().In(displayZone)
	done := 0
	for _, h := range hs {
		if h.done(today) {
			done++
		}
	}
	k.header([]string{"Personal", "Habits"}, "Habits", fmt.Sprintf("%d of %d done today · weighted by how much each matters", done, len(hs)), color.RGBA{})
	k.primary("wk:type-open:newhabit:", "plus", "Habit", wkX1)
	tab := k.sel("tab", 0)
	k.tabs("wk:tab", []string{"Today", "Week", "Month", "Year"}, nil, tab)

	// How the day is going, the streak, the month.
	score := habitScore(hs, today, today)
	streak := habitStreak(hs, today)
	cons := habitConsistency(hs, today)
	hero := image.Rect(wkX0, 312, wkX1, 520)
	k.cardIn(hero, rgb(0x15171a))
	frac := 0.0
	if score > 0 {
		frac = float64(score) / 100
	}
	rc := wkLime
	if score >= 0 && score < keepAt {
		rc = wkAmber
	}
	k.arc(270, 416, 68, 16, frac, rc, blend(wkCard, wkLime, 0.12))
	lbl := "–"
	if score >= 0 {
		lbl = fmt.Sprintf("%d%%", score)
	}
	k.textC(wkFont("bold", 32), 270, 426, wkFG, lbl)
	stats := []struct{ l, v, s string }{
		{"Today", lbl, "of the day's weight"},
		{"Streak", fmt.Sprintf("%d days", streak), fmt.Sprintf("at %d%% or more", keepAt)},
		{"30 days", map[bool]string{true: "–", false: fmt.Sprintf("%d%%", cons)}[cons < 0], "average"},
	}
	for i, s := range stats {
		x := 384 + i*258
		k.text(wkFont("regular", 19), x, 382, wkFaint, s.l)
		k.text(wkFont("bold", 34), x, 424, wkFG, s.v)
		k.text(wkFont("regular", 18), x, 456, wkFaint, s.s)
	}
	if score >= 0 && score < keepAt {
		// What it takes to keep the streak: the weight still to tick.
		need, earned, possible := 0, 0, 0
		var left []somaHabit
		for _, h := range hs {
			if h.goalDays() < 7 && !h.done(today) {
				continue
			}
			possible += h.coef()
			if h.done(today) {
				earned += h.coef()
			} else {
				left = append(left, h)
			}
		}
		sort.SliceStable(left, func(i, j int) bool { return left[i].coef() > left[j].coef() })
		for _, h := range left {
			if earned*100 >= keepAt*possible {
				break
			}
			earned += h.coef()
			need++
		}
		k.text(wkFont("medium", 19), 384, 496, wkAmber, fmt.Sprintf("%d more to keep the streak", need))
	} else if score >= keepAt {
		k.text(wkFont("medium", 19), 384, 496, wkGreen, "Today counts for the streak")
	}

	switch tab {
	case 1:
		return k.habitWeek(hs, now)
	case 2:
		return k.habitMonth(hs, now)
	case 3:
		return k.habitYear(hs, now)
	}
	return k.habitToday(hs, today)
}

func (k *wk) habitToday(hs []somaHabit, today string) int {
	y := 546
	groups := []struct{ when, title, icon string }{{"morning", "Morning", "sunrise"}, {"", "Any time", "sun"}, {"evening", "Evening", "moon"}}
	if len(hs) == 0 {
		cr := image.Rect(wkX0, y, wkX1, y+200)
		k.card(cr)
		k.text(wkFont("semi", 23), 188, y+70, wkFG, "No habits yet")
		k.emptyNote(188, y+112, "Add one with + Habit. Habits sync with Soma on the phone and the PC.")
		return k.habitPresets(cr.Max.Y + 24)
	}
	mon := mondayOf(today)
	for _, g := range groups {
		var list []somaHabit
		for _, h := range hs {
			if h.when() == g.when {
				list = append(list, h)
			}
		}
		if len(list) == 0 {
			continue
		}
		k.icon(g.icon, wkX0+14, y+16, 22, wkMuted)
		k.text(wkFont("semi", 19), wkX0+36, y+24, wkMuted, strings.ToUpper(g.title))
		y += 40
		rowH := 112
		cr := image.Rect(wkX0, y, wkX1, y+rowH*len(list)+8)
		k.card(cr)
		ry := y + 4
		for i, h := range list {
			if i > 0 {
				ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			}
			k.habitRow(h, ry, today, mon)
			ry += rowH
		}
		y = cr.Max.Y + 22
	}
	return k.habitPresets(y)
}

// habitRow is a habit: its tile, name and streak, the week as dots, and its tick (or its
// steps, or its amount).
func (k *wk) habitRow(h somaHabit, ry int, today, mon string) {
	col := habitColour(h)
	cy := ry + 56
	tile := k.R(186, cy-28, 242, cy+28)
	habitTile(k.img, tile, h)
	k.text(wkFont("semi", 23), 262, cy-6, wkFG, clip(wkFont("semi", 23), h.Name, 330))
	sub := []string{}
	if s := h.streak(today); s > 0 {
		sub = append(sub, fmt.Sprintf("%d-day streak", s))
	}
	if h.goalDays() < 7 {
		sub = append(sub, fmt.Sprintf("%d days a week", h.goalDays()))
	}
	if c := h.coef(); c != 2 {
		sub = append(sub, coefLabels[c])
	}
	k.text(wkFont("regular", 18), 262, cy+24, wkFaint, clip(wkFont("regular", 18), strings.Join(sub, " · "), 330))
	k.btn("wk:habit:"+h.ID, k.R(wkX0, ry, 600, ry+112))
	// The week, Monday to Sunday.
	for i := range 7 {
		d := wkAddDays(mon, i)
		x := 618 + i*30
		switch {
		case h.done(d):
			ui.Circle(k.img, x, k.Y(cy), 10, col)
		case d == today:
			ui.Circle(k.img, x, k.Y(cy), 10, col)
			ui.Circle(k.img, x, k.Y(cy), 7, wkCard)
		case d < today && d >= h.start():
			ui.Circle(k.img, x, k.Y(cy), 10, rgb(0x2a2c35))
		default:
			ui.Circle(k.img, x, k.Y(cy), 4, rgb(0x2a2c35))
		}
	}
	// Doing it today.
	if steps := h.steps(); len(steps) > 0 {
		x := 1136
		for i := len(steps) - 1; i >= 0; i-- {
			s := steps[i]
			n := h.stepCount(today, s.ID)
			f := wkFont("semi", 17)
			l := clip(f, s.Name, 90)
			if s.Target > 1 {
				l += fmt.Sprintf(" %d/%d", n, s.Target)
			}
			w := ui.TextWidth(f, l) + 28
			r := k.R(x-w, cy-22, x, cy+22)
			if n >= s.Target {
				ui.RoundRect(k.img, r, 22, col)
				apTextCenter(k.img, f, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, accentInk(col), l)
			} else {
				ui.RoundRect(k.img, r, 22, rgb(0x262833))
				apTextCenter(k.img, f, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, wkText2, l)
			}
			k.btn("wk:hstep:"+h.ID+":"+s.ID, r.Inset(-4))
			x -= w + 8
			if x < 870 {
				break
			}
		}
		return
	}
	if r := h.rampOf(); r != nil {
		logged, rung, unit := h.amount(today)
		step := rampStep(unit)
		k.iconBtn("wk:hamt:"+h.ID+":"+strconv.FormatFloat(-step, 'f', -1, 64), "minus", 900, cy, 24, rgb(0x262833), wkFG)
		vc := wkFG
		if h.done(today) {
			vc = col
		}
		k.textC(wkFont("bold", 22), 990, cy-2, vc, fmt.Sprintf("%g/%g", logged, rung))
		k.textC(wkFont("regular", 15), 990, cy+20, wkFaint, unit)
		k.iconBtn("wk:hamt:"+h.ID+":"+strconv.FormatFloat(step, 'f', -1, 64), "plus", 1080, cy, 24, rgb(0x262833), wkFG)
		k.btn("wk:htick:"+h.ID, k.R(1110, cy-30, 1150, cy+30))
		return
	}
	if h.done(today) {
		ui.Circle(k.img, 1100, k.Y(cy), 30, col)
		lucideIcon(k.img, "check", 1100, k.Y(cy), 30, accentInk(col))
	} else {
		ui.Circle(k.img, 1100, k.Y(cy), 30, col)
		ui.Circle(k.img, 1100, k.Y(cy), 26, wkCard)
	}
	k.btn("wk:htick:"+h.ID, k.R(1040, ry, wkX1, ry+112))
}

// habitPresets are the checklists and ramps everyone sets up (STEP_PRESETS, RAMP_PRESETS).
func (k *wk) habitPresets(y int) int {
	hs := somaHabits()
	have := map[string]bool{}
	for _, h := range hs {
		have[strings.ToLower(strings.TrimSpace(h.Name))] = true
	}
	var show []habitPreset
	for _, p := range habitPresets {
		if !have[strings.ToLower(p.name)] {
			show = append(show, p)
		}
	}
	if len(show) == 0 {
		return y
	}
	k.text(wkFont("semi", 19), wkX0+8, y+24, wkMuted, "READY-MADE")
	y += 40
	x := wkX0
	for i, p := range show {
		f := wkFont("medium", 20)
		w := ui.TextWidth(f, p.name) + 70
		if x+w > wkX1 {
			x, y = wkX0, y+66
		}
		r := k.R(x, y, x+w, y+54)
		ui.RoundRect(k.img, r, 27, rgb(0x191a21))
		lucideIcon(k.img, p.icon, r.Min.X+28, (r.Min.Y+r.Max.Y)/2, 22, somaColour(p.color, wkLime))
		apText(k.img, f, r.Min.X+48, (r.Min.Y+r.Max.Y)/2+7, wkText2, p.name)
		k.btn(fmt.Sprintf("wk:hpreset:%d", i), r)
		x += w + 12
		_ = i
	}
	return y + 80
}

type habitPreset struct {
	name, desc, color, icon string
	steps                   []habitStep
	ramp                    obj
}

var habitPresets = []habitPreset{
	{name: "Brush teeth", desc: "Morning and night", color: "#3dd6f5", icon: "list-checks", steps: []habitStep{{"", "Morning", 1}, {"", "Night", 1}}},
	{name: "Skincare", desc: "Cleanse, treat, moisturise", color: "#e879f9", icon: "list-checks", steps: []habitStep{{"", "Cleanse", 1}, {"", "Treat", 1}, {"", "Moisturise", 1}}},
	{name: "Read", desc: "A little more each day", color: "#a77bff", icon: "trending-up", ramp: obj{"start": 10, "target": 30, "step": 1, "unit": "min", "advance": "earned"}},
	{name: "Push-ups", desc: "One more each day", color: "#ff9e3d", icon: "trending-up", ramp: obj{"start": 10, "target": 50, "step": 1, "unit": "rep", "advance": "earned"}},
}

// --- the week, the month, the year ------------------------------------------------------------------

// habitGrid is habits down the side and days across: a filled square for a day done (today's
// can be tapped), and each day's score along the bottom.
func (k *wk) habitGrid(hs []somaHabit, days []string, y int, title, zone string, labels func(i int) string) int {
	today := somaToday()
	x0 := 420
	cw := float64(wkX1-28-x0) / float64(len(days))
	rowH := 64
	h := 120 + rowH*max(len(hs), 1) + 70
	cr := image.Rect(wkX0, y, wkX1, y+h)
	k.card(cr)
	k.swipe(zone, cr)
	k.cardHead(wkX0+28, y+48, "calendar-range", title, "")
	k.iconBtn("wk:hnav:-1", "chevron-left", 1040, y+36, 24, rgb(0x101117), wkFG)
	k.iconBtn("wk:hnav:1", "chevron-right", 1104, y+36, 24, rgb(0x101117), wkFG)
	for i, d := range days {
		cx := x0 + int(cw*float64(i)+cw/2)
		c := wkFaint
		if d == today {
			c = wkLime
		}
		if l := labels(i); l != "" {
			k.textC(wkFont("semi", 16), cx, y+104, c, l)
		}
	}
	ry := y + 120
	for _, hb := range hs {
		col := habitColour(hb)
		habitTile(k.img, k.R(184, ry+10, 228, ry+54), hb)
		k.text(wkFont("semi", 20), 240, ry+38, wkFG, clip(wkFont("semi", 20), hb.Name, x0-256))
		k.btn("wk:habit:"+hb.ID, k.R(wkX0, ry, x0-8, ry+rowH))
		st := hb.start()
		for i, d := range days {
			cx := x0 + int(cw*float64(i)+cw/2)
			s := int(math.Min(cw-6, 40))
			r := k.R(cx-s/2, ry+30-s/2, cx+s/2, ry+30+s/2)
			switch {
			case hb.done(d):
				ui.RoundRect(k.img, r, min(8, s/3), col)
			case d > today || (st != "" && d < st):
				ui.RoundRect(k.img, r, min(8, s/3), rgb(0x15161c))
			default:
				ui.RoundRect(k.img, r, min(8, s/3), rgb(0x22242c))
			}
			if d == today {
				k.btn("wk:htick:"+hb.ID, r.Inset(-4))
			}
		}
		ry += rowH
	}
	// The day's score.
	ui.Fill(k.img, k.R(wkX0+2, ry+6, wkX1-2, ry+8), wkLine)
	k.text(wkFont("semi", 18), 188, ry+46, wkMuted, "Score")
	for i, d := range days {
		if d > today {
			continue
		}
		cx := x0 + int(cw*float64(i)+cw/2)
		s := habitScore(hs, d, today)
		if s < 0 {
			continue
		}
		c := wkAmber
		if s >= keepAt {
			c = wkGreen
		}
		if cw >= 40 {
			k.textC(wkFont("semi", 16), cx, ry+46, c, fmt.Sprint(s))
		} else {
			ui.Circle(k.img, cx, k.Y(ry+40), 5, c)
		}
	}
	return cr.Max.Y + 20
}

func (k *wk) habitWeek(hs []somaHabit, now time.Time) int {
	off := k.sel("week", 0)
	mon := weekStart(now).AddDate(0, 0, 7*off)
	var days []string
	for i := range 7 {
		days = append(days, mon.AddDate(0, 0, i).Format("2006-01-02"))
	}
	y := k.habitGrid(hs, days, 546, mon.Format("2 Jan")+" – "+mon.AddDate(0, 0, 6).Format("2 Jan"), "hweek:", func(i int) string {
		t, _ := time.Parse("2006-01-02", days[i])
		return t.Format("Mon 2")
	})
	k.text(wkFont("regular", 18), wkX0+4, y+16, wkFaint, "Only today can be ticked, as in Soma. Swipe for other weeks.")
	return y + 40
}

func (k *wk) habitMonth(hs []somaHabit, now time.Time) int {
	off := k.sel("week", 0)
	first := time.Date(now.Year(), now.Month()+time.Month(off), 1, 0, 0, 0, 0, displayZone)
	var days []string
	for d := first; d.Month() == first.Month(); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("2006-01-02"))
	}
	y := k.habitGrid(hs, days, 546, first.Format("January 2006"), "hweek:", func(i int) string {
		if i == 0 || (i+1)%5 == 0 {
			return fmt.Sprint(i + 1)
		}
		return ""
	})
	// Each habit's month: days done against what it aimed for.
	var rows []string
	for _, h := range hs {
		n := 0
		for _, d := range days {
			if h.done(d) {
				n++
			}
		}
		rows = append(rows, fmt.Sprintf("%s|%d", h.ID, n))
	}
	cr := image.Rect(wkX0, y, wkX1, y+90+64*max(len(hs), 1))
	k.card(cr)
	k.cardHead(wkX0+28, y+48, "target", "This month", "· days done against each habit's goal")
	ry := y + 76
	elapsed := len(days)
	if first.Year() == now.Year() && first.Month() == now.Month() {
		elapsed = now.Day()
	}
	for i, h := range hs {
		n, _ := strconv.Atoi(strings.SplitN(rows[i], "|", 2)[1])
		goal := int(math.Ceil(float64(elapsed) * float64(h.goalDays()) / 7))
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 20), 188, ry+40, wkFG, clip(wkFont("semi", 20), h.Name, 340))
		k.hBar(k.R(560, ry+28, 1000, ry+38), float64(n)/float64(max(goal, 1)), habitColour(h))
		k.textR(wkFont("semi", 20), 1136, ry+40, wkFG, fmt.Sprintf("%d / %d", n, goal))
		ry += 64
	}
	return cr.Max.Y + 20
}

func (k *wk) habitYear(hs []somaHabit, now time.Time) int {
	today := somaToday()
	start := weekStart(now).AddDate(0, 0, -7*52)
	y := 546
	cr := image.Rect(wkX0, y, wkX1, y+390)
	k.card(cr)
	k.cardHead(wkX0+28, y+48, "calendar", "The last year", "· each day's score")
	cell := 18
	x0 := 196
	for w := 0; w < 53; w++ {
		for d := range 7 {
			day := start.AddDate(0, 0, w*7+d)
			ds := day.Format("2006-01-02")
			if ds > today {
				continue
			}
			s := habitScore(hs, ds, today)
			c := rgb(0x1e2027)
			if s > 0 {
				c = blend(rgb(0x1e2027), wkLime, 0.2+0.8*float64(s)/100)
			}
			x, yy := x0+w*cell, y+100+d*cell
			ui.RoundRect(k.img, k.R(x, yy, x+cell-3, yy+cell-3), 3, c)
			if day.Day() == 1 && d == 0 || (day.Day() <= 7 && d == 0) {
				k.text(wkFont("regular", 15), x, y+92, wkFaint, day.Format("Jan"))
			}
		}
	}
	for i, l := range []string{"Mon", "", "Wed", "", "Fri", "", "Sun"} {
		k.text(wkFont("regular", 14), 160, y+114+i*cell, wkFaint, l)
	}
	kept, scored := 0, 0
	for i := 0; i < 365; i++ {
		if s := habitScore(hs, wkAddDays(today, -i), today); s >= 0 {
			scored++
			if s >= keepAt {
				kept++
			}
		}
	}
	k.text(wkFont("regular", 19), 188, y+360, wkMuted, fmt.Sprintf("%d of %d scored days kept (%d%% or more)", kept, scored, keepAt))
	y = cr.Max.Y + 20
	hc := image.Rect(wkX0, y, wkX1, y+90+64*max(len(hs), 1))
	k.card(hc)
	k.cardHead(wkX0+28, y+48, "trophy", "Each habit", "· days done in the last year, best run")
	ry := y + 76
	for _, h := range hs {
		n := 0
		for i := 0; i < 365; i++ {
			if h.done(wkAddDays(today, -i)) {
				n++
			}
		}
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 20), 188, ry+40, wkFG, clip(wkFont("semi", 20), h.Name, 340))
		k.hBar(k.R(560, ry+28, 900, ry+38), float64(n)/365, habitColour(h))
		k.textR(wkFont("semi", 20), 1010, ry+40, wkFG, fmt.Sprintf("%d days", n))
		k.textR(wkFont("regular", 18), 1136, ry+40, wkFaint, fmt.Sprintf("best %d", h.bestRun()))
		k.btn("wk:habit:"+h.ID, k.R(wkX0, ry, wkX1, ry+64))
		ry += 64
	}
	return hc.Max.Y + 20
}

// --- a habit's page -----------------------------------------------------------------------------

func (k *wk) habitPage(id string) int {
	var h somaHabit
	ok := false
	for _, x := range somaHabits() {
		if x.ID == id {
			h, ok = x, true
		}
	}
	if !ok {
		k.c.somaUI.workSub = ""
		return k.habits()
	}
	today := somaToday()
	col := habitColour(h)
	sub := str(h.o["desc"])
	if sub == "" {
		sub = fmt.Sprintf("%s · %d days a week", coefLabels[h.coef()], h.goalDays())
	}
	k.header([]string{"Personal", "Habits ›"}, clip(wkFont("bold", 42), h.Name, 640), clip(wkFont("regular", 20), sub, 700), col)
	x := k.square("wk:sheet-open:habit:"+id, "ellipsis", wkX1)
	k.square("wk:back", "chevron-left", x-13)
	// The figures.
	n30 := 0
	for i := 0; i < 30; i++ {
		if h.done(wkAddDays(today, -i)) {
			n30++
		}
	}
	total := 0
	hist, _ := h.o["history"].(obj)
	for _, v := range hist {
		if b, _ := v.(bool); b {
			total++
		}
	}
	box := k.row4(240)
	k.small(box(0), "flame", "Streak", fmt.Sprint(h.streak(today)), "days", "one missed day a week forgiven", wkFG)
	k.small(box(1), "trophy", "Best run", fmt.Sprint(h.bestRun()), "days", "in a row", wkFG)
	k.small(box(2), "chart-pie", "30 days", fmt.Sprintf("%d%%", n30*100/30), "", fmt.Sprintf("%d of 30 days", n30), wkFG)
	k.small(box(3), "circle-check", "All time", fmt.Sprint(total), "days", "since "+dayLabel(h.start()), wkFG)
	y := 424
	// Today.
	tc := image.Rect(wkX0, y, wkX1, y+112+8)
	k.card(tc)
	k.habitRow(h, y+4, today, mondayOf(today))
	y = tc.Max.Y + 20
	// Six months of it.
	start := weekStart(time.Now().In(displayZone)).AddDate(0, 0, -7*25)
	hc := image.Rect(wkX0, y, wkX1, y+300)
	k.card(hc)
	k.cardHead(wkX0+28, y+48, "calendar", "Six months", "")
	cell := 36
	for w := 0; w < 26; w++ {
		for d := range 7 {
			day := start.AddDate(0, 0, w*7+d)
			ds := day.Format("2006-01-02")
			if ds > today {
				continue
			}
			c := rgb(0x1e2027)
			if h.done(ds) {
				c = col
			}
			x := 200 + w*cell
			yy := y + 76 + d*cell - 4
			ui.RoundRect(k.img, k.R(x, yy, x+cell-6, yy+cell-6), 6, c)
		}
	}
	y = hc.Max.Y + 20
	// Notes.
	notes, _ := h.o["notes"].([]any)
	nc := image.Rect(wkX0, y, wkX1, y+90+90*max(len(notes), 1))
	k.card(nc)
	k.cardHead(wkX0+28, y+48, "sticky-note", "Notes", fmt.Sprintf("· %d", len(notes)))
	k.outline("wk:type-open:hnote:"+id, "plus", "Add note", 1136, y+18, 50)
	ry := y + 86
	for _, n := range notes {
		no, _ := n.(obj)
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.text(wkFont("semi", 17), 188, ry+32, wkFaint, wkDate(str(no["date"])))
		k.text(wkFont("regular", 21), 188, ry+64, wkFG, clip(wkFont("regular", 21), str(no["text"]), 880))
		k.icon("trash-2", 1112, ry+46, 22, wkFaint)
		k.btn("wk:hnotedel:"+id+":"+str(no["id"]), k.R(1080, ry, wkX1, ry+90))
		ry += 90
	}
	if len(notes) == 0 {
		k.emptyNote(188, ry+46, "How it's going, what helps, what gets in the way.")
	}
	return nc.Max.Y
}

// --- sheets, actions, fields -----------------------------------------------------------------------

func init() {
	wkActions["habit"] = func(c *console, id string) { c.wkGo("habits", "habit:"+id) }
	wkActions["htick"] = func(c *console, id string) { wkToggleHabit(id) }
	wkActions["hstep"] = func(c *console, arg string) {
		id, sid, _ := strings.Cut(arg, ":")
		wkBumpStep(id, sid)
	}
	wkActions["hamt"] = func(c *console, arg string) {
		id, d, _ := strings.Cut(arg, ":")
		delta, _ := strconv.ParseFloat(d, 64)
		somaLogAmount(id, delta)
	}
	wkLongPress["habit"] = func(c *console, id string) { c.openSheet("habit:" + id) }
	wkSwipes["hweek"] = func(c *console, _ string, dir int) { wkActions["hnav"](c, fmt.Sprint(dir)) }
	wkActions["hnav"] = func(c *console, arg string) {
		su := &c.somaUI
		if su.workSel == nil {
			su.workSel = map[string]string{}
		}
		n, _ := strconv.Atoi(arg)
		cur, _ := strconv.Atoi(su.workSel[su.work+"wk:week"])
		su.workSel[su.work+"wk:week"] = fmt.Sprint(min(cur+n, 0))
	}
	wkActions["hpreset"] = func(c *console, arg string) {
		i, _ := strconv.Atoi(arg)
		if i < 0 {
			return
		}
		hs := somaHabits()
		have := map[string]bool{}
		for _, h := range hs {
			have[strings.ToLower(strings.TrimSpace(h.Name))] = true
		}
		var show []habitPreset
		for _, p := range habitPresets {
			if !have[strings.ToLower(p.name)] {
				show = append(show, p)
			}
		}
		if i >= len(show) {
			return
		}
		p := show[i]
		o := wkHabitObj(p.name, p.color)
		o["desc"] = p.desc
		if len(p.steps) > 0 {
			var steps []any
			for _, s := range p.steps {
				steps = append(steps, obj{"id": "hs-" + somaNewID(), "name": s.Name, "target": s.Target})
			}
			o["steps"] = steps
		}
		if p.ramp != nil {
			r := obj{}
			for k, v := range p.ramp {
				r[k] = v
			}
			r["from"] = somaToday()
			o["ramp"] = r
		}
		somaAdd("habits", str(o["id"]), o)
		c.toastMsg(p.name + " added")
	}
	wkPrompts["newhabit"] = func(string) (string, string) { return "The habit, e.g. Stretch", "" }
	wkTypers["newhabit"] = func(c *console, _, text string) {
		if text == "" {
			return
		}
		used := map[string]bool{}
		for _, h := range somaHabits() {
			used[strings.ToLower(h.Color)] = true
		}
		colour := wkHabitColours[len(somaIDs("habits"))%len(wkHabitColours)]
		for _, h := range wkHabitColours {
			if !used[h] {
				colour = h
				break
			}
		}
		o := wkHabitObj(text, colour)
		somaAdd("habits", str(o["id"]), o)
	}

	wkSheets["habit"] = func(k *wk, id string) *wkSheet {
		var h somaHabit
		ok := false
		for _, x := range somaHabits() {
			if x.ID == id {
				h, ok = x, true
			}
		}
		if !ok {
			return nil
		}
		var sw []wkOpt
		for _, hex := range wkHabitColours {
			sw = append(sw, wkOpt{id: "wk:hset:" + id + ":color:" + hex, col: somaColour(hex, wkLime), sel: strings.EqualFold(hex, h.Color)})
		}
		var imp []wkOpt
		for c := 1; c <= 5; c++ {
			imp = append(imp, wkOpt{id: fmt.Sprintf("wk:hset:%s:coef:%d", id, c), label: fmt.Sprint(c), sel: h.coef() == c})
		}
		when := []wkOpt{{id: "wk:hset:" + id + ":time:morning", label: "Morning", sel: h.when() == "morning"},
			{id: "wk:hset:" + id + ":time:", label: "Any time", sel: h.when() == ""},
			{id: "wk:hset:" + id + ":time:evening", label: "Evening", sel: h.when() == "evening"}}
		desc := str(h.o["desc"])
		if desc == "" {
			desc = "None"
		}
		sh := &wkSheet{title: h.Name, sub: "Changes reach Soma on the phone and the PC", rows: []wkRow{
			{id: "wk:type-open:hrename:" + id, label: "Name", icon: "pencil", detail: h.Name},
			{id: "wk:type-open:hdesc:" + id, label: "Description", icon: "file-text", detail: desc},
			{kind: "stepper", label: "Days a week", detail: fmt.Sprint(h.goalDays()), minus: "wk:hset:" + id + ":goal:-1", plus: "wk:hset:" + id + ":goal:1"},
			{kind: "seg", label: "How much it matters: " + coefLabels[h.coef()], opts: imp},
			{kind: "seg", label: "When", opts: when},
			{kind: "swatches", opts: sw},
		}}
		if h.rampOf() == nil {
			for _, s := range h.steps() {
				d := ""
				if s.Target > 1 {
					d = fmt.Sprintf("%d times", s.Target)
				}
				sh.rows = append(sh.rows, wkRow{id: "wk:hstepdel:" + id + ":" + s.ID, label: "Step: " + s.Name, icon: "x", detail: d})
			}
			sh.rows = append(sh.rows, wkRow{id: "wk:type-open:hnewstep:" + id, label: "Add a step", icon: "list-checks", detail: "a checklist"})
		}
		if k.c.somaUI.workSub != "habit:"+id {
			sh.rows = append(sh.rows, wkRow{id: "wk:habit:" + id, label: "Open its page", icon: "calendar"})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:hdel:" + id, label: "Delete habit", icon: "trash-2", danger: true})
		return sh
	}
	wkActions["hset"] = func(c *console, arg string) {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 {
			return
		}
		id, key, v := parts[0], parts[1], parts[2]
		somaPatch("habits", id, func(o obj) {
			switch key {
			case "goal":
				d, _ := strconv.Atoi(v)
				g := num(o["goalDaysPerWeek"])
				if g <= 0 {
					g = 7
				}
				o["goalDaysPerWeek"] = min(max(g+float64(d), 1), 7)
			case "coef":
				n, _ := strconv.Atoi(v)
				o["coef"] = n
			case "time":
				if v == "" {
					delete(o, "time")
				} else {
					o["time"] = v
				}
			default:
				o[key] = v
			}
		})
		c.somaUI.keep = true
	}
	wkActions["hdel"] = func(c *console, id string) {
		o, at := somaRemove("habits", id)
		if o == nil {
			return
		}
		if c.somaUI.workSub == "habit:"+id {
			c.wkGo("habits", "")
		}
		c.offerUndo("Deleted “"+str(o["name"])+"”", func() { somaRestore("habits", o, at) })
	}
	wkActions["hstepdel"] = func(c *console, arg string) {
		id, sid, _ := strings.Cut(arg, ":")
		somaPatch("habits", id, func(o obj) {
			steps, _ := o["steps"].([]any)
			if i := stepIndex(steps, sid); i >= 0 {
				steps = append(steps[:i], steps[i+1:]...)
			}
			o["steps"] = steps
			if log, ok := o["stepLog"].(obj); ok {
				for d, day := range log {
					if do, ok := day.(obj); ok {
						delete(do, sid)
						if len(do) == 0 {
							delete(log, d)
						}
					}
				}
			}
			if len(steps) == 0 {
				delete(o, "stepLog")
			}
		})
		c.somaUI.keep = true
	}
	wkPrompts["hrename"] = func(id string) (string, string) { return "The habit", str(somaObj("habits#" + id)["name"]) }
	wkTypers["hrename"] = func(c *console, id, text string) {
		if text != "" {
			somaPatch("habits", id, func(o obj) { o["name"] = text })
		}
	}
	wkPrompts["hdesc"] = func(id string) (string, string) {
		return "What it is, why it matters", str(somaObj("habits#" + id)["desc"])
	}
	wkTypers["hdesc"] = func(c *console, id, text string) { somaPatch("habits", id, func(o obj) { o["desc"] = text }) }
	wkPrompts["hnewstep"] = func(string) (string, string) { return "A step, e.g. Floss (a number after it: times a day)", "" }
	wkTypers["hnewstep"] = func(c *console, id, text string) {
		if text == "" {
			return
		}
		target := 1
		if f := strings.Fields(text); len(f) > 1 {
			if n, err := strconv.Atoi(f[len(f)-1]); err == nil && n >= 1 && n <= 12 {
				target, text = n, strings.Join(f[:len(f)-1], " ")
			}
		}
		somaPatch("habits", id, func(o obj) {
			steps, _ := o["steps"].([]any)
			o["steps"] = append(steps, obj{"id": "hs-" + somaNewID(), "name": text, "target": target})
		})
	}
	wkPrompts["hnote"] = func(string) (string, string) { return "A note on this habit", "" }
	wkTypers["hnote"] = func(c *console, id, text string) {
		if text == "" {
			return
		}
		somaPatch("habits", id, func(o obj) {
			notes, _ := o["notes"].([]any)
			o["notes"] = append([]any{obj{"id": "note-" + somaNewID(), "date": somaToday(), "text": text}}, notes...)
		})
	}
	wkActions["hnotedel"] = func(c *console, arg string) {
		id, nid, _ := strings.Cut(arg, ":")
		before := somaObj("habits#" + id)
		somaPatch("habits", id, func(o obj) {
			notes, _ := o["notes"].([]any)
			o["notes"] = func() []any {
				var out []any
				for _, n := range notes {
					if no, _ := n.(obj); str(no["id"]) != nid {
						out = append(out, n)
					}
				}
				return out
			}()
		})
		c.offerUndo("Note deleted", func() { somaPut("habits#"+id, before) })
	}
}

// wkHabitObj is a new habit as addHabit makes it: every day, counted from today.
func wkHabitObj(name, colour string) obj {
	return obj{"id": fmt.Sprintf("habit-%d-%s", time.Now().UnixMilli(), somaNewID()[8:13]), "name": strings.TrimSpace(name),
		"desc": "", "color": colour, "goalDaysPerWeek": 7, "history": obj{}, "since": somaToday()}
}

// habitIcons is Soma's HabitBits.tsx: the glyph a habit's name suggests (first match wins).
var habitIcons = []struct {
	re   *regexp.Regexp
	icon string
}{
	{regexp.MustCompile(`(?i)no phone|offline|detox|unplug`), "phone-off"},
	{regexp.MustCompile(`(?i)alcohol|beer|drink less|sober`), "beer-off"},
	{regexp.MustCompile(`(?i)water|hydrat|drink`), "droplet"},
	{regexp.MustCompile(`(?i)gym|train|lift|workout|push|pull|leg`), "dumbbell"},
	{regexp.MustCompile(`(?i)run|cardio|walk|step`), "footprints"},
	{regexp.MustCompile(`(?i)read|book|page`), "book-open"},
	{regexp.MustCompile(`(?i)meditat|mindful|breath|pray`), "sparkles"},
	{regexp.MustCompile(`(?i)sleep|bed`), "bed"},
	{regexp.MustCompile(`(?i)night|evening`), "moon"},
	{regexp.MustCompile(`(?i)morning|sun|wake`), "sun"},
	{regexp.MustCompile(`(?i)journal|write|note`), "pen-line"},
	{regexp.MustCompile(`(?i)creatine|vitamin|pill|supplement`), "pill"},
	{regexp.MustCompile(`(?i)protein|eat|food|veg|salad|meal`), "salad"},
	{regexp.MustCompile(`(?i)code|work|deep|focus|study`), "laptop"},
	{regexp.MustCompile(`(?i)learn|language|brain`), "brain"},
	{regexp.MustCompile(`(?i)screen|phone|social`), "smartphone"},
	{regexp.MustCompile(`(?i)money|spend|budget|save`), "wallet"},
	{regexp.MustCompile(`(?i)stretch|yoga|mobility`), "activity"},
	// Beyond Soma's list, so fewer habits fall back to a letter.
	{regexp.MustCompile(`(?i)brush|teeth|floss|smile`), "smile"},
	{regexp.MustCompile(`(?i)skin|face|groom|shave`), "scan-face"},
	{regexp.MustCompile(`(?i)shower|cold|bath|wash`), "shower-head"},
	{regexp.MustCompile(`(?i)smok|cigar|vape|nicotine`), "cigarette-off"},
	{regexp.MustCompile(`(?i)alcohol|beer|drink less|sober`), "beer-off"},
	{regexp.MustCompile(`(?i)music|piano|sing|practi`), "music"},
	{regexp.MustCompile(`(?i)guitar`), "guitar"},
	{regexp.MustCompile(`(?i)english|french|arabic|spanish|german|duolingo|vocab|word`), "languages"},
	{regexp.MustCompile(`(?i)course|class|exam|revis|lesson`), "graduation-cap"},
	{regexp.MustCompile(`(?i)bike|cycl`), "bike"},
	{regexp.MustCompile(`(?i)swim`), "waves"},
	{regexp.MustCompile(`(?i)hike|climb`), "mountain"},
	{regexp.MustCompile(`(?i)fruit|apple|diet|sugar|fast`), "apple"},
	{regexp.MustCompile(`(?i)no phone|offline|detox|unplug`), "phone-off"},
	{regexp.MustCompile(`(?i)salat|salah|quran|fajr|dhikr|islam|church|god|faith`), "moon-star"},
	{regexp.MustCompile(`(?i)family|call|friend|kind|gratitude|thank`), "hand-heart"},
}

func habitIcon(name string) string {
	for _, h := range habitIcons {
		if h.re.MatchString(name) {
			return h.icon
		}
	}
	return ""
}

// habitTile is a habit's face, as Soma draws it: its colour falling from 42% to 18% over the
// page, a lighter rim along the top, and the glyph its name suggests (else its initial).
func habitTile(img *image.RGBA, r image.Rectangle, h somaHabit) {
	col := habitColour(h)
	rad := r.Dy() * 30 / 100
	ui.RoundRect(img, r, rad, blend(wkBG, col, 0.30))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		t := float64(y-r.Min.Y) / float64(max(r.Dy()-1, 1))
		row := roundedRow(r, rad, y)
		if row.Dx() > 4 {
			ui.Fill(img, image.Rect(row.Min.X+2, y, row.Max.X-2, y+1), blend(wkBG, col, 0.42-0.24*t))
		}
	}
	ui.Fill(img, image.Rect(r.Min.X+rad, r.Min.Y, r.Max.X-rad, r.Min.Y+1), blend(wkBG, col, 0.6))
	fg := blend(col, rgb(0xffffff), 0.15)
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	if ic := habitIcon(h.Name); ic != "" {
		lucideIcon(img, ic, cx, cy, r.Dy()/2, fg)
		return
	}
	in := strings.ToUpper(string([]rune(strings.TrimSpace(h.Name) + "•")[:1]))
	apTextCenter(img, wkFont("bold", float64(r.Dy())*0.42), cx, cy, fg, in)
}
