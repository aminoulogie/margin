package main

import (
	"bytes"
	"fmt"
	"image"
	"testing"
	"time"

	"margin-init/ui"
)

// fastConsole: a tall page with a fixed header and footer, a sideways row, and something
// different on every band of rows, so a window off by one row shows. Caller holds drawMu.
func fastConsole(t testing.TB) *console {
	var c *console
	if tt, ok := t.(*testing.T); ok {
		c = testConsole(tt)
	} else {
		s := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
		var err error
		if c, err = newConsole(s); err != nil {
			t.Fatal(err)
		}
		c.cfg = savedSettings{Brightness: 80, ScreenOff: 5, Motion: 1}
		backgroundRedraws, homeLoads, turnAhead = false, false, false
	}
	c.mode = modeWords
	c.cfg.SmoothScroll = true
	var items []rowItem
	for k := 0; k < 20; k++ {
		items = append(items, rowItem{it: &storeItem{key: fmt.Sprint(k), title: fmt.Sprint("Book ", k)}, id: fmt.Sprint("b", k), caption: fmt.Sprint("Book ", k)})
	}
	img := canvas(c.s.W, 4000)
	ui.Fill(img, img.Rect, apBG)
	for y := 0; y < 4000; y += 37 {
		ui.Fill(img, image.Rect(y%300, y, y%300+500, y+19), rgb(uint32(0x101010*(y%13)+0x203040)))
	}
	ui.Fill(img, image.Rect(0, 0, c.s.W, tabsH), rgb(0x334455))
	ui.Fill(img, image.Rect(0, 4000-150, c.s.W, 4000), rgb(0x553322))
	c.page = &page{img: img, header: tabsH, footer: 150}
	c.coverRow(c.page, "row", 600, items, apBG)
	c.pageGen++
	c.blitPage()
	return c
}

// screenAt is what blitPage draws for the page at y: the processor's frames must match it.
func screenAt(c *console, y int) []byte {
	saved := append([]byte(nil), c.s.buf...)
	c.sc.y[c.mode] = y
	drag := c.sc.drag
	c.sc.drag = false
	c.blitPage()
	c.sc.drag = drag
	out := append([]byte(nil), c.s.buf...)
	copy(c.s.buf, saved)
	return out
}

// A frame from the turned page is exactly the page as blitPage draws it, wherever it's
// scrolled, the fixed header and footer included, and costs no turning once rows are kept.
func TestScrollFrameIsThePage(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(t)
	ih := c.page.img.Rect.Dy()
	for _, y := range []int{0, 1, 37, 600, 1999, ih - c.viewH() - 1, ih - c.viewH()} {
		want := screenAt(c, y)
		c.scrollFrame(y, false)
		if n := differ(c.s.buf, want, c.s, 0); n != 0 {
			t.Fatalf("scrolled to %d: %d pixels differ from the page", y, n)
		}
	}
	// A row drawn again in place is taken again.
	c.sc.x["row"] = 300
	r := c.rowByID("row")
	r.paint(c.page.img)
	c.pagePainted(r.r.Min.Y, r.r.Max.Y)
	want := screenAt(c, 200)
	c.scrollFrame(200, false)
	if n := differ(c.s.buf, want, c.s, 0); n != 0 {
		t.Fatalf("after a row was repainted: %d pixels differ", n)
	}
}

// Past the top, the gap under the header is the page's background, and the header stays.
func TestRubberBand(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(t)
	c.scrollFrame(-100, false)
	bg := c.s.pack(apBG.R, apBG.G, apBG.B)
	px := func(x, y int) uint32 {
		fx, fy := c.s.rot.toFB(x, y, c.s.fbW, c.s.fbH)
		return words(c.s.buf)[fy*c.s.stride/4+fx]
	}
	if got := px(900, c.barH+tabsH+50); got != bg {
		t.Fatalf("the gap above the page is %#x, want the background %#x", got, bg)
	}
	if got, want := px(900, c.barH+10), c.s.pack(0x33, 0x44, 0x55); got != want {
		t.Fatalf("the header moved: %#x", got)
	}
	// The finger stretches less and less past an end.
	if a, b := rubber(-100, 0, 1000, 1700), rubber(-400, 0, 1000, 1700); !(a < 0 && b < a && -b < 400) {
		t.Fatalf("rubber band: %v, %v", a, b)
	}
}

// A sideways row slid on the processor shows exactly what painting it at that offset
// would, every cover included, and settles painted into the page with its buttons.
func TestRowSlidesFromItsStrip(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(t)
	r := c.rowByID("row")
	st := c.cpuStrip(r)
	if st == nil {
		t.Fatal("no strip")
	}
	for _, off := range []int{0, 1, 183, 777, r.contentW - r.r.Dx()} {
		c.rowFrame(r, st, off)
		saved := append([]uint8(nil), c.page.img.Pix...)
		c.sc.x["row"] = off
		r.paint(c.page.img)
		c.pagePainted(r.r.Min.Y, r.r.Max.Y)
		want := screenAt(c, c.scrollY())
		copy(c.page.img.Pix, saved)
		c.pagePainted(r.r.Min.Y, r.r.Max.Y)
		if n := differ(c.s.buf, want, c.s, 0); n != 0 {
			t.Fatalf("row at %d: %d pixels differ from the row painted there", off, n)
		}
	}
}

// waitStill waits for the processor's frames to stop. Caller holds drawMu; it's released
// while waiting.
func waitStill(t *testing.T, c *console) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.sc.cpuOn || c.sc.looping {
		if time.Now().After(deadline) {
			t.Fatal("still moving")
		}
		drawMu.Unlock()
		time.Sleep(10 * time.Millisecond)
		drawMu.Lock()
	}
}

// With motion on: a quick lift flings the page, which slows, runs into its end, bounces
// and stops exactly there; a finger catches a flinging page where it is; a flicked row
// comes to rest on a cover; a slow lift just stops.
func TestFlingBounceAndSnap(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(t)
	c.cfg.Animations = true
	end := c.page.img.Rect.Dy() - c.viewH()

	flick := func(x0, y0, x1, y1 int) {
		c.pageTouch(TouchPoint{Down: true, X: x0, Y: y0})
		for k := 1; k <= 4; k++ {
			c.pageTouch(TouchPoint{Moved: true, X: x0 + (x1-x0)*k/4, Y: y0 + (y1-y0)*k/4})
			drawMu.Unlock()
			time.Sleep(5 * time.Millisecond)
			drawMu.Lock()
		}
		c.pageTouch(TouchPoint{Up: true, X: x1, Y: y1})
	}
	flick(600, 1800, 600, 1000)
	if !c.sc.cpuOn || c.sc.motion == nil {
		t.Fatal("a quick lift should fling")
	}
	waitStill(t, c)
	if c.scrollY() != end {
		t.Fatalf("flung to %d, want the end %d", c.scrollY(), end)
	}
	if want := screenAt(c, end); differ(c.s.buf, want, c.s, 0) != 0 {
		t.Fatal("not settled exactly on the page's end")
	}

	// Caught: a finger on a moving page holds it where it is.
	c.sc.y[c.mode] = 0
	c.blitPage()
	flick(600, 1700, 600, 1100)
	drawMu.Unlock()
	time.Sleep(40 * time.Millisecond)
	drawMu.Lock()
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 900})
	caught := c.scrollY()
	if c.sc.cpuOn || caught <= 0 || caught >= end {
		t.Fatalf("caught at %d (moving %v)", caught, c.sc.cpuOn)
	}
	c.pageTouch(TouchPoint{Up: true, X: 600, Y: 900})

	// A row comes to rest on a cover.
	c.sc.y[c.mode] = 0
	c.blitPage()
	y := c.barH + 600 + rowCH/2
	flick(1000, y, 700, y)
	waitStill(t, c)
	r := c.rowByID("row")
	if off := c.rowOffset("row"); off%r.snap != 0 && off != r.contentW-r.r.Dx() || off == 0 {
		t.Fatalf("the row stopped at %d, between covers (pitch %d)", off, r.snap)
	}
	for _, b := range c.page.rowBtns["row"] {
		var k int
		fmt.Sscanf(b.id, "b%d", &k)
		if want := 48 + k*r.snap - c.rowOffset("row"); b.r.Min.X != want {
			t.Fatalf("the row's buttons didn't move with it: %s at %d, want %d", b.id, b.r.Min.X, want)
		}
	}
}

// A paged swipe (follow-the-finger off) glides to the next screen with motion on.
func TestPagedSwipeGlides(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(t)
	c.cfg.Animations, c.cfg.SmoothScroll = true, false
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 1800})
	c.pageTouch(TouchPoint{Moved: true, X: 600, Y: 1500})
	c.pageTouch(TouchPoint{Up: true, X: 600, Y: 1500})
	if c.sc.motion == nil {
		t.Fatal("no glide")
	}
	waitStill(t, c)
	if want := c.viewH() - c.page.header - 160; c.scrollY() != want {
		t.Fatalf("glided to %d, want %d", c.scrollY(), want)
	}
}

// A finger on a button shows it pressed (before it lifts), and the screen is the page
// again once it moves away into a scroll.
func TestPressShowsAndClears(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(t)
	c.page.buttons = append(c.page.buttons, button{"pill", image.Rect(100, 300, 400, 380)})
	ui.RoundRect(c.page.img, image.Rect(100, 300, 400, 380), 40, apBlue)
	c.pagePainted(300, 380)
	c.blitPage()
	before := append([]byte(nil), c.s.buf...)
	c.pageTouch(TouchPoint{Down: true, X: 200, Y: c.barH + 340})
	drawMu.Unlock()
	time.Sleep(pressDelay + 50*time.Millisecond)
	drawMu.Lock()
	if !c.press.on || bytes.Equal(before, c.s.buf) {
		t.Fatal("the button isn't shown pressed")
	}
	shot(t, c, "press")
	c.pageTouch(TouchPoint{Moved: true, X: 200, Y: c.barH + 200})
	if c.press.on {
		t.Fatal("still pressed after the finger moved into a scroll")
	}
	c.pageTouch(TouchPoint{Up: true, X: 200, Y: c.barH + 200})
	if want := screenAt(c, c.scrollY()); differ(c.s.buf, want, c.s, 0) != 0 {
		t.Fatal("the screen isn't the page after the press went")
	}
}

// pressedLook: a capsule on the background dims and its corners stay; a row (one colour
// to its edges) gets a highlight.
func TestPressedLook(t *testing.T) {
	setPalette(true)
	img := image.NewRGBA(image.Rect(0, 0, 200, 60))
	ui.Fill(img, img.Rect, apBG)
	ui.RoundRect(img, img.Rect, 30, apBlue)
	pressedLook(img)
	if img.RGBAAt(0, 0) != apBG || img.RGBAAt(100, 30).B >= apBlue.B {
		t.Fatalf("capsule: corner %v, middle %v", img.RGBAAt(0, 0), img.RGBAAt(100, 30))
	}
	row := image.NewRGBA(image.Rect(0, 0, 600, 90))
	ui.Fill(row, row.Rect, apCard)
	pressedLook(row)
	if row.RGBAAt(300, 45).R <= apCard.R || row.RGBAAt(0, 0) != apCard {
		t.Fatalf("row: middle %v, corner %v", row.RGBAAt(300, 45), row.RGBAAt(0, 0))
	}
}

// From the edge of the Store's book page, a swipe right goes back to the Store; a short
// pull doesn't.
func TestSwipeFromEdgeGoesBack(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := testConsole(t)
	c.mode = modeStore
	c.store.sel = &storeItem{key: "x", title: "A Book", author: "Someone"}
	c.showPage()
	if c.backID() != "s:back" {
		t.Fatalf("the book page's way back: %q", c.backID())
	}
	swipe := func(to int) {
		c.pageTouch(TouchPoint{Down: true, X: 10, Y: 900})
		for x := 40; x <= to; x += 30 {
			c.pageTouch(TouchPoint{Moved: true, X: x, Y: 905})
		}
		c.pageTouch(TouchPoint{Up: true, X: to, Y: 905})
	}
	drawMu.Unlock()
	time.Sleep(150 * time.Millisecond) // slow: not a flick
	drawMu.Lock()
	c.pageTouch(TouchPoint{Down: true, X: 10, Y: 900})
	c.pageTouch(TouchPoint{Moved: true, X: 70, Y: 905})
	shot(t, c, "edge-back-pull")
	drawMu.Unlock()
	time.Sleep(150 * time.Millisecond)
	drawMu.Lock()
	c.pageTouch(TouchPoint{Up: true, X: 70, Y: 905})
	if c.store.sel == nil {
		t.Fatal("a short, slow pull shouldn't go back")
	}
	swipe(400)
	if c.store.sel != nil {
		t.Fatal("a swipe in from the edge should go back to the Store")
	}
}

// How long a frame takes: turning the whole screen (blitPage) against copying from the
// turned page.
func BenchmarkScrollBlitPage(b *testing.B) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(b)
	for i := 0; i < b.N; i++ {
		c.sc.y[c.mode] = (i * 37) % 2000
		c.blitPage()
	}
}

func BenchmarkScrollFrame(b *testing.B) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(b)
	c.turned().ensure(0, c.page.img.Rect.Dy())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.scrollFrame((i*37)%2000, true)
	}
}

func BenchmarkRowFrame(b *testing.B) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c := fastConsole(b)
	r := c.rowByID("row")
	st := c.cpuStrip(r)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.rowFrame(r, st, (i*37)%2000)
	}
}
