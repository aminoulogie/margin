package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"
)

// The Work tab's controls beyond drawing: sheets over the page (a menu, steppers, segmented
// choices, colours, a month to pick a date from), fields typed with the keyboard, swipes,
// undo, and Soma's accent colour. Each page registers its own sheets, actions and fields in
// the maps below, so a button's id says what it does: "wk:<action>:<arg>".

// --- the accent colour -------------------------------------------------------------------------

// wkAccents are Soma's twelve accents (theme.ts ACCENT_PRESETS); Volt is the default.
var wkAccents = []struct {
	name string
	hex  uint32
}{
	{"Volt", 0xd3fd50}, {"Mint", 0x34e0a1}, {"Aqua", 0x3dd6f5}, {"Azure", 0x4c8dff},
	{"Indigo", 0x6e6bff}, {"Violet", 0xa77bff}, {"Orchid", 0xe879f9}, {"Rose", 0xff5c8a},
	{"Coral", 0xff6b57}, {"Tangerine", 0xff9e3d}, {"Gold", 0xffcf4a}, {"Pearl", 0xe9e4da},
}

// wkOnLime is what's written on the accent: dark on a light accent, light on a dark one.
var wkOnLime = wkInk

// accentInk is theme.ts accentInk: relative luminance decides dark or light ink.
func accentInk(c color.RGBA) color.RGBA {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	if 0.2126*lin(c.R)+0.7152*lin(c.G)+0.0722*lin(c.B) > 0.45 {
		return rgb(0x0b1207)
	}
	return rgb(0xf8fafc)
}

// wkAccentHex is the accent Soma is set to ("#d3fd50" when none).
func wkAccentHex() string {
	if s, _ := jsonValue(soma.get("settings.accent")).(string); len(s) == 7 && s[0] == '#' {
		if _, err := strconv.ParseUint(s[1:], 16, 32); err == nil {
			return strings.ToLower(s)
		}
	}
	return "#d3fd50"
}

// wkApplyAccent colours the workstation with Soma's accent (settings.accent, shared with the
// phone and the PC).
func wkApplyAccent() {
	wkLime = somaColour(wkAccentHex(), rgb(0xd3fd50))
	wkOnLime = accentInk(wkLime)
}

// --- sheets ------------------------------------------------------------------------------------

type wkOpt struct {
	id, label string
	col       color.RGBA
	sel       bool
}

// wkRow is one line of a sheet.
type wkRow struct {
	kind   string // "" (an item), "stepper", "seg", "swatches", "cal", "text"
	id     string // what a tap does (an item; a stepper's value, to type it)
	label  string
	detail string // an item's value on the right; a stepper's value; a text row's words
	icon   string
	col    color.RGBA // the icon's colour
	sel    bool       // a tick on the right
	danger bool
	minus  string // a stepper's buttons
	plus   string
	opts   []wkOpt   // seg and swatches
	month  time.Time // cal: the month shown
	day    string    // cal: the day chosen
	pick   string    // cal: the id a day's date is added to
	nav    string    // cal: the id ‹ and › add -1 and 1 to
}

type wkSheet struct {
	title, sub string
	rows       []wkRow
	done       string // a button closing the sheet ("" none)
}

var (
	// wkSheets build a sheet for su.sheet = "<name>:<arg>" from the data as it is now.
	wkSheets = map[string]func(k *wk, arg string) *wkSheet{}
	// wkActions handle a tap on "wk:<name>:<arg>". Caller holds drawMu.
	wkActions = map[string]func(c *console, arg string){}
	// wkTypers take what was typed into the field "w.<name>:<arg>".
	wkTypers = map[string]func(c *console, arg, text string){}
	// wkSwipes handle a swipe across a zone: dir 1 is next (finger moved left), -1 back.
	wkSwipes = map[string]func(c *console, arg string, dir int){}
)

// openSheet shows a sheet; a tap outside it closes it. One opened from another goes back to
// it when it's done.
func (c *console) openSheet(spec string) {
	su := &c.somaUI
	if su.sheet != "" && su.sheet != spec {
		su.sheetStack = append(su.sheetStack, su.sheet)
	}
	su.sheet, su.sheetPage, su.keep = spec, 0, true
}

// sheetPop goes back to the sheet this one was opened from (none: closes it).
func (c *console) sheetPop() {
	su := &c.somaUI
	su.sheet, su.sheetPage, su.keep = "", 0, true
	if n := len(su.sheetStack); n > 0 {
		su.sheet, su.sheetStack = su.sheetStack[n-1], su.sheetStack[:n-1]
	}
}

const (
	sheetW     = 780
	sheetItemH = 84
	sheetStepH = 100
)

func (r wkRow) height() int {
	switch r.kind {
	case "stepper":
		return sheetStepH
	case "seg":
		if r.label != "" {
			return 140
		}
		return 96
	case "swatches":
		return 26 + ((len(r.opts)+5)/6)*96 + 10
	case "cal":
		return 96 + 46 + 6*74 + 10
	case "text":
		return 36 + 32*len(wkWrap(wkFont("regular", 21), r.detail, sheetW-80, 8))
	}
	return sheetItemH
}

// drawSheet lays the open sheet over the visible part of the page and makes its buttons the
// page's only ones.
func (k *wk) drawSheet(p *page) {
	su := &k.c.somaUI
	if su.sheet == "" {
		if su.sheetBack == "" { // (typing from a sheet keeps its way back)
			su.sheetStack = nil
		}
		return
	}
	name, arg, _ := strings.Cut(su.sheet, ":")
	build := wkSheets[name]
	var sh *wkSheet
	if build != nil {
		sh = build(k, arg)
	}
	if sh == nil {
		su.sheet = ""
		return
	}
	img := p.img
	w, vh, ih := k.c.s.W, k.c.viewH(), img.Rect.Dy()
	sy := 0
	if ih > vh {
		sy = min(max(k.c.scrollY(), 0), ih-vh)
	}
	blendRect(img, image.Rect(0, 0, w, tabsH), rgb(0), 0.6)
	blendRect(img, image.Rect(0, sy+tabsH, w, sy+vh), rgb(0), 0.6)
	var btns []button
	btn := func(id string, r image.Rectangle) {
		if id != "" {
			btns = append(btns, button{id, r})
		}
	}

	// What fits: rows from the page the sheet is on, and "More" when there are more.
	headH := 40
	if sh.title != "" {
		headH = 96
		if sh.sub != "" {
			headH = 128
		}
	}
	doneH := 0
	if sh.done != "" {
		doneH = 100
	}
	avail := vh - tabsH - 80 - headH - doneH
	rows := sh.rows
	start := 0
	for pg := 0; pg < su.sheetPage && start < len(rows); pg++ { // skip the pages before
		hh := 0
		for start < len(rows) && hh+rows[start].height() <= avail-sheetItemH {
			hh += rows[start].height()
			start++
		}
	}
	if start >= len(rows) {
		start, su.sheetPage = 0, 0
	}
	var shown []wkRow
	hh, more := 0, false
	for i := start; i < len(rows); i++ {
		rh := rows[i].height()
		if hh+rh > avail && i > start {
			more = true
			// leave room for "More"
			for len(shown) > 1 && hh+sheetItemH > avail {
				hh -= shown[len(shown)-1].height()
				shown = shown[:len(shown)-1]
			}
			break
		}
		shown = append(shown, rows[i])
		hh += rh
	}
	if more {
		hh += sheetItemH
	}
	total := headH + hh + doneH + 12
	x0 := (w - sheetW) / 2
	if x0 < wkSideW+12 {
		x0 = (wkSideW + w - sheetW) / 2
	}
	y0 := sy + tabsH + max((vh-tabsH-total)/2, 30)
	card := image.Rect(x0, y0, x0+sheetW, y0+total)
	shadow(img, card, 28, 0.35)
	ui.RoundRect(img, card, 28, rgb(0x2a2c35))
	ui.RoundRect(img, card.Inset(2), 26, rgb(0x17181f))
	btn("wk:sheet:stay", card) // a tap on the sheet's empty parts doesn't close it
	y := y0
	if sh.title != "" {
		apText(img, wkFont("bold", 28), x0+36, y+58, wkFG, clip(wkFont("bold", 28), sh.title, sheetW-140))
		if sh.sub != "" {
			apText(img, wkFont("regular", 20), x0+36, y+92, wkMuted, clip(wkFont("regular", 20), sh.sub, sheetW-72))
		}
		xr := image.Rect(x0+sheetW-84, y+18, x0+sheetW-24, y+78)
		ui.Circle(img, (xr.Min.X+xr.Max.X)/2, (xr.Min.Y+xr.Max.Y)/2, 26, rgb(0x262833))
		lucideIcon(img, "x", (xr.Min.X+xr.Max.X)/2, (xr.Min.Y+xr.Max.Y)/2, 24, wkText2)
		btn("wk:sheet:close", xr)
	}
	y += headH
	var stay []button
	for i, r := range shown {
		rh := r.height()
		if i > 0 || sh.title != "" {
			ui.Fill(img, image.Rect(x0+24, y, x0+sheetW-24, y+2), wkLine)
		}
		rr := image.Rect(x0, y, x0+sheetW, y+rh)
		switch r.kind {
		case "stepper":
			k.sheetStepper(img, rr, r, &stay)
		case "seg":
			k.sheetSeg(img, rr, r, &stay)
		case "swatches":
			k.sheetSwatches(img, rr, r, &stay)
		case "cal":
			k.sheetCal(img, rr, r, &stay)
		case "text":
			ty := y + 40
			for _, l := range wkWrap(wkFont("regular", 21), r.detail, sheetW-80, 8) {
				apText(img, wkFont("regular", 21), x0+40, ty, wkMuted, l)
				ty += 32
			}
		default:
			k.sheetItem(img, rr, r)
			if r.id != "" {
				stay = append(stay, button{r.id, rr})
			}
		}
		y += rh
	}
	if more {
		ui.Fill(img, image.Rect(x0+24, y, x0+sheetW-24, y+2), wkLine)
		rr := image.Rect(x0, y, x0+sheetW, y+sheetItemH)
		k.sheetItem(img, rr, wkRow{label: "More", icon: "chevron-down", col: wkText2})
		stay = append(stay, button{"wk:sheet:more", rr})
		y += sheetItemH
	}
	if sh.done != "" {
		dr := image.Rect(x0+28, y+14, x0+sheetW-28, y+86)
		ui.RoundRect(img, dr, 18, wkLime)
		apTextCenter(img, wkFont("semi", 24), (dr.Min.X+dr.Max.X)/2, (dr.Min.Y+dr.Max.Y)/2, wkOnLime, sh.done)
		stay = append(stay, button{"wk:sheet:close", dr})
	}
	// The sheet's own buttons first (the card's catch-all after them), then anywhere else
	// closes it.
	p.buttons = append(append(append(stay, btns[1:]...), btns[0]), button{"wk:sheet:close", img.Rect})
	p.rowBtns = nil
}

func (k *wk) sheetItem(img *image.RGBA, r image.Rectangle, it wkRow) {
	cy := (r.Min.Y + r.Max.Y) / 2
	x := r.Min.X + 36
	col := wkFG
	if it.danger {
		col = wkRed
	}
	if it.icon != "" {
		ic := it.col
		if ic.A == 0 {
			ic = wkText2
		}
		if it.danger {
			ic = wkRed
		}
		lucideIcon(img, it.icon, x+14, cy, 28, ic)
		x += 52
	} else if it.col.A != 0 {
		ui.RoundRect(img, image.Rect(x, cy-10, x+20, cy+10), 6, it.col)
		x += 40
	}
	right := r.Max.X - 36
	if it.sel {
		lucideIcon(img, "check", right-14, cy, 28, wkLime)
		right -= 44
	}
	dw := 0
	if it.detail != "" {
		f := wkFont("regular", 21)
		d := clip(f, it.detail, 300)
		apTextRight(img, f, right, cy+8, wkMuted, d)
		dw = ui.TextWidth(f, d) + 20
	}
	f := wkFont("semi", 24)
	apText(img, f, x, cy+9, col, clip(f, it.label, right-dw-x))
}

func (k *wk) sheetStepper(img *image.RGBA, r image.Rectangle, it wkRow, btns *[]button) {
	cy := (r.Min.Y + r.Max.Y) / 2
	apText(img, wkFont("semi", 24), r.Min.X+36, cy+9, wkFG, it.label)
	right := r.Max.X - 36
	plus := image.Rect(right-64, cy-32, right, cy+32)
	ui.Circle(img, plus.Min.X+32, cy, 32, rgb(0x262833))
	lucideIcon(img, "plus", plus.Min.X+32, cy, 28, wkFG)
	*btns = append(*btns, button{it.plus, plus.Inset(-8)})
	vf := wkFont("bold", 28)
	vw := max(ui.TextWidth(vf, it.detail)+40, 130)
	vr := image.Rect(plus.Min.X-16-vw, cy-32, plus.Min.X-16, cy+32)
	if it.id != "" {
		ui.RoundRect(img, vr, 14, rgb(0x0f1015))
		*btns = append(*btns, button{it.id, vr})
	}
	apTextCenter(img, vf, (vr.Min.X+vr.Max.X)/2, cy, wkFG, it.detail)
	minus := image.Rect(vr.Min.X-16-64, cy-32, vr.Min.X-16, cy+32)
	ui.Circle(img, minus.Min.X+32, cy, 32, rgb(0x262833))
	lucideIcon(img, "minus", minus.Min.X+32, cy, 28, wkFG)
	*btns = append(*btns, button{it.minus, minus.Inset(-8)})
}

func (k *wk) sheetSeg(img *image.RGBA, r image.Rectangle, it wkRow, btns *[]button) {
	y := r.Min.Y + 16
	if it.label != "" {
		apText(img, wkFont("semi", 21), r.Min.X+36, r.Min.Y+40, wkMuted, it.label)
		y = r.Min.Y + 58
	}
	seg := image.Rect(r.Min.X+28, y, r.Max.X-28, y+64)
	ui.RoundRect(img, seg, 16, rgb(0x0f1015))
	n := max(len(it.opts), 1)
	sw := (seg.Dx() - 8) / n
	for i, o := range it.opts {
		or := image.Rect(seg.Min.X+4+i*sw, seg.Min.Y+4, seg.Min.X+4+(i+1)*sw, seg.Max.Y-4)
		fg := wkText2
		if o.sel {
			bg := wkLime
			if o.col.A != 0 {
				bg = o.col
			}
			ui.RoundRect(img, or, 12, bg)
			fg = accentInk(bg)
		} else if o.col.A != 0 {
			ui.RoundRect(img, image.Rect(or.Min.X+14, (or.Min.Y+or.Max.Y)/2-6, or.Min.X+26, (or.Min.Y+or.Max.Y)/2+6), 4, o.col)
		}
		f := wkFont("semi", 21)
		apTextCenter(img, f, (or.Min.X+or.Max.X)/2, (or.Min.Y+or.Max.Y)/2, fg, clip(f, o.label, or.Dx()-16))
		*btns = append(*btns, button{o.id, or})
	}
}

func (k *wk) sheetSwatches(img *image.RGBA, r image.Rectangle, it wkRow, btns *[]button) {
	for i, o := range it.opts {
		cx := r.Min.X + 36 + 60 + (i%6)*((sheetW-72-120)/5)
		cy := r.Min.Y + 26 + 44 + (i/6)*96
		if o.sel {
			ui.Circle(img, cx, cy, 42, wkFG)
			ui.Circle(img, cx, cy, 38, rgb(0x17181f))
		}
		ui.Circle(img, cx, cy, 34, o.col)
		if o.sel {
			lucideIcon(img, "check", cx, cy, 30, accentInk(o.col))
		}
		*btns = append(*btns, button{o.id, image.Rect(cx-46, cy-46, cx+46, cy+46)})
	}
}

// sheetCal is a month to pick a day from.
func (k *wk) sheetCal(img *image.RGBA, r image.Rectangle, it wkRow, btns *[]button) {
	m := it.month
	apText(img, wkFont("bold", 26), r.Min.X+40, r.Min.Y+60, wkFG, m.Format("January 2006"))
	for i, d := range []int{-1, 1} {
		cx := r.Max.X - 140 + i*80
		ui.Circle(img, cx, r.Min.Y+50, 30, rgb(0x262833))
		lucideIcon(img, map[int]string{-1: "chevron-left", 1: "chevron-right"}[d], cx, r.Min.Y+50, 26, wkFG)
		*btns = append(*btns, button{fmt.Sprintf("%s%d", it.nav, d), image.Rect(cx-38, r.Min.Y+12, cx+38, r.Min.Y+88)})
	}
	cw := (sheetW - 56) / 7
	x0 := r.Min.X + 28
	y := r.Min.Y + 96
	for i, d := range []string{"M", "T", "W", "T", "F", "S", "S"} {
		apTextCenter(img, wkFont("semi", 18), x0+i*cw+cw/2, y+16, wkFaint, d)
	}
	y += 46
	first := time.Date(m.Year(), m.Month(), 1, 0, 0, 0, 0, displayZone)
	off := (int(first.Weekday()) + 6) % 7
	today := somaToday()
	for d := first; d.Month() == m.Month(); d = d.AddDate(0, 0, 1) {
		i := off + d.Day() - 1
		cx, cy := x0+(i%7)*cw+cw/2, y+(i/7)*74+37
		ds := d.Format("2006-01-02")
		fg := wkFG
		switch {
		case ds == it.day:
			ui.Circle(img, cx, cy, 32, wkLime)
			fg = wkOnLime
		case ds == today:
			ui.Circle(img, cx, cy, 32, rgb(0x262833))
			fg = wkLime
		}
		apTextCenter(img, wkFont("semi", 23), cx, cy, fg, fmt.Sprint(d.Day()))
		*btns = append(*btns, button{it.pick + ds, image.Rect(cx-cw/2, cy-37, cx+cw/2, cy+37)})
	}
}

// --- typing ----------------------------------------------------------------------------------

// wkType starts typing into a field ("<name>:<arg>", handled by wkTypers[name]), the keyboard
// up, prompt shown until something is typed, initial already in it.
func (c *console) wkType(field, prompt, initial string) {
	su := &c.somaUI
	if su.sheet != "" { // typed from a sheet: back to it after (not to a menu of things to add)
		if name, _, _ := strings.Cut(su.sheet, ":"); !wkMenuSheets[name] {
			su.sheetBack = su.sheet
		} else {
			su.sheetStack = nil
		}
		su.sheet = ""
	}
	su.typing, su.field, su.editing, su.prompt = true, "w."+field, initial, prompt
	c.fieldBack = 0
	c.skb.visible = true
}

// wkMenuSheets are menus of things to add: done with once something is chosen.
var wkMenuSheets = map[string]bool{"create": true, "addmoney": true, "newentry": true, "switchproj": true, "readlog": true, "addtime": true}

// wkTyped hands what was typed to its field's handler, then shows the sheet it was typed from.
func (c *console) wkTyped(field, text string) {
	su := &c.somaUI
	back := su.sheetBack
	su.sheetBack = ""
	name, arg, _ := strings.Cut(field, ":")
	if f := wkTypers[name]; f != nil {
		f(c, arg, text)
	}
	if back != "" && !su.typing && su.sheet == "" {
		su.sheet = back
	}
}

// --- swipes ------------------------------------------------------------------------------------

// swipeZone is a part of a page where a sideways swipe means something (the next week, say)
// instead of scrolling: id is "<name>:<arg>" for wkSwipes.
type swipeZone struct {
	id string
	r  image.Rectangle // page coordinates
}

func (k *wk) swipe(id string, r image.Rectangle) {
	k.p.swipes = append(k.p.swipes, swipeZone{id, r.Sub(image.Pt(0, k.oy))})
}

// pageSwipe handles a sideways swipe across a zone. Caller holds drawMu.
func (c *console) pageSwipe(id string, dir int) {
	if c.mode != modeSoma {
		return
	}
	name, arg, _ := strings.Cut(id, ":")
	if f := wkSwipes[name]; f != nil {
		c.somaUI.sheet = ""
		f(c, arg, dir)
		c.showPage()
	}
}

// --- undo --------------------------------------------------------------------------------------

type wkUndo struct {
	label string
	until time.Time
	fn    func()
}

// offerUndo shows "label · Undo" for a few seconds; Undo runs fn.
func (c *console) offerUndo(label string, fn func()) {
	u := &wkUndo{label, time.Now().Add(6 * time.Second), fn}
	c.somaUI.undo = u
	time.AfterFunc(6100*time.Millisecond, func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.somaUI.undo == u {
			c.somaUI.undo = nil
			if c.mode == modeSoma && c.screenOn && !c.somaUI.typing {
				c.showPage()
			}
		}
	})
}

// undoPill is the undo offer, floating above the bottom.
func (k *wk) undoPill(h int) {
	u := k.c.somaUI.undo
	if u == nil || time.Now().After(u.until) {
		return
	}
	f := wkFont("semi", 22)
	w := ui.TextWidth(f, u.label) + 220
	cx := (wkSideW + k.c.s.W) / 2
	y0 := h - 230
	r := image.Rect(cx-w/2, y0, cx+w/2, y0+84)
	shadow(k.img, r, 42, 0.3)
	ui.RoundRect(k.img, r, 42, rgb(0x2a2c35))
	ui.RoundRect(k.img, r.Inset(2), 40, rgb(0x1d1f27))
	apText(k.img, f, r.Min.X+40, (r.Min.Y+r.Max.Y)/2+8, wkFG, u.label)
	ur := image.Rect(r.Max.X-150, r.Min.Y+12, r.Max.X-14, r.Max.Y-12)
	ui.RoundRect(k.img, ur, 30, wkLime)
	lucideIcon(k.img, "undo-2", ur.Min.X+36, (ur.Min.Y+ur.Max.Y)/2, 24, wkOnLime)
	apText(k.img, wkFont("semi", 21), ur.Min.X+58, (ur.Min.Y+ur.Max.Y)/2+8, wkOnLime, "Undo")
	k.p.buttons = append([]button{{"wk:undo", ur}}, k.p.buttons...)
}

// --- small controls used across the pages ---------------------------------------------------------

// iconBtn is a round icon button centred on (cx, cy) in screen-y coordinates.
func (k *wk) iconBtn(id, icon string, cx, cy, rad int, bg, fg color.RGBA) {
	ui.Circle(k.img, cx, k.Y(cy), rad, bg)
	lucideIcon(k.img, icon, cx, k.Y(cy), rad*5/6, fg)
	k.btn(id, k.R(cx-rad-8, cy-rad-8, cx+rad+8, cy+rad+8))
}

// segBar is a segmented control in r (screen y); returns nothing, registers prefix:i.
func (k *wk) segBar(prefix string, r image.Rectangle, labels []string, sel int) {
	rr := k.R(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
	ui.RoundRect(k.img, rr, 12, rgb(0x101117))
	n := len(labels)
	sw := (rr.Dx() - 8) / n
	for i, l := range labels {
		b := image.Rect(rr.Min.X+4+i*sw, rr.Min.Y+4, rr.Min.X+4+(i+1)*sw, rr.Max.Y-4)
		col := wkText2
		if i == sel {
			ui.RoundRect(k.img, b, 10, rgb(0x1f212b))
			col = wkFG
		}
		apTextCenter(k.img, wkFont("semi", 20), (b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2, col, l)
		k.btn(fmt.Sprintf("%s:%d", prefix, i), b)
	}
}

// emptyNote is a faint line where a list has nothing.
func (k *wk) emptyNote(x, y int, s string) {
	k.text(wkFont("regular", 20), x, y, wkMuted, s)
}

// wkDate formats a date key as "Mon 2 Jan" ("" stays "").
func wkDate(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.Format("Mon 2 Jan")
}

// wkAddDays is date d moved by n days.
func wkAddDays(d string, n int) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// wkPickDate opens the month sheet for a date: what = "<setter>:<arg>" in wkDateSetters, cur
// the date it has now ("" none), title on top.
func (c *console) wkPickDate(what, cur, title string) {
	su := &c.somaUI
	m := time.Now().In(displayZone)
	if t, err := time.ParseInLocation("2006-01-02", cur, displayZone); err == nil {
		m = t
	}
	su.calMonth = time.Date(m.Year(), m.Month(), 1, 0, 0, 0, 0, displayZone)
	su.dateWhat, su.dateCur, su.dateTitle = what, cur, title
	c.openSheet("date:")
}

// wkDateSetters set a picked date ("" clears it): "<name>:<arg>".
var wkDateSetters = map[string]func(c *console, arg, date string){}

// wkDateRequired are the dates that can be moved but not taken away.
var wkDateRequired = map[string]bool{"edate": true}

func init() {
	wkSheets["date"] = func(k *wk, _ string) *wkSheet {
		su := &k.c.somaUI
		today := somaToday()
		sh := &wkSheet{title: su.dateTitle}
		sh.rows = append(sh.rows, wkRow{kind: "seg", opts: []wkOpt{
			{id: "wk:pickdate:" + today, label: "Today", sel: su.dateCur == today},
			{id: "wk:pickdate:" + wkAddDays(today, 1), label: "Tomorrow", sel: su.dateCur == wkAddDays(today, 1)},
			{id: "wk:pickdate:" + wkAddDays(mondayOf(today), 7), label: "Next week", sel: su.dateCur == wkAddDays(mondayOf(today), 7)},
		}})
		sh.rows = append(sh.rows, wkRow{kind: "cal", month: su.calMonth, day: su.dateCur, pick: "wk:pickdate:", nav: "wk:calnav:"})
		if su.dateCur != "" && !wkDateRequired[strings.SplitN(su.dateWhat, ":", 2)[0]] {
			sh.rows = append(sh.rows, wkRow{id: "wk:pickdate:", label: "No date", icon: "calendar-x", danger: true})
		}
		return sh
	}
	wkActions["calnav"] = func(c *console, arg string) {
		n, _ := strconv.Atoi(arg)
		c.somaUI.calMonth = c.somaUI.calMonth.AddDate(0, n, 0)
		c.somaUI.keep = true
	}
	wkActions["pickdate"] = func(c *console, date string) {
		su := &c.somaUI
		name, arg, _ := strings.Cut(su.dateWhat, ":")
		if f := wkDateSetters[name]; f != nil {
			f(c, arg, date)
		}
	}
	wkActions["sheet"] = func(c *console, arg string) {
		switch arg {
		case "close":
			c.somaUI.sheet, c.somaUI.sheetStack = "", nil
		case "more":
			c.somaUI.sheetPage++
			c.somaUI.keep = true
		default: // "stay": a tap on the sheet itself
			c.somaUI.keep = true
		}
	}
	wkActions["undo"] = func(c *console, _ string) {
		if u := c.somaUI.undo; u != nil {
			c.somaUI.undo = nil
			u.fn()
		}
	}
	wkActions["accent"] = func(c *console, hex string) {
		somaPut("settings.accent", hex)
		c.somaUI.keep = true
	}
}
