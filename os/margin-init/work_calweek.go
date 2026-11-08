package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"margin-init/ui"
)

// The Planner's week, endless both ways (2026-11-08, user: "swipe to infinity ... a little = 1
// day, more = 2 days, like a roller coaster"; "I can only add from 8 a.m. to 8 p.m.: scroll up
// and down, past midnight to 1 a.m., 2 a.m., a roulette").
//
// Sideways, the days run on without end: the finger drags them pixel by pixel, a flick carries
// on, and they come to rest on a whole day, as many days on as the swipe was worth. Up and down,
// the 24 hours of every day turn round like a wheel: past 23:00 comes 00:00, then 01:00.
//
// Every day is drawn once as a column of its whole 24 hours (and its heading), turned into the
// framebuffer's own layout and kept; a frame is then one or two copies per native row (the
// rules of fastscroll.go), from one loop at the panel's pace for wherever the finger is now.
// When it comes to rest, the page's picture and its buttons (each half hour, each to-do, the
// days' headings, where a dragged to-do can land) are put right for the new days and hours,
// and the whole page is drawn again later, once the tablet is left alone, in a way a touch stops.

const (
	calColW = 130
	calCols = 7
	calPxH  = 72 // pixels an hour
	calDayH = 24 * calPxH
	calX0   = 232 // the first day's left edge
	calLblX = 160 // the hours' labels, left of the days
)

// calView is the week calendar on a page: where its parts are (page coordinates) and what it
// shows at rest. Exported: kept with a view on disk (views.go).
type calView struct {
	Grid, Head, Lbl image.Rectangle // the hours, the days' headings, the hours' labels
	From            string          // the leftmost day
	Top             int             // the day's pixel row at the grid's top (0..calDayH)
	PD              string          // the Planner's chosen day (ringed in its heading)
	BG              color.RGBA      // the card under it all
	HeadBG          []color.RGBA    // the card under the headings, row by row
}

// calStrip is a day's column (or heading, or the labels) turned: column x's pixels top to
// bottom at buf[x*h:].
type calStrip struct {
	buf  []uint32
	w, h int
}

// calMotion is the calendar moving under a finger or by itself.
type calMotion struct {
	on, vert     bool
	page         *page
	gen          int
	x, y         float64 // pixels from the view at rest: x later days, y later hours
	wantX, wantY float64
	fromX, fromY float64 // where the finger took it from
	shownX       int
	shownY       int
	shown        bool
	motion       func(t float64) (float64, bool)
	motionT      time.Time
	samples      []termSample
	looping      bool
	caught       bool // a finger stopped it moving: that lift isn't a tap
	refreshGen   int

	key    string // what the kept strips were drawn for
	days   map[string]*calStrip
	heads  map[string]*calStrip
	labels *calStrip
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int) int { return a - floorDiv(a, b)*b }

func calDate(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, displayZone)
	if err != nil {
		return dayStart(time.Now())
	}
	return t
}

// calOK: the calendar can move as turned pictures (the tablet's own layout).
func (c *console) calOK() bool {
	return c.s.rot == Rot90 && c.s.bpp == 32 && c.s.W == c.s.fbH
}

// calDefaultTop is where the hours start when nothing's been scrolled: 08:00, or earlier if
// something's booked earlier on the days shown.
func calDefaultTop(todos []somaTodo, from time.Time) int {
	dates := map[string]bool{}
	for i := range calCols {
		dates[somaDate(from.AddDate(0, 0, i))] = true
	}
	first, _ := hourSpan(todos, dates, 8, 19)
	return floorMod(first*calPxH-20, calDayH) // a little above the hour: its label shows whole
}

// planWeekCal draws the week calendar from day from, its hours from pixel top, into the page
// and registers its buttons. Design coordinates (k.R): the card and the rows as planWeek had.
func (k *wk) planWeekCal(from, pd time.Time, top int) {
	c := k.c
	cr := image.Rect(wkX0, 312, wkX1, 1298)
	k.card(cr)
	cv := &calView{
		Grid: k.R(calX0, 476, calX0+calCols*calColW, 1284),
		Head: k.R(calX0, 320, calX0+calCols*calColW, 450),
		Lbl:  k.R(calLblX, 476, calX0, 1284),
		From: somaDate(from), Top: floorMod(top, calDayH), PD: somaDate(pd),
	}
	cv.BG = k.img.RGBAAt(calX0+3, cv.Grid.Min.Y+3)
	for y := cv.Head.Min.Y; y < cv.Head.Max.Y; y++ {
		cv.HeadBG = append(cv.HeadBG, k.img.RGBAAt(calX0+3, y))
	}
	k.p.cal = cv
	c.calPaint(k.p, cv)
	calButtons(k.p, cv)
	c.wcal.prefetch(c, cv)
}

// --- the strips -----------------------------------------------------------------------------

// calKey names what the strips show: the records, the clock (the now line), today, the chosen
// day, the colours.
func calKey(cv *calView) string {
	return fmt.Sprint(somaGen.Load(), viewClock(time.Now()), somaToday(), cv.PD, wkLime, wkCard, wkFG, cv.BG, len(cv.HeadBG), planSelKey())
}

func (m *calMotion) fresh(cv *calView) {
	if k := calKey(cv); k != m.key || m.days == nil {
		m.key, m.days, m.heads, m.labels = k, map[string]*calStrip{}, map[string]*calStrip{}, nil
	}
}

// calTodos are the to-dos on date: Soma's, and the projects' practice blocks.
func calTodos(date string) []somaTodo {
	var out []somaTodo
	for _, t := range somaTodos() {
		if !t.Cleared && (t.SlotDate == date || t.on(date)) {
			out = append(out, t)
		}
	}
	return append(out, wkPracticeTodos(wkProjects(), date, date)...)
}

// calDayImg draws date's 24 hours.
func calDayImg(date string, bg color.RGBA, now time.Time) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, calColW, calDayH))
	today := date == somaDate(now)
	if today {
		bg = blend(bg, wkLime, 0.03)
	}
	ui.Fill(img, img.Rect, bg)
	for h := range 24 {
		line := rgb(0x1b1c22)
		if h == 0 { // midnight: the day turns
			line = wkLine
		}
		ui.Fill(img, image.Rect(0, h*calPxH, calColW, h*calPxH+1), line)
	}
	ui.Fill(img, image.Rect(0, 0, 1, calDayH), wkLine)
	for _, t := range calTodos(date) {
		if t.SlotDate != date {
			continue
		}
		col := todoCol(t)
		y0, y1 := calBlock(t)
		r := image.Rect(4, y0+2, calColW-4, y1-2)
		fill := blend(bg, col, 0.22)
		if t.Done {
			fill = blend(bg, col, 0.08)
		}
		if planSelected[t.ID] { // picked: ringed in the accent
			ui.RoundRect(img, r.Inset(-3), 10, wkLime)
		}
		ui.RoundRect(img, r, 8, fill)
		ui.RoundRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+4, r.Max.Y), 2, col)
		ty := r.Min.Y + 22
		f := wkFont("semi", 17)
		fg := wkFG
		if t.Done {
			fg = wkFaint
		}
		for _, l := range wkWrap(f, t.Text, r.Dx()-22, max((r.Dy()-10)/22, 1)) {
			if ty > r.Max.Y-4 {
				break
			}
			apText(img, f, r.Min.X+12, ty, fg, l)
			ty += 21
		}
		if ty+4 < r.Max.Y {
			apText(img, wkFont("regular", 15), r.Min.X+12, ty, wkMuted, clockOf(float64(t.SlotStart)/60))
		}
	}
	if today {
		y := int((float64(now.Hour()) + float64(now.Minute())/60) * calPxH)
		ui.Fill(img, image.Rect(0, max(y-1, 0), calColW, min(y+2, calDayH)), wkRed)
		ui.Circle(img, 3, y, 7, wkRed)
	}
	return img
}

// calBlock is where to-do t sits in its day's column (rows).
func calBlock(t somaTodo) (int, int) {
	y0 := t.SlotStart * calPxH / 60
	y1 := y0 + max(max(t.SlotMins, 15)*calPxH/60, 34)
	return min(max(y0, 0), calDayH-34), min(y1, calDayH)
}

// calHeadImg draws date's heading: the weekday, the date (ringed today and on the chosen day),
// and how much of its list is done.
func calHeadImg(date string, cv *calView, now time.Time) *image.RGBA {
	h := cv.Head.Dy()
	img := image.NewRGBA(image.Rect(0, 0, calColW, h))
	for y := range h {
		bg := cv.BG
		if y < len(cv.HeadBG) {
			bg = cv.HeadBG[y]
		}
		ui.Fill(img, image.Rect(0, y, calColW, y+1), bg)
	}
	d := calDate(date)
	today := dayStart(now)
	cx := calColW / 2
	f := wkFont("medium", 18)
	wd := map[time.Weekday]string{0: "SUN", 1: "MON", 2: "TUE", 3: "WED", 4: "THU", 5: "FRI", 6: "SAT"}[d.Weekday()]
	apText(img, f, cx-ui.TextWidth(f, wd)/2, 23, wkFaint, wd)
	num := d.Format("2")
	fb := wkFont("bold", 25)
	if d.Day() == 1 { // a new month: its name over the date
		wd = d.Format("Jan")
	}
	switch {
	case d.Equal(today):
		ui.Circle(img, cx, 62, 28, wkLime)
		apTextCenter(img, fb, cx, 62, wkOnLime, num)
	case date == cv.PD:
		ui.Circle(img, cx, 62, 28, rgb(0x2a2c35))
		apText(img, fb, cx-ui.TextWidth(fb, num)/2, 71, wkFG, num)
	default:
		apText(img, fb, cx-ui.TextWidth(fb, num)/2, 71, wkFG, num)
	}
	if d.Day() == 1 {
		fm := wkFont("medium", 14)
		apText(img, fm, cx-ui.TextWidth(fm, wd)/2, 101, wkLime, wd)
	}
	done, all := 0, 0
	for _, t := range calTodos(date) {
		if t.on(date) {
			all++
			if t.Done {
				done++
			}
		}
	}
	frac, col := 0.0, wkLime
	if all > 0 {
		frac = float64(done) / float64(all)
	}
	if d.Before(today) && all > done {
		frac, col = 1, wkRed
	}
	k := &wk{p: &page{img: img}, img: img}
	k.hBar(image.Rect(cx-31, 111, cx+31, 117), frac, col)
	return img
}

// calLabelsImg draws the hours' labels, 00:00 to 23:00, the first one again past the end
// (the wheel's seam).
func calLabelsImg(bg color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, calX0-calLblX, calDayH))
	ui.Fill(img, img.Rect, bg)
	f := wkFont("regular", 17)
	for h := 0; h <= 24; h++ {
		apText(img, f, 171-calLblX, h*calPxH+6, wkFaint, fmt.Sprintf("%02d:00", h%24))
	}
	// The label at 24 (00:00 again) hangs over the end: its top half is drawn at the bottom,
	// and 00:00's own top half, cut off above row 0, is that same text: the wheel's seam is
	// seamless.
	return img
}

// turn turns img into a strip.
func (c *console) calTurn(img *image.RGBA) *calStrip {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	st := &calStrip{buf: make([]uint32, w*h), w: w, h: h}
	s := c.s
	rs, gs, bs := s.red.offset, s.green.offset, s.blue.offset
	rl, gl, bl := 8-s.red.length, 8-s.green.length, 8-s.blue.length
	for y := range h {
		row := img.Pix[y*img.Stride:]
		for x := range w {
			i := 4 * x
			st.buf[x*h+y] = uint32(row[i])>>rl<<rs | uint32(row[i+1])>>gl<<gs | uint32(row[i+2])>>bl<<bs
		}
	}
	return st
}

const calKeepDays = 35 // day columns kept turned (~0.9 MB each)

func (m *calMotion) day(c *console, cv *calView, date string) *calStrip {
	if st := m.days[date]; st != nil {
		return st
	}
	if len(m.days) >= calKeepDays { // far ones go first
		mid := calDate(cv.From)
		for d := range m.days {
			if math.Abs(calDate(d).Sub(mid).Hours()/24) > calKeepDays/2 {
				delete(m.days, d)
				delete(m.heads, d)
			}
		}
		if len(m.days) >= calKeepDays {
			clear(m.days)
			clear(m.heads)
		}
	}
	st := c.calTurn(calDayImg(date, cv.BG, time.Now().In(displayZone)))
	m.days[date] = st
	return st
}

func (m *calMotion) head(c *console, cv *calView, date string) *calStrip {
	if st := m.heads[date]; st != nil {
		return st
	}
	st := c.calTurn(calHeadImg(date, cv, time.Now().In(displayZone)))
	m.heads[date] = st
	return st
}

func (m *calMotion) lbl(c *console, cv *calView) *calStrip {
	if m.labels == nil {
		m.labels = c.calTurn(calLabelsImg(cv.BG))
	}
	return m.labels
}

// prefetch draws the days either side of the week shown, a few at a time while nothing else
// happens, so a swipe meets days already drawn.
func (m *calMotion) prefetch(c *console, cv *calView) {
	if !c.calOK() || !turnAhead {
		return
	}
	from := calDate(cv.From)
	order := []int{}
	for i := 1; i <= 14; i++ {
		order = append(order, calCols-1+i, -i)
	}
	var step func(n int)
	step = func(n int) {
		if n >= len(order) {
			return
		}
		time.AfterFunc(60*time.Millisecond, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if c.page == nil || c.page.cal != cv || m.on || c.sc.drag || !c.screenOn {
				return
			}
			m.fresh(cv)
			d := somaDate(from.AddDate(0, 0, order[n]))
			m.day(c, cv, d)
			m.head(c, cv, d)
			step(n + 1)
		})
	}
	step(0)
}

// --- where things are -------------------------------------------------------------------------

// calShown maps page rect r to the part of the screen it's shown on: screen rect sr, and skip,
// how many of r's top rows are off it.
func (c *console) calShown(r image.Rectangle) (sr image.Rectangle, skip int) {
	p := c.page
	vh, hh, fh := c.viewH(), p.header, p.footer
	sy := 0
	lo, hi := c.barH, min(c.barH+vh, c.pageBottom())
	if p.img.Rect.Dy() > vh {
		sy = min(max(c.scrollY(), 0), p.img.Rect.Dy()-vh)
		lo, hi = c.barH+hh, min(c.barH+vh-fh, c.pageBottom())
	}
	s := r.Add(image.Pt(0, c.barH-sy))
	sr = s.Intersect(image.Rect(0, lo, c.s.W, hi))
	if sr.Empty() {
		return sr, 0
	}
	return sr, sr.Min.Y - s.Min.Y
}

// calPart says which part of the calendar a screen point is on: "grid", "head", "lbl" or "".
func (c *console) calPart(x, y int) string {
	if c.page == nil || c.page.cal == nil || !c.calOK() || c.mode != modeSoma {
		return ""
	}
	cv := c.page.cal
	for _, p := range []struct {
		name string
		r    image.Rectangle
	}{{"grid", cv.Grid}, {"head", cv.Head}, {"lbl", cv.Lbl}} {
		if sr, _ := c.calShown(p.r); (image.Point{x, y}).In(sr) {
			return p.name
		}
	}
	return ""
}

// --- frames ------------------------------------------------------------------------------------

// calFrame shows the calendar moved x pixels to later days and y pixels to later hours from
// where it rests, into the screen buffer (the caller flushes). Caller holds drawMu.
func (c *console) calFrame(x, y int) {
	cv, s, m := c.page.cal, c.s, &c.wcal
	m.fresh(cv)
	from := calDate(cv.From)
	top := cv.Top + y
	gs, gskip := c.calShown(cv.Grid)
	hs, hskip := c.calShown(cv.Head)
	ls, lskip := c.calShown(cv.Lbl)
	// The days in view, drawn (and turned) first if they aren't yet.
	d0, d1 := floorDiv(x, calColW), floorDiv(x+cv.Grid.Dx()-1, calColW)
	days := map[int]*calStrip{}
	heads := map[int]*calStrip{}
	for i := d0; i <= d1; i++ {
		date := somaDate(from.AddDate(0, 0, i))
		if !gs.Empty() {
			days[i] = m.day(c, cv, date)
		}
		if !hs.Empty() {
			heads[i] = m.head(c, cv, date)
		}
	}
	lbl := m.lbl(c, cv)
	wrap := func(dst, src []uint32, at, n int) { // n rows of a 24-hour column from row at, round the clock
		at = floorMod(at, calDayH)
		for n > 0 {
			k := min(n, calDayH-at)
			copy(dst[:k], src[at:at+k])
			dst, n, at = dst[k:], n-k, 0
		}
	}
	lo, hi := s.fbH, -1
	col := func(X int) []uint32 { return words(s.buf[(s.fbH-1-X)*s.stride:])[:s.fbW] }
	if !gs.Empty() || !hs.Empty() {
		x0 := max(gs.Min.X, hs.Min.X)
		if gs.Empty() {
			x0 = hs.Min.X
		}
		x1 := max(gs.Max.X, hs.Max.X)
		eachColumn(x1-x0, func(a, b int) {
			for X := x0 + a; X < x0+b; X++ {
				g := x + X - cv.Grid.Min.X
				i := floorDiv(g, calColW)
				cc := g - i*calColW
				dst := col(X)
				if st := days[i]; st != nil && X >= gs.Min.X && X < gs.Max.X {
					wrap(dst[gs.Min.Y:gs.Max.Y], st.buf[cc*st.h:(cc+1)*st.h], top+gskip, gs.Dy())
				}
				if st := heads[i]; st != nil && X >= hs.Min.X && X < hs.Max.X {
					copy(dst[hs.Min.Y:hs.Max.Y], st.buf[cc*st.h+hskip:])
				}
			}
		})
		lo, hi = s.fbH-x1, s.fbH-1-x0
	}
	if !ls.Empty() {
		for X := ls.Min.X; X < ls.Max.X; X++ {
			cc := X - cv.Lbl.Min.X
			wrap(col(X)[ls.Min.Y:ls.Max.Y], lbl.buf[cc*lbl.h:(cc+1)*lbl.h], top+lskip, ls.Dy())
		}
		lo, hi = min(lo, s.fbH-ls.Max.X), max(hi, s.fbH-1-ls.Min.X)
	}
	if lo <= hi {
		s.markRows(lo, hi)
	}
	m.shownX, m.shownY, m.shown = x, y, true
}

// calPaint puts the calendar at rest into the page's picture, from the same strips the frames
// use (so the page and the last frame are the same picture). Caller holds drawMu.
func (c *console) calPaint(p *page, cv *calView) {
	m := &c.wcal
	m.fresh(cv)
	if !c.calOK() { // drawn straight (no turned strips on other screens)
		now := time.Now().In(displayZone)
		for i := range calCols {
			date := somaDate(calDate(cv.From).AddDate(0, 0, i))
			calUnwrap(p.img, cv.Grid.Min.X+i*calColW, cv.Grid, calDayImg(date, cv.BG, now), cv.Top)
			hd := calHeadImg(date, cv, now)
			copyInto(p.img, hd, image.Pt(cv.Head.Min.X+i*calColW, cv.Head.Min.Y))
		}
		calUnwrap(p.img, cv.Lbl.Min.X, cv.Lbl, calLabelsImg(cv.BG), cv.Top)
		return
	}
	s := c.s
	unpack := func(v uint32) (uint8, uint8, uint8) {
		part := func(f bitfield) uint8 { return uint8((v >> f.offset) & (1<<f.length - 1) << (8 - f.length)) }
		return part(s.red), part(s.green), part(s.blue)
	}
	put := func(st *calStrip, cx, at int, dx, dy0, n int, wrap bool) {
		src := st.buf[cx*st.h : (cx+1)*st.h]
		for k := range n {
			r := at + k
			if wrap {
				r = floorMod(r, calDayH)
			}
			R, G, B := unpack(src[r])
			i := p.img.PixOffset(dx, dy0+k)
			p.img.Pix[i], p.img.Pix[i+1], p.img.Pix[i+2], p.img.Pix[i+3] = R, G, B, 255
		}
	}
	from := calDate(cv.From)
	for i := range calCols {
		date := somaDate(from.AddDate(0, 0, i))
		day, hd := m.day(c, cv, date), m.head(c, cv, date)
		for cx := range calColW {
			X := cv.Grid.Min.X + i*calColW + cx
			put(day, cx, cv.Top, X, cv.Grid.Min.Y, cv.Grid.Dy(), true)
			put(hd, cx, 0, X, cv.Head.Min.Y, cv.Head.Dy(), false)
		}
	}
	lbl := m.lbl(c, cv)
	for cx := range lbl.w {
		put(lbl, cx, cv.Top, cv.Lbl.Min.X+cx, cv.Lbl.Min.Y, cv.Lbl.Dy(), true)
	}
}

// calUnwrap copies a 24-hour column picture into dst at column x, rows r, from row top round
// the clock.
func calUnwrap(dst *image.RGBA, x int, r image.Rectangle, src *image.RGBA, top int) {
	w := src.Rect.Dx()
	for k := 0; k < r.Dy(); k++ {
		sy := floorMod(top+k, calDayH)
		copy(dst.Pix[dst.PixOffset(x, r.Min.Y+k):][:4*w], src.Pix[sy*src.Stride:][:4*w])
	}
}

// copyInto copies src whole into dst at at.
func copyInto(dst, src *image.RGBA, at image.Point) {
	w := src.Rect.Dx()
	for y := 0; y < src.Rect.Dy(); y++ {
		copy(dst.Pix[dst.PixOffset(at.X, at.Y+y):][:4*w], src.Pix[y*src.Stride:][:4*w])
	}
}

// calButtons registers the calendar's buttons and drop zones for how it rests: each day's
// heading, its to-dos, each free half hour (round the clock: the hours wrap at the grid's seam).
func calButtons(p *page, cv *calView) {
	area := cv.Grid.Union(cv.Head)
	keep := p.buttons[:0]
	for _, b := range p.buttons {
		if !b.r.In(area) {
			keep = append(keep, b)
		}
	}
	p.buttons = keep
	drops := p.drops[:0]
	for _, z := range p.drops {
		if !z.r.In(area) {
			drops = append(drops, z)
		}
	}
	p.drops = drops
	g := cv.Grid
	at := func(px int) int { return g.Min.Y + floorMod(px-cv.Top, calDayH) } // page row of day pixel px
	seam := g.Min.Y + calDayH - cv.Top                                       // where 00:00 comes round
	from := calDate(cv.From)
	var slots []button
	for i := range calCols {
		date := somaDate(from.AddDate(0, 0, i))
		x0, x1 := g.Min.X+i*calColW, g.Min.X+(i+1)*calColW
		hr := image.Rect(x0, cv.Head.Min.Y, x1, cv.Head.Max.Y)
		p.buttons = append(p.buttons, button{"wk:planday:" + date, hr})
		p.drops = append(p.drops, dropZone{r: hr, date: date, first: -1})
		pxMin := float64(calPxH) / 60
		if seam > g.Min.Y && seam < g.Max.Y {
			p.drops = append(p.drops, dropZone{r: image.Rect(x0, g.Min.Y, x1, seam), date: date, first: cv.Top * 60 / calPxH, pxMin: pxMin},
				dropZone{r: image.Rect(x0, seam, x1, g.Max.Y), date: date, first: 0, pxMin: pxMin})
		} else {
			p.drops = append(p.drops, dropZone{r: image.Rect(x0, g.Min.Y, x1, g.Max.Y), date: date, first: cv.Top * 60 / calPxH, pxMin: pxMin})
		}
		for _, t := range calTodos(date) {
			if t.SlotDate != date {
				continue
			}
			y0, y1 := calBlock(t)
			r := image.Rect(x0+4, at(y0)+2, x1-4, at(y0)+(y1-y0)-2).Intersect(g)
			if !r.Empty() {
				p.buttons = append(p.buttons, button{todoTapID(t), r})
			}
		}
		for m := 0; m < 24*60; m += 30 {
			y := at(m * calPxH / 60)
			r := image.Rect(x0, y, x1, y+calPxH/2).Intersect(g)
			if !r.Empty() {
				slots = append(slots, button{fmt.Sprintf("wk:slotadd:%s:%d", date, m), r})
			}
		}
	}
	p.buttons = append(p.buttons, slots...) // after the to-dos: a to-do wins where it is
}

// --- the finger --------------------------------------------------------------------------------

// calGrab: a finger that touched down at (x, y) on the calendar has moved (dx, dy) past the
// slop: the calendar takes the gesture, sideways (days) or up and down (hours). False if it's
// not the calendar's (the page scrolls instead). Caller holds drawMu.
func (c *console) calGrab(x, y, dx, dy int) bool {
	part := c.calPart(x, y)
	side := abs(dx) > abs(dy)
	switch {
	case part == "":
		return false
	case part == "head" && !side, part == "lbl" && side:
		return false
	}
	c.gpuSettle()
	m := &c.wcal
	if m.on && m.page != c.page {
		m.on = false
	}
	if !m.on {
		m.on, m.page, m.gen, m.x, m.y, m.shown = true, c.page, c.pageGen, 0, 0, false
	}
	m.vert, m.motion, m.samples = !side, nil, m.samples[:0]
	m.fromX, m.fromY, m.wantX, m.wantY = m.x, m.y, m.x, m.y
	return true
}

// calFollow moves the calendar with the finger, (dx, dy) from where it took it.
func (c *console) calFollow(dx, dy int) {
	m := &c.wcal
	if !m.on {
		return
	}
	now := time.Now()
	if m.vert {
		m.wantY = m.fromY - float64(dy)
		m.samples = append(m.samples, termSample{now, m.wantY})
	} else {
		m.wantX = m.fromX - float64(dx)
		m.samples = append(m.samples, termSample{now, m.wantX})
	}
	for len(m.samples) > 1 && now.Sub(m.samples[0].t) > 100*time.Millisecond {
		m.samples = m.samples[1:]
	}
	c.calKick()
}

func (m *calMotion) speed() float64 {
	s := m.samples
	if len(s) < 2 || time.Since(s[len(s)-1].t) > 80*time.Millisecond {
		return 0
	}
	dt := s[len(s)-1].t.Sub(s[0].t).Seconds()
	if dt < 0.008 { // moves too close together to tell a speed
		return 0
	}
	return (s[len(s)-1].p - s[0].p) / dt
}

// calRelease: the finger lifted. Hours fling on round the clock; days fling on and come to rest
// on a whole day: the further and faster the swipe, the more days. Caller holds drawMu.
func (c *console) calRelease() {
	m := &c.wcal
	if !m.on {
		return
	}
	v := m.speed()
	if m.vert {
		at := m.wantY
		if !c.animOK() || math.Abs(v) < flingMinSpeed {
			c.calSettle(m.wantX, at)
			return
		}
		m.motion, m.motionT = flingMotion(at, v, -1e12, 1e12), time.Now()
		c.calKick()
		return
	}
	at := m.wantX
	start := math.Round(m.fromX/calColW) * calColW
	to := math.Round((at+v*rowTau)/calColW) * calColW
	if to == start && math.Abs(at-start) > 24 { // a little swipe: a day on
		to = start + math.Copysign(calColW, at-start)
	}
	if !c.animOK() {
		c.calSettle(to, m.wantY)
		return
	}
	m.motion, m.motionT = easeTo(at, to, 0.26+0.1*math.Min(math.Abs(to-at)/(3*calColW), 1)), time.Now()
	c.calKick()
}

// calCatch: a finger lands on the moving calendar: it stops where it is (days on a whole day).
func (c *console) calCatch() bool {
	m := &c.wcal
	if !m.on || m.motion == nil {
		return false
	}
	x, y := m.wantX, m.wantY
	if m.shown {
		x, y = float64(m.shownX), float64(m.shownY)
	}
	if !m.vert {
		x = math.Round(x/calColW) * calColW
	}
	c.calSettle(x, y)
	return true
}

func (c *console) calKick() {
	m := &c.wcal
	if m.looping {
		return
	}
	m.looping = true
	go func() {
		for {
			drawMu.Lock()
			start := time.Now()
			drew, more := c.calNextFrame()
			if !more {
				m.looping = false
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

func (c *console) calNextFrame() (drew, more bool) {
	m := &c.wcal
	if !m.on {
		return false, false
	}
	if !c.screenOn || c.page != m.page || c.pageGen != m.gen || c.page.cal == nil || c.locked || c.pinMode != "" || c.mode != modeSoma {
		m.on, m.motion = false, nil
		return false, false
	}
	if m.motion != nil {
		pos, done := m.motion(time.Since(m.motionT).Seconds())
		if m.vert {
			m.wantY = pos
		} else {
			m.wantX = pos
		}
		if done {
			c.calSettle(m.wantX, m.wantY)
			return true, false
		}
	} else if !c.sc.drag {
		c.calSettle(m.wantX, m.wantY) // lifted with nothing left to move
		return true, false
	}
	x, y := int(math.Round(m.wantX)), int(math.Round(m.wantY))
	if m.shown && x == m.shownX && y == m.shownY {
		return false, m.motion != nil // waiting for the finger: its next move kicks the loop
	}
	c.calFrame(x, y)
	c.s.frame = true
	c.s.Flush()
	c.s.frame = false
	return true, true
}

// calSettle ends the move at (x, y): the calendar rests there (days on a whole day), the page's
// picture and buttons are put right, and the Planner remembers where it is. Caller holds drawMu.
func (c *console) calSettle(x, y float64) {
	m := &c.wcal
	m.on, m.motion = false, nil
	p := c.page
	if p == nil || p.cal == nil || p != m.page {
		return
	}
	days := int(math.Round(x / calColW))
	cv := *p.cal // a new one: a view kept with the old one keeps it
	cv.From = somaDate(calDate(cv.From).AddDate(0, 0, days))
	cv.Top = floorMod(cv.Top+int(math.Round(y)), calDayH)
	p.cal = &cv
	m.x, m.y = 0, 0
	c.calPaint(p, &cv)
	calButtons(p, &cv)
	for _, r := range []image.Rectangle{cv.Grid, cv.Head} {
		c.pagePainted(r.Min.Y, r.Max.Y)
	}
	c.calFrame(0, 0)
	c.s.Flush()
	su := &c.somaUI
	su.weekFrom, su.weekTop = cv.From, cv.Top+1
	m.prefetch(c, &cv)
	c.calRefreshLater()
}

// calRefreshLater draws the Planner again (its title's dates, the lists under the calendar)
// once the tablet is left alone, in a way a touch stops.
func (c *console) calRefreshLater() {
	m := &c.wcal
	m.refreshGen++
	gen := m.refreshGen
	var try func()
	try = func() {
		time.AfterFunc(600*time.Millisecond, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if gen != m.refreshGen || c.mode != modeSoma || c.page == nil || c.page.cal == nil || !c.screenOn || c.somaUI.typing || c.somaUI.sheet != "" {
				return
			}
			if m.on || c.sc.drag || c.drag.on || time.Since(c.lastInput) < 600*time.Millisecond {
				try()
				return
			}
			tabs, pg := c.tabsOn, c.page
			if !yielding(c.showPage) {
				c.tabsOn, c.page = tabs, pg
				try()
			}
		})
	}
	try()
}
