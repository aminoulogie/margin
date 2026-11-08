package main

import (
	"image"
	"math"
	"time"

	"margin-init/ui"
)

// Scrolling the terminal back, smoothly: the text follows the finger pixel by pixel, flings
// and slows as iOS's do, and comes to rest on a whole line.
//
// Before (2026-11-08, user: "scrolling ... is so glitchy and slow"), every finger move drew
// all 38-56 rows again glyph by glyph, for each move the touch screen sent, queued: the text
// lagged behind the finger and jumped a line at a time. Now each line is drawn once, turned
// into the framebuffer's own layout (a logical column is a native row), and kept by its
// number; a frame is then one copy per native row of the lines in view, made by one loop at
// the panel's pace for wherever the finger is now (moves in between are skipped, never
// queued), as pages scroll (fastscroll.go).

// termLine is a terminal line drawn and turned: for logical column x of the text, its ch
// pixels top to bottom at buf[x*ch:].
type termLine struct {
	buf []uint32
	n   int // the line's number (vt.Lines' count); for the screen's own lines, -1-row
	gen int // for the screen's lines: the terminal's change count when drawn
}

// termScroll is the scrollback's motion, beside termUI's line count (back).
type termScroll struct {
	want    float64 // pixels back from live the finger has the text at
	shown   int     // pixels back the screen shows (math.MinInt: not a frame of this move)
	on      bool    // a move is under way: frames show it
	looping bool
	motion  func(t float64) (float64, bool)
	motionT time.Time
	samples []termSample
	lines   map[int]*termLine
	cols    int // the width the kept lines were drawn at
	free    [][]uint32
	pill    *image.RGBA
	pillFor int
}

type termSample struct {
	t time.Time
	p float64
}

// termFast: the terminal's lines can be moved as turned pictures (the tablet's own layout).
func (c *console) termFast() bool {
	return c.s.rot == Rot90 && c.s.bpp == 32 && c.s.W == c.s.fbH
}

// termMaxBack is how far back the scrollback goes, in pixels.
func (c *console) termMaxBack() float64 { return float64(c.t.History() * c.ch) }

// termScrollTo follows the finger: the text shown off pixels back from the live screen.
// Caller holds drawMu.
func (c *console) termScrollTo(off float64) {
	ts := &c.ts
	ts.motion = nil
	if !ts.on {
		ts.on, ts.shown, ts.samples = true, math.MinInt, ts.samples[:0]
	}
	ts.want = clampf(off, 0, c.termMaxBack())
	now := time.Now()
	ts.samples = append(ts.samples, termSample{now, ts.want})
	for len(ts.samples) > 1 && now.Sub(ts.samples[0].t) > 100*time.Millisecond {
		ts.samples = ts.samples[1:]
	}
	if !c.termFast() { // no turned lines: a line at a time, as before
		if back := int(math.Round(ts.want / float64(c.ch))); back != c.tu.back {
			c.tu.back = back
			c.renderView()
			c.s.Flush()
		}
		return
	}
	c.termKick()
}

// termSpeed is how fast the finger was moving the text (pixels a second) as it lifted.
func (c *console) termSpeed() float64 {
	s := c.ts.samples
	if len(s) < 2 {
		return 0
	}
	a, b := s[0], s[len(s)-1]
	if time.Since(b.t) > 80*time.Millisecond { // it had stopped before lifting
		return 0
	}
	dt := b.t.Sub(a.t).Seconds()
	if dt <= 0 {
		return 0
	}
	return (b.p - a.p) / dt
}

// termRelease: the finger lifted from the moving text: it flings on, then rests on a line.
// Caller holds drawMu.
func (c *console) termRelease() {
	ts := &c.ts
	if !ts.on {
		return
	}
	at, v := ts.want, c.termSpeed()
	if !c.animOK() || !c.termFast() {
		c.termSettle(at)
		return
	}
	if math.Abs(v) >= flingMinSpeed {
		ts.motion = flingMotion(at, v, 0, c.termMaxBack())
	} else {
		ts.motion = c.termSnap(at)
	}
	ts.motionT = time.Now()
	c.termKick()
}

// termSnap glides from at to the nearest whole line.
func (c *console) termSnap(at float64) func(t float64) (float64, bool) {
	to := clampf(math.Round(at/float64(c.ch))*float64(c.ch), 0, c.termMaxBack())
	return easeTo(at, to, 0.14)
}

// termCatch: a finger lands on the text while it's still moving: it stops there, on a line.
// Caller holds drawMu.
func (c *console) termCatch() {
	if c.ts.on && !c.tu.down {
		at := c.ts.want
		if c.ts.shown != math.MinInt {
			at = float64(c.ts.shown)
		}
		c.termSettle(at)
	}
}

// termSettle ends the move on the line nearest off, and shows it (the live screen, with its
// cursor, at 0). Caller holds drawMu.
func (c *console) termSettle(off float64) {
	ts := &c.ts
	ts.on, ts.motion = false, nil
	back := min(max(int(math.Round(off/float64(c.ch))), 0), c.t.History())
	ts.want = float64(back * c.ch)
	if back == 0 {
		c.tu.back = 1 // goLive marks everything to be drawn again
		c.goLive()
	} else {
		c.tu.back = back
	}
	c.renderView()
	c.s.Flush()
}

// termKick starts the frame loop if it isn't running. Caller holds drawMu.
func (c *console) termKick() {
	if c.ts.looping {
		return
	}
	c.ts.looping = true
	go func() {
		for {
			drawMu.Lock()
			start := time.Now()
			drew, more := c.termNextFrame()
			if !more {
				c.ts.looping = false
			}
			drawMu.Unlock()
			if !more {
				return
			}
			if drew {
				c.frameTime(time.Since(start))
			}
			if !drew || !waitVsync(c.s) {
				time.Sleep(4 * time.Millisecond) // touches get in between frames
			}
		}
	}()
}

// termNextFrame draws the next frame if the text has somewhere new to be; more is false
// when the loop can stop. Caller holds drawMu.
func (c *console) termNextFrame() (drew, more bool) {
	ts := &c.ts
	if !ts.on {
		return false, false
	}
	if !c.screenOn || c.mode != modeTerminal || c.locked || c.pinMode != "" || c.tu.selecting {
		ts.on, ts.motion = false, nil // something else has the screen: it shows itself
		c.tu.back = min(max(int(math.Round(ts.want/float64(c.ch))), 0), c.t.History())
		return false, false
	}
	pos := ts.want
	if ts.motion != nil {
		var done bool
		pos, done = ts.motion(time.Since(ts.motionT).Seconds())
		if done {
			if p := math.Round(pos); int(p)%c.ch != 0 && p > 0 && p < c.termMaxBack() {
				ts.motion, ts.motionT = c.termSnap(p), time.Now() // on to a whole line
				return false, true
			}
			c.termSettle(pos)
			return true, false
		}
		ts.want = pos
	} else if !c.tu.down {
		c.termSettle(pos) // lifted with nothing left to move
		return true, false
	}
	p := int(math.Round(pos))
	if p == ts.shown {
		return false, ts.motion != nil // waiting for the finger: its next move kicks the loop
	}
	c.termFrame(p, true)
	return true, true
}

// termFrame shows the text off pixels back from the live screen, from the kept lines, and
// sends it (frame: as a moving picture). Caller holds drawMu.
func (c *console) termFrame(off int, frame bool) {
	s, ts, ch := c.s, &c.ts, c.ch
	first, hist, gen := c.t.Lines()
	if ts.cols != c.t.Cols || ts.lines == nil {
		clear(ts.lines)
		if ts.lines == nil {
			ts.lines = map[int]*termLine{}
		}
		ts.cols = c.t.Cols
	}
	off = min(max(off, 0), hist*ch)
	topPx := hist*ch - off // the page pixel (scrollback then screen) at the top of the text
	areaTop, areaBot := c.offY, c.offY+c.t.Rows*ch
	if c.cb.open {
		areaBot = min(areaBot, c.cb.rect.Min.Y)
	}
	type seg struct {
		buf      []uint32 // nil: blank
		sy, a, n int      // screen row sy shows the line's rows a..a+n-1
	}
	var segs []seg
	l0 := topPx / ch
	for l := l0; ; l++ {
		y := areaTop + l*ch - topPx
		if y >= areaBot {
			break
		}
		a := max(areaTop-y, 0)
		n := min(ch, areaBot-y) - a
		if n <= 0 {
			continue
		}
		var buf []uint32
		if l < hist+c.t.Rows {
			buf = c.termLineAt(l, first, hist, gen).buf
		}
		segs = append(segs, seg{buf, y + a, a, n})
	}
	c.termTrim(first+l0-c.t.Rows, first+l0+2*c.t.Rows)
	w := c.t.Cols * c.cw
	bg := s.pack(consoleBG.R, consoleBG.G, consoleBG.B)
	eachColumn(w, func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			fy := s.fbH - 1 - (c.offX + x)
			dst := words(s.buf[fy*s.stride:])[:s.fbW]
			for _, g := range segs {
				if g.buf == nil {
					fillWords(dst[g.sy:g.sy+g.n], bg)
					continue
				}
				copy(dst[g.sy:g.sy+g.n], g.buf[x*ch+g.a:x*ch+g.a+g.n])
			}
		}
	})
	s.markRows(s.fbH-c.offX-w, s.fbH-1-c.offX)
	ts.shown = off
	if back := (off + ch/2) / ch; back > 0 { // where you are in the scrollback
		c.termPill(back)
	}
	s.frame = frame
	s.Flush()
	s.frame = false
}

// termPill draws "↑ N lines back · tap to return" over the top of the text.
func (c *console) termPill(back int) {
	ts := &c.ts
	if ts.pill == nil || ts.pillFor != back {
		f := apple()
		label := "↑ " + itoa(back) + " lines back · tap to return"
		w := ui.TextWidth(f.caption, label) + 48
		pill := image.NewRGBA(image.Rect(0, 0, w, 52))
		ui.Fill(pill, pill.Rect, consoleBG)
		ui.RoundRect(pill, pill.Rect, 26, rgb(0x3a3a3c))
		ui.DrawTextCentered(pill, f.caption, w/2, 26, rgb(0xffffff), label)
		ts.pill, ts.pillFor = pill, back
	}
	c.s.blitRGBA(ts.pill, (c.s.W-ts.pill.Rect.Dx())/2, c.offY+8)
}

// termLineAt is page line l (0 = the oldest kept) drawn and turned, kept for next time.
func (c *console) termLineAt(l, first, hist, gen int) *termLine {
	ts := &c.ts
	key := first + l // a scrollback line keeps its number
	if l >= hist {
		key = -1 - (l - hist) // the screen's row l-hist: drawn again after any change
	}
	if ln := ts.lines[key]; ln != nil && (key >= 0 || ln.gen == gen) {
		return ln
	}
	cells := c.t.View(l-hist, 0)
	w := c.t.Cols * c.cw
	img := image.NewRGBA(image.Rect(0, 0, w, c.ch))
	for x, cell := range cells {
		fg, bg := colorOf(cell.FG, consoleFG, cell.Bold), colorOf(cell.BG, consoleBG, false)
		c.putGlyph(img, x*c.cw, cell.Ch, fg, bg, cell.Bold)
	}
	ln := ts.lines[key]
	if ln == nil {
		ln = &termLine{}
		if n := len(ts.free); n > 0 && len(ts.free[n-1]) == w*c.ch {
			ln.buf, ts.free = ts.free[n-1], ts.free[:n-1]
		} else {
			ln.buf = make([]uint32, w*c.ch)
		}
		ts.lines[key] = ln
	}
	ln.n, ln.gen = key, gen
	s := c.s
	rs, gs, bs := s.red.offset, s.green.offset, s.blue.offset
	rl, gl, bl := 8-s.red.length, 8-s.green.length, 8-s.blue.length
	for y := 0; y < c.ch; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			i := 4 * x
			ln.buf[x*c.ch+y] = uint32(row[i])>>rl<<rs | uint32(row[i+1])>>gl<<gs | uint32(row[i+2])>>bl<<bs
		}
	}
	return ln
}

// termTrim lets go of kept scrollback lines far from those numbered lo..hi (what's in view and
// a screen either side), keeping their pictures for reuse.
func (c *console) termTrim(lo, hi int) {
	ts := &c.ts
	if len(ts.lines) <= 4*c.t.Rows {
		return
	}
	for k, ln := range ts.lines {
		if k >= 0 && (k < lo || k > hi) {
			delete(ts.lines, k)
			if len(ts.free) < 2*c.t.Rows {
				ts.free = append(ts.free, ln.buf)
			}
		}
	}
}
