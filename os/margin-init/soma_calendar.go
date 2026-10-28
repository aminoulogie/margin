package main

import (
	"fmt"
	"image"
	"image/color"
	"margin-init/ui"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (c *console) somaCalendar(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	su := &c.somaUI
	now := time.Now().In(displayZone)
	if su.month.IsZero() {
		su.month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
	}
	if su.sel == "" {
		su.sel = somaToday()
	}
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }

	// Week or Month.
	seg := image.Rect(w/2-160, y+10, w/2+160, y+76)
	ui.RoundRect(img, seg, 18, apCard2)
	for i, m := range []string{"Week", "Month"} {
		r := image.Rect(seg.Min.X+i*160, seg.Min.Y, seg.Min.X+(i+1)*160, seg.Max.Y)
		if (i == 0) == su.calWeek {
			ui.RoundRect(img, r.Inset(5), 14, apCard)
		}
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apLabel, m)
		btn("so:cal:"+strings.ToLower(m), r)
	}
	if su.calWeek {
		return c.somaWeek(p, y+100)
	}
	y += 100
	apText(img, f.title, mx, y+50, apLabel, su.month.Format("January 2006"))
	apText(img, f.title, w-mx-140, y+50, apBlue, "‹")
	apText(img, f.title, w-mx-40, y+50, apBlue, "›")
	btn("so:month:-1", image.Rect(w-mx-200, y, w-mx-90, y+80))
	btn("so:month:1", image.Rect(w-mx-90, y, w, y+80))
	y += 90
	cw := (w - 2*mx) / 7
	for i, d := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		apTextCenter(img, f.caption, mx+i*cw+cw/2, y+20, apSecondary, d)
	}
	y += 50
	todos := somaTodos()
	habits := somaHabits()
	first := su.month
	lead := (int(first.Weekday()) + 6) % 7
	daysIn := first.AddDate(0, 1, -1).Day()
	ch := 128
	for i := 0; i < lead+daysIn; i++ {
		if i < lead {
			continue
		}
		day := i - lead + 1
		date := time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, displayZone).Format("2006-01-02")
		r := image.Rect(mx+(i%7)*cw, y+(i/7)*ch, mx+(i%7+1)*cw, y+(i/7+1)*ch).Inset(4)
		bg := apCard
		if date == su.sel {
			bg = apBlue
		}
		ui.RoundRect(img, r, 18, bg)
		fg := apLabel
		if date == su.sel {
			fg = rgb(0xffffff)
		} else if date == somaToday() {
			fg = apBlue
		}
		apText(img, f.headline, r.Min.X+16, r.Min.Y+40, fg, strconv.Itoa(day))
		// What the day holds: training, to-dos, a plan, habits done.
		dots := []color.RGBA{}
		if somaTraining(date) != nil {
			dots = append(dots, rgb(0xff9f0a))
		}
		for _, t := range todos {
			if t.on(date) {
				dots = append(dots, rgb(0x0a84ff))
				break
			}
		}
		if soma.get("dayPlans/"+date) != "" {
			dots = append(dots, rgb(0xbf5af2))
		}
		n := 0
		for _, h := range habits {
			if h.done(date) {
				n++
			}
		}
		if n > 0 {
			apText(img, f.caption, r.Max.X-60, r.Min.Y+40, fg, fmt.Sprintf("%d✓", n))
		}
		for k, d := range dots {
			ui.Circle(img, r.Min.X+22+k*24, r.Max.Y-24, 8, d)
		}
		btn("so:day:"+date, r)
	}
	y += ((lead+daysIn+6)/7)*ch + 30
	lx := mx
	for _, l := range []struct {
		col   color.RGBA
		label string
	}{{rgb(0xff9f0a), "training"}, {rgb(0x0a84ff), "to-dos"}, {rgb(0xbf5af2), "day plan"}} {
		ui.Circle(img, lx+8, y+2, 8, l.col)
		apText(img, f.caption, lx+24, y+10, apSecondary, l.label)
		lx += 24 + ui.TextWidth(f.caption, l.label) + 36
	}
	apText(img, f.caption, lx, y+10, apSecondary, "n✓ habits done")
	y += 50

	return c.somaDayDetail(p, y)
}

// somaDayDetail is the chosen day under the calendar: training, food, the timeline, to-dos.
func (c *console) somaDayDetail(p *page, y int) int {
	f := apple()
	img := p.img
	mx := pageGutter
	su := &c.somaUI
	todos := somaTodos()
	sel, _ := time.ParseInLocation("2006-01-02", su.sel, displayZone)
	y = somaSection(img, mx, y, sel.Format("Monday 2 January"), "")
	if s := somaTraining(su.sel); s != nil {
		apText(img, f.headline, mx, y+40, apLabel, "Training: "+s.Split)
		apText(img, f.callout, mx, y+84, apSecondary, fmt.Sprintf("%s  ·  %d sets  ·  %.0f kg volume  ·  %.0f kcal", s.Duration, s.Sets, s.Volume, s.Calories))
		y += 110
	}
	if n := somaNutrition(su.sel); n.Logged {
		apText(img, f.headline, mx, y+40, apLabel, "Food")
		apText(img, f.callout, mx, y+84, apSecondary, fmt.Sprintf("%.0f / %.0f kcal  ·  protein %.0f / %.0f g  ·  carbs %.0f g  ·  fat %.0f g", n.Cals, n.GoalCals, n.P, n.GoalP, n.C, n.F))
		y += 110
	}
	y = c.somaTimeline(p, y+10, todos)
	var items []somaTodo
	for _, t := range todos {
		if t.on(su.sel) && !t.Cleared && t.SlotDate != su.sel { // the rest are on the timeline
			items = append(items, t)
		}
	}
	y = somaSection(img, mx, y, "To-dos", "not at a time")
	y = c.somaTodoRows(p, items, y)
	y = c.somaAddRow(p, "day", "Add a to-do for this day", y)
	return y
}

// --- the day, hour by hour --------------------------------------------------------------------

const somaHourH = 84 // pixels an hour on the timeline

type somaEvent struct {
	start, end      int // minutes from midnight
	label           string
	col             color.RGBA
	done            bool
	id              string // a to-do's: tapping ticks it
	column, columns int
}

// somaTimeline draws the chosen day the way Soma's day view does (lib/timeline.ts): the day
// plan faintly behind, the training session when it happened, and to-dos at their times.
// Tapping an empty half hour puts a new to-do there. Returns the y under it.
func (c *console) somaTimeline(p *page, y int, todos []somaTodo) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	su := &c.somaUI
	date := su.sel
	var evs []somaEvent
	for _, t := range todos {
		if t.SlotDate == date && !t.Cleared {
			evs = append(evs, somaEvent{start: t.SlotStart, end: t.SlotStart + max(t.SlotMins, 15), label: t.Text, col: apBlue, done: t.Done, id: t.ID})
		}
	}
	if s := somaTraining(date); s != nil && !s.Ended.IsZero() && somaDate(s.Ended) == date {
		end := s.Ended.Hour()*60 + s.Ended.Minute()
		start := max(min(end-int(s.minutes()+0.5), end-15), 0)
		evs = append(evs, somaEvent{start: start, end: max(end, start+15), label: "Workout · " + s.Split, col: rgb(0xff9f0a), done: true})
	}
	if m, ok := strings.CutPrefix(su.field, "at:"); ok && su.typing { // the one being typed
		at, _ := strconv.Atoi(m)
		evs = append(evs, somaEvent{start: at, end: at + 30, label: withCursor(su.editing, c.fieldBack), col: rgb(0x30d158)})
	}
	first := 5
	for _, e := range evs {
		first = min(first, e.start/60)
	}
	y = somaSection(img, mx, y, "Plan the day", "tap a time to add a to-do there")
	top := y + 10
	x0, x1 := mx+120, w-mx
	yOf := func(min int) int { return top + (min-first*60)*somaHourH/60 }

	// The plan, behind: each block from its start, around midnight if it crosses it.
	for _, b := range somaPlan(date) {
		s0 := int(b.Start*60 + 0.5)
		e0 := s0 + int(b.Hours*60+0.5)
		col := somaColour(b.Color, apBlue)
		for _, seg := range [][2]int{{s0, min(e0, 1440)}, {0, e0 - 1440}} {
			a, z := max(seg[0], first*60), min(seg[1], 1440)
			if a >= z {
				continue
			}
			r := image.Rect(x0, yOf(a), x1, yOf(z))
			ui.Fill(img, r, blend(col, apBG, 0.84))
			ui.Fill(img, image.Rect(x1-8, r.Min.Y, x1, r.Max.Y), blend(col, apBG, 0.5))
			if r.Dy() >= 34 && a == max(s0, first*60) {
				apText(img, f.caption, x1-24-ui.TextWidth(f.caption, b.Label), r.Min.Y+28, blend(col, apLabel, 0.45), b.Label)
			}
		}
	}
	for h := first; h <= 24; h++ {
		yy := yOf(h * 60)
		ui.Fill(img, image.Rect(x0, yy, x1, yy+1), apSeparator)
		if h < 24 {
			apText(img, f.caption, mx, yy+20, apSecondary, fmt.Sprintf("%02d:00", h))
		}
	}
	if date == somaToday() { // now
		now := time.Now().In(displayZone)
		if m := now.Hour()*60 + now.Minute(); m >= first*60 {
			ny := yOf(m)
			ui.Fill(img, image.Rect(x0, ny-1, x1, ny+2), apRed)
			ui.Circle(img, x0, ny, 7, apRed)
		}
	}

	// Events side by side where they overlap, as Calendar lays out a double booking.
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].start < evs[j].start })
	for i := 0; i < len(evs); {
		j, end := i, evs[i].end
		var colEnds []int
		for ; j < len(evs) && (j == i || evs[j].start < end); j++ {
			e := &evs[j]
			e.column = -1
			for k, ce := range colEnds {
				if ce <= e.start {
					e.column, colEnds[k] = k, e.end
					break
				}
			}
			if e.column < 0 {
				e.column = len(colEnds)
				colEnds = append(colEnds, e.end)
			}
			end = max(end, e.end)
		}
		for k := i; k < j; k++ {
			evs[k].columns = len(colEnds)
		}
		i = j
	}
	for _, e := range evs {
		cw := (x1 - x0 - 8) / e.columns
		r := image.Rect(x0+4+e.column*cw, yOf(e.start)+2, x0+4+(e.column+1)*cw-4, yOf(e.end)-2)
		r.Max.Y = max(r.Max.Y, r.Min.Y+40)
		bg, fg := blend(e.col, apBG, 0.55), apLabel
		if e.done && e.id != "" {
			bg, fg = apCard2, apSecondary
		}
		ui.RoundRect(img, r, 12, bg)
		ui.RoundRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+8, r.Max.Y), 4, e.col)
		tx := r.Min.X + 22
		if e.id != "" { // a to-do: its tick
			cy := r.Min.Y + 20
			ui.Circle(img, tx+12, cy, 13, e.col)
			if e.done {
				iconCheck(img, tx+3, cy, rgb(0xffffff))
			} else {
				ui.Circle(img, tx+12, cy, 10, bg)
			}
			tx += 46
		}
		label := clockOf(float64(e.start)/60) + "  " + e.label
		apText(img, f.callout, tx, r.Min.Y+30, fg, clip(f.callout, label, r.Max.X-tx-12))
		if m, ok := strings.CutPrefix(su.field, "at:"); ok && su.typing && e.id == "" && e.col == rgb(0x30d158) && m == strconv.Itoa(e.start) {
			su.slotY = r.Max.Y
		}
		if e.id != "" {
			// The handle on the right moves it: then tap the new time.
			hr := image.Rect(r.Max.X-70, r.Min.Y, r.Max.X, r.Max.Y)
			for k := -1; k <= 1; k++ {
				ui.Fill(img, image.Rect(hr.Min.X+20, r.Min.Y+20+k*8, hr.Max.X-20, r.Min.Y+23+k*8), fg)
			}
			if su.moving == e.id {
				ui.RoundRect(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+4), 2, apBlue)
			}
			p.buttons = append(p.buttons, button{"so:todo:" + e.id, image.Rect(r.Min.X, r.Min.Y, hr.Min.X, r.Max.Y)}, button{"so:move:" + e.id, hr})
		}
	}
	if su.moving != "" {
		for _, t := range todos {
			if t.ID == su.moving {
				ui.RoundRect(img, image.Rect(x0, top-70, x1, top-10), 16, apBlue)
				apText(img, f.callout, x0+24, top-28, rgb(0xffffff), clip(f.callout, "Tap the new time for "+t.Text+" (or its handle to cancel)", x1-x0-48))
			}
		}
	}
	for m := first * 60; m < 1440; m += 30 { // the empty half hours (after the events: they win)
		id := fmt.Sprintf("so:at:%d", m)
		if su.moving != "" {
			id = fmt.Sprintf("so:moveto:%d", m)
		}
		p.buttons = append(p.buttons, button{id, image.Rect(x0, yOf(m), x1, yOf(m+30))})
	}
	return yOf(1440) + 40
}

// somaWeek is the calendar's week: a card a day with its plan, training and to-dos; tapping a
// day opens it below.
func (c *console) somaWeek(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	su := &c.somaUI
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	mon, _ := time.ParseInLocation("2006-01-02", mondayOf(su.sel), displayZone)
	_, wk := mon.ISOWeek()
	apText(img, f.title, mx, y+50, apLabel, fmt.Sprintf("Week %d · %s", wk, mon.Format("2 Jan")))
	apText(img, f.title, w-mx-140, y+50, apBlue, "‹")
	apText(img, f.title, w-mx-40, y+50, apBlue, "›")
	btn("so:wk:-1", image.Rect(w-mx-200, y, w-mx-90, y+80))
	btn("so:wk:1", image.Rect(w-mx-90, y, w, y+80))
	y += 100
	todos := somaTodos()
	for i := 0; i < 7; i++ {
		d := mon.AddDate(0, 0, i)
		date := d.Format("2006-01-02")
		var lines []string
		for _, b := range somaPlan(date) {
			lines = append(lines, clockOf(b.Start)+" "+b.Label)
		}
		if s := somaTraining(date); s != nil {
			lines = append(lines, "Training · "+s.Split)
		}
		var items []somaTodo
		for _, t := range todos {
			if t.on(date) && !t.Cleared {
				items = append(items, t)
			}
		}
		sort.SliceStable(items, func(a, b int) bool { return items[a].SlotStart < items[b].SlotStart })
		h := 90 + 44*len(lines) + 70*len(items)
		r := image.Rect(mx, y, w-mx, y+h)
		bg := apCard
		if date == su.sel {
			bg = blend(apBlue, apBG, 0.75)
		}
		ui.RoundRect(img, r, radiusCard, bg)
		fg := apLabel
		if date == somaToday() {
			fg = apBlue
		}
		apText(img, f.headline, r.Min.X+30, r.Min.Y+56, fg, d.Format("Monday 2"))
		btn("so:day:"+date, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+80))
		ty := r.Min.Y + 110
		for _, l := range lines {
			apText(img, f.caption, r.Min.X+30, ty, apSecondary, clip(f.caption, l, r.Dx()-60))
			ty += 44
		}
		for _, t := range items {
			cy := ty - 10
			ui.Circle(img, r.Min.X+46, cy, 16, apBlue)
			if t.Done {
				iconCheck(img, r.Min.X+36, cy, rgb(0xffffff))
			} else {
				ui.Circle(img, r.Min.X+46, cy, 13, bg)
			}
			text := t.Text
			if t.SlotDate == date {
				text = clockOf(float64(t.SlotStart)/60) + "  " + text
			}
			apText(img, f.body, r.Min.X+80, ty, apLabel, clip(f.body, text, r.Dx()-110))
			btn("so:todo:"+t.ID, image.Rect(r.Min.X, ty-50, r.Max.X, ty+20))
			ty += 70
		}
		y += h + 16
	}
	y += 20
	return c.somaDayDetail(p, y)
}
