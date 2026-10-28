package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	"golang.org/x/image/font"

	"margin-init/fonts"
	"margin-init/ui"
)

// The Soma tab's Home, a copy of Soma's iPhone dashboard (DashboardView.tsx), at twice its
// CSS pixels: the header (the day, ‹ Today ›, the calendar and charts), the evening check-in,
// the rings, the tiles (Health, Screen Time, Sleep, Session, Today's score), the last seven
// days, Log the gap, To do, and the dock fixed at the bottom. Colours are Soma's (styles.css,
// glass.css): the page #0b0c10, cards #141720 with the glass sheen and a 9% white rim.

const sk = 2 // Soma's CSS pixels to the tablet's

var (
	smBG      = rgb(0x0b0c10)
	smSurface = rgb(0x141720)
	smSurf2   = rgb(0x1b1f2b)
	smSurf3   = rgb(0x232838)
	smFG      = rgb(0xf5f7fa)
	smMuted   = rgb(0x9aa4b4)
	smFaint   = rgb(0x6b7385)
	smPink    = rgb(0xe8304f)
	smPurple  = rgb(0x8b7cff)
)

func smFont(weight string, cssPx float64) font.Face {
	switch weight {
	case "bold":
		return textFace("inter-bold", fonts.InterBold, true, cssPx*sk)
	case "semi":
		return textFace("inter-semibold", fonts.InterSemiBold, true, cssPx*sk)
	}
	return textFace("inter", fonts.InterRegular, false, cssPx*sk)
}

// smCard is Soma's glass card: the surface, the sheen falling from the top (13% white to
// nothing by 70%), and the 9% white rim, rounded 16 CSS pixels (rounded-2xl).
func smCard(img *image.RGBA, r image.Rectangle, fill color.RGBA) {
	rad := 16 * sk
	ui.RoundRect(img, r, rad, blend(smBG, rgb(0xffffff), 0.09)) // the rim
	in := r.Inset(1)
	ui.RoundRect(img, in, rad-1, fill)
	for y := in.Min.Y; y < in.Max.Y; y++ {
		t := float64(y-in.Min.Y) / float64(in.Dy())
		// --glass-sheen-strong: 13% white at the top, 3% at 40%, nothing from 70%.
		a := 0.13 - 0.10*t/0.4
		if t > 0.4 {
			a = 0.03 * (1 - (t-0.4)/0.3)
		}
		if a <= 0 {
			break
		}
		glass(img, image.Rect(in.Min.X, y, in.Max.X, y+1).Intersect(roundedRow(in, rad, y)), 0, rgb(0xffffff), a)
	}
}

// roundedRow is the part of row y inside a rounded rectangle.
func roundedRow(r image.Rectangle, rad, y int) image.Rectangle {
	dy := 0
	if y < r.Min.Y+rad {
		dy = r.Min.Y + rad - y
	} else if y >= r.Max.Y-rad {
		dy = y - (r.Max.Y - rad - 1)
	}
	dx := 0
	if dy > 0 {
		dx = rad - int(math.Sqrt(float64(rad*rad-min(dy*dy, rad*rad))))
	}
	return image.Rect(r.Min.X+dx, y, r.Max.X-dx, y+1)
}

// smLabel is a tile's label: an icon colour bar or nothing, small caps, faint.
func smLabel(img *image.RGBA, x, y int, col color.RGBA, text string) {
	apText(img, smFont("bold", 10.5), x, y, col, strings.ToUpper(text))
}

// --- the day's score (day-score.ts, from what the tablet has) ---------------------------------

type scoreLine struct {
	label            string
	earned, possible float64
	tracked          bool
}

// somaDayScore is Soma's score for a day, from the parts the tablet can see: the workout
// (whole if one was saved), protein and calories against their goals, sleep against 8 h,
// creatine against 5 g, and habits done.
func somaDayScore(date string) (score int, lines []scoreLine) {
	n := somaNutrition(date)
	day := somaObj("nutrition/" + date)
	add := func(label string, possible, ratio float64, ok bool) {
		lines = append(lines, scoreLine{label, math.Round(min(max(ratio, 0), 1)*possible*10) / 10, possible, ok})
	}
	if s := somaTraining(date); s != nil {
		add("Workout", 40, 1, true)
	} else {
		add("Workout", 40, 0, false)
	}
	add("Protein", 18, n.P/max(n.GoalP, 1), n.Logged)
	add("Calories", 13, n.Cals/max(n.GoalCals, 1), n.Logged)
	sl, _ := day["sleep"].(obj)
	add("Sleep", 13, num(sl["hours"])/8, num(sl["hours"]) > 0)
	add("Creatine", 8, num(day["creatine"])/5, day != nil)
	hs := somaHabits()
	done := 0
	for _, h := range hs {
		if h.done(date) {
			done++
		}
	}
	add("Habits", 15, float64(done)/float64(max(len(hs), 1)), len(hs) > 0)
	earned, tracked := 0.0, 0.0
	for _, l := range lines {
		if l.tracked {
			earned += l.earned
			tracked += l.possible
		}
	}
	if tracked == 0 {
		return 0, lines
	}
	return int(math.Round(earned / tracked * 100)), lines
}

// --- the page ---------------------------------------------------------------------------------

const smDockH = 96 * sk // the dock's band, fixed at the bottom

// somaHome draws Soma's dashboard from y. Returns the y under it (the dock is the footer).
func (c *console) somaHome(p *page, y int) int {
	img := p.img
	w := c.s.W
	mx := 16 * sk
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	date := somaToday()
	now := time.Now().In(displayZone)

	// The page: Soma's near-black, warmed at the top.
	for yy := 0; yy < min(img.Rect.Dy(), 900); yy++ {
		ui.Fill(img, image.Rect(0, yy, w, yy+1), blend(rgb(0x1d0d18), smBG, float64(yy)/900))
	}
	ui.Fill(img, image.Rect(0, 900, w, img.Rect.Dy()), smBG)
	c.booksTabs(p, "tab:soma")

	// The header: the circle, ‹ Today ›, and the pill of the calendar, the charts, more.
	y += 10
	cb := image.Rect(mx, y, mx+44*sk, y+44*sk)
	ui.Circle(img, (cb.Min.X+cb.Max.X)/2, (cb.Min.Y+cb.Max.Y)/2, 22*sk, blend(smBG, rgb(0xffffff), 0.14))
	ui.Circle(img, (cb.Min.X+cb.Max.X)/2, (cb.Min.Y+cb.Max.Y)/2, 22*sk-2, rgb(0x24252c))
	for _, rr := range []int{19, 12, 5} {
		ui.Circle(img, (cb.Min.X+cb.Max.X)/2, (cb.Min.Y+cb.Max.Y)/2, rr, smFG)
		ui.Circle(img, (cb.Min.X+cb.Max.X)/2, (cb.Min.Y+cb.Max.Y)/2, rr-4, rgb(0x24252c))
	}
	btn("so:view:goals", cb)
	title := smFont("semi", 20)
	tx := cb.Max.X + 20*sk
	iconBack(img, tx, (cb.Min.Y+cb.Max.Y)/2, smMuted)
	label := "Today"
	apText(img, title, tx+36, (cb.Min.Y+cb.Max.Y)/2+14, smFG, label)
	iconChevronRight(img, tx+36+ui.TextWidth(title, label)+20, (cb.Min.Y+cb.Max.Y)/2, smMuted)
	pill := image.Rect(w-mx-172*sk, y, w-mx, y+44*sk)
	ui.RoundRect(img, pill, 22*sk, blend(smBG, rgb(0xffffff), 0.14))
	ui.RoundRect(img, pill.Inset(2), 22*sk-2, rgb(0x2a2026))
	pw := pill.Dx() / 3
	for i, id := range []string{"so:view:calendar", "so:view:reports", "so:view:today"} {
		cx, cy := pill.Min.X+i*pw+pw/2, (pill.Min.Y+pill.Max.Y)/2
		switch i {
		case 0: // the calendar
			r := image.Rect(cx-20, cy-17, cx+20, cy+19)
			ui.RoundRect(img, r, 7, smFG)
			ui.RoundRect(img, r.Inset(3), 5, rgb(0x2a2026))
			for gy := 0; gy < 3; gy++ {
				for gx := 0; gx < 4; gx++ {
					ui.Fill(img, image.Rect(cx-13+gx*7, cy-6+gy*7, cx-9+gx*7, cy-2+gy*7), smFG)
				}
			}
		case 1: // the chart
			line(img, cx-20, cy+18, cx+22, cy+18, 3, smFG)
			line(img, cx-20, cy-18, cx-20, cy+18, 3, smFG)
			line(img, cx-14, cy+6, cx-4, cy-6, 3, smFG)
			line(img, cx-4, cy-6, cx+6, cy+2, 3, smFG)
			line(img, cx+6, cy+2, cx+20, cy-14, 3, smFG)
		case 2: // more
			iconDots(img, cx, cy, smFG)
		}
		btn(id, image.Rect(pill.Min.X+i*pw, pill.Min.Y, pill.Min.X+(i+1)*pw, pill.Max.Y))
	}
	y = cb.Max.Y + 20*sk

	// The evening check-in: what's still open, from five in the afternoon.
	habits := somaHabits()
	open := 0
	for _, h := range habits {
		if !h.done(date) {
			open++
		}
	}
	for _, t := range somaTodos() {
		if t.Scope == "day" && t.active(date) && !t.Done {
			open++
		}
	}
	score, lines := somaDayScore(date)
	if now.Hour() >= 17 {
		r := image.Rect(mx, y, w-mx, y+64*sk)
		ui.RoundRect(img, r, 16*sk, blend(smBG, smPurple, 0.45))
		ui.RoundRect(img, r.Inset(2), 16*sk-2, blend(smBG, smPurple, 0.12))
		mc := image.Pt(r.Min.X+40*sk, (r.Min.Y+r.Max.Y)/2)
		ui.Circle(img, mc.X, mc.Y, 20*sk, blend(smBG, smPurple, 0.28))
		ui.Circle(img, mc.X, mc.Y, 14, smPurple) // the moon
		ui.Circle(img, mc.X+7, mc.Y-6, 13, blend(smBG, smPurple, 0.28))
		apText(img, smFont("bold", 15), r.Min.X+72*sk, r.Min.Y+28*sk, smFG, "Evening check-in")
		apText(img, smFont("reg", 12.5), r.Min.X+72*sk, r.Min.Y+48*sk, smMuted, fmt.Sprintf("%d things still open · about %d seconds", open, max(10, open+3)))
		sc := fmt.Sprint(score)
		apText(img, smFont("bold", 20), r.Max.X-24*sk-ui.TextWidth(smFont("bold", 20), sc), (r.Min.Y+r.Max.Y)/2+14, rgb(0xa99cff), sc)
		btn("so:view:today", r)
		y = r.Max.Y + 12*sk
	}

	// The rings.
	y = c.somaRingsCard(p, image.Rect(mx, y, w-mx, y), date) + 12*sk

	// The tiles, two columns.
	gap := 12 * sk
	colW := (w - 2*mx - gap) / 2
	left, right := mx, mx+colW+gap
	tile := func(x, y, h int) image.Rectangle {
		r := image.Rect(x, y, x+colW, y+h)
		smCard(img, r, smSurface)
		return r
	}
	th := 80 * sk
	// Apple Health: there's no Health on the tablet; what the phone sent comes through Soma.
	r := tile(left, y, th)
	smLabel(img, r.Min.X+12*sk, r.Min.Y+24*sk, smMuted, "♥ Apple Health")
	apText(img, smFont("reg", 13), r.Min.X+12*sk, r.Min.Y+62*sk, smFG, "Nothing from Health yet today")
	// Screen Time, tall.
	r = tile(right, y, 2*th+gap)
	smLabel(img, r.Min.X+12*sk, r.Min.Y+24*sk, smMuted, "▯ Screen Time")
	st := somaObj("screenTime/" + date)
	if m := num(st["total"]); m > 0 {
		apText(img, smFont("bold", 34), r.Min.X+12*sk, r.Min.Y+90*sk, smFG, fmt.Sprintf("%dh %02dm", int(m)/60, int(m)%60))
		apText(img, smFont("reg", 12), r.Min.X+12*sk, r.Min.Y+112*sk, smFaint, "on the phone today")
	} else {
		apText(img, smFont("reg", 13), r.Min.X+12*sk, (r.Min.Y+r.Max.Y)/2+10, smFG, "Not logged today")
	}
	// Sleep: last night, with the nights before as a line.
	r = tile(left, y+th+gap, th)
	smLabel(img, r.Min.X+12*sk, r.Min.Y+24*sk, smPurple, "☾ Sleep")
	var nights []float64
	for _, d := range days(date, 14) {
		o := somaObj("nutrition/" + d)
		sl, _ := o["sleep"].(obj)
		nights = append(nights, num(sl["hours"]))
	}
	if last := nights[len(nights)-1]; last > 0 {
		v := fmt.Sprintf("%.1f", last)
		big := smFont("bold", 28)
		apText(img, big, r.Max.X-28*sk-ui.TextWidth(big, v), r.Min.Y+52*sk, smFG, v)
		apText(img, smFont("bold", 12), r.Max.X-24*sk, r.Min.Y+52*sk, smFaint, "h")
	} else {
		apText(img, smFont("reg", 13), r.Min.X+12*sk, r.Min.Y+52*sk, smFG, "No night logged")
	}
	gy0, gy1 := r.Min.Y+58*sk, r.Max.Y-6*sk
	for i := 1; i < len(nights); i++ {
		if nights[i-1] == 0 || nights[i] == 0 {
			continue
		}
		x0 := r.Min.X + 6*sk + (i-1)*(r.Dx()-12*sk)/(len(nights)-1)
		x1 := r.Min.X + 6*sk + i*(r.Dx()-12*sk)/(len(nights)-1)
		yOf := func(h float64) int { return gy1 - int(float64(gy1-gy0)*min(h/10, 1)) }
		line(img, x0, yOf(nights[i-1]), x1, yOf(nights[i]), 4, smPurple)
	}
	y += 2*th + 2*gap

	// Session and Today's score.
	r = tile(left, y, th)
	smLabel(img, r.Min.X+12*sk, r.Min.Y+24*sk, rgb(0xbf5af2), "⚑ Session")
	if s := somaTraining(date); s != nil {
		n := fmt.Sprint(max(s.Sets, 1))
		big := smFont("bold", 30)
		apText(img, big, r.Min.X+12*sk, r.Min.Y+56*sk, smFG, n)
		apText(img, smFont("bold", 13), r.Min.X+16*sk+ui.TextWidth(big, n), r.Min.Y+56*sk, smFaint, "sets")
		apText(img, smFont("reg", 11), r.Min.X+12*sk, r.Min.Y+72*sk, smFaint, s.Split+" · done today")
	} else {
		apText(img, smFont("reg", 13), r.Min.X+12*sk, r.Min.Y+56*sk, smFG, "No session yet")
	}
	r = tile(right, y, th)
	ring(img, r.Min.X+42*sk, (r.Min.Y+r.Max.Y)/2, 28*sk, 6*sk, float64(score)/100, rgb(0x3a3420), rgb(0xffd60a))
	apTextCenter(img, smFont("bold", 15), r.Min.X+42*sk, (r.Min.Y+r.Max.Y)/2, rgb(0xffd60a), fmt.Sprint(score))
	ui.RoundRect(img, image.Rect(r.Min.X+84*sk, r.Min.Y+16*sk, r.Min.X+84*sk+6, r.Min.Y+28*sk), 3, rgb(0xffd60a))
	smLabel(img, r.Min.X+90*sk, r.Min.Y+27*sk, smMuted, "Today's score")
	counted := 0
	for _, l := range lines {
		if l.tracked {
			counted++
		}
	}
	apText(img, smFont("bold", 13), r.Min.X+84*sk, r.Min.Y+50*sk, smFG, "out of 100 · "+fmt.Sprint(counted))
	apText(img, smFont("bold", 13), r.Min.X+84*sk, r.Min.Y+68*sk, smFG, "counted")
	btn("so:view:reports", r)
	y += th + gap

	// The last seven days: the average, and what pulled it down.
	type pull struct {
		label string
		lost  float64
		days  int
	}
	pulls := map[string]*pull{}
	total, n := 0, 0
	for _, d := range days(date, 7) {
		s, ls := somaDayScore(d)
		if s > 0 {
			total += s
			n++
		}
		for _, l := range ls {
			if l.tracked && l.earned < l.possible {
				p := pulls[l.label]
				if p == nil {
					p = &pull{label: l.label}
					pulls[l.label] = p
				}
				p.lost += l.possible - l.earned
				p.days++
			}
		}
	}
	var pl []*pull
	for _, p := range pulls {
		pl = append(pl, p)
	}
	sort.Slice(pl, func(i, j int) bool { return pl[i].lost > pl[j].lost })
	pl = pl[:min(len(pl), 4)]
	r = image.Rect(mx, y, w-mx, y+(84+len(pl)*44)*sk)
	smCard(img, r, smSurface)
	apText(img, smFont("bold", 17), r.Min.X+16*sk, r.Min.Y+36*sk, smFG, "The last 7 days")
	if n > 0 {
		avg := fmt.Sprint(total / n)
		big := smFont("bold", 30)
		apText(img, big, r.Max.X-50*sk-ui.TextWidth(big, avg), r.Min.Y+40*sk, smFG, avg)
		apText(img, smFont("bold", 12), r.Max.X-44*sk, r.Min.Y+40*sk, smFaint, "avg")
	}
	smLabel(img, r.Min.X+16*sk, r.Min.Y+68*sk, smMuted, "What pulled it down")
	ly := r.Min.Y + 84*sk
	for _, p := range pl {
		lr := image.Rect(r.Min.X+16*sk, ly, r.Max.X-16*sk, ly+36*sk)
		smCard(img, lr, smSurf2)
		apText(img, smFont("semi", 14), lr.Min.X+14*sk, lr.Min.Y+24*sk, smFG, fmt.Sprintf("%s missed %d day%s", strings.ToLower(p.label), p.days, map[bool]string{true: "", false: "s"}[p.days == 1]))
		pts := fmt.Sprintf("-%.0f pts", p.lost)
		apText(img, smFont("bold", 13), lr.Max.X-14*sk-ui.TextWidth(smFont("bold", 13), pts), lr.Min.Y+24*sk, rgb(0xff9f0a), pts)
		ly += 44 * sk
	}
	y = r.Max.Y + 12*sk

	// Log the gap: sleep, creatine, the habits left.
	y = c.smLogTheGap(p, image.Rect(mx, y, w-mx, y), date, habits) + 12*sk

	// To do.
	y = c.smTodo(p, image.Rect(mx, y, w-mx, y), date) + 24*sk
	return y
}

// smLogTheGap is Soma's LogTheGap card. Returns the y under it.
func (c *console) smLogTheGap(p *page, at image.Rectangle, date string, habits []somaHabit) int {
	img := p.img
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	var left []somaHabit
	for _, h := range habits {
		if !h.done(date) && !h.ramp() {
			left = append(left, h)
		}
	}
	rowH := 64 * sk
	chipRows := (len(left) + 3) / 4
	r := image.Rect(at.Min.X, at.Min.Y, at.Max.X, at.Min.Y+(60+2*76+48+chipRows*44)*sk)
	smCard(img, r, smSurface)
	apText(img, smFont("bold", 17), r.Min.X+20*sk, r.Min.Y+38*sk, smFG, "Log the gap")
	y := r.Min.Y + 60*sk
	row := func(h int) image.Rectangle {
		rr := image.Rect(r.Min.X+20*sk, y, r.Max.X-20*sk, y+h)
		ui.RoundRect(img, rr, h/2, blend(smBG, rgb(0xffffff), 0.1))
		ui.RoundRect(img, rr.Inset(1), h/2-1, smSurf2)
		return rr
	}
	// Sleep: last night's hours, typed, then Log.
	rr := row(rowH)
	cy := (rr.Min.Y + rr.Max.Y) / 2
	ui.Circle(img, rr.Min.X+28*sk, cy, 10*sk, smPurple)
	ui.Circle(img, rr.Min.X+28*sk+7, cy-6, 9*sk, smSurf2)
	apText(img, smFont("bold", 16), rr.Min.X+50*sk, cy+11, smFG, "Sleep")
	field := image.Rect(rr.Max.X-196*sk, rr.Min.Y+10*sk, rr.Max.X-96*sk, rr.Max.Y-10*sk)
	ui.RoundRect(img, field, field.Dy()/2, blend(smBG, rgb(0xffffff), 0.1))
	ui.RoundRect(img, field.Inset(1), field.Dy()/2-1, smSurface)
	text, col := "7.5", smFaint
	if c.somaUI.typing && c.somaUI.field == "sleep" {
		text, col = withCursor(c.somaUI.editing, c.fieldBack), smFG
	}
	apTextCenter(img, smFont("reg", 18), (field.Min.X+field.Max.X)/2, cy, col, text)
	btn("so:type:sleep", field)
	apText(img, smFont("reg", 14), field.Max.X+8*sk, cy+10, smFaint, "h")
	lg := image.Rect(rr.Max.X-66*sk, rr.Min.Y+10*sk, rr.Max.X-8*sk, rr.Max.Y-10*sk)
	ui.RoundRect(img, lg, lg.Dy()/2, smSurf3)
	apTextCenter(img, smFont("bold", 14), (lg.Min.X+lg.Max.X)/2, cy, smMuted, "Log")
	btn("so:logsleep", lg)
	y += rowH + 12*sk

	// Creatine: +5 g.
	rr = row(rowH)
	cy = (rr.Min.Y + rr.Max.Y) / 2
	line(img, rr.Min.X+20*sk, cy+8, rr.Min.X+34*sk, cy-8, 10, smFaint)
	apText(img, smFont("bold", 16), rr.Min.X+50*sk, cy+11, smFG, "Creatine")
	pb := image.Rect(rr.Max.X-74*sk, rr.Min.Y+8*sk, rr.Max.X-8*sk, rr.Max.Y-8*sk)
	ui.RoundRect(img, pb, pb.Dy()/2, smPink)
	apTextCenter(img, smFont("bold", 15), (pb.Min.X+pb.Max.X)/2, cy, rgb(0xffffff), "+5g")
	btn("so:creatine", pb)
	y += rowH + 12*sk

	// The habits left, as chips to tick.
	rr = row(r.Max.Y - 20*sk - y)
	iconCheck(img, rr.Min.X+20*sk, rr.Min.Y+28*sk, smFaint)
	apText(img, smFont("bold", 16), rr.Min.X+50*sk, rr.Min.Y+36*sk, smFG, fmt.Sprintf("%d habits left", len(left)))
	cx, cyy := rr.Min.X+50*sk, rr.Min.Y+52*sk
	chip := smFont("bold", 13)
	for _, h := range left {
		cw := ui.TextWidth(chip, h.Name) + 32*sk
		if cx+cw > rr.Max.X-12*sk {
			cx, cyy = rr.Min.X+50*sk, cyy+44*sk
		}
		cr := image.Rect(cx, cyy, cx+cw, cyy+36*sk)
		ui.RoundRect(img, cr, cr.Dy()/2, blend(smBG, rgb(0xffffff), 0.12))
		ui.RoundRect(img, cr.Inset(1), cr.Dy()/2-1, smSurf2)
		apTextCenter(img, chip, (cr.Min.X+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2, smFG, h.Name)
		btn("so:habit:"+h.ID, cr)
		cx += cw + 8*sk
	}
	return r.Max.Y
}

// smTodo is Soma's TodoCard: Today or This week, the list, and the field to add. Returns the
// y under it.
func (c *console) smTodo(p *page, at image.Rectangle, date string) int {
	img := p.img
	su := &c.somaUI
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	scope := "day"
	if su.todoWeek {
		scope = "week"
	}
	var items []somaTodo
	for _, t := range somaTodos() {
		if t.Scope == scope && t.active(date) {
			items = append(items, t)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].rank() < items[j].rank() })
	rowH := 52 * sk
	listH := 40 * sk
	if len(items) > 0 {
		listH = len(items)*rowH + 8*sk
	}
	r := image.Rect(at.Min.X, at.Min.Y, at.Max.X, at.Min.Y+(56+60+16+52+20)*sk+listH)
	smCard(img, r, smSurface)
	apText(img, smFont("bold", 17), r.Min.X+20*sk, r.Min.Y+38*sk, smFG, "To do")
	hist := smFont("bold", 14)
	apText(img, hist, r.Max.X-20*sk-ui.TextWidth(hist, "History"), r.Min.Y+38*sk, smFaint, "History")
	btn("so:view:calendar", image.Rect(r.Max.X-130*sk, r.Min.Y, r.Max.X, r.Min.Y+56*sk))
	// Today | This week.
	seg := image.Rect(r.Min.X+20*sk, r.Min.Y+56*sk, r.Max.X-20*sk, r.Min.Y+116*sk)
	ui.RoundRect(img, seg, seg.Dy()/2, blend(smBG, rgb(0xffffff), 0.1))
	ui.RoundRect(img, seg.Inset(1), seg.Dy()/2-1, rgb(0x15171f))
	half := seg.Dx() / 2
	for i, t := range []string{"Today", "This week"} {
		sr := image.Rect(seg.Min.X+i*half, seg.Min.Y, seg.Min.X+(i+1)*half, seg.Max.Y)
		on := (i == 1) == su.todoWeek
		if on {
			ui.RoundRect(img, sr.Inset(6*sk), (sr.Dy()-12*sk)/2, smPink)
		}
		col := smMuted
		if on {
			col = rgb(0xffffff)
		}
		apTextCenter(img, smFont("bold", 14), (sr.Min.X+sr.Max.X)/2, (sr.Min.Y+sr.Max.Y)/2, col, t)
		btn([]string{"so:todoscope:day", "so:todoscope:week"}[i], sr)
	}
	y := seg.Max.Y + 16*sk
	if len(items) == 0 {
		msg := "Nothing on for today. Check History for what came before."
		if su.todoWeek {
			msg = "Nothing on for this week."
		}
		apTextCenter(img, smFont("reg", 13), (r.Min.X+r.Max.X)/2, y+14*sk, smFaint, msg)
	}
	for _, t := range items {
		cx, cy := r.Min.X+34*sk, y+rowH/2
		ui.Circle(img, cx, cy, 11*sk, smPink)
		if t.Done {
			iconCheck(img, cx-12, cy, rgb(0xffffff))
		} else {
			ui.Circle(img, cx, cy, 11*sk-4, smSurface)
		}
		col := smFG
		if t.Done {
			col = smFaint
		}
		apText(img, smFont("semi", 15), cx+24*sk, cy+11, col, clip(smFont("semi", 15), t.Text, r.Dx()-110*sk))
		if t.Priority == "high" {
			apText(img, smFont("bold", 15), r.Max.X-30*sk, cy+11, smPink, "!")
		}
		btn("so:todo:"+t.ID, image.Rect(r.Min.X, y, r.Max.X, y+rowH))
		y += rowH
	}
	y = r.Max.Y - 72*sk
	// The field and +.
	f := image.Rect(r.Min.X+20*sk, y, r.Max.X-20*sk-62*sk, y+52*sk)
	ui.RoundRect(img, f, f.Dy()/2, blend(smBG, rgb(0xffffff), 0.1))
	ui.RoundRect(img, f.Inset(1), f.Dy()/2-1, smSurf2)
	field := "todo"
	if su.todoWeek {
		field = "week"
	}
	text, col := "Something to do today", smFaint
	if su.todoWeek {
		text = "Something for this week"
	}
	if su.typing && su.field == field {
		text, col = withCursor(su.editing, c.fieldBack), smFG
	}
	apText(img, smFont("reg", 18), f.Min.X+16*sk, (f.Min.Y+f.Max.Y)/2+12, col, clip(smFont("reg", 18), text, f.Dx()-32*sk))
	btn("so:type:"+field, f)
	pl := image.Rect(f.Max.X+10*sk, y, f.Max.X+62*sk, y+52*sk)
	ui.Circle(img, (pl.Min.X+pl.Max.X)/2, (pl.Min.Y+pl.Max.Y)/2, 26*sk, blend(smBG, rgb(0xffffff), 0.12))
	ui.Circle(img, (pl.Min.X+pl.Max.X)/2, (pl.Min.Y+pl.Max.Y)/2, 26*sk-2, smSurf2)
	pcx, pcy := (pl.Min.X+pl.Max.X)/2, (pl.Min.Y+pl.Max.Y)/2
	line(img, pcx-14, pcy, pcx+14, pcy, 4, smFG)
	line(img, pcx, pcy-14, pcx, pcy+14, 4, smFG)
	btn("so:type:"+field, pl)
	return r.Max.Y
}

// smDock is Soma's dock, drawn in the page's last smDockH rows (the fixed footer).
func (c *console) smDock(p *page) {
	img := p.img
	w := c.s.W
	y0 := img.Rect.Dy() - smDockH
	// What's under the dock fades to the page's black.
	for yy := y0; yy < img.Rect.Dy(); yy++ {
		ui.Fill(img, image.Rect(0, yy, w, yy+1), smBG)
	}
	bar := image.Rect(16*sk, y0+12*sk, w-16*sk, y0+12*sk+70*sk)
	ui.RoundRect(img, bar, bar.Dy()/2, blend(smBG, rgb(0xffffff), 0.16))
	ui.RoundRect(img, bar.Inset(2), bar.Dy()/2-2, rgb(0x1a1d27))
	items := []struct{ id, label, view string }{
		{"so:view:notes", "Mind", "notes"}, {"so:view:goals", "Projects", "goals"}, {"so:view:money", "Money", "money"},
		{"so:view:home", "Home", "home"}, {"so:view:reports", "Train", "reports"}, {"so:view:today", "Fuel", "today"},
	}
	iw := bar.Dx() / len(items)
	for i, it := range items {
		r := image.Rect(bar.Min.X+i*iw, bar.Min.Y, bar.Min.X+(i+1)*iw, bar.Max.Y)
		on := c.somaUI.view == it.view
		col := smFG
		if on {
			ui.RoundRect(img, r.Inset(6*sk), 22*sk, blend(rgb(0x1a1d27), smPink, 0.35))
			col = rgb(0xff5d7a)
		}
		cx, cy := (r.Min.X+r.Max.X)/2, r.Min.Y+26*sk
		smDockIcon(img, it.label, cx, cy, col)
		apTextCenter(img, smFont("semi", 12), cx, r.Min.Y+54*sk, col, it.label)
		p.buttons = append(p.buttons, button{it.id, r})
	}
	p.footer = smDockH
}

// smDockIcon draws a dock item's icon, after the lucide icons Soma uses.
func smDockIcon(img *image.RGBA, name string, cx, cy int, col color.RGBA) {
	s := 4
	switch name {
	case "Mind": // a head
		ring(img, cx, cy, 16, 4, 0.8, col, col)
		line(img, cx-4, cy-6, cx+6, cy+4, s, col)
	case "Projects": // a folder
		r := image.Rect(cx-20, cy-14, cx+20, cy+16)
		ui.RoundRect(img, r, 5, col)
		ui.RoundRect(img, r.Inset(4), 3, rgb(0x1a1d27))
		ui.Fill(img, image.Rect(cx-20, cy-18, cx-4, cy-12), col)
	case "Money": // a wallet
		r := image.Rect(cx-20, cy-14, cx+20, cy+16)
		ui.RoundRect(img, r, 6, col)
		ui.RoundRect(img, r.Inset(4), 4, rgb(0x1a1d27))
		ui.Fill(img, image.Rect(cx+6, cy-2, cx+20, cy+6), col)
	case "Home": // four squares
		for _, d := range [][2]int{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}} {
			r := image.Rect(cx+d[0]*11-8, cy+d[1]*11-8, cx+d[0]*11+8, cy+d[1]*11+8)
			ui.RoundRect(img, r, 4, col)
			ui.RoundRect(img, r.Inset(3), 2, rgb(0x2c2030))
		}
	case "Train": // a dumbbell
		line(img, cx-16, cy+12, cx+16, cy-12, s, col)
		line(img, cx-22, cy+6, cx-10, cy+18, s+2, col)
		line(img, cx+10, cy-18, cx+22, cy-6, s+2, col)
	case "Fuel": // a fork and knife
		line(img, cx-10, cy-16, cx-10, cy+18, s, col)
		line(img, cx-16, cy-16, cx-16, cy-4, s, col)
		line(img, cx-4, cy-16, cx-4, cy-4, s, col)
		line(img, cx+10, cy-16, cx+10, cy+18, s, col)
		line(img, cx+10, cy-16, cx+16, cy, s, col)
	}
}
