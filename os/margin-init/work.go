package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"golang.org/x/image/font"

	"margin-init/fonts"
	"margin-init/ui"
)

// The Work tab: Soma's workstation (src/workstation) on the tablet, in the look of its
// tablet screens: a rail on the left (Overview, Projects, Planner, Time, Money, Reports,
// Findings, Work log, Personal), and each page with its breadcrumb, title, tabs, chips, cards
// and charts. Measurements are the tablet's pixels from those screens; y counts from the top
// of the screen, the status bar included (wk.R and wk.Y take it off).

var (
	wkBG      = rgb(0x0b0c10)
	wkSide    = rgb(0x111216)
	wkSideSel = rgb(0x1b1d29)
	wkCard    = rgb(0x15161d)
	wkCardTop = rgb(0x1a1c24)
	wkRim     = rgb(0x22232b)
	wkLine    = rgb(0x1f2027)
	wkFG      = rgb(0xeef0f3)
	wkText2   = rgb(0xc4c7ce)
	wkMuted   = rgb(0x9a9da6)
	wkFaint   = rgb(0x676a74)
	wkLime    = rgb(0xd3fd50)
	wkInk     = rgb(0x0b0c10)
	wkBlue    = rgb(0x5b8cff)
	wkGreen   = rgb(0x3ecf8e)
	wkAmber   = rgb(0xf0a63c)
	wkRed     = rgb(0xf06a6a)
	wkPurple  = rgb(0xa98bfa)
	wkYellow  = rgb(0xe3f25a)
)

const (
	wkSideW = 124  // the rail
	wkX0    = 160  // the page's left edge
	wkX1    = 1164 // and right
)

func wkFont(weight string, px float64) font.Face {
	switch weight {
	case "bold":
		return textFace("inter-bold", fonts.InterBold, true, px)
	case "semi":
		return textFace("inter-semibold", fonts.InterSemiBold, true, px)
	case "medium":
		return textFace("inter-medium", fonts.InterMedium, false, px)
	}
	return textFace("inter", fonts.InterRegular, false, px)
}

type wk struct {
	c   *console
	p   *page
	img *image.RGBA
	oy  int // the status bar's height: screen y to page y
}

func (k *wk) Y(y int) int                          { return y - k.oy }
func (k *wk) R(x0, y0, x1, y1 int) image.Rectangle { return image.Rect(x0, y0-k.oy, x1, y1-k.oy) }

func (k *wk) btn(id string, r image.Rectangle) {
	k.p.buttons = append(k.p.buttons, button{id, r})
}

func (k *wk) text(f font.Face, x, y int, col color.RGBA, s string) int {
	apText(k.img, f, x, k.Y(y), col, s)
	return x + ui.TextWidth(f, s)
}

func (k *wk) textR(f font.Face, right, y int, col color.RGBA, s string) {
	apTextRight(k.img, f, right, k.Y(y), col, s)
}

func (k *wk) textC(f font.Face, cx, y int, col color.RGBA, s string) {
	apText(k.img, f, cx-ui.TextWidth(f, s)/2, k.Y(y), col, s)
}

func (k *wk) icon(name string, cx, cy, size int, col color.RGBA) {
	lucideIcon(k.img, name, cx, k.Y(cy), size, col)
}

// card is a panel: a hairline rim, the surface, a little lighter at the top.
func (k *wk) card(r image.Rectangle) {
	k.cardIn(r, wkCard)
}

func (k *wk) cardIn(r image.Rectangle, fill color.RGBA) {
	r = r.Sub(image.Pt(0, k.oy))
	ui.RoundRect(k.img, r, 20, wkRim)
	in := r.Inset(2)
	ui.RoundRect(k.img, in, 18, fill)
	// The sheen: on a flat fill each row is one colour, so the row's middle is a fill and
	// only its anti-aliased ends are blended (blending every pixel cost a tenth of a page).
	top := blend(fill, rgb(0xffffff), 0.035)
	for y := in.Min.Y; y < in.Min.Y+min(120, in.Dy()); y++ {
		if y < k.img.Rect.Min.Y || y >= k.img.Rect.Max.Y {
			continue
		}
		t := 1 - float64(y-in.Min.Y)/120
		row := roundedRow(in, 18, y)
		if row.Dx() <= 6 {
			glass(k.img, row, 0, top, t)
			continue
		}
		mid := image.Rect(row.Min.X+3, y, row.Max.X-3, y+1)
		ui.Fill(k.img, mid, blend(fill, top, t))
		glass(k.img, image.Rect(row.Min.X, y, mid.Min.X, y+1), 0, top, t)
		glass(k.img, image.Rect(mid.Max.X, y, row.Max.X, y+1), 0, top, t)
	}
}

// wkGlowCache is the header's haze drawn once over the page colour: each page copies it.
var wkGlowCache struct {
	key [3]any
	img *image.RGBA
}

// glow is the lime haze behind a page's header.
func (k *wk) glow() {
	cx, cy, rx, ry := 700.0, 110.0, 560.0, 230.0
	y0, y1 := max(k.Y(0), 0), min(k.Y(360), k.img.Rect.Dy())
	w := k.c.s.W
	if y1 <= y0 || w <= wkSideW {
		return
	}
	key := [3]any{k.oy, wkLime, wkBG}
	g := wkGlowCache.img
	if g == nil || wkGlowCache.key != key || g.Rect.Dx() != w-wkSideW || g.Rect.Dy() != y1-y0 {
		g = image.NewRGBA(image.Rect(0, 0, w-wkSideW, y1-y0))
		ui.Fill(g, g.Rect, wkBG)
		for y := y0; y < y1; y++ {
			for x := wkSideW; x < w; x++ {
				d := math.Hypot((float64(x)-cx)/rx, (float64(y+k.oy)-cy)/ry)
				if d >= 1 {
					continue
				}
				a := 0.07 * (1 - d) * (1 - d)
				o := g.PixOffset(x-wkSideW, y-y0)
				g.Pix[o] = uint8(float64(g.Pix[o])*(1-a) + float64(wkLime.R)*a)
				g.Pix[o+1] = uint8(float64(g.Pix[o+1])*(1-a) + float64(wkLime.G)*a)
				g.Pix[o+2] = uint8(float64(g.Pix[o+2])*(1-a) + float64(wkLime.B)*a)
			}
		}
		wkGlowCache.key, wkGlowCache.img = key, g
	}
	n := 4 * g.Rect.Dx()
	for y := y0; y < y1; y++ {
		copy(k.img.Pix[k.img.PixOffset(wkSideW, y):][:n], g.Pix[(y-y0)*g.Stride:][:n])
	}
}

// header draws the breadcrumb, the title and the line under it. Returns nothing: the tabs
// follow at a fixed place.
func (k *wk) header(crumb []string, title, sub string, titleDot color.RGBA) {
	x := wkX0
	k.icon("building-2", x+10, 83, 24, wkMuted)
	x += 32
	for i, c := range crumb {
		col := wkMuted
		if i == len(crumb)-1 {
			col = wkText2
		}
		x = k.text(wkFont("medium", 20), x, 91, col, c)
		if i < len(crumb)-1 {
			k.icon("chevron-right", x+20, 84, 20, wkMuted)
			x += 40
		}
	}
	tx := wkX0
	if titleDot.A > 0 {
		ui.RoundRect(k.img, k.R(tx, 120, tx+22, 142), 5, titleDot)
		tx += 34
	}
	k.text(wkFont("bold", 42), tx, 152, wkFG, title)
	k.text(wkFont("regular", 20), wkX0, 190, wkMuted, sub)
}

// primary is a lime button at the header's right; returns its left edge.
func (k *wk) primary(id, icon, label string, right int) int {
	f := wkFont("semi", 21)
	w := 64 + ui.TextWidth(f, label) + 28
	r := k.R(right-w, 103, right, 166)
	ui.RoundRect(k.img, r, 14, wkLime)
	lucideIcon(k.img, icon, r.Min.X+40, (r.Min.Y+r.Max.Y)/2, 26, wkOnLime)
	apText(k.img, f, r.Min.X+64, (r.Min.Y+r.Max.Y)/2+8, wkOnLime, label)
	k.btn(id, r)
	return r.Min.X
}

// square is a dark icon button at the header's right; returns its left edge.
func (k *wk) square(id, icon string, right int) int {
	r := k.R(right-64, 103, right, 166)
	ui.RoundRect(k.img, r, 14, wkRim)
	ui.RoundRect(k.img, r.Inset(2), 12, rgb(0x16171e))
	lucideIcon(k.img, icon, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, 28, wkFG)
	k.btn(id, r)
	return r.Min.X
}

// tabs are the underlined tabs under the header; n is a count shown after a label ("" none).
func (k *wk) tabs(prefix string, labels []string, counts []string, sel int) {
	x := wkX0
	f := wkFont("semi", 22)
	ui.Fill(k.img, k.R(wkX0, 286, wkX1, 288), wkLine)
	for i, l := range labels {
		w := ui.TextWidth(f, l)
		if i < len(counts) && counts[i] != "" {
			w += 8 + ui.TextWidth(f, counts[i])
		}
		col := wkText2
		if i == sel {
			col = wkFG
			ui.RoundRect(k.img, k.R(x, 284, x+w+40, 289), 2, wkLime)
		}
		k.text(f, x+20, 260, col, l)
		if i < len(counts) && counts[i] != "" {
			k.text(f, x+20+ui.TextWidth(f, l)+8, 260, col, counts[i])
		}
		k.btn(fmt.Sprintf("%s:%d", prefix, i), k.R(x, 220, x+w+40, 290))
		x += w + 46
	}
}

// chips are the filters under the tabs: the chosen one lime. Returns their right edge.
func (k *wk) chips(prefix string, y int, labels []string, icons []string, sel int) int {
	x := wkX0
	f := wkFont("medium", 21)
	for i, l := range labels {
		w := ui.TextWidth(f, l) + 40
		ic := ""
		if i < len(icons) {
			ic = icons[i]
		}
		if ic != "" {
			w += 34
		}
		r := k.R(x, y, x+w, y+53)
		fg := wkText2
		if i == sel {
			ui.RoundRect(k.img, r, 12, wkLime)
			fg = wkOnLime
		} else {
			ui.RoundRect(k.img, r, 12, wkRim)
			ui.RoundRect(k.img, r.Inset(2), 10, rgb(0x15161c))
		}
		tx := r.Min.X + 20
		if ic != "" {
			lucideIcon(k.img, ic, tx+12, (r.Min.Y+r.Max.Y)/2, 24, fg)
			tx += 34
		}
		face := f
		if i == sel {
			face = wkFont("semi", 21)
		}
		apText(k.img, face, tx, (r.Min.Y+r.Max.Y)/2+8, fg, l)
		k.btn(fmt.Sprintf("%s:%d", prefix, i), r)
		x += w + 13
	}
	return x
}

// outline is a dark outlined button ending at right; returns its left edge.
func (k *wk) outline(id, icon, label string, right, y, h int) int {
	f := wkFont("medium", 21)
	w := ui.TextWidth(f, label) + 40
	if icon != "" {
		w += 34
	}
	if label == "" {
		w = h
	}
	r := k.R(right-w, y, right, y+h)
	ui.RoundRect(k.img, r, 12, wkRim)
	ui.RoundRect(k.img, r.Inset(2), 10, rgb(0x15161c))
	tx := r.Min.X + 20
	if label == "" {
		lucideIcon(k.img, icon, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, 24, wkFG)
	} else {
		if icon != "" {
			lucideIcon(k.img, icon, tx+12, (r.Min.Y+r.Max.Y)/2, 24, wkText2)
			tx += 34
		}
		apText(k.img, f, tx, (r.Min.Y+r.Max.Y)/2+8, wkFG, label)
	}
	k.btn(id, r)
	return r.Min.X
}

// cardHead is a card's title row: an icon, the title, a faint note after a dot.
func (k *wk) cardHead(x, y int, icon, title, note string) int {
	k.icon(icon, x+12, y-8, 26, wkMuted)
	e := k.text(wkFont("semi", 23), x+40, y, wkFG, title)
	if note != "" {
		e = k.text(wkFont("regular", 19), e+14, y, wkFaint, note)
	}
	return e
}

// delta is "↑ 12%" in green (or "↓ 9%" in red), then a faint note.
func (k *wk) delta(x, y int, pct float64, good bool, note string) {
	f := wkFont("semi", 19)
	col := wkGreen
	if !good {
		col = wkRed
	}
	icon := "arrow-up"
	if pct < 0 {
		icon = "arrow-down"
	}
	if math.IsNaN(pct) {
		k.text(wkFont("regular", 19), x, y, wkFaint, note)
		return
	}
	k.icon(icon, x+8, y-7, 20, col)
	e := k.text(f, x+22, y, col, fmt.Sprintf("%.0f%%", math.Abs(pct)))
	k.text(wkFont("regular", 19), e+10, y, wkFaint, note)
}

// pctChange is the change from a to b in percent; NaN when there's nothing to compare with.
func pctChange(a, b float64) float64 {
	if a <= 0 {
		return math.NaN()
	}
	return (b - a) / a * 100
}

// spark is a small line chart in r (page coordinates).
func (k *wk) spark(r image.Rectangle, vals []float64, col color.RGBA) {
	if len(vals) < 2 {
		ui.Fill(k.img, image.Rect(r.Min.X, r.Max.Y-r.Dy()/2-1, r.Max.X, r.Max.Y-r.Dy()/2+2), col)
		return
	}
	hi, lo := vals[0], vals[0]
	for _, v := range vals {
		hi, lo = max(hi, v), min(lo, v)
	}
	span := max(hi-lo, 1e-9)
	pt := func(i int) (int, int) {
		x := r.Min.X + i*r.Dx()/(len(vals)-1)
		y := r.Max.Y - int((vals[i]-lo)/span*float64(r.Dy()))
		if hi == lo {
			y = r.Min.Y + r.Dy()/2
		}
		return x, y
	}
	for i := 1; i < len(vals); i++ {
		x0, y0 := pt(i - 1)
		x1, y1 := pt(i)
		line(k.img, x0, y0, x1, y1, 3, col)
	}
}

// kpi is a figure card: an icon and label, the value (a unit after it, smaller), a change
// and a sparkline.
func (k *wk) kpi(r image.Rectangle, icon, label, value, unit string, pct float64, good bool, note string, spark []float64, sparkCol color.RGBA, valCol color.RGBA) {
	k.card(r)
	x := r.Min.X + 26
	k.icon(icon, x+10, r.Min.Y+34, 24, wkMuted)
	k.text(wkFont("medium", 20), x+32, r.Min.Y+42, wkText2, label)
	e := k.text(wkFont("bold", 40), x, r.Min.Y+96, valCol, value)
	if unit != "" {
		k.text(wkFont("medium", 22), e+3, r.Min.Y+96, wkMuted, unit)
	}
	k.delta(x, r.Min.Y+142, pct, good, note)
	if spark != nil {
		k.spark(image.Rect(r.Max.X-100, k.Y(r.Min.Y+118), r.Max.X-26, k.Y(r.Min.Y+136)), spark, sparkCol)
	}
}

// badge is a tinted pill ending at right, centred on cy.
func (k *wk) badge(right, cy int, text string, col color.RGBA) int {
	f := wkFont("semi", 18)
	w := ui.TextWidth(f, text) + 28
	r := k.R(right-w, cy-18, right, cy+18)
	ui.RoundRect(k.img, r, 18, blend(wkCard, col, 0.16))
	apText(k.img, f, r.Min.X+14, k.Y(cy)+7, col, text)
	return r.Min.X
}

// arc draws a ring's share frac from the top, clockwise, w wide, with round ends.
func (k *wk) arc(cx, cy, rad, w int, frac float64, col, track color.RGBA) {
	ring(k.img, cx, k.Y(cy), rad, w, min(max(frac, 0), 1), track, col)
}

// donut draws shares round a ring with small gaps, from the top.
func (k *wk) donut(cx, cy, rad, w int, parts []float64, cols []color.RGBA) {
	tot := 0.0
	for _, v := range parts {
		tot += v
	}
	if tot <= 0 {
		ring(k.img, cx, k.Y(cy), rad, w, 1, wkRim, wkRim)
		return
	}
	cy = k.Y(cy)
	a0 := 0.0
	r0, r1 := float64(rad-w/2), float64(rad+w/2)
	for i, v := range parts {
		a1 := a0 + v/tot
		gap := 0.006
		for y := cy - rad - w; y <= cy+rad+w; y++ {
			for x := cx - rad - w; x <= cx+rad+w; x++ {
				dx, dy := float64(x)+0.5-float64(cx), float64(y)+0.5-float64(cy)
				d := math.Hypot(dx, dy)
				cov := min(d-r0+0.5, r1-d+0.5, 1)
				if cov <= 0 || !(image.Point{x, y}.In(k.img.Rect)) {
					continue
				}
				t := math.Atan2(dx, -dy) / (2 * math.Pi)
				if t < 0 {
					t++
				}
				if t < a0+gap || t > a1-gap {
					continue
				}
				o := k.img.PixOffset(x, y)
				c := cols[i%len(cols)]
				k.img.Pix[o] = uint8(float64(k.img.Pix[o])*(1-cov) + float64(c.R)*cov)
				k.img.Pix[o+1] = uint8(float64(k.img.Pix[o+1])*(1-cov) + float64(c.G)*cov)
				k.img.Pix[o+2] = uint8(float64(k.img.Pix[o+2])*(1-cov) + float64(c.B)*cov)
			}
		}
		a0 = a1
	}
}

// --- the rail ---------------------------------------------------------------------------------

var wkPersonal = []struct{ id, label, icon string }{
	{"calendar", "Calendar", "calendar"}, {"habits", "Habits", "circle-check"}, {"health", "Health", "heart-pulse"}, {"training", "Training", "dumbbell"},
	{"nutrition", "Nutrition", "utensils"}, {"mind", "Mind", "brain-circuit"}, {"stats", "Stats", "trending-up"},
}

var wkPages = []struct{ id, label, icon string }{
	{"overview", "Overview", "gauge"}, {"projects", "Projects", "folder-kanban"}, {"planner", "Planner", "calendar-days"},
	{"time", "Time", "timer"}, {"money", "Money", "wallet"}, {"reports", "Reports", "file-text"},
	{"findings", "Findings", "shield-alert"}, {"worklog", "Work log", "briefcase"},
}

func (k *wk) rail(h int, badges map[string]int) {
	img := k.img
	top := tabsH // under Margin's tab bar
	ui.Fill(img, image.Rect(0, top, wkSideW-1, h), wkSide)
	ui.Fill(img, image.Rect(wkSideW-1, top, wkSideW+1, h), rgb(0x1a1b1f))
	page := k.c.somaUI.work
	item := func(id, label, icon string, y int) {
		r := k.R(10, y, 114, y+78)
		col, ic := wkText2, wkText2
		if page == id || (id == "projects" && page == "project") {
			ui.RoundRect(img, r, 16, wkSideSel)
			col, ic = wkFG, wkLime
		}
		k.icon(icon, 61, y+27, 28, ic)
		k.textC(wkFont("medium", 16), 61, y+62, col, label)
		if n := badges[id]; n > 0 {
			ui.Circle(img, 82, k.Y(y+10), 12, wkRed)
			apTextCenter(img, wkFont("semi", 15), 82, k.Y(y+10), rgb(0xffffff), fmt.Sprint(n))
		}
		k.btn("wk:go:"+id, r)
	}
	// One list, as Soma's own rail: the work, then the personal side, then settings.
	y := 72
	for _, it := range wkPages {
		item(it.id, it.label, it.icon, y)
		y += 82
	}
	ui.Fill(img, k.R(40, y+4, 84, y+6), rgb(0x24252b))
	y += 14
	for _, it := range wkPersonal {
		item(it.id, it.label, it.icon, y)
		y += 82
	}
	ui.Fill(img, k.R(40, y+4, 84, y+6), rgb(0x24252b))
	item("settings", "Settings", "settings", y+14)
}

func wkInitials(name string) string {
	f := strings.Fields(name)
	switch {
	case len(f) >= 2:
		return strings.ToUpper(f[0][:1] + f[1][:1])
	case len(f) == 1 && len(f[0]) >= 2:
		return strings.ToUpper(f[0][:2])
	}
	return "S"
}

// timerPill is the running timer, floating at the bottom: the project, the clock, what it's
// worth, a break and a stop.
func (k *wk) timerPill(h int) {
	if k.c.somaUI.work == "training" && k.restPill(h) {
		return
	}
	entries := wkEntries()
	e, ok := wkRunning(entries)
	if !ok {
		return
	}
	// The band it floats in, the page's last rows: fixed at the screen's bottom (the footer),
	// so it stays as the page scrolls, and it ticks on its own (wkTimerTick).
	ui.Fill(k.img, image.Rect(wkSideW+1, h-wkTimerH, k.c.s.W, h), wkBG)
	ui.Fill(k.img, image.Rect(0, h-wkTimerH, wkSideW-1, h), wkSide)
	ui.Fill(k.img, image.Rect(wkSideW-1, h-wkTimerH, wkSideW+1, h), rgb(0x1a1b1f))
	k.p.footer = wkTimerH
	now := time.Now()
	p, _ := wkProjectByID(e.ProjectID)
	cx := 660
	y0 := k.oy + h - 120 // screen y of the pill's top
	r := k.R(cx-280, y0, cx+280, y0+90)
	ui.RoundRect(k.img, r, 45, rgb(0x2a2c35))
	ui.RoundRect(k.img, r.Inset(2), 43, rgb(0x16171f))
	cy := (r.Min.Y + r.Max.Y) / 2
	ui.Circle(k.img, r.Min.X+36, cy, 8, wkGreen)
	apText(k.img, wkFont("regular", 18), r.Min.X+62, cy-8, wkText2, clip(wkFont("regular", 18), p.Name, 220))
	d := e.dur(now)
	apText(k.img, wkFont("bold", 27), r.Min.X+62, cy+24, wkFG, fmt.Sprintf("%02d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60))
	if p.Rate > 0 {
		v := wkMoney2(d.Hours() * p.Rate)
		f := wkFont("semi", 18)
		w := ui.TextWidth(f, v) + 28
		br := image.Rect(r.Max.X-178-w, cy-18, r.Max.X-178, cy+18)
		ui.RoundRect(k.img, br, 18, blend(rgb(0x16171f), wkLime, 0.18))
		apText(k.img, f, br.Min.X+14, cy+7, wkLime, v)
	}
	ui.Circle(k.img, r.Max.X-126, cy, 32, rgb(0x262833))
	lucideIcon(k.img, "coffee", r.Max.X-126, cy, 28, wkFG)
	k.btn("wk:break", image.Rect(r.Max.X-166, r.Min.Y, r.Max.X-86, r.Max.Y))
	ui.Circle(k.img, r.Max.X-48, cy, 32, wkRed)
	lucideIcon(k.img, "square", r.Max.X-48, cy, 26, rgb(0xffffff))
	k.btn("wk:stop", image.Rect(r.Max.X-82, r.Min.Y, r.Max.X, r.Max.Y))
}

// wkTimerH is the band at the bottom the running timer floats in.
const wkTimerH = 140

// wkTimerTick keeps the running timer's clock going: once a second, only its band is drawn
// again and sent to the screen (the page isn't). Caller holds drawMu.
func (c *console) wkTimerTick() {
	if c.mode != modeSoma || c.page == nil || !c.screenOn || c.somaUI.typing || c.somaUI.sheet != "" ||
		c.sc.drag || c.sc.cpuOn || c.page.footer != wkTimerH || c.somaUI.work == "reports" {
		return
	}
	if c.somaUI.work == "training" && !c.somaUI.restEnd.After(time.Now()) {
		if _, ok := wkRunning(wkEntries()); !ok {
			return // rest over: startRest's own redraw takes the band away
		}
	}
	img := c.page.img
	ih := img.Rect.Dy()
	k := &wk{c: c, p: &page{img: img}, img: img, oy: c.barH - (tabsH - 24)}
	k.timerPill(ih)
	// Its buttons are the page's already (the page keeps the ones it was drawn with).
	vh := c.viewH()
	c.pagePainted(ih-wkTimerH, ih)
	band := img.SubImage(image.Rect(0, ih-wkTimerH, img.Rect.Dx(), ih)).(*image.RGBA)
	if ih > vh {
		c.s.blitRGBA(band, 0, c.barH+vh-wkTimerH)
	} else {
		c.s.blitRGBA(band, 0, c.barH+ih-wkTimerH)
	}
	c.s.Flush()
}

// startTimerTicks runs wkTimerTick every second, for as long as Margin runs.
func (c *console) startTimerTicks() {
	go func() {
		for range time.Tick(time.Second) {
			drawMu.Lock()
			c.wkTimerTick()
			drawMu.Unlock()
		}
	}()
}

// --- the page --------------------------------------------------------------------------------

// workPage draws the Work tab. Caller holds drawMu.
func (c *console) workPage() *page {
	su := &c.somaUI
	if su.work == "" {
		su.work = "overview"
	}
	if su.need == nil {
		su.need = map[string]int{}
	}
	wkApplyAccent()
	w := c.s.W
	needKey := "work:" + su.work + "/" + su.workSub
	h := max(c.viewH(), su.need[needKey])
	// A page drawn before, with nothing it shows changed since: shown as it was (switching
	// between the rail's pages, or back to one, is then a copy, not a drawing).
	key := c.workKey()
	if e := wkPageCache.find(key); e != nil {
		c.tabsOn = e.tabsOn
		return e.p
	}
	img := wkPageCache.canvas(w, h)
	ui.Fill(img, img.Rect, wkBG)
	p := &page{img: img, header: tabsH}
	// Margin's tab bar stays at the top: the pages sit under it.
	k := &wk{c: c, p: p, img: img, oy: c.barH - (tabsH - 24)}
	k.glow()
	var end int
	switch su.work {
	case "projects":
		end = k.projects()
	case "project":
		end = k.project()
	case "time":
		end = k.timePage()
	case "planner":
		end = k.planner()
	case "money":
		end = k.money()
	case "reports":
		end = k.reports()
	case "worklog":
		end = k.worklog()
	case "findings":
		end = k.findingsPage()
	case "health":
		end = k.health()
	case "training":
		end = k.training()
	case "nutrition":
		end = k.nutrition()
	case "mind":
		end = k.mindPage()
	case "stats":
		end = k.stats()
	case "habits":
		end = k.habits()
	case "calendar":
		end = k.calendar()
	case "settings":
		end = k.settingsPage()
	default:
		end = k.overview()
	}
	end = k.Y(end) + 160         // room for the timer
	k.oy = c.barH - (tabsH - 24) // a page may have moved its lower part down
	if end > h {
		su.need[needKey] = end
		if wkPageCache.next != nil { // drawn again, taller: this picture is free
			wkPageCache.spare = append(wkPageCache.spare, wkPageCache.next)
			wkPageCache.next = nil
		}
		return c.workPage()
	}
	if end < h && h > c.viewH() {
		p.img = img.SubImage(image.Rect(0, 0, w, max(end, c.viewH()))).(*image.RGBA)
		k.img = p.img
	}
	open := 0
	for _, f := range wkFindings() {
		if !f.closed() {
			open++
		}
	}
	planned := 0
	today := somaToday()
	for _, t := range somaTodos() {
		if !t.Done && !t.Cleared && (t.Date == today || t.Due == today) {
			planned++
		}
	}
	k.rail(p.img.Rect.Dy(), map[string]int{"planner": planned, "findings": open})
	p.left = wkSideW // the rail stays put as the page scrolls
	sy := 0
	if p.img.Rect.Dy() > c.viewH() {
		sy = min(max(c.scrollY(), 0), p.img.Rect.Dy()-c.viewH())
	}
	if su.work != "reports" { // the report's buttons sit where it would
		k.timerPill(p.img.Rect.Dy()) // in the fixed band at the bottom
	}
	c.booksTabs(p, "tab:soma")
	k.undoPill(sy + c.viewH())
	k.drawSheet(p)
	wkPageCache.keep(key, p, c.tabsOn)
	return p
}

// wkPageCache keeps the last few workstation pages drawn, each in its own picture.
var wkPageCache wkPages_

type wkCached struct {
	key    string
	p      *page
	tabsOn string
	used   int
}

type wkPages_ struct {
	list  []*wkCached
	spare []*image.RGBA // pictures of pages dropped, drawn into again
	clock int
	next  *image.RGBA // the picture the page being drawn is in
}

const wkCacheN = 5

func (pc *wkPages_) find(key string) *wkCached {
	if key == "" {
		return nil
	}
	for _, e := range pc.list {
		if e.key == key {
			pc.clock++
			e.used = pc.clock
			return e
		}
	}
	return nil
}

// canvas is a picture for a page about to be drawn: not one a kept page is in.
func (pc *wkPages_) canvas(w, h int) *image.RGBA {
	for i, im := range pc.spare {
		if im.Rect.Dx() == w && cap(im.Pix) >= 4*w*h {
			pc.spare = append(pc.spare[:i], pc.spare[i+1:]...)
			im = &image.RGBA{Pix: im.Pix[:4*w*h], Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}
			pc.next = im
			return im
		}
	}
	pc.next = image.NewRGBA(image.Rect(0, 0, w, h))
	return pc.next
}

// keep remembers the page just drawn (key "": not kept, its picture is drawn into next time).
func (pc *wkPages_) keep(key string, p *page, tabsOn string) {
	im := pc.next
	pc.next = nil
	if im == nil {
		return
	}
	for i, e := range pc.list { // the same page drawn again: the old one goes
		if e.key == key {
			pc.spare = append(pc.spare, e.p.img)
			pc.list = append(pc.list[:i], pc.list[i+1:]...)
			break
		}
	}
	if key == "" {
		pc.spare = append(pc.spare[:0], im)
		return
	}
	if len(pc.list) >= wkCacheN {
		old := 0
		for i, e := range pc.list {
			if e.used < pc.list[old].used {
				old = i
			}
		}
		pc.spare = append(pc.spare, pc.list[old].p.img)
		pc.list = append(pc.list[:old], pc.list[old+1:]...)
	}
	if len(pc.spare) > 2 {
		pc.spare = pc.spare[len(pc.spare)-2:]
	}
	pc.clock++
	pc.list = append(pc.list, &wkCached{key: key, p: p, tabsOn: tabsOn, used: pc.clock})
}

// drop forgets every kept page (what they show has changed in a way the key can't see).
func (pc *wkPages_) drop() {
	for _, e := range pc.list {
		pc.spare = append(pc.spare, e.p.img)
	}
	pc.list = nil
	if len(pc.spare) > 2 {
		pc.spare = pc.spare[len(pc.spare)-2:]
	}
}

// workKey is everything a workstation page shows that can change: the records, the page and
// its choices, the minute, where it's scrolled to (the pills and sheets sit on the screen).
// "" while typing: that page is drawn each time.
func (c *console) workKey() string {
	su := c.somaUI
	if su.typing {
		return ""
	}
	su.need, su.keep, su.toTop = nil, false, false
	var undo string
	if su.undo != nil {
		undo = su.undo.label + su.undo.until.String()
	}
	su.undo = nil
	now := time.Now().In(displayZone)
	seeded := loadSeed() != nil
	return fmt.Sprintf("%v|%s|%d|%s|%d|%v|%v|%v|%d", su, undo, somaGen.Load(), now.Format("2006-01-02 15:")+fmt.Sprint(now.Minute()/5),
		c.scrollY(), c.cfg.SomaHidden, wkLime, seeded, c.barH)
}

// workTap handles the Work tab's buttons: "wk:<action>:<arg>", an action registered in
// wkActions, or one of the page's own below. Caller holds drawMu.
func (c *console) workTap(id string) bool {
	su := &c.somaUI
	if su.typing {
		c.somaDone() // a tap elsewhere: what was typed is kept
	}
	prev := su.sheet
	su.keep, su.toTop = false, false
	name, arg, _ := strings.Cut(strings.TrimPrefix(id, "wk:"), ":")
	if f := wkActions[name]; f != nil {
		f(c, arg)
	} else if !c.workTapPage(id) {
		return false
	}
	if prev != "" && su.sheet == prev && !su.keep {
		c.sheetPop() // done with it: back to the sheet it came from, if any
	}
	if su.toTop {
		c.setScroll(modeSoma, 0)
	}
	c.showPage()
	return true
}

// wkGo opens a page of the workstation (and a page within it), at its top.
func (c *console) wkGo(page, sub string) {
	su := &c.somaUI
	su.work, su.workSub, su.sheet, su.sheetStack, su.toTop = page, sub, "", nil, true
}

// wkHandled says whether a button id does something (the dead-button test asks).
func wkHandled(id string) bool {
	name, _, _ := strings.Cut(strings.TrimPrefix(id, "wk:"), ":")
	if wkActions[name] != nil {
		return true
	}
	for _, p := range []string{"go", "proj", "back", "stop", "clockout", "finding", "water", "start", "tab", "chip", "seg", "week", "client", "step", "todo", "lib"} {
		if name == p {
			return true
		}
	}
	return false
}

func (c *console) workTapPage(id string) bool {
	su := &c.somaUI
	switch {
	case strings.HasPrefix(id, "wk:go:"):
		c.wkGo(strings.TrimPrefix(id, "wk:go:"), "")
	case strings.HasPrefix(id, "wk:proj:"):
		c.wkGo("project", "")
		su.workProj = strings.TrimPrefix(id, "wk:proj:")
	case id == "wk:back":
		if su.workSub != "" {
			su.workSub, su.toTop = "", true
		} else {
			c.wkGo("projects", "")
		}
	case id == "wk:stop":
		wkStopTimer()
	case id == "wk:clockout":
		wkClockOut()
	case strings.HasPrefix(id, "wk:finding:"):
		c.openSheet("finding:" + strings.TrimPrefix(id, "wk:finding:"))
	case strings.HasPrefix(id, "wk:water:"):
		ml := 0
		fmt.Sscan(strings.TrimPrefix(id, "wk:water:"), &ml)
		somaAddWater(float64(ml))
	case strings.HasPrefix(id, "wk:start:"):
		wkStartTimer(strings.TrimPrefix(id, "wk:start:"))
	case strings.HasPrefix(id, "wk:tab:"), strings.HasPrefix(id, "wk:chip:"), strings.HasPrefix(id, "wk:seg:"), strings.HasPrefix(id, "wk:week:"), strings.HasPrefix(id, "wk:client:"), strings.HasPrefix(id, "wk:lib:"):
		if su.workSel == nil {
			su.workSel = map[string]string{}
		}
		i := strings.LastIndex(id, ":")
		key := su.work + id[:i]
		if su.workSub != "" && !strings.HasPrefix(id, "wk:week:") {
			key = su.work + "/" + strings.SplitN(su.workSub, ":", 2)[0] + id[:i]
		}
		su.workSel[key] = id[i+1:]
		su.toTop = strings.HasPrefix(id, "wk:tab:") || strings.HasPrefix(id, "wk:chip:")
	case strings.HasPrefix(id, "wk:step:"):
		parts := strings.SplitN(strings.TrimPrefix(id, "wk:step:"), ":", 2)
		if len(parts) == 2 {
			somaStepTap(parts[0], parts[1])
		}
	case strings.HasPrefix(id, "wk:todo:"):
		somaToggleTodo(strings.TrimPrefix(id, "wk:todo:"))
	default:
		return false
	}
	return true
}

// sel is which tab, chip or segment of a group is chosen on this page (def until one is).
func (k *wk) sel(group string, def int) int {
	su := &k.c.somaUI
	key := su.work + "wk:" + group
	if su.workSub != "" && group != "week" {
		key = su.work + "/" + strings.SplitN(su.workSub, ":", 2)[0] + "wk:" + group
	}
	v, ok := su.workSel[key]
	if !ok {
		return def
	}
	n := 0
	fmt.Sscan(v, &n)
	return n
}

// wkStopTimer ends the running time entry (stopTimer).
func wkStopTimer() {
	for _, o := range somaList("timeEntries") {
		if num(o["end"]) == 0 && num(o["start"]) > 0 {
			o["end"] = float64(time.Now().UnixMilli())
			somaPut("timeEntries#"+str(o["id"]), o)
		}
	}
}

// wkStartTimer starts a timer on a project, stopping any other (startTimer: one at a time).
func wkStartTimer(projectID string) { wkStartTimerOn(projectID, "", "") }
