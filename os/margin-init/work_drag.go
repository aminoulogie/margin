package main

import (
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"margin-init/ui"
)

// Dragging a to-do, as in a calendar on the iPad: hold a to-do (a block on the week or the
// day, or a row of a list) until it lifts, move it, and let go where it should be. Over the
// week's or the day's hours it snaps to the quarter hour under it, shown as it will be, with
// the day and the time beside it; over a day's heading it goes on that day's list. Let go
// where it started and the to-do's sheet opens instead (what holding it did before).

// dropZone is somewhere a dragged to-do can land, in page coordinates: a day's hours
// (first: the minute at r's top, pxMin: pixels a minute) or a day's heading (first -1).
type dropZone struct {
	r     image.Rectangle
	date  string
	first int
	pxMin float64
}

// dropHours makes a column of hours a place to drop to-dos on (screen-y coordinates).
func (k *wk) dropHours(date string, x0, x1, top, bot, firstH int, pxH float64) {
	k.p.drops = append(k.p.drops, dropZone{r: k.R(x0, top, x1, bot), date: date, first: firstH * 60, pxMin: pxH / 60})
}

// dropDay makes r (a day's heading) a place to drop to-dos on: onto that day's list.
func (k *wk) dropDay(date string, r image.Rectangle) {
	k.p.drops = append(k.p.drops, dropZone{r: r.Sub(image.Pt(0, k.oy)), date: date, first: -1})
}

type dragState struct {
	on        bool
	id        string
	t         somaTodo
	gx, gy    int             // where in the block the finger holds it
	x0, y0    int             // where the finger was when it lifted
	shown     image.Rectangle // on the screen: the block and its label, as last drawn
	zone      *dropZone
	at        int // the minute it would start at (zone with hours)
	travelled bool
}

// dragTodoID is the to-do a button holds: a block or a row's text, or its tick box.
func dragTodoID(id string) (string, bool) {
	if t, ok := strings.CutPrefix(id, "wk:sheet-open:task:"); ok {
		return t, true
	}
	if t, ok := strings.CutPrefix(id, "wk:todo:"); ok {
		return t, true
	}
	return "", false
}

// dragStart lifts the to-do under (x, y) on the screen. True if there was one to lift.
// Caller holds drawMu.
func (c *console) dragStart(id string, x, y int) bool {
	tid, ok := dragTodoID(id)
	if !ok || c.page == nil || len(c.page.drops) == 0 {
		return false
	}
	var t somaTodo
	for _, x := range somaTodos() {
		if x.ID == tid {
			t, ok = x, true
		}
	}
	if !ok {
		return false
	}
	_, r := c.page.hitRect(x, c.pageYAt(x, y))
	scr, _ := c.onScreen(r, y)
	if scr.Empty() {
		scr = image.Rect(x-130, y-40, x+130, y+40)
	}
	if t.Scope == "week" || scr.Dx() > 400 { // a list row: held by its middle, as a block
		scr = image.Rect(x-130, y-40, x+130, y+40)
	}
	c.drag = dragState{on: true, id: tid, t: t, gx: x - scr.Min.X, gy: y - scr.Min.Y, x0: x, y0: y}
	vibrate(15)
	c.dragDraw(x, y)
	return true
}

// dragZone is the drop zone under page point (x, y).
func (c *console) dragZone(x, py int) *dropZone {
	for i := range c.page.drops {
		if (image.Point{x, py}).In(c.page.drops[i].r) {
			return &c.page.drops[i]
		}
	}
	return nil
}

// dragDraw draws the to-do where the finger has it: snapped into a zone's hours, or loose.
// Caller holds drawMu.
func (c *console) dragDraw(x, y int) {
	d := &c.drag
	if abs(x-d.x0) > scrollSlop || abs(y-d.y0) > scrollSlop {
		d.travelled = true
	}
	c.dragRestore()
	hh, vh, fh := c.page.header, c.viewH(), c.page.footer
	top, bottom := c.barH+hh, c.barH+vh-fh
	sy := 0
	if c.page.img.Rect.Dy() > vh {
		sy = c.scrollY()
	}
	py := y - c.barH + sy // the finger, in page coordinates (in the scrolling part)
	if y-c.barH < hh {
		py = -1
	}
	d.zone = c.dragZone(x, py)
	mins := max(d.t.SlotMins, 15)
	if d.t.SlotDate == "" {
		mins = 60
	}
	var block image.Rectangle // on the screen
	label := ""
	switch z := d.zone; {
	case z != nil && z.first >= 0:
		// The block's top follows the finger (where it was held), snapped to a quarter hour.
		topPY := py - d.gy
		m := z.first + int(math.Round(float64(topPY-z.r.Min.Y)/z.pxMin/15))*15
		m = min(max(m, z.first), 24*60-15)
		d.at = m
		by := z.r.Min.Y + int(float64(m-z.first)*z.pxMin) - sy + c.barH
		bh := max(int(float64(mins)*z.pxMin), 40)
		block = image.Rect(z.r.Min.X+4, by+2, z.r.Max.X-4, by+bh-2)
		label = fmt.Sprintf("%s  %s", wkShortDay(z.date), clockOf(float64(m)/60))
	case z != nil:
		block = image.Rect(x-d.gx, y-d.gy, x-d.gx+260, y-d.gy+80)
		label = "To " + wkShortDay(z.date) + "'s list"
	default:
		block = image.Rect(x-d.gx, y-d.gy, x-d.gx+260, y-d.gy+80)
	}
	// Keep it on the page (not over the rail, the tab bar or the band at the bottom).
	if block.Min.X < c.page.left+4 {
		block = block.Add(image.Pt(c.page.left+4-block.Min.X, 0))
	}
	if block.Max.X > c.s.W-4 {
		block = block.Add(image.Pt(c.s.W-4-block.Max.X, 0))
	}
	if block.Min.Y < top {
		block = block.Add(image.Pt(0, top-block.Min.Y))
	}
	if block.Max.Y > bottom {
		block = block.Add(image.Pt(0, bottom-block.Max.Y))
	}
	area := block.Inset(-14)
	var lr image.Rectangle
	f := wkFont("semi", 19)
	if label != "" {
		lw := ui.TextWidth(f, label) + 32
		ly := block.Min.Y - 52
		if ly < top+4 {
			ly = block.Max.Y + 10
		}
		lx := min(max(block.Min.X, c.page.left+4), c.s.W-4-lw)
		lr = image.Rect(lx, ly, lx+lw, ly+42)
		area = area.Union(lr)
	}
	area = area.Intersect(image.Rect(c.page.left, top, c.s.W, bottom))
	// Drawn off the screen, over what the page has there, then sent in one piece.
	img := image.NewRGBA(area)
	c.dragUnder(img)
	shadow(img, block, 12, 0.45)
	col := todoCol(d.t)
	ui.RoundRect(img, block.Inset(-3), 13, wkLime)
	ui.RoundRect(img, block, 10, blend(wkCard, col, 0.42))
	ui.RoundRect(img, image.Rect(block.Min.X, block.Min.Y, block.Min.X+5, block.Max.Y), 2, col)
	ft := wkFont("semi", 18)
	ty := block.Min.Y + 26
	for _, l := range wkWrap(ft, d.t.Text, block.Dx()-24, max((block.Dy()-10)/22, 1)) {
		if ty > block.Max.Y-4 {
			break
		}
		apText(img, ft, block.Min.X+14, ty, wkFG, l)
		ty += 22
	}
	if label != "" {
		ui.RoundRect(img, lr, 21, wkLime)
		apText(img, f, lr.Min.X+16, lr.Min.Y+28, wkOnLime, label)
	}
	c.s.blitRGBA(img, area.Min.X, area.Min.Y)
	c.s.Flush()
	d.shown = area
}

// dragUnder fills img (in screen coordinates) with what the page shows there.
func (c *console) dragUnder(img *image.RGBA) {
	r := img.Rect
	sy := 0
	if c.page.img.Rect.Dy() > c.viewH() {
		sy = c.scrollY()
	}
	src := r.Add(image.Pt(0, sy-c.barH)).Intersect(c.page.img.Rect)
	ui.Fill(img, r, wkBG)
	for y := src.Min.Y; y < src.Max.Y; y++ {
		sy0 := y - sy + c.barH
		copy(img.Pix[img.PixOffset(src.Min.X, sy0):][:4*src.Dx()], c.page.img.Pix[c.page.img.PixOffset(src.Min.X, y):][:4*src.Dx()])
	}
}

// dragRestore puts back what the dragged to-do covered.
func (c *console) dragRestore() {
	if c.drag.shown.Empty() {
		return
	}
	img := image.NewRGBA(c.drag.shown)
	c.dragUnder(img)
	c.s.blitRGBA(img, img.Rect.Min.X, img.Rect.Min.Y)
	c.drag.shown = image.Rectangle{}
}

// dragDrop lets the to-do go at (x, y). Caller holds drawMu.
func (c *console) dragDrop(x, y int) {
	c.dragDraw(x, y)
	d := c.drag
	c.drag = dragState{}
	switch z := d.zone; {
	case !d.travelled:
		c.openSheet("task:" + d.id) // held, not moved: its sheet, as before
	case z != nil && z.first >= 0:
		mins := d.t.SlotMins
		if d.t.SlotDate == "" {
			mins = 60
		}
		wkSetSlot(d.id, z.date, d.at, mins)
		c.toastMsg(fmt.Sprintf("%s · %s %s", clip(wkFont("regular", 20), d.t.Text, 400), wkShortDay(z.date), clockOf(float64(d.at)/60)))
	case z != nil:
		wkSetTodoDate(d.id, z.date)
		c.toastMsg("Moved to " + wkDate(z.date))
	}
	c.showPage()
}

// dragCancel drops the drag without moving anything (the page went away).
func (c *console) dragCancel() {
	c.drag = dragState{}
}

// wkShortDay is "Wed 29".
func wkShortDay(date string) string {
	t, err := time.ParseInLocation("2006-01-02", date, displayZone)
	if err != nil {
		return date
	}
	if date == somaToday() {
		return "Today"
	}
	return t.Format("Mon 2")
}

// --- sideways swipes that follow the finger ------------------------------------------------------

// swipeView shows a swipe zone (page rect zr) moved sideways by dx, from page picture src:
// what slides off leaves the card's colour behind. Caller holds drawMu.
func (c *console) swipeView(src *image.RGBA, zr image.Rectangle, dx int) {
	sy := 0
	if src.Rect.Dy() > c.viewH() {
		sy = c.scrollY()
	}
	hh, vh, fh := c.page.header, c.viewH(), c.page.footer
	scr := zr.Add(image.Pt(0, c.barH-sy)).Intersect(image.Rect(0, c.barH+hh, c.s.W, c.barH+vh-fh))
	if scr.Empty() {
		return
	}
	img := image.NewRGBA(scr)
	// The card's own colour, from inside its corner, for the part left empty.
	bg := src.RGBAAt(zr.Min.X+24, zr.Min.Y+24)
	ui.Fill(img, scr, bg)
	// Only the inside of the card slides; its rim stays.
	in := scr.Inset(3)
	for y := in.Min.Y; y < in.Max.Y; y++ {
		py := y - c.barH + sy
		x0, x1 := max(in.Min.X, in.Min.X+dx), min(in.Max.X, in.Max.X+dx)
		if x0 >= x1 || py < 0 || py >= src.Rect.Dy() {
			continue
		}
		copy(img.Pix[img.PixOffset(x0, y):][:4*(x1-x0)], src.Pix[src.PixOffset(x0-dx, py):][:4*(x1-x0)])
	}
	// The rim, as the page has it.
	for _, r := range []image.Rectangle{{scr.Min, image.Pt(scr.Max.X, in.Min.Y)}, {image.Pt(scr.Min.X, in.Max.Y), scr.Max},
		{image.Pt(scr.Min.X, in.Min.Y), image.Pt(in.Min.X, in.Max.Y)}, {image.Pt(in.Max.X, in.Min.Y), image.Pt(scr.Max.X, in.Max.Y)}} {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			py := y - c.barH + sy
			if py >= 0 && py < src.Rect.Dy() && r.Dx() > 0 {
				copy(img.Pix[img.PixOffset(r.Min.X, y):][:4*r.Dx()], src.Pix[src.PixOffset(r.Min.X, py):][:4*r.Dx()])
			}
		}
	}
	c.s.blitRGBA(img, scr.Min.X, scr.Min.Y)
}

// swipeZoneRect is the page rect of the swipe zone id on the current page.
func (c *console) swipeZoneRect(id string) (image.Rectangle, bool) {
	for _, z := range c.page.swipes {
		if z.id == id {
			return z.r, true
		}
	}
	return image.Rectangle{}, false
}

// swipeFollow moves the zone with the finger (dx from where the swipe began).
func (c *console) swipeFollow(id string, dx int) {
	zr, ok := c.swipeZoneRect(id)
	if !ok || c.mode != modeSoma {
		return
	}
	if time.Since(c.sc.last) < 16*time.Millisecond {
		return
	}
	c.sc.last = time.Now()
	c.swipeView(c.page.img, zr, dx)
	c.s.Flush()
}

// swipeFinish ends a swipe across zone id that had moved dx: far enough, the next (or the
// last) week, day or month slides in; else it settles back.
func (c *console) swipeFinish(id string, dx int) {
	zr, ok := c.swipeZoneRect(id)
	if !ok || c.mode != modeSoma {
		if abs(dx) >= 60 {
			c.pageSwipe(id, map[bool]int{true: -1, false: 1}[dx > 0])
		}
		return
	}
	w := zr.Dx()
	anim := c.animOK()
	if abs(dx) < min(120, w/6) { // not far: back where it was
		if anim {
			from := float64(dx)
			c.frames(140*time.Millisecond, func(t float64) { c.swipeView(c.page.img, zr, int(from*(1-t))) })
		}
		c.swipeView(c.page.img, zr, 0)
		c.s.Flush()
		return
	}
	dir := 1 // finger right to left: the next one
	if dx > 0 {
		dir = -1
	}
	if anim { // the old one leaves the way the finger went...
		from, to := float64(dx), float64(-dir*w)
		old := c.page.img
		c.frames(110*time.Millisecond, func(t float64) { c.swipeView(old, zr, int(from+(to-from)*t)) })
	}
	c.pageSwipe(id, dir)
	if anim { // ...and the new one comes in after it
		if nr, ok := c.swipeZoneRect(id); ok {
			from := float64(dir * nr.Dx())
			c.frames(150*time.Millisecond, func(t float64) { c.swipeView(c.page.img, nr, int(from*(1-t))) })
			c.swipeView(c.page.img, nr, 0)
			c.s.Flush()
		}
	}
}
