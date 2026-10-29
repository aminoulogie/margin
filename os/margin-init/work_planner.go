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

// The Planner (TasksPage.tsx and the phone's Plan the day): the week, a day and a month of
// to-dos, each swiped to the next; a board of what's on today, this week, by a deadline and
// done; and the day routines. A tap on an empty time adds a to-do there, a tap on a to-do
// shows everything that can be done with it.

// planDay is the day the Planner is on (its week, its month).
func (c *console) planDay() time.Time {
	if t, err := time.ParseInLocation("2006-01-02", c.somaUI.planDate, displayZone); err == nil {
		return t
	}
	return dayStart(time.Now())
}

func (c *console) setPlanDay(t time.Time) {
	c.somaUI.planDate = t.Format("2006-01-02")
	if t.Equal(dayStart(time.Now())) {
		c.somaUI.planDate = ""
	}
}

// todoCol is a to-do's colour on the calendar: red when it matters, else one of the palette.
func todoCol(t somaTodo) color.RGBA {
	if t.col.A != 0 { // a plan's step: its project's colour
		return t.col
	}
	if t.Priority == "high" {
		return rgb(0xf06a8a)
	}
	n := 0
	for _, r := range t.ID {
		n += int(r)
	}
	return []color.RGBA{wkBlue, wkGreen, wkPurple, rgb(0x4cc9f0), wkAmber}[n%5]
}

func minsLabel(m int) string {
	switch {
	case m%60 == 0:
		return fmt.Sprintf("%dh", m/60)
	case m > 60:
		return fmt.Sprintf("%dh %02dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

func (k *wk) planner() int {
	now := time.Now().In(displayZone)
	today := dayStart(now)
	pd := k.c.planDay()
	todos := somaTodos()
	tab := k.sel("tab", 0)
	mon := weekStart(pd)

	booked := 0
	for _, t := range todos {
		if t.SlotDate >= mon.Format("2006-01-02") && t.SlotDate < mon.AddDate(0, 0, 7).Format("2006-01-02") {
			booked += t.SlotMins
		}
	}
	sub := fmt.Sprintf("%s – %s · %.1f h booked", mon.Format("2 Jan"), mon.AddDate(0, 0, 6).Format("2 Jan"), float64(booked)/60)
	inNow := mon.Equal(weekStart(today))
	switch tab {
	case 1:
		sub = pd.Format("Monday 2 January 2006")
		inNow = pd.Equal(today)
	case 2:
		sub = pd.Format("January 2006")
		inNow = pd.Year() == today.Year() && pd.Month() == today.Month()
	case 3:
		sub, inNow = "Today, this week, deadlines and done", true
	case 4:
		sub, inNow = "Your routines: steps that fit a window of time", true
	}
	k.header([]string{wkUser() + " · Freelance", "Planner"}, "Tasks & calendar", sub, color.RGBA{})
	var x int
	if tab == 4 {
		x = k.primary("wk:type-open:rtnew:", "plus", "Routine", wkX1)
	} else {
		x = k.primary("wk:newtask", "plus", "Task", wkX1)
	}
	if tab <= 2 {
		x = k.square("wk:plannav:1", "chevron-right", x-13)
		x = k.square("wk:plannav:-1", "chevron-left", x-13)
		if !inNow {
			k.outline("wk:plannav:0", "", "Today", x-13, 103, 63)
		}
	}
	k.tabs("wk:tab", []string{"Week", "Day", "Month", "Board", "Routines"}, nil, tab)
	switch tab {
	case 1:
		return k.planDayView(pd, todos, now)
	case 2:
		return k.planMonth(pd, todos, now)
	case 3:
		return k.planBoard(todos, now)
	case 4:
		return k.routines()
	}
	return k.planWeek(mon, pd, todos, now)
}

// hourSpan is the hours a calendar shows: at least first to last, wider for what's booked.
func hourSpan(todos []somaTodo, dates map[string]bool, first, last int) (int, int) {
	for _, t := range todos {
		if t.SlotDate == "" || !dates[t.SlotDate] {
			continue
		}
		first = min(first, t.SlotStart/60)
		last = max(last, (t.SlotStart+max(t.SlotMins, 15)+59)/60)
	}
	return max(first, 0), min(last, 24)
}

// slotButtons makes every free half hour of a column a button to add a to-do at.
func (k *wk) slotButtons(date string, x0, x1, top int, firstH, lastH int, pxH float64) {
	for m := firstH * 60; m < lastH*60; m += 30 {
		y0 := top + int(float64(m-firstH*60)/60*pxH)
		y1 := top + int(float64(m+30-firstH*60)/60*pxH)
		k.btn(fmt.Sprintf("wk:slotadd:%s:%d", date, m), k.R(x0, y0, x1, y1))
	}
}

func (k *wk) planWeek(mon, pd time.Time, todos []somaTodo, now time.Time) int {
	today := dayStart(now)
	cr := image.Rect(wkX0, 312, wkX1, 1298)
	k.card(cr)
	k.swipe("plan:", cr)
	x0, colW := 232, 130
	dates := map[string]bool{}
	for i := range 7 {
		dates[mon.AddDate(0, 0, i).Format("2006-01-02")] = true
	}
	firstH, lastH := hourSpan(todos, dates, 8, 19)
	top, bot := 476, 1284
	pxH := float64(bot-top) / float64(lastH-firstH)
	for i := range 7 {
		d := mon.AddDate(0, 0, i)
		ds := d.Format("2006-01-02")
		cx := x0 + i*colW + colW/2
		k.textC(wkFont("medium", 18), cx, 343, wkFaint, strings.ToUpper(d.Format("Mon")))
		switch {
		case d.Equal(today):
			ui.Fill(k.img, k.R(x0+i*colW+1, 452, x0+(i+1)*colW, bot), blend(wkCard, wkLime, 0.03))
			ui.Circle(k.img, cx, k.Y(382), 28, wkLime)
			apTextCenter(k.img, wkFont("bold", 25), cx, k.Y(382), wkOnLime, d.Format("2"))
		case d.Equal(pd):
			ui.Circle(k.img, cx, k.Y(382), 28, rgb(0x2a2c35))
			k.textC(wkFont("bold", 25), cx, 391, wkFG, d.Format("2"))
		default:
			k.textC(wkFont("bold", 25), cx, 391, wkFG, d.Format("2"))
		}
		done, all := 0, 0
		for _, t := range todos {
			if !t.Cleared && t.on(ds) {
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
		ui.Fill(k.img, k.R(x0+i*colW, top, x0+i*colW+1, bot), wkLine)
		k.btn("wk:planday:"+ds, k.R(x0+i*colW, 320, x0+(i+1)*colW, 450))
		k.dropDay(ds, k.R(x0+i*colW, 320, x0+(i+1)*colW, 450))
		k.dropHours(ds, x0+i*colW, x0+(i+1)*colW, top, bot, firstH, pxH)
	}
	for h := firstH; h <= lastH; h++ {
		y := top + int(float64(h-firstH)*pxH)
		k.text(wkFont("regular", 17), 171, y+6, wkFaint, fmt.Sprintf("%02d:00", h%24))
		ui.Fill(k.img, k.R(x0, y, 1146, y+1), rgb(0x1b1c22))
	}
	for _, t := range todos {
		if t.SlotDate == "" || !dates[t.SlotDate] || t.Cleared {
			continue
		}
		d, _ := time.ParseInLocation("2006-01-02", t.SlotDate, displayZone)
		i := int(math.Round(d.Sub(mon).Hours() / 24))
		col := todoCol(t)
		y0 := top + int((float64(t.SlotStart)/60-float64(firstH))*pxH)
		y1 := y0 + max(int(float64(max(t.SlotMins, 15))/60*pxH), 34)
		y0, y1 = max(y0, top+2), min(y1, bot)
		r := k.R(x0+i*colW+4, y0+2, x0+(i+1)*colW-4, y1-2)
		fill := blend(wkCard, col, 0.22)
		if t.Done {
			fill = blend(wkCard, col, 0.08)
		}
		ui.RoundRect(k.img, r, 8, fill)
		ui.RoundRect(k.img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+4, r.Max.Y), 2, col)
		ty := r.Min.Y + 22
		f := wkFont("semi", 17)
		fg := wkFG
		if t.Done {
			fg = wkFaint
		}
		for _, l := range wkWrap(f, t.Text, r.Dx()-22, max((r.Dy()-10)/22, 1)) {
			if ty > r.Max.Y-4 {
				break
			}
			apText(k.img, f, r.Min.X+12, ty, fg, l)
			ty += 21
		}
		if ty+4 < r.Max.Y {
			apText(k.img, wkFont("regular", 15), r.Min.X+12, ty, wkMuted, clockOf(float64(t.SlotStart)/60))
		}
		k.btn("wk:sheet-open:task:"+t.ID, r)
	}
	for i := range 7 {
		k.slotButtons(mon.AddDate(0, 0, i).Format("2006-01-02"), x0+i*colW, x0+(i+1)*colW, top, firstH, lastH, pxH)
	}
	if mon.Equal(weekStart(today)) {
		i := int(today.Sub(mon).Hours() / 24)
		hh := float64(now.Hour()) + float64(now.Minute())/60
		if hh >= float64(firstH) && hh <= float64(lastH) {
			y := k.Y(top + int((hh-float64(firstH))*pxH))
			ui.Fill(k.img, image.Rect(x0+i*colW-4, y-1, x0+(i+1)*colW, y+2), wkRed)
			ui.Circle(k.img, x0+i*colW-4, y, 7, wkRed)
		}
	}
	y := k.dayList(pd, todos, now, 1323)
	return k.weekList(mon, todos, y+24)
}

// dayList is a day's to-dos: on its list, at a time on it, or due then.
func (k *wk) dayList(day time.Time, todos []somaTodo, now time.Time, y int) int {
	ds := day.Format("2006-01-02")
	today := dayStart(now)
	var list []somaTodo
	for _, t := range todos {
		if !t.Cleared && (t.Scope != "week" && t.Date == ds || t.SlotDate == ds || t.Due == ds) {
			list = append(list, t)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Done != b.Done {
			return !a.Done
		}
		if (a.SlotDate == ds) != (b.SlotDate == ds) {
			return a.SlotDate == ds
		}
		if a.SlotDate == ds {
			return a.SlotStart < b.SlotStart
		}
		return a.rank() < b.rank()
	})
	done, leftMin := 0, 0
	for _, t := range list {
		if t.Done {
			done++
		} else {
			leftMin += t.SlotMins
		}
	}
	title := day.Format("Monday 2 Jan")
	icon := "calendar"
	switch {
	case day.Equal(today):
		title, icon = "Today", "sun"
	case day.Equal(today.AddDate(0, 0, 1)):
		title = "Tomorrow"
	}
	steps := wkStepsDue(wkProjects(), ds, ds, false)
	tc := image.Rect(wkX0, y, wkX1, y+96+83*max(len(list)+len(steps), 1)+20)
	k.card(tc)
	e := k.cardHead(wkX0+28, y+54, icon, title, "")
	note := fmt.Sprintf("%d of %d done", done, len(list))
	if leftMin > 0 {
		note += " · " + minsLabel(leftMin) + " booked"
	}
	k.text(wkFont("regular", 20), e+14, y+54, wkFaint, note)
	right := 1136
	if day.Equal(today) {
		right = k.outline("wk:planmyday", "wand-sparkles", "Plan my day", right, y+23, 50) - 12
	}
	k.outline("wk:type-open:newtask:"+ds, "plus", "Add", right, y+23, 50)
	k.todoRows(list, y+94, ds)
	k.datedStepRows(steps, y+94+83*len(list), now, false)
	if len(list)+len(steps) == 0 {
		k.emptyNote(190, y+94+50, "Nothing planned. Add a to-do, or tap a time on the calendar.")
	}
	return tc.Max.Y
}

// todoRows are to-dos to tick (the box) or open (the rest of the row).
func (k *wk) todoRows(list []somaTodo, ry int, day string) {
	for _, t := range list {
		ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
		cy := ry + 42
		k.checkbox(188, cy, t.Done)
		k.btn("wk:todo:"+t.ID, k.R(wkX0, ry, 236, ry+83))
		ui.RoundRect(k.img, k.R(241, cy-7, 255, cy+7), 3, todoCol(t))
		k.struck(273, cy+8, t.Text, t.Done, 620)
		var when []string
		if t.SlotDate != "" {
			s := clockOf(float64(t.SlotStart)/60) + " · " + minsLabel(t.SlotMins)
			if t.SlotDate != day {
				s = wkDate(t.SlotDate) + " " + clockOf(float64(t.SlotStart)/60)
			}
			when = append(when, s)
		}
		if t.Due != "" && t.Due != day {
			when = append(when, "due "+wkDate(t.Due))
		}
		col := wkMuted
		if t.Due != "" && t.Due < somaToday() && !t.Done {
			col = wkRed
		}
		k.textR(wkFont("regular", 19), 1060, cy+7, col, strings.Join(when, " · "))
		if t.Priority == "high" {
			k.icon("flag", 1100, cy, 24, rgb(0xf06a8a))
		}
		k.icon("ellipsis", 1124, cy, 22, wkFaint)
		k.btn("wk:sheet-open:task:"+t.ID, k.R(236, ry, wkX1, ry+83))
		ry += 83
	}
}

// weekList is the week's own list (to-dos for some time this week).
func (k *wk) weekList(mon time.Time, todos []somaTodo, y int) int {
	ms := mon.Format("2006-01-02")
	var list []somaTodo
	for _, t := range todos {
		if !t.Cleared && t.Scope == "week" && mondayOf(t.Date) == ms {
			list = append(list, t)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Done != list[j].Done {
			return !list[i].Done
		}
		return list[i].rank() < list[j].rank()
	})
	tc := image.Rect(wkX0, y, wkX1, y+96+83*max(len(list), 1)+20)
	k.card(tc)
	e := k.cardHead(wkX0+28, y+54, "calendar-range", "This week", "")
	k.text(wkFont("regular", 20), e+14, y+54, wkFaint, "any day this week")
	k.outline("wk:type-open:newweek:"+ms, "plus", "Add", 1136, y+23, 50)
	k.todoRows(list, y+94, "")
	if len(list) == 0 {
		k.emptyNote(190, y+94+50, "Nothing for the week as a whole.")
	}
	return tc.Max.Y
}

// --- a day -------------------------------------------------------------------------------------

func (k *wk) planDayView(day time.Time, todos []somaTodo, now time.Time) int {
	ds := day.Format("2006-01-02")
	firstH, lastH := hourSpan(todos, map[string]bool{ds: true}, 6, 23)
	pxH := 76.0
	top := 340
	bot := top + int(float64(lastH-firstH)*pxH)
	cr := image.Rect(wkX0, 312, wkX1, bot+30)
	k.card(cr)
	k.swipe("plan:", cr)
	x0, x1 := 250, 1136
	for h := firstH; h <= lastH; h++ {
		y := top + int(float64(h-firstH)*pxH)
		k.text(wkFont("regular", 18), 176, y+6, wkFaint, fmt.Sprintf("%02d:00", h%24))
		ui.Fill(k.img, k.R(x0, y, x1, y+1), rgb(0x1b1c22))
	}
	// The timed to-dos, side by side where they overlap.
	var ts []somaTodo
	for _, t := range todos {
		if t.SlotDate == ds && !t.Cleared {
			ts = append(ts, t)
		}
	}
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].SlotStart < ts[j].SlotStart })
	col := make([]int, len(ts))
	ends := []int{}
	for i, t := range ts {
		placed := false
		for c, e := range ends {
			if e <= t.SlotStart {
				col[i], ends[c], placed = c, t.SlotStart+max(t.SlotMins, 15), true
				break
			}
		}
		if !placed {
			col[i] = len(ends)
			ends = append(ends, t.SlotStart+max(t.SlotMins, 15))
		}
	}
	nc := max(len(ends), 1)
	cw := (x1 - x0 - 8) / nc
	for i, t := range ts {
		c := todoCol(t)
		y0 := top + int((float64(t.SlotStart)/60-float64(firstH))*pxH)
		y1 := y0 + max(int(float64(max(t.SlotMins, 15))/60*pxH), 40)
		r := k.R(x0+6+col[i]*cw, y0+2, x0+6+(col[i]+1)*cw-6, y1-2)
		fill := blend(wkCard, c, 0.24)
		if t.Done {
			fill = blend(wkCard, c, 0.08)
		}
		ui.RoundRect(k.img, r, 10, fill)
		ui.RoundRect(k.img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+5, r.Max.Y), 2, c)
		f := wkFont("semi", 22)
		fg := wkFG
		if t.Done {
			fg = wkFaint
		}
		apText(k.img, f, r.Min.X+20, r.Min.Y+30, fg, clip(f, t.Text, r.Dx()-40))
		if r.Dy() > 56 {
			apText(k.img, wkFont("regular", 18), r.Min.X+20, r.Min.Y+56, wkMuted,
				clockOf(float64(t.SlotStart)/60)+" – "+clockOf(float64(t.SlotStart+t.SlotMins)/60))
		}
		k.btn("wk:sheet-open:task:"+t.ID, r)
	}
	k.slotButtons(ds, x0, x1, top, firstH, lastH, pxH)
	k.dropHours(ds, x0, x1, top, bot, firstH, pxH)
	if day.Equal(dayStart(now)) {
		hh := float64(now.Hour()) + float64(now.Minute())/60
		if hh >= float64(firstH) && hh <= float64(lastH) {
			y := k.Y(top + int((hh-float64(firstH))*pxH))
			ui.Fill(k.img, image.Rect(x0-6, y-1, x1, y+2), wkRed)
			ui.Circle(k.img, x0-6, y, 7, wkRed)
		}
	}
	k.text(wkFont("regular", 18), wkX0+4, cr.Max.Y+36, wkFaint, "Tap an empty time to add a to-do. Hold one to drag it to another time. Swipe for the next day.")
	return k.dayList(day, todos, now, cr.Max.Y+64)
}

// --- a month -----------------------------------------------------------------------------------

func (k *wk) planMonth(pd time.Time, todos []somaTodo, now time.Time) int {
	first := time.Date(pd.Year(), pd.Month(), 1, 0, 0, 0, 0, displayZone)
	start := weekStart(first)
	weeks := int(math.Ceil(first.AddDate(0, 1, 0).Sub(start).Hours() / 24 / 7))
	cellH := 178
	cr := image.Rect(wkX0, 312, wkX1, 312+70+weeks*cellH+12)
	k.card(cr)
	k.swipe("plan:", cr)
	cw := (wkX1 - wkX0 - 24) / 7
	gx := wkX0 + 12
	for i, d := range []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"} {
		k.textC(wkFont("semi", 17), gx+i*cw+cw/2, 360, wkFaint, d)
	}
	by := map[string][]somaTodo{}
	for _, t := range todos {
		if t.Cleared {
			continue
		}
		seen := map[string]bool{}
		for _, d := range []string{t.SlotDate, t.Due, map[bool]string{true: t.Date}[t.Scope != "week"]} {
			if d != "" && !seen[d] {
				by[d], seen[d] = append(by[d], t), true
			}
		}
	}
	for _, d := range wkStepsDue(wkProjects(), start.Format("2006-01-02"), start.AddDate(0, 0, 7*weeks).Format("2006-01-02"), false) {
		by[d.s.Due] = append(by[d.s.Due], somaTodo{ID: "step:" + d.s.ID, Text: d.s.Label, Date: d.s.Due, col: wkColour(d.p, d.p.order)})
	}
	today := dayStart(now)
	for w := range weeks {
		for i := range 7 {
			d := start.AddDate(0, 0, w*7+i)
			ds := d.Format("2006-01-02")
			x, y := gx+i*cw, 382+w*cellH
			r := k.R(x+3, y, x+cw-3, y+cellH-6)
			in := d.Month() == pd.Month()
			bg := rgb(0x17181e)
			if !in {
				bg = rgb(0x121318)
			}
			ui.RoundRect(k.img, r, 12, bg)
			nc := wkFG
			if !in {
				nc = wkFaint
			}
			if d.Equal(today) {
				ui.Circle(k.img, x+30, k.Y(y+28), 20, wkLime)
				apTextCenter(k.img, wkFont("bold", 20), x+30, k.Y(y+28), wkOnLime, d.Format("2"))
			} else {
				k.textC(wkFont("bold", 20), x+30, y+35, nc, d.Format("2"))
			}
			list := by[ds]
			sort.SliceStable(list, func(a, b int) bool { return list[a].rank() < list[b].rank() })
			open := 0
			for _, t := range list {
				if !t.Done {
					open++
				}
			}
			if len(list) > 0 && d.Before(today) && open > 0 {
				ui.Circle(k.img, x+cw-22, k.Y(y+26), 6, wkRed)
			}
			ly := y + 74
			for j, t := range list {
				if j == 3 {
					k.text(wkFont("semi", 15), x+12, ly, wkMuted, fmt.Sprintf("+%d more", len(list)-3))
					break
				}
				c := todoCol(t)
				ui.RoundRect(k.img, k.R(x+10, ly-14, x+14, ly+2), 2, c)
				fg := wkText2
				if t.Done {
					fg = wkFaint
				}
				k.text(wkFont("regular", 15), x+20, ly, fg, clip(wkFont("regular", 15), t.Text, cw-34))
				ly += 26
			}
			k.btn("wk:planday:"+ds, r)
		}
	}
	k.text(wkFont("regular", 18), wkX0+4, cr.Max.Y+36, wkFaint, "Tap a day to open it. Swipe for the next month.")
	return k.dayList(pd, todos, now, cr.Max.Y+64)
}

// --- the board -----------------------------------------------------------------------------------

func (k *wk) planBoard(todos []somaTodo, now time.Time) int {
	today := somaToday()
	mon := mondayOf(today)
	cols := []struct {
		title, icon, add string
		list             []somaTodo
	}{{"Today", "sun", "wk:type-open:newtask:" + today, nil}, {"This week", "calendar-range", "wk:type-open:newweek:" + mon, nil},
		{"Deadlines", "calendar-clock", "", nil}, {"Done", "circle-check", "", nil}}
	for _, t := range todos {
		if t.Cleared {
			continue
		}
		switch {
		case t.Done:
			cols[3].list = append(cols[3].list, t)
		case t.Scope != "week" && t.Date == today || t.SlotDate == today || t.Due == today:
			cols[0].list = append(cols[0].list, t)
		case t.Scope == "week" && mondayOf(t.Date) == mon:
			cols[1].list = append(cols[1].list, t)
		case t.Due != "":
			cols[2].list = append(cols[2].list, t)
		case t.Scope != "week" && t.Date < today:
			// an old day's to-do still open: due back on today's mind
			cols[0].list = append(cols[0].list, t)
		}
	}
	sort.SliceStable(cols[2].list, func(i, j int) bool { return cols[2].list[i].Due < cols[2].list[j].Due })
	for i := range cols[:2] {
		sort.SliceStable(cols[i].list, func(a, b int) bool { return cols[i].list[a].rank() < cols[i].list[b].rank() })
	}
	if d := cols[3].list; len(d) > 20 {
		cols[3].list = d[len(d)-20:]
	}
	cw := (wkX1 - wkX0 - 45) / 4
	bottom := 0
	for i, c := range cols {
		x0 := wkX0 + i*(cw+15)
		h := 86 + 118*max(len(c.list), 1) + 70
		cr := image.Rect(x0, 312, x0+cw, 312+h)
		k.cardIn(cr, rgb(0x121318))
		k.icon(c.icon, x0+30, 352, 22, wkMuted)
		e := k.text(wkFont("semi", 21), x0+50, 360, wkFG, c.title)
		k.text(wkFont("regular", 19), e+10, 360, wkFaint, fmt.Sprint(len(c.list)))
		y := 392
		for _, t := range c.list {
			r := image.Rect(x0+10, y, x0+cw-10, y+108)
			k.card(r)
			tc := todoCol(t)
			ui.RoundRect(k.img, k.R(r.Min.X+14, y+18, r.Min.X+18, y+46), 2, tc)
			f := wkFont("semi", 18)
			fg := wkFG
			if t.Done {
				fg = wkFaint
			}
			ly := y + 34
			for _, l := range wkWrap(f, t.Text, r.Dx()-46, 2) {
				k.text(f, r.Min.X+28, ly, fg, l)
				ly += 24
			}
			meta := ""
			switch {
			case t.SlotDate != "":
				meta = wkDate(t.SlotDate)[:3] + " " + clockOf(float64(t.SlotStart)/60)
			case t.Due != "":
				meta = "due " + dayLabel(t.Due)
			}
			mc := wkMuted
			if t.Due != "" && t.Due < today && !t.Done {
				mc = wkRed
			}
			k.text(wkFont("regular", 16), r.Min.X+28, y+94, mc, meta)
			if t.Priority == "high" {
				k.icon("flag", r.Max.X-24, y+90, 18, rgb(0xf06a8a))
			}
			k.btn("wk:sheet-open:task:"+t.ID, k.R(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y))
			y += 118
		}
		if len(c.list) == 0 {
			k.textC(wkFont("regular", 18), x0+cw/2, y+60, wkFaint, "Nothing here")
			y += 118
		}
		if c.add != "" {
			ar := k.R(x0+10, y+4, x0+cw-10, y+56)
			ui.RoundRect(k.img, ar, 12, rgb(0x191a21))
			lucideIcon(k.img, "plus", ar.Min.X+28, (ar.Min.Y+ar.Max.Y)/2, 22, wkMuted)
			apText(k.img, wkFont("medium", 18), ar.Min.X+48, (ar.Min.Y+ar.Max.Y)/2+7, wkMuted, "Add")
			k.btn(c.add, ar)
		}
		bottom = max(bottom, cr.Max.Y)
	}
	return bottom + 20
}

// --- routines (routine.ts) ---------------------------------------------------------------------

type wkRoutine struct {
	ID, Name, Color string
	Window          int // seconds
	Steps           []wkRStep
}

type wkRStep struct {
	ID, Label, Source string
	Seconds           int
}

func wkRoutines() []wkRoutine {
	var out []wkRoutine
	for _, o := range somaList("dayRoutines") {
		r := wkRoutine{ID: str(o["id"]), Name: str(o["name"]), Color: str(o["color"]), Window: int(num(o["windowSeconds"]))}
		steps, _ := o["steps"].([]any)
		for _, s := range steps {
			if so, ok := s.(obj); ok {
				r.Steps = append(r.Steps, wkRStep{ID: str(so["id"]), Label: str(so["label"]), Source: str(so["source"]), Seconds: int(num(so["seconds"]))})
			}
		}
		out = append(out, r)
	}
	return out
}

func secsLabel(s int) string {
	if s >= 3600 {
		return fmt.Sprintf("%dh %02dm", s/3600, s/60%60)
	}
	if s%60 == 0 {
		return fmt.Sprintf("%d min", s/60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

var wkRoutineColours = []string{"#5b8cff", "#35c9a8", "#f0a63c", "#e2607a", "#9b7bf0", "#3fb6f0", "#5ecf7a", "#e0785a"}

func (k *wk) routines() int {
	rs := wkRoutines()
	y := 312
	for _, r := range rs {
		col := somaColour(r.Color, wkBlue)
		total := 0
		for _, s := range r.Steps {
			total += s.Seconds
		}
		h := 116 + 78*len(r.Steps) + 76
		cr := image.Rect(wkX0, y, wkX1, y+h)
		k.card(cr)
		ui.RoundRect(k.img, k.R(188, y+30, 208, y+50), 6, col)
		k.text(wkFont("bold", 26), 224, y+50, wkFG, clip(wkFont("bold", 26), r.Name, 600))
		fit := wkGreen
		if total > r.Window {
			fit = wkAmber
		}
		k.text(wkFont("regular", 19), 188, y+86, fit, fmt.Sprintf("%d steps · %s of a %s window", len(r.Steps), secsLabel(total), secsLabel(r.Window)))
		k.outline("wk:sheet-open:routine:"+r.ID, "ellipsis", "", 1136, y+24, 54)
		ry := y + 110
		for i, s := range r.Steps {
			ui.Fill(k.img, k.R(wkX0+2, ry, wkX1-2, ry+2), wkLine)
			k.textC(wkFont("semi", 18), 202, ry+46, wkFaint, fmt.Sprint(i+1))
			icon := map[string]string{"habit": "circle-check", "todo": "list-checks"}[s.Source]
			lx := 236
			if icon != "" {
				k.icon(icon, 250, ry+39, 22, wkMuted)
				lx = 272
			}
			k.text(wkFont("semi", 22), lx, ry+47, wkFG, clip(wkFont("semi", 22), s.Label, 640))
			k.textR(wkFont("medium", 20), 1090, ry+47, wkText2, secsLabel(s.Seconds))
			k.icon("ellipsis", 1120, ry+40, 22, wkFaint)
			k.btn("wk:sheet-open:rstep:"+r.ID+":"+s.ID, k.R(wkX0, ry, wkX1, ry+78))
			ry += 78
		}
		ar := k.R(188, ry+12, 520, ry+62)
		ui.RoundRect(k.img, ar, 12, rgb(0x191a21))
		lucideIcon(k.img, "plus", ar.Min.X+30, (ar.Min.Y+ar.Max.Y)/2, 22, wkMuted)
		apText(k.img, wkFont("medium", 19), ar.Min.X+52, (ar.Min.Y+ar.Max.Y)/2+7, wkMuted, "Add a step")
		k.btn("wk:type-open:rtstep:"+r.ID, ar)
		y = cr.Max.Y + 20
	}
	if len(rs) == 0 {
		cr := image.Rect(wkX0, y, wkX1, y+180)
		k.card(cr)
		k.text(wkFont("semi", 23), 188, y+70, wkFG, "No routines yet")
		k.emptyNote(188, y+112, "A routine is a few steps that fit a window of time, like a 20-minute morning.")
		y = cr.Max.Y + 20
	}
	return y
}

// --- to-do sheet, actions and fields -------------------------------------------------------------

// planMyDay gives today's open to-dos without a time the free times after now, the ones that
// matter first: half an hour each, from 08:00 (or now) to 23:00.
func planMyDay() int {
	now := time.Now().In(displayZone)
	today := somaToday()
	todos := somaTodos()
	type span struct{ a, b int }
	var busy []span
	var want []somaTodo
	for _, t := range todos {
		if t.Cleared || t.Done {
			continue
		}
		if t.SlotDate == today {
			busy = append(busy, span{t.SlotStart, t.SlotStart + t.SlotMins})
		} else if t.SlotDate == "" && (t.Scope != "week" && t.Date == today || t.Due == today) {
			want = append(want, t)
		}
	}
	sort.SliceStable(want, func(i, j int) bool { return want[i].rank() < want[j].rank() })
	at := max(8*60, (now.Hour()*60+now.Minute()+14)/15*15)
	n := 0
	for _, t := range want {
		mins := 30
		for at+mins <= 23*60 {
			clash := false
			for _, b := range busy {
				if at < b.b && b.a < at+mins {
					clash, at = true, b.b
					break
				}
			}
			if !clash {
				break
			}
		}
		if at+mins > 23*60 {
			break
		}
		wkSetSlot(t.ID, today, at, mins)
		busy = append(busy, span{at, at + mins})
		at += mins
		n++
	}
	return n
}

func init() {
	wkSwipes["plan"] = func(c *console, _ string, dir int) { wkActions["plannav"](c, fmt.Sprint(dir)) }
	wkActions["plannav"] = func(c *console, arg string) {
		n, _ := strconv.Atoi(arg)
		if n == 0 {
			c.somaUI.planDate = ""
			return
		}
		k := &wk{c: c}
		d := c.planDay()
		switch k.sel("tab", 0) {
		case 1:
			d = d.AddDate(0, 0, n)
		case 2:
			d = time.Date(d.Year(), d.Month()+time.Month(n), 1, 0, 0, 0, 0, displayZone)
			if t := dayStart(time.Now()); t.Year() == d.Year() && t.Month() == d.Month() {
				d = t
			}
		default:
			d = d.AddDate(0, 0, 7*n)
		}
		c.setPlanDay(d)
	}
	wkActions["planday"] = func(c *console, date string) {
		if t, err := time.ParseInLocation("2006-01-02", date, displayZone); err == nil {
			c.setPlanDay(t)
		}
		if c.somaUI.workSel == nil {
			c.somaUI.workSel = map[string]string{}
		}
		c.somaUI.workSel[c.somaUI.work+"wk:tab"] = "1"
		c.somaUI.toTop = true
	}
	wkActions["planmyday"] = func(c *console, _ string) {
		if n := planMyDay(); n > 0 {
			c.toastMsg(fmt.Sprintf("%d planned into today", n))
		} else {
			c.toastMsg("Nothing left without a time")
		}
	}
	wkActions["newtask"] = func(c *console, _ string) {
		d := c.planDay().Format("2006-01-02")
		c.wkType("newtask:"+d, "A to-do for "+map[bool]string{true: "today", false: wkDate(d)}[d == somaToday()], "")
	}
	wkTypers["newtask"] = func(c *console, date, text string) { c.composerAdd(text) }
	wkTypers["newweek"] = func(c *console, mon, text string) { c.composerAdd(text) }
	wkActions["slotadd"] = func(c *console, arg string) {
		date, m, _ := strings.Cut(arg, ":")
		mins, _ := strconv.Atoi(m)
		c.wkType("slotadd:"+arg, fmt.Sprintf("A to-do at %s, %s", clockOf(float64(mins)/60), wkDate(date)), "")
	}
	wkTypers["slotadd"] = func(c *console, arg, text string) { c.composerAdd(text) }
	wkPrompts["newtask"] = func(date string) (string, string) { return "A to-do for " + wkDate(date), "" }
	wkPrompts["newweek"] = func(string) (string, string) { return "A to-do for this week", "" }
	wkPrompts["trename"] = func(id string) (string, string) { return "The to-do", str(somaObj("todos#" + id)["text"]) }
	wkTypers["trename"] = func(c *console, id, text string) {
		if text != "" {
			somaPatch("todos", id, func(o obj) { o["text"] = text })
		}
	}

	wkSheets["task"] = func(k *wk, id string) *wkSheet {
		var t somaTodo
		ok := false
		for _, x := range somaTodos() {
			if x.ID == id {
				t, ok = x, true
			}
		}
		if !ok {
			return nil
		}
		sub := "On " + wkDate(t.Date) + "'s list"
		if t.Scope == "week" {
			sub = "On the list for the week of " + wkDate(mondayOf(t.Date))
		}
		sh := &wkSheet{title: t.Text, sub: sub}
		if t.Done {
			sh.rows = append(sh.rows, wkRow{id: "wk:todo:" + id, label: "Not done yet", icon: "rotate-ccw"})
		} else {
			sh.rows = append(sh.rows, wkRow{id: "wk:todo:" + id, label: "Done", icon: "circle-check", col: wkGreen})
		}
		sh.rows = append(sh.rows, wkRow{id: "wk:type-open:trename:" + id, label: "Rename", icon: "pencil"})
		if t.SlotDate != "" {
			sh.rows = append(sh.rows,
				wkRow{kind: "stepper", label: "Starts", detail: clockOf(float64(t.SlotStart) / 60), minus: "wk:tslot:" + id + ":start:-15", plus: "wk:tslot:" + id + ":start:15"},
				wkRow{kind: "stepper", label: "Takes", detail: minsLabel(t.SlotMins), minus: "wk:tslot:" + id + ":mins:-15", plus: "wk:tslot:" + id + ":mins:15"},
				wkRow{id: "wk:tslot:" + id + ":off:0", label: "Take the time off", icon: "x"})
		} else {
			sh.rows = append(sh.rows, wkRow{id: "wk:tslot:" + id + ":on:0", label: "Give it a time", icon: "clock"})
		}
		day := t.Date
		if t.SlotDate != "" {
			day = t.SlotDate
		}
		due := "None"
		if t.Due != "" {
			due = wkDate(t.Due)
		}
		pr := []wkOpt{{id: "wk:tprio:" + id + ":high", label: "High", sel: t.Priority == "high", col: rgb(0xf06a8a)},
			{id: "wk:tprio:" + id + ":", label: "Normal", sel: t.Priority == ""},
			{id: "wk:tprio:" + id + ":low", label: "Low", sel: t.Priority == "low"}}
		sh.rows = append(sh.rows,
			wkRow{id: "wk:tday:" + id, label: "Day", icon: "calendar", detail: wkDate(day)},
			wkRow{id: "wk:tdue:" + id, label: "Deadline", icon: "calendar-clock", detail: due},
			wkRow{kind: "seg", label: "Priority", opts: pr})
		if t.Scope == "week" {
			sh.rows = append(sh.rows, wkRow{id: "wk:tscope:" + id + ":day", label: "Move to today's list", icon: "sun"})
		} else {
			sh.rows = append(sh.rows, wkRow{id: "wk:tscope:" + id + ":week", label: "Move to this week's list", icon: "calendar-range"})
		}
		sh.rows = append(sh.rows,
			wkRow{id: "wk:ttomorrow:" + id, label: "Tomorrow", icon: "arrow-right"},
			wkRow{id: "wk:tdel:" + id, label: "Delete", icon: "trash-2", danger: true})
		return sh
	}
	wkActions["tslot"] = func(c *console, arg string) {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 {
			return
		}
		id, what := parts[0], parts[1]
		d, _ := strconv.Atoi(parts[2])
		o := somaObj("todos#" + id)
		if o == nil {
			return
		}
		s, _ := o["slot"].(obj)
		start, mins, date := int(num(s["start"])), int(num(s["mins"])), str(s["date"])
		c.somaUI.keep = true
		switch what {
		case "off":
			wkSetSlot(id, "", 0, 0)
			return
		case "on":
			date = str(o["date"])
			if date == "" || date < somaToday() {
				date = somaToday()
			}
			now := time.Now().In(displayZone)
			start, mins = 9*60, 60
			if date == somaToday() {
				start = min(max((now.Hour()+1)*60, 8*60), 22*60)
			}
		case "start":
			start = min(max(start+d, 0), 24*60-15)
		case "mins":
			mins = max(mins+d, 15)
		}
		wkSetSlot(id, date, start, mins)
	}
	wkActions["tprio"] = func(c *console, arg string) {
		id, p, _ := strings.Cut(arg, ":")
		somaPatch("todos", id, func(o obj) {
			if p == "" {
				delete(o, "priority")
			} else {
				o["priority"] = p
			}
		})
		c.somaUI.keep = true
	}
	wkActions["tscope"] = func(c *console, arg string) {
		id, sc, _ := strings.Cut(arg, ":")
		somaPatch("todos", id, func(o obj) {
			o["scope"], o["date"], o["cleared"] = sc, somaToday(), false
			if sc == "week" {
				delete(o, "slot")
			}
		})
	}
	wkActions["ttomorrow"] = func(c *console, id string) {
		o := somaObj("todos#" + id)
		day := str(o["date"])
		if s, ok := o["slot"].(obj); ok {
			day = str(s["date"])
		}
		if day < somaToday() {
			day = somaToday()
		}
		wkSetTodoDate(id, wkAddDays(day, 1))
		c.toastMsg("Moved to " + wkDate(wkAddDays(day, 1)))
	}
	wkActions["tdel"] = func(c *console, id string) {
		o, at := somaRemove("todos", id)
		if o != nil {
			c.offerUndo("To-do deleted", func() { somaRestore("todos", o, at) })
		}
	}
	wkActions["tday"] = func(c *console, id string) {
		o := somaObj("todos#" + id)
		day := str(o["date"])
		if s, ok := o["slot"].(obj); ok {
			day = str(s["date"])
		}
		c.wkPickDate("tday:"+id, day, "Which day")
	}
	wkDateRequired["tday"] = true
	wkDateSetters["tday"] = func(c *console, id, date string) {
		if date != "" {
			wkSetTodoDate(id, date)
		}
	}
	wkActions["tdue"] = func(c *console, id string) {
		c.wkPickDate("tdue:"+id, str(somaObj("todos#" + id)["due"]), "Deadline")
	}
	wkDateSetters["tdue"] = func(c *console, id, date string) {
		somaPatch("todos", id, func(o obj) {
			if date == "" {
				delete(o, "due")
			} else {
				o["due"] = date
			}
		})
	}
	wkLongPress["todo"] = func(c *console, id string) { c.openSheet("task:" + id) }

	// Routines.
	wkPrompts["rtnew"] = func(string) (string, string) { return "The routine's name, e.g. Morning", "" }
	wkTypers["rtnew"] = func(c *console, _, text string) {
		if text == "" {
			return
		}
		id := "rt-" + somaNewID()
		somaAdd("dayRoutines", id, obj{"id": id, "name": text, "windowSeconds": 20 * 60, "steps": []any{},
			"color": wkRoutineColours[len(somaIDs("dayRoutines"))%len(wkRoutineColours)], "createdAt": nowMs()})
	}
	wkPrompts["rtstep"] = func(string) (string, string) { return "A step, e.g. Stretch 5 (minutes after the name)", "" }
	wkTypers["rtstep"] = func(c *console, id, text string) {
		if text == "" {
			return
		}
		mins := 5
		if f := strings.Fields(text); len(f) > 1 {
			if n, err := strconv.Atoi(f[len(f)-1]); err == nil && n > 0 && n <= 240 {
				mins, text = n, strings.Join(f[:len(f)-1], " ")
			}
		}
		somaPatch("dayRoutines", id, func(o obj) {
			steps, _ := o["steps"].([]any)
			o["steps"] = append(steps, obj{"id": "rs-" + somaNewID(), "label": text, "seconds": mins * 60, "source": "free"})
		})
		c.wkType("rtstep:"+id, "Next step (or hide the keyboard)", "")
	}
	wkSheets["routine"] = func(k *wk, id string) *wkSheet {
		for _, r := range wkRoutines() {
			if r.ID != id {
				continue
			}
			var sw []wkOpt
			for _, h := range wkRoutineColours {
				sw = append(sw, wkOpt{id: "wk:rtset:" + id + ":color:" + h, col: somaColour(h, wkBlue), sel: strings.EqualFold(h, r.Color)})
			}
			return &wkSheet{title: r.Name, rows: []wkRow{
				{id: "wk:type-open:rtrename:" + id, label: "Rename", icon: "pencil", detail: r.Name},
				{kind: "stepper", label: "Window", detail: secsLabel(r.Window), minus: "wk:rtset:" + id + ":window:-300", plus: "wk:rtset:" + id + ":window:300"},
				{kind: "swatches", opts: sw},
				{id: "wk:rtdel:" + id, label: "Delete routine", icon: "trash-2", danger: true},
			}}
		}
		return nil
	}
	wkPrompts["rtrename"] = func(id string) (string, string) {
		return "The routine's name", str(somaObj("dayRoutines#" + id)["name"])
	}
	wkTypers["rtrename"] = func(c *console, id, text string) {
		if text != "" {
			somaPatch("dayRoutines", id, func(o obj) { o["name"] = text })
		}
	}
	wkActions["rtset"] = func(c *console, arg string) {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 {
			return
		}
		somaPatch("dayRoutines", parts[0], func(o obj) {
			switch parts[1] {
			case "color":
				o["color"] = parts[2]
			case "window":
				d, _ := strconv.Atoi(parts[2])
				o["windowSeconds"] = max(num(o["windowSeconds"])+float64(d), 60)
			}
		})
		c.somaUI.keep = true
	}
	wkActions["rtdel"] = func(c *console, id string) {
		o, at := somaRemove("dayRoutines", id)
		if o != nil {
			c.offerUndo("Routine deleted", func() { somaRestore("dayRoutines", o, at) })
		}
	}
	wkSheets["rstep"] = func(k *wk, arg string) *wkSheet {
		rid, sid, _ := strings.Cut(arg, ":")
		for _, r := range wkRoutines() {
			for i, s := range r.Steps {
				if r.ID != rid || s.ID != sid {
					continue
				}
				sh := &wkSheet{title: s.Label, sub: r.Name, rows: []wkRow{
					{kind: "stepper", label: "Takes", detail: secsLabel(s.Seconds), minus: "wk:rsset:" + arg + ":-60", plus: "wk:rsset:" + arg + ":60"},
				}}
				if s.Source == "free" || s.Source == "" {
					sh.rows = append(sh.rows, wkRow{id: "wk:type-open:rsrename:" + arg, label: "Rename", icon: "pencil"})
				}
				if i > 0 {
					sh.rows = append(sh.rows, wkRow{id: "wk:rsmove:" + arg + ":-1", label: "Move up", icon: "arrow-up"})
				}
				if i < len(r.Steps)-1 {
					sh.rows = append(sh.rows, wkRow{id: "wk:rsmove:" + arg + ":1", label: "Move down", icon: "arrow-down"})
				}
				sh.rows = append(sh.rows, wkRow{id: "wk:rsdel:" + arg, label: "Remove step", icon: "trash-2", danger: true})
				return sh
			}
		}
		return nil
	}
	rsteps := func(rid string, fn func(steps []any, i int) []any, sid string) {
		somaPatch("dayRoutines", rid, func(o obj) {
			steps, _ := o["steps"].([]any)
			if i := stepIndex(steps, sid); i >= 0 {
				o["steps"] = fn(steps, i)
			}
		})
	}
	wkActions["rsset"] = func(c *console, arg string) {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 {
			return
		}
		d, _ := strconv.Atoi(parts[2])
		rsteps(parts[0], func(steps []any, i int) []any {
			so := steps[i].(obj)
			so["seconds"] = min(max(num(so["seconds"])+float64(d), 15), 4*3600)
			return steps
		}, parts[1])
		c.somaUI.keep = true
	}
	wkActions["rsmove"] = func(c *console, arg string) {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 {
			return
		}
		d, _ := strconv.Atoi(parts[2])
		rsteps(parts[0], func(steps []any, i int) []any {
			if j := i + d; j >= 0 && j < len(steps) {
				steps[i], steps[j] = steps[j], steps[i]
			}
			return steps
		}, parts[1])
		c.somaUI.keep = true
	}
	wkActions["rsdel"] = func(c *console, arg string) {
		rid, sid, _ := strings.Cut(arg, ":")
		rsteps(rid, func(steps []any, i int) []any { return append(steps[:i], steps[i+1:]...) }, sid)
	}
	wkPrompts["rsrename"] = func(arg string) (string, string) {
		rid, sid, _ := strings.Cut(arg, ":")
		for _, r := range wkRoutines() {
			for _, s := range r.Steps {
				if r.ID == rid && s.ID == sid {
					return "The step", s.Label
				}
			}
		}
		return "The step", ""
	}
	wkTypers["rsrename"] = func(c *console, arg, text string) {
		rid, sid, _ := strings.Cut(arg, ":")
		if text != "" {
			rsteps(rid, func(steps []any, i int) []any { steps[i].(obj)["label"] = text; return steps }, sid)
		}
	}
}
