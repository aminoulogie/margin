package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Every screen of Margin opens at once after the first time, for good.
//
// Drawing a page takes the tablet's Atom from a tenth of a second to more than half of one,
// and every tab and sub-tab used to be drawn again each time it was opened. Now each view (a
// tab, and within it the page, sub-page and choices on show) is kept once drawn:
//   - in memory, the last few: the page itself (its picture, buttons, rows, swipe zones), so
//     showing it again is turning its window onto the screen, nothing drawn;
//   - on the tablet (/data/condor/views), every view: the whole page, packed (a run of the
//     same pixel is stored once: pages are mostly flat colour), with its buttons, so after a
//     restart, or once memory has let it go, it still opens at once, and scrolls.
//
// What is kept is drawn fresh only when what it shows may have changed, once the tablet has
// been left alone for a moment: for Soma's pages, when a kind of record the page read has
// changed since (somasync.go notes what each drawing reads), or the five-minute clock has
// moved; for the other tabs, a minute after they were drawn. A view read from the tablet is
// drawn fresh once after a restart. Soma's views are also made ready in the background: one
// kept whose records changed (an edit, a sync from the phone) is drawn again off screen while
// the tablet is left alone, one at a time, so opening it is already up to date. Anything done
// on a view is drawn at once as before.
//
// Memory is kept small on purpose. The tablet has 2 GB and margin-init is a 32-bit program:
// keeping every tab's page and screen in memory (320 MB of them, plus copies) grew the
// program past a gigabyte, and it slowed to a crawl, then died with the last picture frozen
// on the screen.

const (
	viewMemBytes   = 112 << 20 // what the views kept in memory may take
	viewDiskMax    = 60        // views kept on the tablet
	viewFreshFor   = time.Minute
	viewIdleBefore = 700 * time.Millisecond  // untouched this long before a kept view is redrawn
	viewSaveIdle   = 1500 * time.Millisecond // and this long before one is packed for the tablet
)

type viewEntry struct {
	key     string
	p       *page
	src     *page // the page p is a copy of
	tabsOn  string
	scrollY int
	gen     int64    // somaGen when it was drawn
	fields  []string // the kinds of Soma record its drawing read
	clock   string   // the five-minute clock when it was drawn (viewClock)
	soma    bool     // a Soma page: fresh while its records and the clock haven't moved
	at      time.Time
	used    time.Time
	disk    bool    // read from the tablet: drawn fresh at the first moment alone
	saved   bool    // what's on the tablet is this drawing (or no newer one needs writing)
	su      *somaUI // a Soma view: its choices, to draw it again in the background
	failed  bool    // drawing it again in the background didn't give the same view
}

func (e *viewEntry) size() int { return cap(e.p.img.Pix) }

var views struct {
	sync.Mutex
	m      map[string]*viewEntry
	bytes  int
	sums   map[string]uint32 // what each view's file on the tablet holds, packed
	saving bool              // a save is waiting for a moment alone
	writes sync.WaitGroup
	async  bool
	off    bool // tests that count drawings switch it off
	noPrep bool // tests: no background drawing (they call viewPrepTick themselves)
	c      *console
}

var viewPrepOnce sync.Once

func init() { views.async = true }

func viewDir() string { return filepath.Join(marginHome, "views") }

var viewDirFn = viewDir

// viewClock is the five-minute clock Soma's pages are drawn against (the workstation's own
// kept pages use the same, workKey).
func viewClock(t time.Time) string {
	t = t.In(displayZone)
	return t.Format("2006-01-02 15:") + fmt.Sprint(t.Minute()/5)
}

// viewKey names the view the console is on, "" for one not to keep (typing, a sheet, the
// reader, the terminal, a film).
func (c *console) viewKey() string {
	if views.off || c.locked || c.alarm != "" || c.pinMode != "" || c.xapp != nil {
		return ""
	}
	return c.viewKeyOf()
}

// viewKeyOf is viewKey without what's over the screen (the lock screen, an alarm).
func (c *console) viewKeyOf() string {
	var k string
	switch c.mode {
	case modeBooksHome:
		if c.homeEdit {
			return ""
		}
		k = "home"
	case modeBooks:
		k = "library"
	case modeStore:
		st := &c.store
		if st.typing {
			return ""
		}
		sel := ""
		if st.sel != nil {
			sel = fmt.Sprintf("%p", st.sel)
		}
		k = fmt.Sprintf("store|%s|%d|%d|%d|%s|%d", st.query, st.topic, st.lang, st.view, sel, len(st.results))
	case modeWords:
		w := &c.wui
		if w.edit {
			return ""
		}
		k = fmt.Sprintf("words|%s|%s|%d|%v", w.view, w.sel, w.from, w.shown)
	case modeSoma:
		su := &c.somaUI
		if su.typing || su.sheet != "" || su.undo != nil || c.drag.on {
			return ""
		}
		var sel []string
		for kk, v := range su.workSel {
			sel = append(sel, kk+"="+v)
		}
		sort.Strings(sel)
		k = fmt.Sprintf("soma|%v|%s|%s|%s|%s|%s|%s|%s|%d|%s|%v", soma.linked(), su.view, su.work, su.workSub, su.workProj,
			strings.Join(sel, ","), su.planDate, su.calDay, su.calOff, su.nutDate, c.cfg.SomaHidden)
	case modePlaybox:
		pb := &c.pb
		if pb.typing {
			return ""
		}
		sel := ""
		if pb.sel != nil {
			sel = fmt.Sprintf("%p|%d", pb.sel, pb.sel.season)
		}
		k = fmt.Sprintf("playbox|%s|%s|%s|%v", pb.view, pb.query, sel, pb.libEdit)
	case modeBrowser:
		br := &c.br
		if br.typing || br.loading != "" {
			return ""
		}
		u := ""
		if br.cur != nil {
			u = fmt.Sprintf("%s|%d", br.cur.url, br.cur.part)
		}
		k = "browser|" + u
	case modeSettings:
		if c.apps.typing || c.wifi.typing {
			return ""
		}
		k = "settings|" + c.setPane
	default:
		return ""
	}
	return fmt.Sprintf("%s|%v|%v|%d|%dx%d", k, apDark, wkLime, c.barH, c.s.W, c.s.H)
}

// viewStale: what e shows may have changed since it was drawn.
func viewStale(e *viewEntry, now time.Time) bool {
	switch {
	case e.disk:
		return true
	case len(e.fields) > 0 && soma.changedSince(e.fields, e.gen):
		return true
	case e.soma && len(e.fields) == 0 && e.gen != somaGen.Load(): // what it read isn't known
		return true
	case e.soma:
		return e.clock != viewClock(now)
	}
	return now.Sub(e.at) > viewFreshFor
}

// viewNeedsDraw: the view on screen may be out of date (Soma's records changed, say), so a
// redraw is worth its time. Caller holds drawMu.
func (c *console) viewNeedsDraw() bool {
	if c.viewOn == "" || c.viewKey() != c.viewOn {
		return true
	}
	views.Lock()
	e := views.m[c.viewOn]
	views.Unlock()
	return e == nil || viewStale(e, time.Now())
}

// viewShow shows the view key from what was kept, if it was. True if it did. Caller holds
// drawMu (showPage, before it would draw).
func (c *console) viewShow(key string) bool {
	views.Lock()
	e := views.m[key]
	if e != nil {
		e.used = time.Now()
	}
	views.Unlock()
	if e == nil {
		e = c.viewFromDisk(key)
		if e == nil {
			return false
		}
	}
	c.page, c.tabsOn, c.viewOn = e.p, e.tabsOn, key
	c.setScroll(c.mode, e.scrollY)
	c.blitPage()
	if viewStale(e, time.Now()) {
		c.viewRefreshLater(key)
	}
	return true
}

// viewKeep keeps the view just drawn, and what its drawing read of Soma's records. Caller
// holds drawMu.
func (c *console) viewKeep(key string, fields []string) {
	if key == "" || c.page == nil || c.page.img == nil {
		return
	}
	e := c.viewEntryOf(key, c.page, c.scrollY(), fields)
	c.page = e.p // what's on screen is the kept one: taps, scrolling and repaints use it
	c.viewPut(e)
	viewPrepOnce.Do(func() { go viewPrepLoop() })
}

// viewEntryOf is page pg kept as view key, in a picture of its own (a shared canvas is taken
// over, not copied). Caller holds drawMu.
func (c *console) viewEntryOf(key string, pg *page, scrollY int, fields []string) *viewEntry {
	p := *pg
	switch {
	case p.owned: // drawn in its own picture (the workstation): kept as it is
	case sameBacking(p.img, pageCanvas):
		pageCanvas = nil // taken: the next page gets a fresh one (cheaper than a copy)
		p.owned = true
	case sameBacking(p.img, tabCanvas):
		tabCanvas = nil
		p.owned = true
	default:
		p.img = cloneRGBA(p.img)
		p.owned = true
	}
	now := time.Now()
	e := &viewEntry{key: key, p: &p, tabsOn: c.tabsOn, scrollY: scrollY, gen: somaGen.Load(), fields: fields,
		clock: viewClock(now), soma: c.mode == modeSoma, at: now, used: now}
	if c.mode == modeSoma {
		su := c.somaUI
		su.workSel = maps.Clone(su.workSel)
		su.sheetStack, su.undo = nil, nil
		e.su = &su
	}
	return e
}

// viewPut keeps entry e, dropping the least recently shown past the memory allowed, and has
// it written to the tablet. Caller holds drawMu.
func (c *console) viewPut(e *viewEntry) {
	views.Lock()
	views.c = c
	if views.m == nil {
		views.m = map[string]*viewEntry{}
	}
	if old := views.m[e.key]; old != nil {
		views.bytes -= old.size()
	}
	views.m[e.key] = e
	views.bytes += e.size()
	for views.bytes > viewMemBytes && len(views.m) > 1 { // the least recently shown go
		var oldest *viewEntry
		for _, x := range views.m {
			if x != e && (oldest == nil || x.used.Before(oldest.used)) {
				oldest = x
			}
		}
		if oldest == nil {
			break
		}
		views.bytes -= oldest.size()
		delete(views.m, oldest.key)
	}
	async := views.async
	views.Unlock()
	if !async {
		c.viewSave(e) // tests: at once
		return
	}
	c.viewSaveLater()
}

// sameBacking: a and b are pictures in the same memory.
func sameBacking(a, b *image.RGBA) bool {
	if a == nil || b == nil || cap(a.Pix) == 0 || cap(b.Pix) == 0 {
		return false
	}
	return &a.Pix[:cap(a.Pix)][cap(a.Pix)-1] == &b.Pix[:cap(b.Pix)][cap(b.Pix)-1]
}

// cloneRGBA is a copy of img in a picture of its own, just its size.
func cloneRGBA(img *image.RGBA) *image.RGBA {
	r := img.Rect
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	n := 4 * r.Dx()
	for y := 0; y < r.Dy(); y++ {
		copy(out.Pix[y*out.Stride:][:n], img.Pix[img.PixOffset(r.Min.X, r.Min.Y+y):][:n])
	}
	return out
}

// viewRefreshLater draws view key fresh once the tablet has been left alone a moment.
func (c *console) viewRefreshLater(key string) {
	c.viewRefreshGen++
	gen := c.viewRefreshGen
	var try func()
	try = func() {
		time.AfterFunc(viewIdleBefore, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if c.viewRefreshGen != gen || c.viewOn != key || !c.screenOn {
				return
			}
			if c.sc.drag || c.sc.cpuOn || c.press.on || time.Since(c.lastInput) < viewIdleBefore {
				try() // still in use: later
				return
			}
			if c.viewKey() != key {
				return
			}
			c.viewFresh = true
			c.showPage()
			c.viewFresh = false
		})
	}
	try()
}

// --- made ready in the background ---------------------------------------------------------------

const (
	viewPrepEvery = 2 * time.Second
	viewPrepIdle  = 1500 * time.Millisecond // untouched this long before drawing in the background
	viewPrepMost  = 10                      // the Soma views most recently shown are kept ready
)

func viewPrepLoop() {
	for {
		time.Sleep(viewPrepEvery)
		views.Lock()
		c, skip := views.c, views.off || views.noPrep
		views.Unlock()
		if c == nil || skip || !drawMu.TryLock() {
			continue
		}
		c.viewPrepTick()
		drawMu.Unlock()
	}
}

// viewPrepStale: kept view e is worth drawing again in the background, as a record its
// drawing read has changed. (The five-minute clock alone isn't: that is drawn when the view is
// opened, a moment after it shows; drawing every kept view again every five minutes held the
// tablet for seconds.)
func viewPrepStale(e *viewEntry) bool {
	if e.failed || e.disk {
		return false
	}
	if len(e.fields) == 0 {
		return e.gen != somaGen.Load()
	}
	return soma.changedSince(e.fields, e.gen)
}

// viewPrepTick draws again one kept Soma view whose records have moved on, if the tablet is
// being left alone. True if it drew one. Caller holds drawMu.
func (c *console) viewPrepTick() bool {
	if time.Since(c.lastInput) < viewPrepIdle || c.sc.drag || c.sc.cpuOn || c.sc.motion != nil || c.press.on || c.drag.on {
		return false
	}
	if c.mode == modeVideo || c.mode == modeSoma && (c.viewOn == "" || c.viewKey() != c.viewOn) {
		return false // a film, or a Soma page on screen that isn't a kept copy (typing, a sheet)
	}
	if !soma.linked() {
		return false // Soma's own pages draw into the shared canvas, which may be on screen
	}
	views.Lock()
	var kept []*viewEntry
	for _, e := range views.m {
		if e.su != nil {
			kept = append(kept, e)
		}
	}
	views.Unlock()
	sort.Slice(kept, func(i, j int) bool { return kept[i].used.After(kept[j].used) })
	for i, e := range kept {
		if i >= viewPrepMost {
			break
		}
		if !viewPrepStale(e) || c.mode == modeSoma && e.key == c.viewOn {
			continue // the one on screen redraws itself when its records change
		}
		c.viewPrepare(e)
		return true
	}
	return false
}

// viewPrepare draws Soma view e again as it would be opened now, off the screen, and keeps it.
// What's on screen is left as it was. Caller holds drawMu.
func (c *console) viewPrepare(e *viewEntry) {
	saved, mode, tabs, pg := c.somaUI, c.mode, c.tabsOn, c.page
	c.scrollY()
	sy, had := c.sc.y[modeSoma]
	defer func() {
		c.somaUI, c.mode, c.tabsOn, c.page = saved, mode, tabs, pg
		if had {
			c.sc.y[modeSoma] = sy
		} else {
			delete(c.sc.y, modeSoma)
		}
	}()
	su := *e.su
	su.workSel = maps.Clone(su.workSel)
	c.somaUI, c.mode = su, modeSoma
	c.sc.y[modeSoma] = e.scrollY
	soma.track()
	p := c.somaPage()
	fields := soma.tracked()
	if c.viewKeyOf() != e.key || p == nil || p.img == nil {
		e.failed = true
		return
	}
	scroll := 0
	if p.img.Rect.Dy() > c.viewH() {
		scroll = min(max(e.scrollY, 0), p.img.Rect.Dy()-c.viewH())
	}
	ne := c.viewEntryOf(e.key, p, scroll, fields)
	ne.used = e.used
	c.viewPut(ne)
}

// --- on the tablet ----------------------------------------------------------------------------

type viewFileBtn struct {
	ID string
	R  image.Rectangle
}

type viewFile struct {
	Key                  string
	W, H                 int // the page, its whole height
	ViewH                int // the screen it was drawn for
	Header, Footer, Left int
	TabsOn               string
	ScrollY              int
	Buttons              []viewFileBtn
	Swipes               []viewFileBtn
}

const viewMagic = "MRGVIEW2"

func viewPath(key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(viewDirFn(), hex.EncodeToString(sum[:10])+".mv")
}

// viewSaveLater packs the views drawn since they were last written, one at a time, each once
// the tablet has been left alone for a moment (packing a page holds the screen a few tens of
// milliseconds on the tablet); the file is written in the background.
func (c *console) viewSaveLater() {
	views.Lock()
	if views.saving {
		views.Unlock()
		return
	}
	views.saving = true
	views.Unlock()
	var try func(d time.Duration)
	try = func(d time.Duration) {
		time.AfterFunc(d, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if c.sc.drag || c.sc.cpuOn || c.press.on || time.Since(c.lastInput) < viewSaveIdle {
				try(viewSaveIdle)
				return
			}
			views.Lock()
			var next *viewEntry
			for _, e := range views.m {
				if !e.saved && (next == nil || e.used.After(next.used)) {
					next = e
				}
			}
			if next == nil {
				views.saving = false
				views.Unlock()
				return
			}
			views.Unlock()
			c.viewSave(next)
			try(200 * time.Millisecond)
		})
	}
	try(viewSaveIdle)
}

// viewSave packs view e for the tablet and writes it (in the background, unless tests want it
// at once), if it isn't what the tablet has already. Caller holds drawMu.
func (c *console) viewSave(e *viewEntry) {
	views.Lock()
	e.saved = true
	views.Unlock()
	p := e.p
	img := p.img
	f := viewFile{Key: e.key, W: img.Rect.Dx(), H: img.Rect.Dy(), ViewH: c.viewH(), Header: p.header, Footer: p.footer,
		Left: p.left, TabsOn: e.tabsOn, ScrollY: e.scrollY}
	for _, b := range p.buttons {
		f.Buttons = append(f.Buttons, viewFileBtn{b.id, b.r})
	}
	for _, bs := range p.rowBtns {
		for _, b := range bs {
			f.Buttons = append(f.Buttons, viewFileBtn{b.id, b.r})
		}
	}
	for _, z := range p.swipes {
		f.Swipes = append(f.Swipes, viewFileBtn{z.id, z.r})
	}
	var meta bytes.Buffer
	if gob.NewEncoder(&meta).Encode(f) != nil {
		return
	}
	var out bytes.Buffer
	out.WriteString(viewMagic)
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(meta.Len()))
	out.Write(n[:])
	out.Write(meta.Bytes())
	data := packRGBA(out.Bytes(), img)
	sum := crc32.ChecksumIEEE(data)
	views.Lock()
	if views.sums == nil {
		views.sums = map[string]uint32{}
	}
	same := views.sums[e.key] == sum
	views.sums[e.key] = sum
	async := views.async
	views.Unlock()
	if same {
		return // the tablet has this already
	}
	path := viewPath(e.key)
	if !async {
		writeView(path, data)
		return
	}
	views.writes.Add(1)
	go func() {
		defer views.writes.Done()
		writeView(path, data)
	}()
}

func writeView(path string, data []byte) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		os.Remove(tmp)
		return
	}
	os.Rename(tmp, path)
	pruneViews(filepath.Dir(path))
}

// viewFromDisk is view key as it was last kept on the tablet, the whole page (its sideways
// rows and drop zones come with its fresh drawing a moment later).
func (c *console) viewFromDisk(key string) *viewEntry {
	b, err := os.ReadFile(viewPath(key))
	if err != nil || len(b) < len(viewMagic)+4 || string(b[:len(viewMagic)]) != viewMagic {
		return nil
	}
	b = b[len(viewMagic):]
	n := int(binary.LittleEndian.Uint32(b[:4]))
	if n <= 0 || 4+n > len(b) {
		return nil
	}
	var f viewFile
	if gob.NewDecoder(bytes.NewReader(b[4:4+n])).Decode(&f) != nil || f.Key != key || f.W != c.s.W || f.ViewH != c.viewH() ||
		f.H <= 0 || f.H > pageMaxRows {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, f.W, f.H))
	if unpackRGBA(b[4+n:], img.Pix) != nil {
		return nil
	}
	p := &page{img: img, header: f.Header, footer: f.Footer, left: f.Left, owned: true}
	for _, x := range f.Buttons {
		p.buttons = append(p.buttons, button{x.ID, x.R})
	}
	for _, x := range f.Swipes {
		p.swipes = append(p.swipes, swipeZone{x.ID, x.R})
	}
	now := time.Now()
	return &viewEntry{key: key, p: p, tabsOn: f.TabsOn, scrollY: f.ScrollY, at: now, used: now, disk: true, saved: true}
}

func pruneViews(dir string) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fi struct {
		name string
		t    time.Time
	}
	var fs []fi
	for _, e := range es {
		switch {
		case strings.HasSuffix(e.Name(), ".view"): // the first version's files: a whole screen, unpacked
			os.Remove(filepath.Join(dir, e.Name()))
		case strings.HasSuffix(e.Name(), ".mv"):
			if info, err := e.Info(); err == nil {
				fs = append(fs, fi{e.Name(), info.ModTime()})
			}
		}
	}
	if len(fs) <= viewDiskMax {
		return
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].t.After(fs[j].t) })
	for _, f := range fs[viewDiskMax:] {
		os.Remove(filepath.Join(dir, f.name))
	}
}

// --- packing ----------------------------------------------------------------------------------

// packRGBA appends img's pixels to out, packed: a word n with the top bit set is a run of
// n&^(1<<31) copies of the pixel after it; otherwise n pixels follow as they are.
func packRGBA(out []byte, img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w <= 0 || h <= 0 {
		return out
	}
	if cap(out)-len(out) < 4*w*h/8 {
		out = append(make([]byte, 0, len(out)+4*w*h/8), out...)
	}
	put := func(v uint32) { out = binary.LittleEndian.AppendUint32(out, v) }
	pack := func(px []byte) {
		// As words: the tablet and the PC are both little-endian, so a word is a pixel's bytes.
		ws := unsafe.Slice((*uint32)(unsafe.Pointer(&px[0])), len(px)/4)
		n, lit := len(ws), 0
		for i := 0; i < n; {
			v := ws[i]
			j := i + 1
			for j < n && ws[j] == v {
				j++
			}
			if j-i >= 4 { // four or more the same: a run
				if lit < i {
					put(uint32(i - lit))
					out = append(out, px[4*lit:4*i]...)
				}
				put(1<<31 | uint32(j-i))
				put(v)
				lit = j
			}
			i = j
		}
		if lit < n {
			put(uint32(n - lit))
			out = append(out, px[4*lit:]...)
		}
	}
	if img.Stride == 4*w { // one block of rows
		o := img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y)
		pack(img.Pix[o : o+4*w*h])
		return out
	}
	for y := 0; y < h; y++ {
		pack(img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y):][:4*w])
	}
	return out
}

var errPacked = errors.New("damaged view")

// unpackRGBA fills pix (exactly) from what packRGBA wrote.
func unpackRGBA(b []byte, pix []byte) error {
	o := 0
	for len(b) >= 4 {
		n := binary.LittleEndian.Uint32(b)
		b = b[4:]
		if n&(1<<31) != 0 {
			cnt := int(n &^ (1 << 31))
			if len(b) < 4 || o+4*cnt > len(pix) {
				return errPacked
			}
			v := b[:4]
			b = b[4:]
			seg := pix[o : o+4*cnt]
			if cnt > 0 {
				copy(seg, v)
				for k := 4; k < len(seg); k *= 2 { // doubling copies: a run is a few memmoves
					copy(seg[k:], seg[:k])
				}
			}
			o += 4 * cnt
			continue
		}
		cnt := int(n)
		if len(b) < 4*cnt || o+4*cnt > len(pix) {
			return errPacked
		}
		copy(pix[o:], b[:4*cnt])
		b = b[4*cnt:]
		o += 4 * cnt
	}
	if o != len(pix) || len(b) != 0 {
		return errPacked
	}
	return nil
}
