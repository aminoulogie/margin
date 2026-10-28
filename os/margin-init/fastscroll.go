package main

import (
	"image"
	"log"
	"math"
	"runtime"
	"sync"
	"time"
)

// Scrolling on the processor, at the panel's pace. The tablet is used in portrait on a
// landscape framebuffer, so drawing a page means turning every pixel a quarter: following
// the finger that way redrew 2.3 million pixels per move, a few times a second at best. So a
// tall page is turned once, as it first comes into view, into the framebuffer's own layout,
// where a logical column is a native row: a frame scrolled to any y is then one copy per
// native row, as cheap as a transition's frame. A sideways row is drawn whole (every cover)
// and turned the same way, so sliding it is a copy too.
//
// Frames come from one loop, for wherever the finger (or a fling) is now: moves in between
// are skipped, never queued, and each frame waits for the panel's vertical blank. Lifting
// with speed flings the page, slowing as on iOS; past an end the page stretches like a
// rubber band and springs back; rows come to rest on a cover; paged swipes glide.

// turnedPage is the current page turned a quarter into the framebuffer's layout, filled in
// as its rows are first shown.
type turnedPage struct {
	page *page
	gen  int
	s    *Screen // the page turned: fbW = the page's height, fbH = its width
	done []bool  // page rows already turned
}

// rowStrip is a sideways row drawn whole and turned, for sliding on the processor.
type rowStrip struct {
	id   string
	page *page
	gen  int
	s    *Screen // fbW = the row's height, fbH = everything in it, side to side
	bg   uint32  // the row's background, shown past its ends
}

var turnBuf, stripBuf []byte // reused: a fresh 40 MB picture per page would keep the GC busy

const (
	rowTau   = 0.3  // seconds: how far a flicked row carries before it settles on a cover
	bounceW  = 14.0 // how stiff the bounce at an end is (1/s)
	maxBounc = 2500 // pixels a second: the fastest a fling goes into its bounce
)

// fastScrolls: the processor can scroll the current page from its turned copy: portrait on
// the tablet's 32 bpp framebuffer, the screen not shown by the GPU, a page as wide as it.
func (c *console) fastScrolls() bool {
	return c.page != nil && c.disp == nil && c.s.rot == Rot90 && c.s.bpp == 32 && c.s.W == c.s.fbH &&
		c.page.img.Rect.Dx() == c.s.W && c.page.img.Rect.Min == (image.Point{}) &&
		c.page.img.Rect.Dy() <= pageMaxRows
}

// turned is the current page's turned copy, started afresh for each drawing of a page.
func (c *console) turned() *turnedPage {
	t := &c.sc.turn
	h := c.page.img.Rect.Dy()
	if t.page == c.page && t.gen == c.pageGen && t.s != nil && t.s.fbW == h {
		return t
	}
	n := 4 * h * c.s.W
	if cap(turnBuf) < n {
		turnBuf = make([]byte, n)
	}
	t.s = c.turnScreen(turnBuf[:n], h, c.s.W)
	t.page, t.gen = c.page, c.pageGen
	if cap(t.done) < h {
		t.done = make([]bool, h)
	}
	t.done = t.done[:h]
	clear(t.done)
	return t
}

// turnScreen is a Screen over buf for a picture w logical pixels wide and h tall, turned as
// the tablet's screen is (a logical column is a native row).
func (c *console) turnScreen(buf []byte, h, w int) *Screen {
	return &Screen{buf: buf, stride: 4 * h, bpp: 32, fbW: h, fbH: w, W: w, H: h, rot: Rot90,
		red: c.s.red, green: c.s.green, blue: c.s.blue, dirtyLo: w, dirtyHi: -1}
}

// ensure turns the page's rows y0..y1-1 that aren't yet.
func (t *turnedPage) ensure(y0, y1 int) {
	img := t.page.img
	y0, y1 = max(y0, 0), min(y1, len(t.done))
	for y := y0; y < y1; {
		if t.done[y] {
			y++
			continue
		}
		e := y
		for e < y1 && !t.done[e] {
			t.done[e] = true
			e++
		}
		t.s.blitRGBA(img.SubImage(image.Rect(0, y, img.Rect.Dx(), e)).(*image.RGBA), 0, y)
		y = e
	}
}

// keep takes page rows y0..y1-1 from the screen, where blitPage has just drawn them at
// screen row sy0 on: turned already, they cost a copy.
func (t *turnedPage) keep(s *Screen, y0, y1, sy0 int) {
	y0, y1 = max(y0, 0), min(y1, len(t.done))
	if y0 >= y1 {
		return
	}
	eachColumn(s.W, func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			fy := s.fbH - 1 - x
			copy(t.s.buf[fy*t.s.stride+4*y0:fy*t.s.stride+4*y1], s.buf[fy*s.stride+4*sy0:])
		}
	})
	for y := y0; y < y1; y++ {
		t.done[y] = true
	}
}

// pagePainted: rows y0..y1-1 of the page were drawn again in place (a row slid, a cover
// arrived); its turned copy takes them again when they're next shown. Caller holds drawMu.
func (c *console) pagePainted(y0, y1 int) {
	t := &c.sc.turn
	if t.page != c.page || t.gen != c.pageGen {
		return
	}
	for y := max(y0, 0); y < min(y1, len(t.done)); y++ {
		t.done[y] = false
	}
}

// keepShown notes what blitPage just drew, the window of a tall page. Caller holds drawMu.
func (c *console) keepShown(sy int) {
	if !c.fastScrolls() || c.page.img.Rect.Dy() <= c.viewH() {
		return
	}
	t := c.turned()
	hh, fh, vh, ih := c.page.header, c.page.footer, c.viewH(), c.page.img.Rect.Dy()
	t.keep(c.s, 0, hh, c.barH)
	t.keep(c.s, sy+hh, sy+vh-fh, c.barH+hh)
	if fh > 0 {
		t.keep(c.s, ih-fh, ih, c.barH+vh-fh)
	}
}

// eachColumn runs f over the logical columns [0, w) (native rows) in bands, on the
// processor's threads at once: the bands share no memory.
func eachColumn(w int, f func(x0, x1 int)) {
	n := min(runtime.GOMAXPROCS(0), 4)
	if n <= 1 || w < 256 {
		f(0, w)
		return
	}
	var wg sync.WaitGroup
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f(w*k/n, w*(k+1)/n)
		}()
	}
	wg.Wait()
}

func fillWords(w []uint32, v uint32) {
	for i := range w {
		w[i] = v
	}
}

// pageBottom is the screen row where the page stops showing: the top of a keyboard (and of
// the bar of what's typed) when one is up over a page, else the screen's bottom.
func (c *console) pageBottom() int {
	switch {
	case c.skb == nil || !c.skb.visible:
	case c.mode == modeStore && c.store.typing:
		return c.skb.y0
	default:
		if _, ok := c.pageTyping(); ok {
			return c.skb.y0 - typeBarH
		}
	}
	return c.s.H
}

// scrollFrame draws the page scrolled to y from its turned copy into the screen buffer
// (the caller flushes). y may run past the page's ends by a rubber band's stretch: the gap
// shows the page's background. bar: with the scroll bar. Caller holds drawMu.
func (c *console) scrollFrame(y int, bar bool) {
	t := c.turned()
	s := c.s
	ih, vh, hh, fh := c.page.img.Rect.Dy(), c.viewH(), c.page.header, c.page.footer
	bottom := min(c.pageBottom()-c.barH, vh)
	end := min(vh-fh, bottom) // the scrolling part: view rows hh..end-1
	lo, hi := y+hh, y+end     // the page rows it shows
	cs, ce := max(lo, hh), min(hi, ih-fh)
	t.ensure(0, hh)
	t.ensure(cs, ce)
	t.ensure(hh, hh+1)
	t.ensure(ih-fh-1, ih-fh)
	footer := fh > 0 && bottom >= vh
	if footer {
		t.ensure(ih-fh, ih)
	}
	px := func(x, y int) uint32 { return words(t.s.buf)[(t.s.fbH-1-x)*t.s.fbW+min(max(y, 0), ih-1)] }
	top, low := px(1, hh), px(1, ih-fh-1) // the background past the ends
	bh := c.barH
	eachColumn(s.W, func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			fy := s.fbH - 1 - x
			dst := words(s.buf[fy*s.stride:])[:s.fbW]
			src := words(t.s.buf[fy*t.s.stride:])[:ih]
			copy(dst[bh:bh+hh], src[:hh])
			if a := min(cs, hi); lo < a { // above the page's top
				fillWords(dst[bh+lo-y:bh+a-y], top)
			}
			if cs < ce {
				copy(dst[bh+cs-y:bh+ce-y], src[cs:ce])
			}
			if a := max(ce, lo); a < hi { // below its end
				fillWords(dst[bh+a-y:bh+hi-y], low)
			}
			if footer {
				copy(dst[bh+vh-fh:bh+vh], src[ih-fh:])
			}
		}
	})
	if bar && ih > vh {
		c.scrollBarAt(min(max(y, 0), ih-vh))
	}
	s.markRows(0, s.fbH-1)
}

// scrollBarAt draws the thin bar showing where on the page the screen is, a capsule.
func (c *console) scrollBarAt(sy int) {
	s, ih, vh, hh := c.s, c.page.img.Rect.Dy(), c.viewH(), c.page.header
	track := vh - hh - 40
	thumb := max(track*vh/ih, 60)
	ty := c.barH + hh + 20 + (track-thumb)*sy/max(ih-vh, 1)
	col := blend(apBG, apLabel, 0.45)
	v := s.pack(col.R, col.G, col.B)
	for k := range scrollbarWide {
		x := s.W - 6 - scrollbarWide + k
		cut := 2 - min(k, scrollbarWide-1-k, 2) // rounded ends
		fillWords(words(s.buf[(s.fbH-1-x)*s.stride:])[ty+cut:ty+thumb-cut], v)
	}
}

// cpuStrip is row r drawn whole and turned, made once per drawing of the page; nil if it's
// too big to keep.
func (c *console) cpuStrip(r *hrow) *rowStrip {
	st := &c.sc.cstrip
	if st.id == r.id && st.page == c.page && st.gen == c.pageGen && st.s != nil {
		return st
	}
	if r.contentW > rowStripCols || r.r.Dy() > rowStripRows || r.contentW < c.s.W {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, r.r.Min.Y, r.contentW, r.r.Max.Y))
	saved := c.rowOffset(r.id)
	c.sc.x[r.id] = 0
	r.paint(img) // at offset 0 on a picture as wide as the row: every cover
	c.sc.x[r.id] = saved
	h, w := r.r.Dy(), r.contentW
	n := 4 * h * w
	if cap(stripBuf) < n {
		stripBuf = make([]byte, n)
	}
	st.s = c.turnScreen(stripBuf[:n], h, w)
	st.s.blitRGBA(img, 0, 0)
	st.id, st.page, st.gen = r.id, c.page, c.pageGen
	st.bg = words(st.s.buf)[(w-1)*h] // logical (0, 0): the margin before the first cover
	return st
}

// rowFrame draws row r slid to off (which may run past its ends) from its strip into the
// screen buffer (the caller flushes). Caller holds drawMu.
func (c *console) rowFrame(r *hrow, st *rowStrip, off int) {
	s, sy := c.s, c.scrollY()
	if c.page.img.Rect.Dy() <= c.viewH() {
		sy = 0
	}
	end := min(c.viewH()-c.page.footer, c.pageBottom()-c.barH)
	v0, v1 := max(r.r.Min.Y, sy+c.page.header), min(r.r.Max.Y, sy+end)
	if v0 >= v1 {
		return
	}
	d0 := c.barH + v0 - sy
	c0, c1 := v0-r.r.Min.Y, v1-r.r.Min.Y
	w := st.s.fbH
	eachColumn(s.W, func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			dst := words(s.buf[(s.fbH-1-x)*s.stride:])[d0 : d0+v1-v0]
			if sx := off + x; sx >= 0 && sx < w {
				copy(dst, words(st.s.buf[(w-1-sx)*st.s.stride:])[c0:c1])
			} else {
				fillWords(dst, st.bg)
			}
		}
	})
	s.markRows(0, s.fbH-1)
}

// rubber: past an end, the page follows the finger less and less (iOS's rubber band); d is
// the size of the view it moves in.
func rubber(x, lo, hi, d float64) float64 {
	band := func(o float64) float64 { return (1 - 1/(o*0.55/d+1)) * d }
	switch {
	case x < lo:
		return lo - band(lo-x)
	case x > hi:
		return hi + band(x-hi)
	}
	return x
}

// moveRange is how far the page (or the row) being moved can go.
func (c *console) moveRange() (lo, hi float64) {
	if c.sc.vert {
		return 0, float64(max(c.page.img.Rect.Dy()-c.viewH(), 0))
	}
	r := c.sc.row
	return 0, float64(max(r.contentW-r.r.Dx(), 0))
}

func clampf(x, lo, hi float64) float64 { return math.Min(math.Max(x, lo), hi) }

// cpuFollow puts the page (or the row under the finger) where the finger has taken it, for
// the frame loop to show. False if this can't be done from a turned copy. Caller holds
// drawMu.
func (c *console) cpuFollow(dx, dy int) bool {
	sc := &c.sc
	raw := float64(sc.from - dx)
	size := float64(c.s.W)
	if sc.vert {
		if c.page.img.Rect.Dy() <= c.viewH() {
			return true // nothing to scroll
		}
		raw, size = float64(sc.from-dy), float64(c.viewH())
	} else if c.cpuStrip(sc.row) == nil {
		return false
	}
	lo, hi := c.moveRange()
	if !sc.cpuOn {
		sc.cpuOn, sc.mpage, sc.mgen, sc.shown = true, c.page, c.pageGen, math.MinInt
	}
	sc.motion = nil
	sc.want = rubber(raw, lo, hi, size)
	c.sample(int(raw))
	c.kickFrames()
	return true
}

// cpuRelease: the finger lifted from a page (or row) the processor was moving: fling it,
// spring it back from past an end, settle a row on a cover, or just stop. Caller holds drawMu.
func (c *console) cpuRelease() {
	sc := &c.sc
	at, v := sc.want, c.speed()
	lo, hi := c.moveRange()
	if !c.animOK() { // no motion: it stays where the finger left it, within its ends
		c.cpuSettle(int(math.Round(clampf(at, lo, hi))))
		return
	}
	switch {
	case !sc.vert && sc.row.snap > 0: // rows come to rest on a cover, as Books' shelves do
		snap := float64(sc.row.snap)
		to := clampf(math.Round((at+v*rowTau)/snap)*snap, lo, hi)
		c.startMotion(easeTo(at, to, 0.28+0.12*math.Min(math.Abs(to-at)/1000, 1)))
	case at < lo || at > hi: // stretched past an end: back to it
		edge := clampf(at, lo, hi)
		c.startMotion(springBack(at-edge, edge))
	case math.Abs(v) >= flingMinSpeed:
		c.startMotion(flingMotion(at, v, lo, hi))
	default:
		c.cpuSettle(int(math.Round(at)))
	}
}

func (c *console) startMotion(m func(t float64) (float64, bool)) {
	sc := &c.sc
	if !sc.cpuOn {
		sc.cpuOn, sc.mpage, sc.mgen, sc.shown = true, c.page, c.pageGen, math.MinInt
	}
	sc.motion, sc.motionT = m, time.Now()
	c.kickFrames()
}

// stopMotion: a finger catches a page or row in motion; it stays where it is. Caller holds
// drawMu.
func (c *console) stopMotion() {
	sc := &c.sc
	if !sc.cpuOn {
		return
	}
	if sc.mpage != c.page || sc.mgen != c.pageGen || c.page == nil {
		sc.cpuOn, sc.motion = false, nil
		return
	}
	p := c.scrollY()
	if !sc.vert {
		p = c.rowOffset(sc.row.id)
	}
	c.cpuSettle(p) // where the last frame put it, within the ends
}

// easeTo glides from a to b over d seconds, quick at first and landing softly.
func easeTo(a, b, d float64) func(t float64) (float64, bool) {
	return func(t float64) (float64, bool) {
		if t >= d*animScale || animScale <= 0 {
			return b, true
		}
		return a + (b-a)*spring(t/(d*animScale)), false
	}
}

// springBack returns from o past an edge to the edge, a critically damped spring.
func springBack(o, edge float64) func(t float64) (float64, bool) {
	const w = 12.0
	return func(t float64) (float64, bool) {
		x := o * (1 + w*t) * math.Exp(-w*t)
		if math.Abs(x) < 0.5 || t > 0.8 {
			return edge, true
		}
		return edge + x, false
	}
}

// flingMotion carries the page on from at at v pixels a second, slowing as iOS's do; if it
// reaches an end still moving, it runs a little past and bounces back.
func flingMotion(at, v, lo, hi float64) func(t float64) (float64, bool) {
	hitT, edge := -1.0, 0.0
	if reach := at + v*flingTau; reach > hi || reach < lo {
		edge = hi
		if reach < lo {
			edge = lo
		}
		hitT = -flingTau * math.Log(1-(edge-at)/(v*flingTau))
	}
	return func(t float64) (float64, bool) {
		if hitT < 0 || t < hitT {
			x := at + v*flingTau*(1-math.Exp(-t/flingTau))
			if math.Abs(v*math.Exp(-t/flingTau)) < flingStop {
				return clampf(x, lo, hi), true
			}
			return x, false
		}
		u := t - hitT
		ve := clampf(v*math.Exp(-hitT/flingTau), -maxBounc, maxBounc)
		o := ve * u * math.Exp(-bounceW*u)
		if u > 4/bounceW && math.Abs(o) < 0.5 {
			return edge, true
		}
		return edge + o, false
	}
}

// kickFrames starts the frame loop if it isn't running. Caller holds drawMu.
func (c *console) kickFrames() {
	if c.sc.looping {
		return
	}
	c.sc.looping = true
	go c.frameLoop()
}

// frameLoop draws a frame whenever the page (or row) has somewhere new to be, one per
// vertical blank, and ends when there's nothing left to show.
func (c *console) frameLoop() {
	for {
		drawMu.Lock()
		start := time.Now()
		drew, more := c.nextFrame()
		if !more {
			c.sc.looping = false
			drawMu.Unlock()
			return
		}
		drawMu.Unlock()
		if drew {
			c.frameTime(time.Since(start))
		}
		if !drew || !waitVsync(c.s) {
			time.Sleep(4 * time.Millisecond) // touches get in between frames
		}
	}
}

// nextFrame draws the next frame if the page or row has moved; more is false when the loop
// can stop. Caller holds drawMu.
func (c *console) nextFrame() (drew, more bool) {
	sc := &c.sc
	if !sc.cpuOn {
		return false, false
	}
	if !c.screenOn || c.page == nil || sc.mpage != c.page || sc.mgen != c.pageGen || c.locked || c.pinMode != "" {
		sc.cpuOn, sc.motion = false, nil // the page was drawn again (or covered): it shows itself
		return false, false
	}
	var pos float64
	done := false
	switch {
	case sc.motion != nil:
		pos, done = sc.motion(time.Since(sc.motionT).Seconds())
	case sc.drag:
		pos = sc.want
	default:
		return false, false
	}
	p := int(math.Round(pos))
	if done {
		lo, hi := c.moveRange()
		c.cpuSettle(int(math.Round(clampf(pos, lo, hi))))
		return true, false
	}
	if p == sc.shown {
		return false, sc.motion != nil // waiting for the finger: it kicks the loop again
	}
	c.cpuDraw(p, true)
	return true, true
}

// cpuDraw shows the page (or row) at p as a frame. Caller holds drawMu.
func (c *console) cpuDraw(p int, bar bool) {
	sc := &c.sc
	lo, hi := c.moveRange()
	in := int(clampf(float64(p), lo, hi))
	if sc.vert {
		sc.y[c.mode] = in // where a redraw would put it (a rubber band's stretch isn't kept)
		c.scrollFrame(p, bar)
	} else {
		sc.x[sc.row.id] = in
		c.rowFrame(sc.row, c.cpuStrip(sc.row), p)
	}
	sc.shown = p
	c.s.frame = true
	c.s.Flush()
	c.s.frame = false
}

// cpuSettle ends a move on the processor at p, within the page's or row's ends: a row is
// painted into the page there (its covers' buttons with it), and the screen gets its last
// frame. Caller holds drawMu.
func (c *console) cpuSettle(p int) {
	sc := &c.sc
	sc.cpuOn, sc.motion = false, nil
	if c.page == nil || sc.mpage != c.page || sc.mgen != c.pageGen {
		return
	}
	if sc.vert {
		sc.y[c.mode] = p
		if c.page.img.Rect.Dy() > c.viewH() {
			c.scrollFrame(p, false)
		}
	} else if r := sc.row; r != nil {
		sc.x[r.id] = p
		r.paint(c.page.img)
		c.pagePainted(r.r.Min.Y, r.r.Max.Y)
		if st := c.cpuStrip(r); st != nil {
			c.rowFrame(r, st, p)
		}
	}
	sc.shown = p
	c.s.Flush() // an ordinary flush: the shadow is taken again
}

// frameTime keeps an eye on how long frames take, logged now and then (init.log), so a
// slow screen shows in the log.
func (c *console) frameTime(d time.Duration) {
	sc := &c.sc
	sc.frameN++
	sc.frameSum += d
	if sc.frameN == 120 {
		log.Printf("scrolling: %v a frame on average", (sc.frameSum / 120).Round(100*time.Microsecond))
		sc.frameN, sc.frameSum = 0, 0
	}
}

// turnAhead: prefetchTurn works in the background (tests turn it off).
var turnAhead = true

// prefetchTurn turns the next screen of a tall page below the one shown, a little at a time
// while nothing else happens, so a fling meets rows already turned. Caller holds drawMu.
func (c *console) prefetchTurn() {
	if !turnAhead || !c.fastScrolls() || c.page.img.Rect.Dy() <= c.viewH() {
		return
	}
	gen, pg := c.pageGen, c.page
	var step func(from int)
	step = func(from int) {
		time.AfterFunc(120*time.Millisecond, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if c.page != pg || c.pageGen != gen || c.sc.drag || c.sc.cpuOn || !c.screenOn || !c.fastScrolls() {
				return
			}
			sy := c.scrollY()
			to := min(sy+2*c.viewH(), c.page.img.Rect.Dy())
			from = max(from, sy)
			if from >= to {
				return
			}
			c.turned().ensure(from, min(from+256, to))
			step(from + 256)
		})
	}
	step(c.scrollY() + c.viewH())
}
