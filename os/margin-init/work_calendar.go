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

// The Calendar (TrainCalendar.tsx on the iPhone): the month, each day drawn as its rings with
// its score and a dot for each part of life that moved that day (trained, ate, spent, read,
// habits), rest days marked; under it the chosen day: its score line by line, its rings, the
// session (open, rename, log one, or mark the day as rest), and the rest of the day.

var dayDots = []struct {
	id, label string
	col       color.RGBA
}{
	{"train", "Trained", rgb(0x10b981)}, {"food", "Ate", rgb(0xfb923c)}, {"money", "Spent", rgb(0x38bdf8)},
	{"mind", "Read", rgb(0xa78bfa)}, {"habits", "Habits", rgb(0xd3fd50)},
}

// dayMarks is what happened on a date, as buildDayMarks counts it.
func dayMarks(date string, ledger map[string]bool, mind map[string]bool, hs []somaHabit) map[string]bool {
	m := map[string]bool{}
	if somaObj("history/"+date) != nil {
		m["train"] = true
	}
	if n := somaNutrition(date); n.Logged {
		m["food"] = true
	}
	m["money"] = ledger[date]
	m["mind"] = mind[date] || somaReading(date) > 0
	for _, h := range hs {
		if h.done(date) {
			m["habits"] = true
			break
		}
	}
	return m
}

func isRestDay(date string) bool { return soma.get("restDays/"+date) != "" }

func (k *wk) calendar() int {
	su := &k.c.somaUI
	now := time.Now().In(displayZone)
	today := somaToday()
	sel := su.calDay
	if sel == "" {
		sel = today
	}
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone).AddDate(0, su.calOff, 0)
	k.header([]string{"Personal", "Calendar"}, "Calendar", month.Format("January 2006")+" · every day as its rings and its score", color.RGBA{})
	x := k.square("wk:calnavm:1", "chevron-right", wkX1)
	x = k.square("wk:calnavm:-1", "chevron-left", x-13)
	if su.calOff != 0 || sel != today {
		k.outline("wk:calnavm:0", "", "Today", x-13, 103, 63)
	}

	// The month.
	ledger, mind := map[string]bool{}, map[string]bool{}
	for _, o := range somaList("ledger") {
		ledger[str(o["date"])] = true
	}
	for _, o := range somaList("mind") {
		mind[str(o["date"])] = true
	}
	hs := somaHabits()
	start := weekStart(month)
	weeks := int(math.Ceil(month.AddDate(0, 1, 0).Sub(start).Hours() / 24 / 7))
	cellH := 196
	cr := image.Rect(wkX0, 236, wkX1, 236+66+weeks*cellH+10)
	k.card(cr)
	k.swipe("calm:", cr)
	cw := (wkX1 - wkX0 - 24) / 7
	gx := wkX0 + 12
	for i, d := range []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"} {
		k.textC(wkFont("semi", 16), gx+i*cw+cw/2, 280, wkFaint, d)
	}
	for w := range weeks {
		for i := range 7 {
			d := start.AddDate(0, 0, w*7+i)
			ds := d.Format("2006-01-02")
			in := d.Month() == month.Month()
			x0, y0 := gx+i*cw, 300+w*cellH
			r := k.R(x0+3, y0, x0+cw-3, y0+cellH-6)
			bg := rgb(0x16171d)
			switch {
			case ds == sel:
				bg = blend(wkCard, wkLime, 0.14)
			case !in:
				bg = rgb(0x111217)
			}
			ui.RoundRect(k.img, r, 14, bg)
			if ds == today {
				ui.RoundRect(k.img, r, 14, wkLime)
				ui.RoundRect(k.img, r.Inset(3), 12, bg)
			}
			nc := wkFG
			if !in {
				nc = wkFaint
			}
			k.text(wkFont("semi", 19), x0+16, y0+30, nc, d.Format("2"))
			if isRestDay(ds) {
				ui.Circle(k.img, x0+cw-22, k.Y(y0+22), 6, rgb(0x38bdf8))
			}
			if ds <= today && in {
				specs := somaRingSpecs(ds)
				ringSet(k.img, x0+cw/2-44, k.Y(y0+42), 88, specs, ds != today && !ringsClosed(specs), false)
				if sc, lines := somaDayScore(ds); anyTracked(lines) {
					c := wkAmber
					if sc >= 80 {
						c = wkGreen
					}
					k.textC(wkFont("bold", 19), x0+cw/2, y0+158, c, fmt.Sprint(sc))
				}
				m := dayMarks(ds, ledger, mind, hs)
				n := 0
				for _, dd := range dayDots {
					if m[dd.id] {
						n++
					}
				}
				dx := x0 + cw/2 - (n*14-6)/2
				for _, dd := range dayDots {
					if m[dd.id] {
						ui.Circle(k.img, dx+4, k.Y(y0+176), 4, dd.col)
						dx += 14
					}
				}
			}
			k.btn("wk:calday:"+ds, r)
		}
	}
	// The key.
	lx := wkX0 + 8
	ly := cr.Max.Y + 34
	for _, dd := range dayDots {
		ui.Circle(k.img, lx+6, k.Y(ly-6), 6, dd.col)
		lx = k.text(wkFont("regular", 18), lx+20, ly, wkText2, dd.label) + 24
	}
	ui.Circle(k.img, lx+6, k.Y(ly-6), 6, rgb(0x38bdf8))
	k.text(wkFont("regular", 18), lx+20, ly, wkText2, "Rest day (top right)")
	return k.dayCard(sel, cr.Max.Y+64)
}

func ringsClosed(specs []ringSpec) bool {
	for _, s := range specs {
		if s.f < 1 {
			return false
		}
	}
	return len(specs) > 0
}

func anyTracked(lines []scoreLine) bool {
	for _, l := range lines {
		if l.tracked {
			return true
		}
	}
	return false
}

// dayCard is the chosen day, everything about it.
func (k *wk) dayCard(date string, y int) int {
	t, _ := time.ParseInLocation("2006-01-02", date, displayZone)
	today := somaToday()
	title := t.Format("Monday 2 January")
	if date == today {
		title = "Today · " + t.Format("2 January")
	}
	k.text(wkFont("bold", 30), wkX0+4, y+34, wkFG, title)
	y += 60

	// The score, line by line.
	sc, lines := somaDayScore(date)
	h := 110 + 52*len(lines)
	cr := image.Rect(wkX0, y, 652, y+h)
	k.card(cr)
	tracked := anyTracked(lines)
	lbl := "–"
	if tracked {
		lbl = fmt.Sprint(sc)
	}
	c := wkAmber
	if sc >= 80 {
		c = wkGreen
	}
	k.text(wkFont("bold", 44), wkX0+28, y+66, c, lbl)
	k.text(wkFont("semi", 20), wkX0+28+ui.TextWidth(wkFont("bold", 44), lbl)+12, y+64, wkMuted, "day score")
	ry := y + 100
	for _, l := range lines {
		ui.Fill(k.img, k.R(wkX0+2, ry, 650, ry+2), wkLine)
		k.text(wkFont("medium", 19), 188, ry+34, wkText2, l.label)
		v := "not logged"
		vc := wkFaint
		if l.tracked {
			v, vc = fmt.Sprintf("%g / %g", l.earned, l.possible), wkFG
		} else if l.label == "Workout" && isRestDay(date) {
			v = "rest day"
		}
		k.textR(wkFont("semi", 19), 624, ry+34, vc, v)
		if l.tracked {
			k.hBar(k.R(380, ry+24, 520, ry+30), l.earned/math.Max(l.possible, 1), c)
		}
		ry += 52
	}
	// The rings.
	rr := image.Rect(672, y, wkX1, y+h)
	k.card(rr)
	k.cardHead(700, y+46, "target", "Rings", "")
	ringSet(k.img, 700, k.Y(y+70), 200, somaRingSpecs(date), false, true)
	ry = y + 92
	for _, rg := range somaRingsFor(date) {
		col := rgb(rg.To)
		k.text(wkFont("semi", 16), 920, ry, wkMuted, strings.ToUpper(rg.Label))
		k.text(wkFont("bold", 24), 920, ry+30, col, fmt.Sprintf("%.0f", rg.Value))
		k.textR(wkFont("regular", 17), 1136, ry+30, wkFaint, fmt.Sprintf("/ %.0f %s", rg.Goal, rg.Unit))
		ry += 64
	}
	k.btn("wk:nutday:"+date, k.R(672, y, wkX1, y+h))
	y = cr.Max.Y + 20

	// Training.
	tc := image.Rect(wkX0, y, wkX1, y+150)
	k.card(tc)
	if o := somaObj("history/" + date); o != nil {
		s := parseSession(date, o)
		k.cardHead(wkX0+28, y+46, "dumbbell", firstNonEmpty(s.Split, "Session"), "")
		ex := []string{}
		for _, e := range s.Ex {
			ex = append(ex, e.Name)
		}
		k.text(wkFont("regular", 19), 188, y+84, wkMuted, clip(wkFont("regular", 19), fmt.Sprintf("%s · %d sets · %s kg · %.0f kcal", firstNonEmpty(s.Duration, "0:00"), s.Sets, fmtThousands(int(s.Vol)), s.Cals), 900))
		k.text(wkFont("regular", 18), 188, y+116, wkFaint, clip(wkFont("regular", 18), strings.Join(ex, ", "), 600))
		k.outline("wk:type-open:srename:"+date, "pencil", "Rename", 880, y+96, 50)
		k.outline("wk:sess:"+date, "chevron-right", "Open", wkX1-28, y+96, 50)
	} else {
		var lifted []string
		for _, l := range trainingLog() {
			if len(l.Days[date]) > 0 {
				lifted = append(lifted, l.Name)
			}
		}
		switch {
		case len(lifted) > 0:
			k.cardHead(wkX0+28, y+46, "dumbbell", "From your sheet", "")
			k.text(wkFont("regular", 19), 188, y+84, wkMuted, clip(wkFont("regular", 19), strings.Join(lifted, ", "), 900))
			k.outline("wk:sess:"+date, "chevron-right", "Open", wkX1-28, y+96, 50)
		case isRestDay(date):
			k.cardHead(wkX0+28, y+46, "moon", "Rest day", "")
			k.text(wkFont("regular", 19), 188, y+84, wkMuted, "Saved as rest and recovery: the workout doesn't count against the day.")
			k.outline("wk:restday:"+date, "x", "Not a rest day", wkX1-28, y+96, 50)
		default:
			k.cardHead(wkX0+28, y+46, "dumbbell", "No training", "")
			k.text(wkFont("regular", 19), 188, y+84, wkMuted, "Nothing logged on this day.")
			k.outline("wk:restday:"+date, "moon", "Rest day", 880, y+96, 50)
			k.outline("wk:logon:"+date, "plus", "Log a session", wkX1-28, y+96, 50)
		}
	}
	y = tc.Max.Y + 20

	// The rest of the day.
	type row struct{ icon, label, value, id string }
	n := somaNutrition(date)
	rows := []row{}
	food := "nothing logged"
	if n.Logged {
		food = fmt.Sprintf("%s kcal · %.0f g protein", fmtThousands(int(n.Cals)), n.P)
	}
	rows = append(rows, row{"utensils", "Food", food, "wk:nutday:" + date})
	var spend, income float64
	for _, o := range somaList("ledger") {
		if str(o["date"]) != date {
			continue
		}
		v := toBase(math.Abs(num(o["amount"])), str(o["currency"]))
		switch str(o["kind"]) {
		case "spend":
			spend += v
		case "income":
			income += v
		}
	}
	money := "nothing logged"
	if spend > 0 || income > 0 {
		money = fmt.Sprintf("%s out · %s in", wkMoney(spend), wkMoney(income))
	}
	rows = append(rows, row{"wallet", "Money", money, "wk:go:money"})
	var minds []string
	for _, o := range somaList("mind") {
		if str(o["date"]) == date {
			minds = append(minds, str(o["title"]))
		}
	}
	read := fmt.Sprintf("%d minutes read", somaReading(date))
	if len(minds) > 0 {
		read += " · " + strings.Join(minds, ", ")
	}
	rows = append(rows, row{"brain-circuit", "Mind", read, "wk:go:mind"})
	done := 0
	for _, h := range somaHabits() {
		if h.done(date) {
			done++
		}
	}
	rows = append(rows, row{"circle-check", "Habits", fmt.Sprintf("%d of %d done", done, len(somaHabits())), "wk:go:habits"})
	td, tdDone := 0, 0
	for _, t := range somaTodos() {
		if !t.Cleared && t.on(date) {
			td++
			if t.Done {
				tdDone++
			}
		}
	}
	rows = append(rows, row{"list-checks", "To-dos", fmt.Sprintf("%d of %d done", tdDone, td), "wk:planday:" + date})
	if sl := sleepHours(date); sl > 0 {
		rows = append(rows, row{"moon", "Sleep", fmt.Sprintf("%.1f h", sl), "wk:go:health"})
	}
	// The plans' steps due that day, in their project's colour: the box ticks the step.
	if steps := wkStepsDue(wkProjects(), date, date, false); len(steps) > 0 {
		sc := image.Rect(wkX0, y, wkX1, y+76+83*len(steps))
		k.card(sc)
		k.cardHead(wkX0+28, y+46, "flag", "Plan steps", "")
		k.datedStepRows(steps, y+76, time.Now().In(displayZone), false)
		y = sc.Max.Y + 20
	}
	rc := image.Rect(wkX0, y, wkX1, y+80+76*len(rows))
	k.card(rc)
	k.cardHead(wkX0+28, y+46, "calendar", "The rest of the day", "")
	ry = y + 76
	for _, r := range rows {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		k.icon(r.icon, 202, ry+38, 24, wkMuted)
		k.text(wkFont("semi", 21), 232, ry+46, wkFG, r.label)
		k.textR(wkFont("regular", 19), 1100, ry+46, wkText2, clip(wkFont("regular", 19), r.value, 700))
		k.icon("chevron-right", 1124, ry+40, 20, wkFaint)
		k.btn(r.id, k.R(wkX0, ry, wkX1, ry+76))
		ry += 76
	}
	return rc.Max.Y + 20
}

func init() {
	wkActions["calnavm"] = func(c *console, arg string) {
		su := &c.somaUI
		switch arg {
		case "0":
			su.calOff, su.calDay = 0, ""
		case "1":
			su.calOff++
		default:
			su.calOff--
		}
	}
	wkSwipes["calm"] = func(c *console, _ string, dir int) { c.somaUI.calOff += dir }
	wkActions["calday"] = func(c *console, date string) {
		c.somaUI.calDay = date
		if date == somaToday() {
			c.somaUI.calDay = ""
		}
	}
	wkActions["restday"] = func(c *console, date string) {
		if isRestDay(date) {
			soma.put("restDays/"+date, "")
		} else {
			somaPut("restDays/"+date, nowMs())
		}
	}
	wkActions["logon"] = func(c *console, date string) {
		c.wkType("newsession:"+date, "What you trained on "+wkDate(date)+", e.g. Push A", "")
	}
}
