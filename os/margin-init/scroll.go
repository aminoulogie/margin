package main

import (
	"image"
	"strings"
	"time"

	"margin-init/ui"
)

// Scrolling, as on Kindle and Apple Books: a page taller than the screen moves up and down
// under the finger, and rows of covers move sideways. The page is drawn once, tall; scrolling
// up and down only copies a different window of it to the screen, so it costs no redraw. A
// sideways row is drawn at its offset, so moving it redraws the page (a few times a second
// while the finger moves, and once when it lifts). The band at the top (the tab bar) stays
// put. A touch that hasn't moved is a tap, as before.

// hrow is a row of a page that scrolls sideways: rect in page coordinates, and the width of
// everything in it.
type hrow struct {
	id       string
	r        image.Rectangle
	contentW int
	paint    func(img *image.RGBA) // redraws the row at its current offset
	covers   map[string]bool       // the covers in it (repainted alone when one arrives)
	snap     int                   // the pitch of its covers: a flicked row comes to rest on one
}

// scrollState: per-mode scroll positions, per-row offsets, and the finger moving them.
type scrollState struct {
	y     map[mode]int
	x     map[string]int
	drag  bool // a finger is down on a page
	moved bool // ...and moving it scrolls (no tap)
	vert  bool
	row   *hrow
	x0    int
	y0    int
	from  int // scroll or row offset when the finger touched down
	last  time.Time

	// The GPU (gpuscroll.go): the page is shown from its copy on the GPU while gpuOn.
	gpuOn    bool
	texGen   int   // c.pageGen of the page on the GPU (0: none)
	texFail  int   // c.pageGen of a page the GPU couldn't take
	resident []int // its slices on the GPU, least recently used first

	strip     string // the row on the GPU drawn whole, to slide sideways
	stripGen  int    // c.pageGen it was drawn at
	stripOn   bool   // the overlay shows it (it's being slid)
	stripFail string // row@pageGen the GPU couldn't take
	fling     int    // bumped to stop a fling
	longGen   int    // bumped by each touch: a long press fires only for its own
	longFired bool   // the finger was held: its lift isn't a tap
	samples   []scrollSample
	ox0, oy0  int // where the finger touched down (x0, y0 move to where a scroll took over)

	// The processor's frames (fastscroll.go).
	turn     turnedPage
	cstrip   rowStrip
	cpuOn    bool    // the processor is moving the page or a row: the finger, or a motion
	looping  bool    // its frame loop is running
	want     float64 // where the finger has taken it (past an end: stretched)
	shown    int     // where the last frame put it
	mpage    *page   // the page, and its drawing, being moved
	mgen     int
	motion   func(t float64) (float64, bool) // a fling, a spring back, a glide: position at t s
	motionT  time.Time
	frameN   int
	frameSum time.Duration
	zone     string // the swipe zone the finger touched down in ("" none)
	swiping  bool   // ...and it's moving sideways across it
}

const (
	scrollSlop    = 20 // pixels a finger moves before it's a scroll, not a tap
	scrollEvery   = 33 * time.Millisecond
	rowDrawEvery  = 70 * time.Millisecond
	scrollbarWide = 6
)

func (c *console) scrollY() int {
	if c.sc.y == nil {
		c.sc.y = map[mode]int{}
	}
	return c.sc.y[c.mode]
}

// setScroll puts a mode's page at y (0 = the top).
func (c *console) setScroll(m mode, y int) {
	c.scrollY()
	c.sc.y[m] = y
}

// rowOffset is how far row id is scrolled sideways (pages read it while drawing).
func (c *console) rowOffset(id string) int {
	if c.sc.x == nil {
		c.sc.x = map[string]int{}
	}
	return c.sc.x[id]
}

// addRow registers a sideways row on p.
func (p *page) addRow(id string, r image.Rectangle, contentW int, paint func(*image.RGBA)) {
	p.rows = append(p.rows, hrow{id: id, r: r, contentW: contentW, paint: paint})
}

// setRowButtons replaces a row's buttons (they move with it).
func (p *page) setRowButtons(id string, btns []button) {
	if p.rowBtns == nil {
		p.rowBtns = map[string][]button{}
	}
	p.rowBtns[id] = btns
}

// viewH is the height of the page area under the status bar.
func (c *console) viewH() int { return c.s.H - c.barH }

// pageYAt turns a screen point's y into page coordinates: as pageY, except in the fixed
// column at the left (the rail), which doesn't scroll either.
func (c *console) pageYAt(x, y int) int {
	if c.page != nil && x < c.page.left {
		if py := y - c.barH; c.page.footer == 0 || py < c.viewH()-c.page.footer || c.page.img.Rect.Dy() <= c.viewH() {
			return py
		}
	}
	return c.pageY(y)
}

// pageY turns a screen y into page coordinates (the fixed header doesn't scroll).
func (c *console) pageY(y int) int {
	py := y - c.barH
	if c.page != nil && c.page.footer > 0 && py >= c.viewH()-c.page.footer && c.page.img.Rect.Dy() > c.viewH() {
		return c.page.img.Rect.Dy() - (c.viewH() - py) // the fixed footer: the page's last rows
	}
	if c.page != nil && py >= c.page.header && c.page.img.Rect.Dy() > c.viewH() { // as blitPage shows it
		py += c.scrollY()
	}
	return py
}

// blitPage copies the visible window of the current page to the screen. Caller holds drawMu.
func (c *console) blitPage() {
	img := c.page.img
	vh := c.viewH()
	if img.Rect.Dy() <= vh {
		c.s.blitRGBA(img, 0, c.barH)
		return
	}
	sy := min(max(c.scrollY(), 0), img.Rect.Dy()-vh)
	c.sc.y[c.mode] = sy
	hh := c.page.header
	c.blitWindow(c.page, sy)
	c.keepShown(sy) // already turned: scrolling starts from it
	// Where you are on the page: a thin bar on the right, while scrolling.
	if c.sc.drag && c.sc.moved && c.sc.vert {
		track := vh - hh - 40
		thumb := max(track*vh/img.Rect.Dy(), 60)
		ty := c.barH + hh + 20 + (track-thumb)*sy/max(img.Rect.Dy()-vh, 1)
		bar := image.NewRGBA(image.Rect(0, 0, scrollbarWide, thumb))
		ui.Fill(bar, bar.Rect, blend(apBG, apLabel, 0.45))
		c.s.blitRGBA(bar, c.s.W-scrollbarWide-6, ty)
	}
}

// blitWindow copies page p, scrolled to sy, to the screen: its window, fixed header, footer and
// rail. Caller holds drawMu.
func (c *console) blitWindow(p *page, sy int) {
	img := p.img
	vh := c.viewH()
	if img.Rect.Dy() <= vh {
		c.s.blitRGBA(img, 0, c.barH)
		return
	}
	hh, fh := p.header, p.footer
	c.s.blitRGBA(img.SubImage(image.Rect(0, sy+hh, img.Rect.Dx(), sy+vh-fh)).(*image.RGBA), 0, c.barH+hh)
	if fh > 0 {
		c.s.blitRGBA(img.SubImage(image.Rect(0, img.Rect.Dy()-fh, img.Rect.Dx(), img.Rect.Dy())).(*image.RGBA), 0, c.barH+vh-fh)
	}
	if hh > 0 {
		c.s.blitRGBA(img.SubImage(image.Rect(0, 0, img.Rect.Dx(), hh)).(*image.RGBA), 0, c.barH)
	}
	if l := p.left; l > 0 { // the rail, as at the page's top
		c.s.blitRGBA(img.SubImage(image.Rect(0, hh, l, vh-fh)).(*image.RGBA), 0, c.barH+hh)
	}
}

// pageTouch handles a finger on a page (not the reader, terminal or a keyboard): scroll the
// page or a row, or tap. Caller holds drawMu.
func (c *console) pageTouch(p TouchPoint) {
	sc := &c.sc
	switch {
	case p.Down:
		if c.page == nil {
			return
		}
		if c.composerTap(p.X, p.Y) { // the new to-do's day, time, priority, Add
			sc.drag = false
			return
		}
		c.stopMotion() // a finger on a moving page catches it
		c.pressClear(true)
		sc.fling++ // ...and on a flinging one (the GPU's overlay stays until it lifts)
		sc.drag, sc.moved, sc.row, sc.samples = true, false, nil, nil
		// Held still for a moment: a long press (as on iOS); a moment less: shown pressed.
		sc.longGen++
		sc.longFired = false
		gen, lx, ly := sc.longGen, p.X, p.Y
		if c.page.header > 0 && p.Y-c.barH < c.page.header {
			c.pressShow(p.X, p.Y) // the tab bar doesn't scroll: shown at once
		}
		time.AfterFunc(pressDelay, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if sc.longGen == gen && sc.drag && !sc.moved && c.screenOn {
				c.pressShow(lx, ly)
			}
		})
		time.AfterFunc(longPressAfter, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if sc.longGen == gen && sc.drag && !sc.moved && c.screenOn {
				c.pressClear(true)
				sc.longFired = c.pageLongPress(lx, ly)
			}
		})
		sc.x0, sc.y0, sc.ox0, sc.oy0, sc.from = p.X, p.Y, p.X, p.Y, c.scrollY()
		// The tab bar and the workstation's rail answer as the finger lands, as a switch
		// does: nothing there scrolls, so there's no telling a tap from a scroll to wait for.
		fixed := p.Y-c.barH < c.page.header || p.X < c.page.left
		if id, _ := c.page.hitRect(p.X, c.pageYAt(p.X, p.Y)); fixed && instantTap(id) {
			sc.longGen++
			sc.drag = false
			if !c.press.on {
				c.pressShow(p.X, p.Y)
			}
			c.pressClear(false)
			c.pageTap(p.X, p.Y)
			c.s.Flush()
			return
		}
		c.edgeDown(p.X, p.Y)
		py := c.pageY(p.Y)
		sc.zone, sc.swiping = "", false
		for _, z := range c.page.swipes {
			if (image.Point{p.X, py}).In(z.r) {
				sc.zone = z.id
			}
		}
		for i := range c.page.rows {
			if r := &c.page.rows[i]; (image.Point{p.X, py}).In(r.r) && r.contentW > r.r.Dx() {
				sc.row = r
			}
		}
	case p.Moved && sc.drag && c.drag.on:
		c.dragDraw(p.X, p.Y) // a to-do being dragged follows the finger
	case p.Moved && sc.drag && c.sheetOpen():
		// Nothing under a sheet moves; a finger that slid away isn't a tap.
		if !sc.moved && (abs(p.X-sc.x0) >= scrollSlop || abs(p.Y-sc.y0) >= scrollSlop) {
			sc.moved = true
			c.pressClear(true)
		}
	case p.Moved && sc.drag:
		if c.edge.on {
			c.edgeMove(p.X, p.Y)
			return
		}
		dx, dy := p.X-sc.x0, p.Y-sc.y0
		if !sc.moved {
			if abs(dx) < scrollSlop && abs(dy) < scrollSlop {
				return
			}
			sc.moved = true
			c.pressClear(true)
			if sc.zone != "" && sc.row == nil && abs(dx) > abs(dy) { // a sideways swipe: no scrolling
				sc.swiping, sc.vert = true, false
				sc.x0 += scrollSlop * sign(dx) // it moves from here, without a jump
				return
			}
			if c.edgeStart(dx, dy) { // in from the left edge: going back
				c.edgeMove(p.X, p.Y)
				return
			}
			sc.vert = sc.row == nil || abs(dy) > abs(dx)
			if !sc.vert {
				sc.from = c.rowOffset(sc.row.id)
			}
			// The page moves from here on, without first jumping by the slop.
			if sc.vert {
				sc.y0 += scrollSlop * sign(dy)
			} else {
				sc.x0 += scrollSlop * sign(dx)
			}
			dx, dy = p.X-sc.x0, p.Y-sc.y0
		}
		if sc.swiping {
			c.swipeFollow(sc.zone, dx) // the week (the day, the month) moves with the finger
			return
		}
		if c.gpuScrolls() { // the GPU follows the finger, every frame
			switch {
			case sc.vert && c.page.img.Rect.Dy() <= c.viewH():
				return
			case sc.vert:
				if c.gpuScrollTo(sc.from - dy) {
					c.sample(c.scrollY())
					return
				}
			default:
				r := sc.row
				if c.gpuRowTo(r, min(max(sc.from-dx, 0), max(r.contentW-r.r.Dx(), 0))) {
					c.sample(c.rowOffset(r.id))
					return
				}
			}
		}
		if !c.cfg.SmoothScroll {
			return // paged: the page jumps when the finger lifts
		}
		if c.fastScrolls() && c.cpuFollow(dx, dy) {
			return // the frame loop shows it, as often as the panel can
		}
		if time.Since(sc.last) < map[bool]time.Duration{true: scrollEvery, false: rowDrawEvery}[sc.vert] {
			return // the screen can't keep up with every move: draw a few times a second
		}
		c.scrollTo(dx, dy)
	case p.Up:
		if !sc.drag {
			return
		}
		sc.longGen++
		if c.drag.on { // a dragged to-do: let go here
			sc.longFired, sc.drag = false, false
			c.dragDrop(p.X, p.Y)
			return
		}
		if sc.longFired { // held: the long press did its thing, the lift isn't a tap
			sc.longFired, sc.drag = false, false
			if sc.gpuOn {
				c.gpuSettle()
			}
			return
		}
		moved := sc.moved
		if moved && c.sheetOpen() {
			sc.drag = false
			return
		}
		if sc.swiping { // across a zone: the next (or the last) week, day, month
			sc.drag, sc.swiping = false, false
			if sc.gpuOn {
				c.gpuSettle()
			}
			c.swipeFinish(sc.zone, p.X-sc.x0)
			return
		}
		if c.edge.on {
			sc.drag = false
			c.edgeUp(p.X)
			return
		}
		if sc.gpuOn {
			sc.drag = false
			if moved {
				c.gpuRelease()
				return
			}
			c.gpuSettle()
			if !moved {
				c.pageTap(p.X, p.Y)
			}
			return
		}
		if sc.cpuOn {
			sc.drag = false
			c.cpuRelease()
			return
		}
		switch {
		case moved && !c.cfg.SmoothScroll:
			sc.drag = false
			c.pageJump(p.X-sc.ox0, p.Y-sc.oy0)
			return
		case moved:
			c.scrollTo(p.X-sc.x0, p.Y-sc.y0)
		}
		sc.drag = false
		if moved {
			if sc.vert {
				c.blitPage() // without the scroll bar
				c.s.Flush()
			}
			return
		}
		if !c.press.on { // a quick tap: shown pressed while what it opens is drawn
			c.pressShow(p.X, p.Y)
		}
		c.pressClear(false)
		c.pageTap(p.X, p.Y)
		c.s.Flush()
	}
}

// sheetOpen: a sheet is open over the page (work_ui.go); the page under it doesn't move.
func (c *console) sheetOpen() bool { return c.mode == modeSoma && c.somaUI.sheet != "" }

// instantTap: a button that does its thing on touch-down (the tabs and the rail).
func instantTap(id string) bool {
	return strings.HasPrefix(id, "tab:") || strings.HasPrefix(id, "wk:go:")
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

// scrollTo moves the page (or the row under the finger) by the finger's travel and shows it.
func (c *console) scrollTo(dx, dy int) {
	sc := &c.sc
	sc.last = time.Now()
	if sc.vert {
		if c.page.img.Rect.Dy() <= c.viewH() {
			return
		}
		sc.y[c.mode] = min(max(sc.from-dy, 0), c.page.img.Rect.Dy()-c.viewH())
		c.blitPage()
		c.s.Flush()
		return
	}
	r := sc.row
	off := min(max(sc.from-dx, 0), max(r.contentW-r.r.Dx(), 0))
	if off != c.rowOffset(r.id) {
		c.sc.x[r.id] = off
		r.paint(c.page.img) // just this row, at its new offset
		c.pagePainted(r.r.Min.Y, r.r.Max.Y)
		c.blitPage()
		c.s.Flush()
	}
}

// pageJump is paged scrolling, as on a Kindle: a swipe moves the page by a screen (or a row by
// the covers it shows), in one redraw. Following the finger costs a full-screen copy per step,
// which this tablet's processor can do only a few times a second.
func (c *console) pageJump(dx, dy int) {
	sc := &c.sc
	if sc.vert {
		ih, vh := c.page.img.Rect.Dy(), c.viewH()
		if ih <= vh {
			return
		}
		step := vh - c.page.header - 160 // keep a little of the last screen in view
		if dy > 0 {
			step = -step // finger down: back up the page
		}
		y := min(max(c.scrollY()+step, 0), ih-vh)
		if y == c.scrollY() {
			return
		}
		if c.animOK() && c.fastScrolls() { // it glides there
			c.startMotion(easeTo(float64(c.scrollY()), float64(y), 0.32))
			return
		}
		sc.y[c.mode] = y
		c.blitPage()
		c.s.Flush()
		return
	}
	r := sc.row
	step := (r.r.Dx() - 2*48) / (rowCW + rowGap) * (rowCW + rowGap) // the covers on screen
	if step <= 0 || r.id == "s:topics" {
		step = r.r.Dx() / 2
	}
	if dx > 0 {
		step = -step // finger right: back to the start of the row
	}
	off := min(max(c.rowOffset(r.id)+step, 0), max(r.contentW-r.r.Dx(), 0))
	if off == c.rowOffset(r.id) {
		return
	}
	if c.animOK() && c.fastScrolls() && c.cpuStrip(r) != nil { // the covers glide along
		c.startMotion(easeTo(float64(c.rowOffset(r.id)), float64(off), 0.32))
		return
	}
	sc.x[r.id] = off
	r.paint(c.page.img)
	c.pagePainted(r.r.Min.Y, r.r.Max.Y)
	c.blitPage()
	c.s.Flush()
}

const longPressAfter = 550 * time.Millisecond

// pageLongPress handles a finger held still on a page. True if it did something. Caller
// holds drawMu.
func (c *console) pageLongPress(x, y int) bool {
	if c.page == nil {
		return false
	}
	id := c.page.hit(x, c.pageYAt(x, y))
	switch {
	case c.mode == modeBooksHome && !c.homeEdit && id != "" && c.pageY(y) > tabsH+40:
		// Home's widgets: rearrange them.
		c.homeEdit = true
		vibrate(20)
		c.showPage()
		return true
	case c.mode == modeSoma && !c.somaUI.typing && c.somaUI.sheet == "" && c.dragStart(id, x, y):
		return true // a to-do lifted, to be dragged
	case c.mode == modeSoma && strings.HasPrefix(id, "wk:"):
		name, arg, _ := strings.Cut(strings.TrimPrefix(id, "wk:"), ":")
		f := wkLongPress[name]
		if f == nil {
			return false
		}
		if c.somaUI.typing {
			c.somaDone()
		}
		f(c, arg)
		vibrate(20)
		c.showPage()
		return true
	case c.mode == modeSoma && strings.HasPrefix(id, "so:todo:"):
		c.somaTodoMenu(strings.TrimPrefix(id, "so:todo:"))
		vibrate(20)
		return true
	}
	return false
}
