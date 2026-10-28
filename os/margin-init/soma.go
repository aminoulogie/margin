package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"

	"rsc.io/qr"
)

// The Soma tab: Soma (the PC and iPhone app) on the tablet, kept in step with them through
// Soma's own sync (somasync.go). A beta, kept simple: Today (habits, reading, to-dos),
// Calendar (a month; a day's plan, to-dos, training and food), Reports (training,
// nutrition, habits, reading). Ticking a habit, reading time and to-dos can be changed here;
// the rest is shown.

type somaUI struct {
	view    string    // "today", "calendar", "goals", "notes", "reports"
	note    string    // the note open in "notes" ("" for the list)
	month   time.Time // the calendar's month
	sel     string    // the calendar's day
	calWeek bool      // the calendar shows a week, not the month
	moving  string    // a timed to-do being moved: the next time tapped is its new time
	typing  bool
	field   string // what's being typed: "todo", "week", "day" (a to-do), "url", "code"
	editing string
	url     string // the sync address, typed before the code
	msg     string // the last thing that went wrong
	slotY   int    // page y under the to-do being typed on the timeline
}

func somaColour(hex string, def color.RGBA) color.RGBA {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if len(h) != 6 || err != nil {
		return def
	}
	return rgb(uint32(v))
}

func (c *console) somaPage() *page {
	f := apple()
	w, mx := c.s.W, 48
	tw := w - 2*mx
	su := &c.somaUI
	if su.view == "" {
		su.view = "today"
	}
	soma.load()
	soma.mu.Lock()
	soma.changed = func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.mode == modeSoma && !c.somaUI.typing {
			c.redrawSoon(func() {
				if c.mode == modeSoma {
					c.showPage()
				}
			})
		}
	}
	soma.mu.Unlock()

	h := map[string]int{"today": 2400, "calendar": 6500, "goals": 6000, "notes": 8000, "reports": 3000}[su.view]
	img := canvas(w, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img, header: tabsH}
	c.booksTabs(p, "tab:soma")
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	pill := func(id, label string, r image.Rectangle, primary bool) {
		bg, fg := apCard2, apBlue
		if primary {
			bg, fg = apBlue, rgb(0xffffff)
		}
		ui.RoundRect(img, r, r.Dy()/2, bg)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, label)
		btn(id, r)
	}
	field := func(id, label, value string, y int) {
		r := image.Rect(mx, y, w-mx, y+96)
		ui.RoundRect(img, r, 22, apCard)
		on := su.typing && su.field == id
		text, col := value, apLabel
		if text == "" && !on {
			text, col = label, apSecondary
		}
		if on {
			text += "|"
		}
		apText(img, f.body, r.Min.X+30, r.Min.Y+60, col, clip(f.body, text, r.Dx()-60))
		btn("so:type:"+id, r)
	}

	apText(img, f.serifLarge, mx, 228, apLabel, "Soma")
	if !soma.linked() {
		y := 300
		how := "Link the tablet to your Soma: in Soma on the phone or the PC, open Settings > Sync, and type its server address and recovery code here. The tablet then keeps your habits, to-dos, plans and reports in step with them."
		if a := sendAddress(); a != "" {
			how = "Link the tablet to your Soma. Easiest: on your phone, open Soma > Settings > Sync, copy the server address and the recovery code, then open http://" + a + " in the phone's browser and paste them under Link Soma. Or type them here."
		}
		drawParagraphs(img, f.callout, how, mx, y, tw, 44, y+300, apSecondary)
		y += 240
		url := su.url
		if su.typing && su.field == "url" {
			url = su.editing
		}
		field("url", "Sync address (https://...workers.dev)", url, y)
		code := ""
		if su.typing && su.field == "code" {
			code = su.editing
		}
		field("code", "Recovery code (56 characters)", code, y+120)
		pill("so:link", "Link", image.Rect(mx, y+260, w-mx, y+356), true)
		if su.msg != "" {
			drawParagraphs(img, f.callout, su.msg, mx, y+420, tw, 44, y+560, apRed)
		}
		if a := sendAddress(); a != "" {
			// Scanned with the phone's camera: opens the page where the address and code are pasted.
			const size = 440
			x0 := (w - size) / 2
			drawQR(img, "http://"+a+"/#soma", image.Rect(x0, y+600, x0+size, y+600+size))
			apText(img, f.callout, mx, y+600+size+60, apSecondary, "Scan with the iPhone camera, then paste the address and code.")
		}
		return p
	}

	apText(img, f.caption, mx, 280, apSecondary, time.Now().In(displayZone).Format("Monday 2 January")+"  ·  "+soma.status())
	apText(img, f.body, w-mx-150, 280, apBlue, "Sync now")
	btn("so:sync", image.Rect(w-mx-180, 230, w, 310))
	// The three parts, as a segmented control.
	seg := image.Rect(mx, 310, w-mx, 386)
	ui.RoundRect(img, seg, 20, apCard2)
	views := []struct{ id, label string }{{"today", "Today"}, {"calendar", "Calendar"}, {"goals", "Goals"}, {"notes", "Notes"}, {"reports", "Reports"}}
	for i, v := range views {
		r := image.Rect(seg.Min.X+i*seg.Dx()/len(views), seg.Min.Y, seg.Min.X+(i+1)*seg.Dx()/len(views), seg.Max.Y)
		if su.view == v.id {
			ui.RoundRect(img, r.Inset(6), 16, apCard)
		}
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apLabel, v.label)
		btn("so:view:"+v.id, r)
	}
	if su.msg != "" {
		apText(img, f.caption, mx, 430, apRed, clip(f.caption, su.msg, tw))
	}
	y := 460
	switch su.view {
	case "calendar":
		y = c.somaCalendar(p, y)
	case "reports":
		y = c.somaReports(p, y)
	case "goals":
		y = c.somaGoals(p, y)
	case "notes":
		y = c.somaNotesView(p, y)
	default:
		y = c.somaToday(p, y)
	}
	if y+200 < img.Rect.Dy() && img.Rect.Dy() > c.viewH() {
		// Shorter than drawn: crop the page so it doesn't scroll into nothing.
		p.img = img.SubImage(image.Rect(0, 0, w, max(y+200, c.viewH()))).(*image.RGBA)
	}
	return p
}

// section draws a section title; returns the y under it.
func somaSection(img *image.RGBA, x, y int, title, note string) int {
	f := apple()
	apText(img, f.title, x, y+50, apLabel, title)
	if note != "" {
		apText(img, f.caption, x+ui.TextWidth(f.title, title)+24, y+50, apSecondary, note)
	}
	return y + 80
}

func (c *console) somaToday(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	date := somaToday()
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }

	habits := somaHabits()
	done := 0
	for _, h := range habits {
		if h.done(date) {
			done++
		}
	}
	c.sleepPill(p, y+45)
	y = somaSection(img, mx, y, "Habits", fmt.Sprintf("%d of %d today", done, len(habits)))
	if len(habits) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "No habits yet: add them in Soma.")
		y += 60
	}
	y = c.somaHabitRows(p, habits, date, y)

	y += 20
	y = somaSection(img, mx, y, "Reading", fmt.Sprintf("%d min today", somaReading(date)))
	for i, m := range []int{10, 15, 30} {
		r := image.Rect(mx+i*190, y, mx+i*190+170, y+84)
		ui.RoundRect(img, r, 42, apCard2)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apBlue, fmt.Sprintf("+%d min", m))
		btn(fmt.Sprintf("so:read:%d", m), r)
	}
	y += 120

	todos := somaTodos()
	sort.SliceStable(todos, func(i, j int) bool { return todos[i].rank() < todos[j].rank() })
	// The evening review: what's left today moves to tomorrow in one tap.
	left := 0
	for _, t := range todos {
		if t.Scope == "day" && t.active(date) && !t.Done {
			left++
		}
	}
	if left > 0 && (time.Now().In(displayZone).Hour() >= 18 || c.winding) {
		r := image.Rect(mx, y, w-mx, y+92)
		ui.RoundRect(img, r, 22, rgb(0x2c2c54))
		apText(img, f.headline, r.Min.X+34, r.Min.Y+58, rgb(0xbf9fff), fmt.Sprintf("Move %d unfinished to tomorrow", left))
		btn("so:tomorrow", r)
		y += 120
	}
	for _, list := range []struct{ scope, title, add string }{{"day", "To Do Today", "todo"}, {"week", "This Week", "week"}} {
		var items []somaTodo
		left := 0
		for _, t := range todos {
			if t.Scope == list.scope && t.active(date) {
				items = append(items, t)
				if !t.Done {
					left++
				}
			}
		}
		y = somaSection(img, mx, y, list.title, fmt.Sprintf("%d left", left))
		y = c.somaTodoRows(p, items, y)
		y = c.somaAddRow(p, list.add, "Add a to-do", y)
		y += 30
	}
	return y
}

// somaTodoRows draws to-dos as rows to tick.
func (c *console) somaTodoRows(p *page, items []somaTodo, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	for _, t := range items {
		r := image.Rect(mx, y, w-mx, y+92)
		ui.RoundRect(img, r, 22, apCard)
		cx, cy := r.Min.X+50, (r.Min.Y+r.Max.Y)/2
		ui.Circle(img, cx, cy, 22, apBlue)
		if t.Done {
			iconCheck(img, cx-13, cy, rgb(0xffffff))
		} else {
			ui.Circle(img, cx, cy, 18, apCard)
		}
		col := apLabel
		if t.Done {
			col = apSecondary
		}
		text := t.Text
		if t.SlotDate != "" {
			text = clockOf(float64(t.SlotStart)/60) + "  " + text
		}
		apText(img, f.body, r.Min.X+96, cy+14, col, clip(f.body, text, r.Dx()-230))
		// Priority: ! high, · low; tap to change.
		pr := image.Rect(r.Max.X-110, r.Min.Y, r.Max.X, r.Max.Y)
		switch t.Priority {
		case "high":
			apTextCenter(img, f.title, (pr.Min.X+pr.Max.X)/2, cy, apRed, "!")
		case "low":
			apTextCenter(img, f.caption, (pr.Min.X+pr.Max.X)/2, cy, apSecondary, "low")
		default:
			apTextCenter(img, f.caption, (pr.Min.X+pr.Max.X)/2, cy, apCard2, "!")
		}
		p.buttons = append(p.buttons, button{"so:todo:" + t.ID, image.Rect(r.Min.X, r.Min.Y, pr.Min.X, r.Max.Y)}, button{"so:prio:" + t.ID, pr})
		y += 104
	}
	return y
}

func (c *console) somaAddRow(p *page, id, label string, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	su := &c.somaUI
	r := image.Rect(mx, y, w-mx, y+92)
	ui.RoundRect(img, r, 22, apCard2)
	text, col := "+  "+label, apBlue
	if su.typing && su.field == id {
		text, col = su.editing+"|", apLabel
	}
	apText(img, f.body, r.Min.X+34, (r.Min.Y+r.Max.Y)/2+14, col, clip(f.body, text, r.Dx()-60))
	p.buttons = append(p.buttons, button{"so:type:" + id, r})
	return y + 104
}

func (c *console) somaCalendar(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
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
	mx := 48
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
	w, mx := c.s.W, 48
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
		evs = append(evs, somaEvent{start: at, end: at + 30, label: su.editing + "|", col: rgb(0x30d158)})
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

func (c *console) somaReports(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	date := somaToday()

	// Training: the last two weeks.
	var sessions []*somaSession
	for _, d := range days(date, 14) {
		if s := somaTraining(d); s != nil {
			sessions = append(sessions, s)
		}
	}
	vol, sets := 0.0, 0
	for _, s := range sessions {
		vol += s.Volume
		sets += s.Sets
	}
	y = somaSection(img, mx, y, "Training", fmt.Sprintf("%d sessions in 2 weeks  ·  %d sets  ·  %.0f kg", len(sessions), sets, vol))
	if len(sessions) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "No training logged in the last two weeks.")
		y += 60
	}
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		t, _ := time.Parse("2006-01-02", s.Date)
		r := image.Rect(mx, y, w-mx, y+92)
		ui.RoundRect(img, r, 18, apCard)
		apText(img, f.headline, r.Min.X+30, r.Min.Y+40, apLabel, s.Split)
		apText(img, f.caption, r.Min.X+30, r.Min.Y+76, apSecondary, t.Format("Mon 2 Jan"))
		apText(img, f.callout, r.Min.X+420, r.Min.Y+58, apSecondary, fmt.Sprintf("%s  ·  %d sets  ·  %.0f kg  ·  %.0f kcal", s.Duration, s.Sets, s.Volume, s.Calories))
		y += 102
	}

	// Nutrition: the last week, calories against the goal.
	y += 20
	y = somaSection(img, mx, y, "Nutrition", "last 7 days")
	for _, d := range days(date, 7) {
		n := somaNutrition(d)
		t, _ := time.Parse("2006-01-02", d)
		r := image.Rect(mx, y, w-mx, y+92)
		ui.RoundRect(img, r, 18, apCard)
		apText(img, f.headline, r.Min.X+30, r.Min.Y+56, apLabel, t.Format("Mon 2"))
		if !n.Logged {
			apText(img, f.callout, r.Min.X+200, r.Min.Y+56, apSecondary, "nothing logged")
			y += 102
			continue
		}
		bar := image.Rect(r.Min.X+200, r.Min.Y+30, r.Min.X+600, r.Min.Y+62)
		ui.RoundRect(img, bar, 16, apCard2)
		if n.GoalCals > 0 {
			fill := min(n.Cals/n.GoalCals, 1.3) / 1.3
			col := rgb(0x30d158)
			if n.Cals > n.GoalCals*1.1 || n.Cals < n.GoalCals*0.8 {
				col = rgb(0xff9f0a)
			}
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(int(float64(bar.Dx())*fill), 32), bar.Max.Y), 16, col)
		}
		txt := fmt.Sprintf("%.0f/%.0f kcal  P %.0f/%.0f", n.Cals, n.GoalCals, n.P, n.GoalP)
		if n.Kg > 0 {
			txt += fmt.Sprintf("  %.1f kg", n.Kg)
		}
		apText(img, f.callout, r.Min.X+630, r.Min.Y+58, apSecondary, clip(f.callout, txt, r.Max.X-r.Min.X-660))
		y += 102
	}

	// Habits: this week.
	y += 20
	monday := mondayOf(date)
	var week []string
	for _, d := range days(date, 7) {
		if d >= monday {
			week = append(week, d)
		}
	}
	y = somaSection(img, mx, y, "Habits", "this week")
	for _, h := range somaHabits() {
		n := 0
		for _, d := range week {
			if h.done(d) {
				n++
			}
		}
		r := image.Rect(mx, y, w-mx, y+76)
		ui.RoundRect(img, r, 18, apCard)
		apText(img, f.body, r.Min.X+30, r.Min.Y+50, apLabel, clip(f.body, h.Name, 460))
		bar := image.Rect(r.Min.X+520, r.Min.Y+26, r.Max.X-150, r.Min.Y+50)
		ui.RoundRect(img, bar, 12, apCard2)
		if n > 0 {
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(bar.Dx()*n/max(len(week), 1), 24), bar.Max.Y), 12, somaColour(h.Color, apBlue))
		}
		apText(img, f.callout, r.Max.X-120, r.Min.Y+50, apSecondary, fmt.Sprintf("%d / %d", n, len(week)))
		y += 86
	}

	// Reading: the last week.
	y += 20
	total := 0
	for _, d := range days(date, 7) {
		total += somaReading(d)
	}
	y = somaSection(img, mx, y, "Reading", fmt.Sprintf("%d min in 7 days", total))
	top := 1
	for _, d := range days(date, 7) {
		top = max(top, somaReading(d))
	}
	bw := (w - 2*mx) / 7
	for i, d := range days(date, 7) {
		m := somaReading(d)
		bh := 200 * m / top
		x := mx + i*bw
		ui.RoundRect(img, image.Rect(x+20, y+220-bh, x+bw-20, y+220), 10, apBlue)
		t, _ := time.Parse("2006-01-02", d)
		apTextCenter(img, f.caption, x+bw/2, y+250, apSecondary, t.Format("Mon"))
		apTextCenter(img, f.caption, x+bw/2, y+200-bh, apSecondary, strconv.Itoa(m))
	}
	return y + 300
}

// somaTap handles the Soma tab's buttons. Caller holds drawMu.
func (c *console) somaTap(id string) bool {
	if !strings.HasPrefix(id, "so:") {
		return false
	}
	su := &c.somaUI
	su.msg = ""
	field := strings.TrimPrefix(id, "so:type:")
	if at, ok := strings.CutPrefix(id, "so:at:"); ok {
		field = "at:" + at
	}
	if su.typing && field != su.field {
		c.somaDone() // a tap elsewhere: what was typed is kept
	}
	switch {
	case strings.HasPrefix(id, "so:view:"):
		su.view = strings.TrimPrefix(id, "so:view:")
		c.setScroll(modeSoma, 0)
	case id == "so:sync":
		go soma.sync()
	case strings.HasPrefix(id, "so:habit:"):
		if err := somaToggleHabit(strings.TrimPrefix(id, "so:habit:")); err != nil {
			su.msg = err.Error()
		}
	case strings.HasPrefix(id, "so:goal:"): // so:goal:<id>:<delta>
		g, d, _ := strings.Cut(strings.TrimPrefix(id, "so:goal:"), ":")
		delta, _ := strconv.ParseFloat(d, 64)
		somaGoalTap(g, delta)
	case id == "so:sleep":
		somaGoToSleep()
		go soma.sync()
		c.setScreen(false)
		return true
	case id == "so:wake":
		if msg := somaWakeUp(); msg != "" {
			c.toastMsg(msg)
		}
		go soma.sync()
	case strings.HasPrefix(id, "so:note:"):
		su.note = strings.TrimPrefix(id, "so:note:")
		c.setScroll(modeSoma, 0)
	case id == "so:notes":
		su.note = ""
		c.setScroll(modeSoma, 0)
	case strings.HasPrefix(id, "so:tick:"): // so:tick:<line>
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "so:tick:"))
		somaNoteToggle(su.note, i)
	case strings.HasPrefix(id, "so:step:"): // so:step:<project>:<step>
		pj, st, _ := strings.Cut(strings.TrimPrefix(id, "so:step:"), ":")
		somaStepTap(pj, st)
	case id == "so:cal:week", id == "so:cal:month":
		su.calWeek = id == "so:cal:week"
	case strings.HasPrefix(id, "so:wk:"):
		d, _ := strconv.Atoi(strings.TrimPrefix(id, "so:wk:"))
		t, _ := time.ParseInLocation("2006-01-02", su.sel, displayZone)
		su.sel = t.AddDate(0, 0, 7*d).Format("2006-01-02")
	case strings.HasPrefix(id, "so:move:"):
		if m := strings.TrimPrefix(id, "so:move:"); su.moving == m {
			su.moving = ""
		} else {
			su.moving = m
		}
	case strings.HasPrefix(id, "so:moveto:"):
		m, _ := strconv.Atoi(strings.TrimPrefix(id, "so:moveto:"))
		somaMoveTodo(su.moving, su.sel, m)
		su.moving = ""
	case strings.HasPrefix(id, "so:amt:"): // so:amt:<habit>:<delta>
		h, d, _ := strings.Cut(strings.TrimPrefix(id, "so:amt:"), ":")
		delta, _ := strconv.ParseFloat(d, 64)
		somaLogAmount(h, delta)
	case strings.HasPrefix(id, "so:when:"):
		somaHabitWhen(strings.TrimPrefix(id, "so:when:"))
	case strings.HasPrefix(id, "so:prio:"):
		somaTodoPriority(strings.TrimPrefix(id, "so:prio:"))
	case id == "so:tomorrow":
		if n := somaMoveToTomorrow(); n > 0 {
			c.toastMsg(fmt.Sprintf("Moved %d to tomorrow", n))
		}
	case strings.HasPrefix(id, "so:todo:"):
		somaToggleTodo(strings.TrimPrefix(id, "so:todo:"))
	case strings.HasPrefix(id, "so:read:"):
		m, _ := strconv.Atoi(strings.TrimPrefix(id, "so:read:"))
		somaAddReading(somaToday(), m)
	case strings.HasPrefix(id, "so:month:"):
		d, _ := strconv.Atoi(strings.TrimPrefix(id, "so:month:"))
		su.month = su.month.AddDate(0, d, 0)
	case strings.HasPrefix(id, "so:day:"):
		su.sel = strings.TrimPrefix(id, "so:day:")
	case strings.HasPrefix(id, "so:at:"):
		if !su.typing {
			su.typing, su.field, su.editing = true, field, ""
		}
		c.skb.visible = true
		c.showPage()
		// Keep the slot being typed in above the keyboard.
		if sy := c.barH + su.slotY - c.scrollY(); sy > c.skb.y0-30 {
			c.setScroll(modeSoma, c.scrollY()+sy-(c.skb.y0-30))
		}
	case strings.HasPrefix(id, "so:type:"):
		f := strings.TrimPrefix(id, "so:type:")
		if !su.typing || su.field != f {
			su.typing, su.field, su.editing = true, f, ""
			if f == "url" {
				su.editing = su.url
			}
		}
		c.skb.visible = true
	case id == "so:link": // what was being typed was just tried (somaDone)
		if !soma.linked() && su.msg == "" {
			su.msg = "Type the sync address, then the recovery code."
		}
	}
	c.showPage()
	return true
}

// somaKey takes the keyboard's output while typing in Soma. Caller holds drawMu.
func (c *console) somaKey(b []byte) {
	su := &c.somaUI
	if len(b) == 0 || b[0] == 0x1b {
		return
	}
	for _, ch := range string(b) {
		switch {
		case ch == '\r' || ch == '\n':
			c.somaDone()
			c.showPage()
			return
		case ch == 0x7f || ch == 0x08:
			if r := []rune(su.editing); len(r) > 0 {
				su.editing = string(r[:len(r)-1])
			}
		case ch == 0x15:
			su.editing = ""
		case ch >= ' ' && len([]rune(su.editing)) < 200:
			su.editing += string(ch)
		}
	}
	c.showPage()
}

// somaDone finishes what was being typed: a to-do is added, the link is tried.
func (c *console) somaDone() {
	su := &c.somaUI
	text := strings.TrimSpace(su.editing)
	su.typing, su.editing = false, ""
	c.skb.visible = false
	switch su.field {
	case "todo":
		somaAddTodo(text, "day", somaToday())
	case "week":
		somaAddTodo(text, "week", somaToday())
	case "day":
		somaAddTodo(text, "day", su.sel)
	case "url":
		su.url = text
		if text != "" { // the code next
			su.typing, su.field = true, "code"
			c.skb.visible = true
		}
	case "code":
		if err := soma.setLink(su.url, text); err != nil {
			su.msg = err.Error()
		} else {
			su.msg = ""
			go soma.sync()
		}
	default:
		if pj, ok := strings.CutPrefix(su.field, "step:"); ok {
			somaAddStep(pj, text)
		}
		switch su.field {
		case "noteline":
			somaNoteAppend(su.note, text)
		case "notetask":
			if text != "" {
				somaNoteAppend(su.note, "- [ ] "+text)
			}
		}
		if su.field == "newnote" {
			if id := somaNewNote(text); id != "" {
				su.note = id
			}
		}
		if at, ok := strings.CutPrefix(su.field, "at:"); ok {
			m, _ := strconv.Atoi(at)
			somaAddTodoAt(text, su.sel, m, 30)
		}
	}
}

// drawQR draws text as a QR code filling r (a square), black on white with a quiet border.
func drawQR(img *image.RGBA, text string, r image.Rectangle) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return
	}
	n := code.Size + 8 // 4 modules of white all round
	m := r.Dx() / n
	if m < 1 {
		return
	}
	draw.Draw(img, r, image.White, image.Point{}, draw.Src)
	o := r.Min.Add(image.Pt((r.Dx()-code.Size*m)/2, (r.Dy()-code.Size*m)/2))
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				draw.Draw(img, image.Rect(o.X+x*m, o.Y+y*m, o.X+(x+1)*m, o.Y+(y+1)*m), image.Black, image.Point{}, draw.Src)
			}
		}
	}
}

// somaGoals is the week's, month's and year's goals, then the active projects with their steps.
func (c *console) somaGoals(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	now := time.Now().In(displayZone)
	_, wk := now.ISOWeek()
	for _, hz := range []struct{ id, title, note string }{
		{"week", "This Week", fmt.Sprintf("week %d", wk)},
		{"month", "This Month", now.Format("January")},
		{"year", "This Year", now.Format("2006")},
	} {
		goals := somaGoalsNow(hz.id)
		done := 0
		for _, g := range goals {
			if g.Done {
				done++
			}
		}
		y = somaSection(img, mx, y, hz.title, fmt.Sprintf("%s  ·  %d of %d done", hz.note, done, len(goals)))
		if len(goals) == 0 {
			apText(img, f.callout, mx, y+30, apSecondary, "No goals here yet: add them in Soma > Goals.")
			y += 70
		}
		for _, g := range goals {
			r := image.Rect(mx, y, w-mx, y+104)
			ui.RoundRect(img, r, 22, apCard)
			col := somaColour(g.Color, apBlue)
			cx, cy := r.Min.X+56, (r.Min.Y+r.Max.Y)/2
			ui.Circle(img, cx, cy, 26, col)
			if g.Done {
				iconCheck(img, cx-14, cy, rgb(0xffffff))
			} else {
				ui.Circle(img, cx, cy, 21, apCard)
			}
			right := r.Max.X - 30
			if g.Target > 0 { // counted: progress, a bar and - / + buttons
				minus := image.Rect(r.Max.X-230, r.Min.Y+18, r.Max.X-140, r.Max.Y-18)
				plus := image.Rect(r.Max.X-120, r.Min.Y+18, r.Max.X-30, r.Max.Y-18)
				for _, b := range []struct {
					r     image.Rectangle
					t, id string
				}{{minus, "-", ":-1"}, {plus, "+", ":1"}} {
					ui.RoundRect(img, b.r, 30, apCard2)
					apTextCenter(img, f.title, (b.r.Min.X+b.r.Max.X)/2, (b.r.Min.Y+b.r.Max.Y)/2, apBlue, b.t)
					btn("so:goal:"+g.ID+b.id, b.r)
				}
				label := fmt.Sprintf("%s / %s %s", strconv.FormatFloat(g.Progress, 'f', -1, 64), strconv.FormatFloat(g.Target, 'f', -1, 64), g.Unit)
				lw := ui.TextWidth(f.caption, label)
				apText(img, f.caption, r.Max.X-260-lw, cy+10, apSecondary, label)
				right = r.Max.X - 280 - lw
				bar := image.Rect(r.Min.X+110, r.Max.Y-18, right, r.Max.Y-10)
				ui.RoundRect(img, bar, 4, apCard2)
				if fill := int(float64(bar.Dx()) * min(1, g.Progress/g.Target)); fill > 8 {
					ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+fill, bar.Max.Y), 4, col)
				}
			} else {
				btn("so:goal:"+g.ID+":0", r)
			}
			tcol := apLabel
			if g.Done {
				tcol = apSecondary
			}
			apText(img, f.headline, r.Min.X+110, cy+8, tcol, clip(f.headline, g.Title, right-r.Min.X-120))
			y += 120
		}
		y += 30
	}

	projects := somaProjects()
	y = somaSection(img, mx, y, "Projects", fmt.Sprintf("%d active", len(projects)))
	if len(projects) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "No active projects: add them in Soma > Projects.")
		y += 70
	}
	today := somaToday()
	for _, pj := range projects {
		col := somaColour(pj.Color, apBlue)
		head := image.Rect(mx, y, w-mx, y+130)
		ui.RoundRect(img, head, 22, apCard)
		apText(img, f.headline, head.Min.X+34, head.Min.Y+56, apLabel, clip(f.headline, pj.Name, head.Dx()-400))
		var sub []string
		if pj.Client != "" {
			sub = append(sub, pj.Client)
		}
		if pj.Due != "" {
			if t, err := time.Parse("2006-01-02", pj.Due); err == nil {
				t0, _ := time.Parse("2006-01-02", today)
				d := int(t.Sub(t0).Hours() / 24)
				switch {
				case d < 0:
					sub = append(sub, fmt.Sprintf("%d days late", -d))
				case d == 0:
					sub = append(sub, "due today")
				default:
					sub = append(sub, fmt.Sprintf("%d days left", d))
				}
			}
		}
		if pj.Priority != "" {
			sub = append(sub, pj.Priority+" priority")
		}
		apText(img, f.caption, head.Min.X+34, head.Min.Y+100, apSecondary, clip(f.caption, strings.Join(sub, "  ·  "), head.Dx()-400))
		n, done := len(pj.Steps), pj.done()
		count := fmt.Sprintf("%d / %d", done, n)
		apText(img, f.headline, head.Max.X-34-ui.TextWidth(f.headline, count), head.Min.Y+56, col, count)
		bar := image.Rect(head.Max.X-334, head.Min.Y+86, head.Max.X-34, head.Min.Y+96)
		ui.RoundRect(img, bar, 5, apCard2)
		if n > 0 && done > 0 {
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+bar.Dx()*done/n, bar.Max.Y), 5, col)
		}
		y += 140
		for _, st := range pj.Steps {
			r := image.Rect(mx+40, y, w-mx, y+84)
			ui.RoundRect(img, r, 20, apCard)
			cx, cy := r.Min.X+44, (r.Min.Y+r.Max.Y)/2
			ui.Circle(img, cx, cy, 20, col)
			if st.Done {
				iconCheck(img, cx-12, cy, rgb(0xffffff))
			} else {
				ui.Circle(img, cx, cy, 16, apCard)
			}
			tcol := apLabel
			if st.Done {
				tcol = apSecondary
			}
			apText(img, f.body, r.Min.X+86, cy+13, tcol, clip(f.body, st.Label, r.Dx()-120))
			btn("so:step:"+pj.ID+":"+st.ID, r)
			y += 94
		}
		y = c.somaAddRow(p, "step:"+pj.ID, "Add a step", y)
		y += 30
	}
	return y
}

// somaNotesView is the vault's notes, newest first, or the open note: its lines drawn as
// Markdown (headings, checkboxes to tick, bullets), with a line or a task to add at the end.
func (c *console) somaNotesView(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	tw := w - 2*mx
	su := &c.somaUI
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }

	if n := somaNoteByID(su.note); su.note != "" && n != nil {
		apText(img, f.body, mx, y+40, apBlue, "‹ All notes")
		btn("so:notes", image.Rect(0, y, mx+300, y+70))
		y += 90
		if n.folder() != "" {
			apText(img, f.caption, mx, y+30, apSecondary, n.folder())
			y += 44
		}
		inBlock := false
		for i, l := range strings.Split(n.Text, "\n") {
			if strings.Contains(l, "<!-- soma -->") {
				inBlock = true
				continue
			}
			if strings.Contains(l, "<!-- /soma -->") {
				inBlock = false
				continue
			}
			t := strings.TrimSpace(l)
			switch {
			case t == "":
				y += 24
			case strings.HasPrefix(t, "# "):
				y = drawParagraphs(img, f.serifLarge, strings.TrimPrefix(t, "# "), mx, y+20, tw, 80, y+400, apLabel) + 10
			case strings.HasPrefix(t, "## "), strings.HasPrefix(t, "### "):
				y = drawParagraphs(img, f.title, strings.TrimLeft(t, "# "), mx, y+16, tw, 56, y+300, apLabel) + 6
			default:
				if task, done, text := noteTask(l); task {
					r := image.Rect(mx, y, w-mx, y+84)
					col := apBlue
					if inBlock {
						col = apSecondary // SOMA's own list: it follows SOMA, not taps
					}
					cx, cy := r.Min.X+30, (r.Min.Y+r.Max.Y)/2
					ui.Circle(img, cx, cy, 20, col)
					if done {
						iconCheck(img, cx-12, cy, rgb(0xffffff))
					} else {
						ui.Circle(img, cx, cy, 16, apBG)
					}
					tcol := apLabel
					if done {
						tcol = apSecondary
					}
					apText(img, f.body, r.Min.X+74, cy+13, tcol, clip(f.body, text, r.Dx()-90))
					if !inBlock {
						btn(fmt.Sprintf("so:tick:%d", i), r)
					}
					y += 88
					continue
				}
				if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
					ui.Circle(img, mx+14, y+30, 6, apSecondary)
					y = drawParagraphs(img, f.body, t[2:], mx+40, y, tw-40, 48, y+600, apLabel)
				} else {
					y = drawParagraphs(img, f.body, strings.ReplaceAll(t, "**", ""), mx, y, tw, 48, y+900, apLabel)
				}
				y += 8
			}
			if y > p.img.Rect.Dy()-600 {
				apText(img, f.caption, mx, y+30, apSecondary, "The rest is on the PC: this note is long.")
				y += 60
				break
			}
		}
		y += 30
		y = c.somaAddRow(p, "notetask", "Add a task", y)
		y = c.somaAddRow(p, "noteline", "Add a line", y)
		return y
	}

	notes := somaNotes()
	y = somaSection(img, mx, y, "Notes", fmt.Sprintf("%d in the vault", len(notes)))
	y = c.somaAddRow(p, "newnote", "New note", y)
	y += 10
	if len(notes) == 0 {
		y = drawParagraphs(img, f.callout, "No notes yet. In Soma on the PC, open Settings > Notes vault and choose your Obsidian vault: its notes appear here, and what you write here goes back to it.", mx, y+20, tw, 44, y+400, apSecondary)
		return y
	}
	for _, n := range notes {
		r := image.Rect(mx, y, w-mx, y+120)
		ui.RoundRect(img, r, 22, apCard)
		apText(img, f.headline, r.Min.X+34, r.Min.Y+52, apLabel, clip(f.headline, n.title(), r.Dx()-300))
		when := time.UnixMilli(n.Mtime).In(displayZone)
		stamp := when.Format("2 Jan")
		if somaDate(when) == somaToday() {
			stamp = when.Format("15:04")
		}
		apText(img, f.caption, r.Max.X-34-ui.TextWidth(f.caption, stamp), r.Min.Y+52, apSecondary, stamp)
		preview := ""
		for _, l := range strings.Split(n.Text, "\n") {
			if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "<!--") {
				preview = strings.ReplaceAll(t, "**", "")
				break
			}
		}
		if n.folder() != "" {
			preview = n.folder() + "  ·  " + preview
		}
		apText(img, f.caption, r.Min.X+34, r.Min.Y+94, apSecondary, clip(f.caption, preview, r.Dx()-68))
		btn("so:note:"+n.ID, r)
		y += 132
		if y > p.img.Rect.Dy()-300 {
			break
		}
	}
	return y
}

// gradientRing is a Soma (Apple Watch) ring: a dim track, and the share reached drawn from
// one colour to the other, going round again past 100%.
func gradientRing(img *image.RGBA, cx, cy, rad, width int, frac float64, from, to uint32) {
	a, b := rgb(from), rgb(to)
	ring(img, cx, cy, rad, width, 1, blend(a, rgb(0x000000), 0.75), blend(a, rgb(0x000000), 0.75))
	end := min(frac, 1) * 360
	for d := 0.0; d < end; d += 1.0 {
		t := (d - 90) * math.Pi / 180
		col := blend(a, b, d/360)
		ui.Circle(img, cx+int(float64(rad)*math.Cos(t)), cy+int(float64(rad)*math.Sin(t)), width/2, col)
	}
}

// somaHomeToday is the top of Home, from Soma: the day's four rings, the habits, and today's
// to-dos to tick. Returns the y under it.
func (c *console) somaHomeToday(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	date := somaToday()
	apText(img, f.serifTitle, mx, y+90, apLabel, "Today")
	c.sleepPill(p, y+45)
	y += 120

	// The rings card.
	card := image.Rect(mx, y, w-mx, y+380)
	ui.RoundRect(img, card, 28, apCard)
	rings := somaRingsFor(date)
	cx, cy := card.Min.X+190, card.Min.Y+190
	for i, r := range rings {
		frac := 0.0
		if r.Goal > 0 {
			frac = r.Value / r.Goal
		}
		gradientRing(img, cx, cy, 150-i*40, 34, frac, r.From, r.To)
	}
	ly := card.Min.Y + 78
	for _, r := range rings {
		apText(img, f.caption, card.Min.X+420, ly, rgb(r.To), strings.ToUpper(r.Label))
		apText(img, f.headline, card.Min.X+420, ly+38, apLabel, fmt.Sprintf("%.0f / %.0f %s", r.Value, r.Goal, r.Unit))
		ly += 76
	}
	p.buttons = append(p.buttons, button{"tab:soma", card})
	y += 400

	// The habits, a small ring each: tap to tick.
	habits := somaHabits()
	for i := 0; i < len(habits); i += 5 {
		row := image.Rect(mx, y, w-mx, y+100)
		ui.RoundRect(img, row, 24, apCard)
		for k, hb := range habits[i:min(i+5, len(habits))] {
			x := row.Min.X + 50 + k*row.Dx()/5
			cy := (row.Min.Y + row.Max.Y) / 2
			col := somaColour(hb.Color, apBlue)
			frac := 0.0
			if hb.done(date) {
				frac = 1
			}
			ring(img, x, cy, 22, 10, frac, blend(col, rgb(0x000000), 0.7), col)
			apText(img, f.caption, x+38, cy+10, apLabel, clip(f.caption, hb.Name, row.Dx()/5-100))
			p.buttons = append(p.buttons, button{"so:habit:" + hb.ID, image.Rect(x-36, row.Min.Y, x+row.Dx()/5-50, row.Max.Y)})
		}
		y += 116
	}
	y += 4

	// Today's to-dos.
	var items []somaTodo
	for _, t := range somaTodos() {
		if t.Scope == "day" && t.active(date) {
			items = append(items, t)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].rank() < items[j].rank() })
	if len(items) > 5 {
		items = items[:5]
	}
	y = c.somaTodoRows(p, items, y)
	if len(items) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "Nothing to do today.")
		y += 60
	}
	return y
}

// somaHomeHeight is how much room somaHomeToday takes.
func somaHomeHeight() int {
	n := 0
	for _, t := range somaTodos() {
		if t.Scope == "day" && t.active(somaToday()) {
			n++
		}
	}
	return 120 + 400 + (len(somaHabits())+4)/5*116 + max(min(n, 5)*104, 60) + 30
}

// sleepPill is "Going to sleep" (or "I'm up" while asleep), at the right of a title.
func (c *console) sleepPill(p *page, cy int) {
	f := apple()
	label, id := "Going to sleep", "so:sleep"
	if somaAsleepSince() != 0 {
		label, id = "I'm up", "so:wake"
	}
	w := ui.TextWidth(f.headline, label) + 70
	r := image.Rect(c.s.W-48-w, cy-38, c.s.W-48, cy+38)
	ui.RoundRect(p.img, r, 38, rgb(0x2c2c54))
	apTextCenter(p.img, f.headline, (r.Min.X+r.Max.X)/2, cy, rgb(0xbf9fff), label)
	p.buttons = append(p.buttons, button{id, r})
}

// somaHabitRows draws the habits, in Morning and Evening groups when they have times: a tick
// (or - / + for an amount), the run of days, the last week, and the time to tap through.
func (c *console) somaHabitRows(p *page, habits []somaHabit, date string, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	grouped := false
	for _, h := range habits {
		grouped = grouped || h.when() != ""
	}
	groups := []struct{ when, title string }{{"", ""}}
	if grouped {
		groups = []struct{ when, title string }{{"morning", "Morning"}, {"evening", "Evening"}, {"", "Any Time"}}
	}
	week := days(date, 7)
	for _, g := range groups {
		first := true
		for _, h := range habits {
			if grouped && h.when() != g.when {
				continue
			}
			if first && g.title != "" {
				apText(img, f.captionBold, mx+8, y+34, apSecondary, strings.ToUpper(g.title))
				y += 50
			}
			first = false
			r := image.Rect(mx, y, w-mx, y+112)
			ui.RoundRect(img, r, 22, apCard)
			col := somaColour(h.Color, apBlue)
			cx, cy := r.Min.X+56, (r.Min.Y+r.Max.Y)/2
			ui.Circle(img, cx, cy, 26, col)
			if h.done(date) {
				iconCheck(img, cx-14, cy, rgb(0xffffff))
			} else {
				ui.Circle(img, cx, cy, 21, apCard)
			}
			apText(img, f.headline, r.Min.X+110, cy-4, apLabel, clip(f.headline, h.Name, r.Dx()-110-560))
			sub := ""
			if n := h.streak(date); n > 0 {
				sub = fmt.Sprintf("%d-day run", n)
			}
			whenLabel := map[string]string{"morning": "Morning", "evening": "Evening"}[h.when()]
			if whenLabel == "" {
				whenLabel = "Any time"
			}
			if sub != "" {
				sub += "  ·  "
			}
			apText(img, f.caption, r.Min.X+110, cy+34, apSecondary, sub+whenLabel)
			btn("so:when:"+h.ID, image.Rect(r.Min.X+100, cy+4, r.Min.X+110+ui.TextWidth(f.caption, sub+whenLabel)+20, r.Max.Y))
			if h.ramp() { // an amount: logged of today's, and - / +
				logged, rung, unit := h.amount(date)
				step := rampStep(unit)
				for i, b := range []struct {
					t string
					d float64
				}{{"−", -step}, {"+", step}} {
					br := image.Rect(r.Max.X-220+i*100, r.Min.Y+18, r.Max.X-130+i*100, r.Max.Y-18)
					ui.RoundRect(img, br, 30, apCard2)
					apTextCenter(img, f.title, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2, apBlue, b.t)
					btn(fmt.Sprintf("so:amt:%s:%g", h.ID, b.d), br)
				}
				label := fmt.Sprintf("%g / %g %s", logged, rung, unit)
				apText(img, f.callout, r.Max.X-250-ui.TextWidth(f.callout, label), cy+10, apSecondary, label)
			} else {
				for i, d := range week { // the last seven days
					dx := r.Max.X - 260 + i*36
					if h.done(d) {
						ui.Circle(img, dx, cy, 11, col)
					} else {
						ui.Circle(img, dx, cy, 11, apCard2)
					}
				}
				btn("so:habit:"+h.ID, image.Rect(r.Min.X, r.Min.Y, r.Min.X+100+ui.TextWidth(f.headline, h.Name)+40, cy+4))
				btn("so:habit:"+h.ID, image.Rect(r.Max.X-300, r.Min.Y, r.Max.X, r.Max.Y))
				btn("so:habit:"+h.ID, image.Rect(r.Min.X, cy+4, r.Min.X+100, r.Max.Y))
			}
			y += 126
		}
	}
	return y
}

// somaWeek is the calendar's week: a card a day with its plan, training and to-dos; tapping a
// day opens it below.
func (c *console) somaWeek(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
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
		ui.RoundRect(img, r, 22, bg)
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
