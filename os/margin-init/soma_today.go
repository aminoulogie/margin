package main

import (
	"fmt"
	"image"
	"margin-init/ui"
	"math"
	"sort"
	"strings"
	"time"
)

func (c *console) somaToday(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
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

	// Food, water and training, logged here.
	y += 20
	n := somaNutrition(date)
	y = somaSection(img, mx, y, "Food", fmt.Sprintf("%.0f kcal  ·  %.0f g protein  ·  %.0f ml water", n.Cals, n.P, n.Water))
	for i, ml := range []int{250, 500} {
		r := image.Rect(mx+i*230, y, mx+i*230+210, y+84)
		ui.RoundRect(img, r, 42, apCard2)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, rgb(0x00d8ff), fmt.Sprintf("+%d ml", ml))
		btn(fmt.Sprintf("so:water:%d", ml), r)
	}
	y += 104
	y = c.somaAddRow(p, "food", "Add food: name, kcal, protein (Wrap 450 35)", y)
	if s := somaTraining(date); s != nil {
		apText(img, f.callout, mx, y+40, apSecondary, fmt.Sprintf("Trained today: %s, %s", s.Split, s.Duration))
		y += 70
	} else {
		y = c.somaAddRow(p, "workout", "Log a workout: split, minutes (Push 60)", y)
	}

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
		ui.RoundRect(img, r, radiusCard, rgb(0x2c2c54))
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
	w, mx := c.s.W, pageGutter
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
	w, mx := c.s.W, pageGutter
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
			ui.RoundRect(img, r, radiusCard, apCard)
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
			apText(img, f.caption, r.Min.X+110, cy+34, apSecondary, sub)
			// The time of day, as a chip to tap through: Any time, Morning, Evening.
			wx := r.Min.X + 110 + ui.TextWidth(f.caption, sub)
			chip := image.Rect(wx, cy+6, wx+ui.TextWidth(f.caption, whenLabel)+32, cy+46)
			ui.RoundRect(img, chip, 20, apCard2)
			apText(img, f.caption, chip.Min.X+16, cy+34, apLabel, whenLabel)
			btn("so:when:"+h.ID, chip.Inset(-8))
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
