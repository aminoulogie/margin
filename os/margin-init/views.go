package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"image"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Every screen of Margin opens at once after the first time, for good.
//
// Drawing a page takes the tablet's Atom from a tenth of a second to more than half of one,
// and every tab and sub-tab used to be drawn again each time it was opened. Now each view (a
// tab, and within it the page, sub-page and choices on show) is kept twice once drawn:
//   - in memory: the page itself (its picture, buttons, rows, swipe zones) and the screen as it
//     was sent to the panel, in the panel's own layout, so showing it again is one copy of the
//     screen, with nothing turned or drawn;
//   - on the tablet (/data/condor/views): its first screen and its buttons, so after a restart
//     even the first opening is instant.
// A view shown from what was kept is drawn fresh once the tablet has been left alone for a
// moment, if what it shows may have changed (Soma's records, or simply some time); drawn the
// same, nothing on the panel changes (Flush sends only rows that differ). Soma's views are also
// made ready in the background: when its records change (an edit, or a sync from the phone)
// or its clock moves on, the views kept are drawn again while the tablet is left alone, one at
// a time, so the next time one is opened it is already up to date. Anything done on a view (a
// tap that changes it) is drawn at once as before: only going to another view uses what was
// kept.

const (
	viewMemBytes   = 320 << 20 // what the views kept in memory may take
	viewDiskMax    = 80        // views kept on the tablet
	viewFreshFor   = 20 * time.Second
	viewIdleBefore = 700 * time.Millisecond // untouched this long before a kept view is redrawn
)

type viewEntry struct {
	key     string
	p       *page
	src     *page // the page p is a copy of
	tabsOn  string
	scrollY int
	screen  []byte // the panel's buffer as it was sent, nil for a view read from the tablet
	gen     int64  // somaGen when it was drawn
	at      time.Time
	used    time.Time
	disk    bool    // read from the tablet: drawn fresh at the first moment alone
	su      *somaUI // a Soma view: its choices, to draw it again in the background
	bucket  string  // the 5-minute clock it was drawn at (Soma's pages show the time)
	status  string  // how syncing went, on the pages that say (wkSyncShown)
	failed  bool    // drawing it again in the background didn't give the same view
}

// viewBucket is the 5-minute clock Soma's pages are drawn to (as workKey).
func viewBucket(t time.Time) string {
	t = t.In(displayZone)
	return t.Format("2006-01-02 15:") + fmt.Sprint(t.Minute()/5)
}

// stale says whether what the view shows may have changed since it was drawn.
func (e *viewEntry) stale() bool {
	switch {
	case e.disk:
		return true
	case e.su != nil:
		return e.gen != somaGen.Load() || e.bucket != viewBucket(time.Now()) || e.status != wkSyncShown(e.su)
	}
	return time.Since(e.at) > viewFreshFor
}

func (e *viewEntry) size() int { return len(e.p.img.Pix) + len(e.screen) }

var views struct {
	sync.Mutex
	m      map[string]*viewEntry
	bytes  int
	saved  map[string]time.Time
	writes sync.WaitGroup
	async  bool
	off    bool // tests that count drawings switch it off
	noPrep bool // tests: no background drawing (they call viewPrepTick themselves)
	c      *console
	back   []byte // the screen, kept while a view is drawn in the background
}

var viewPrepOnce sync.Once

func init() { views.async = true }

func viewDir() string { return filepath.Join(marginHome, "views") }

var viewDirFn = viewDir

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
		k = fmt.Sprintf("soma|%v|%s|%s|%s|%s|%s|%s|%s|%d|%s", soma.linked(), su.view, su.work, su.workSub, su.workProj,
			strings.Join(sel, ","), su.planDate, su.calDay, su.calOff, su.nutDate)
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
	if e.screen != nil && len(e.screen) == len(c.s.buf) {
		copy(c.s.buf, e.screen) // the screen as it was: one copy
		c.s.markRows(0, c.s.fbH-1)
		c.drawBar()
	} else {
		c.blitPage()
	}
	if e.stale() {
		c.viewRefreshLater(key)
	}
	return true
}

// viewKeep keeps the view just drawn (the page, and the screen as it now is). Caller holds
// drawMu.
func (c *console) viewKeep(key string) {
	if key == "" || c.page == nil || c.page.img == nil {
		return
	}
	// The very page kept already (a sheet closed over it, say), at the same place: kept as it is
	// (copying a tall page again was tens of megabytes on every tap).
	views.Lock()
	if old := views.m[key]; old != nil && old.src == c.page && old.scrollY == c.scrollY() && old.screen != nil {
		old.used = time.Now()
		c.page = old.p
		views.Unlock()
		return
	}
	views.Unlock()
	e := c.viewEntryOf(key, c.page, c.scrollY())
	e.screen = append([]byte(nil), c.s.buf...)
	c.page = e.p // what's on screen is the kept one: taps, scrolling and repaints use it
	c.viewPut(e)
	if e.scrollY == 0 {
		c.viewSave(e)
	}
	viewPrepOnce.Do(func() { go viewPrepLoop() })
}

// viewEntryOf is a copy of page pg as view key (its own picture: the page canvases are drawn
// into again).
func (c *console) viewEntryOf(key string, pg *page, scrollY int) *viewEntry {
	p := *pg
	im := *p.img
	im.Pix = append([]byte(nil), p.img.Pix[:len(p.img.Pix)]...)
	p.img = &im
	now := time.Now()
	e := &viewEntry{key: key, p: &p, src: pg, tabsOn: c.tabsOn, scrollY: scrollY, gen: somaGen.Load(),
		at: now, used: now, bucket: viewBucket(now)}
	if c.mode == modeSoma {
		su := c.somaUI
		su.workSel = maps.Clone(su.workSel)
		su.sheetStack, su.undo = nil, nil
		e.su, e.status = &su, wkSyncShown(&su)
	}
	return e
}

// viewPut keeps entry e, dropping the least recently shown past the memory allowed.
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
	views.Unlock()
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

// viewPrepTick draws again one kept Soma view whose records or clock have moved on, if the
// tablet is being left alone. True if it drew one. Caller holds drawMu.
func (c *console) viewPrepTick() bool {
	if time.Since(c.lastInput) < viewPrepIdle || c.sc.drag || c.sc.cpuOn || c.sc.motion != nil || c.press.on || c.drag.on {
		return false
	}
	if c.mode == modeVideo || c.mode == modeSoma && (c.viewOn == "" || c.viewKey() != c.viewOn) {
		return false // a film, or a Soma page on screen that isn't a kept copy (typing, a sheet)
	}
	views.Lock()
	var soma []*viewEntry
	for _, e := range views.m {
		if e.su != nil {
			soma = append(soma, e)
		}
	}
	views.Unlock()
	sort.Slice(soma, func(i, j int) bool { return soma[i].used.After(soma[j].used) })
	for i, e := range soma {
		if i >= viewPrepMost {
			break
		}
		if e.failed || !e.stale() || c.mode == modeSoma && e.key == c.viewOn {
			continue // the one on screen redraws itself when its records change
		}
		c.viewPrepare(e)
		return true
	}
	return false
}

// viewPrepare draws Soma view e again as it would be opened now, off the screen, and keeps it
// (with its screen, so opening it is one copy). What's on screen is left as it was. Caller
// holds drawMu.
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
	p := c.somaPage()
	if c.viewKeyOf() != e.key || p == nil || p.img == nil {
		e.failed = true
		return
	}
	scroll := 0
	if p.img.Rect.Dy() > c.viewH() {
		scroll = min(max(e.scrollY, 0), p.img.Rect.Dy()-c.viewH())
	}
	ne := c.viewEntryOf(e.key, p, scroll)
	ne.used = e.used
	// Its screen: drawn into the screen buffer, kept, and the screen put back as it was (no
	// Flush between, so the panel never sees it).
	s := c.s
	views.back = append(views.back[:0], s.buf...)
	lo, hi := s.dirtyLo, s.dirtyHi
	c.blitWindow(p, scroll)
	ne.screen = append([]byte(nil), s.buf...)
	copy(s.buf, views.back)
	s.dirtyLo, s.dirtyHi = lo, hi
	c.viewPut(ne)
	if scroll == 0 {
		c.viewSave(ne)
	}
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

// --- on the tablet ----------------------------------------------------------------------------

type viewFileBtn struct {
	ID string
	R  image.Rectangle
}

type viewFile struct {
	Key                  string
	W, H                 int
	Header, Footer, Left int
	TabsOn               string
	Buttons              []viewFileBtn
	Swipes               []viewFileBtn
}

func viewPath(key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(viewDirFn(), hex.EncodeToString(sum[:10])+".view")
}

// viewSave writes the view's first screen and its buttons to the tablet, in the background,
// at most once every half a minute a view.
func (c *console) viewSave(e *viewEntry) {
	views.Lock()
	if views.saved == nil {
		views.saved = map[string]time.Time{}
	}
	if time.Since(views.saved[e.key]) < 30*time.Second {
		views.Unlock()
		return
	}
	views.saved[e.key] = time.Now()
	views.Unlock()
	vh, W := c.viewH(), c.s.W
	src := e.p.img
	ih := src.Rect.Dy()
	pic := make([]byte, 4*W*vh)
	fh := 0
	if ih > vh {
		fh = e.p.footer
	}
	rows := min(ih, vh)
	for y := 0; y < rows-fh; y++ {
		copy(pic[y*4*W:][:4*W], src.Pix[src.PixOffset(0, y):][:4*W])
	}
	for y := 0; y < fh; y++ {
		copy(pic[(vh-fh+y)*4*W:][:4*W], src.Pix[src.PixOffset(0, ih-fh+y):][:4*W])
	}
	for y := rows; y < vh; y++ {
		copy(pic[y*4*W:][:4*W], pic[(rows-1)*4*W:][:4*W])
	}
	f := viewFile{Key: e.key, W: W, H: vh, Header: e.p.header, Footer: fh, Left: e.p.left, TabsOn: e.tabsOn}
	keep := func(b button) bool { // on the first screen
		if ih <= vh || b.r.Max.Y <= vh-fh {
			return true
		}
		return fh > 0 && b.r.Min.Y >= ih-fh
	}
	shift := func(r image.Rectangle) image.Rectangle {
		if fh > 0 && r.Min.Y >= ih-fh {
			return r.Sub(image.Pt(0, ih-vh))
		}
		return r
	}
	for _, b := range e.p.buttons {
		if keep(b) {
			f.Buttons = append(f.Buttons, viewFileBtn{b.id, shift(b.r)})
		}
	}
	for _, bs := range e.p.rowBtns {
		for _, b := range bs {
			if keep(b) {
				f.Buttons = append(f.Buttons, viewFileBtn{b.id, shift(b.r)})
			}
		}
	}
	for _, z := range e.p.swipes {
		if z.r.Min.Y < vh {
			f.Swipes = append(f.Swipes, viewFileBtn{z.id, z.r})
		}
	}
	path := viewPath(e.key)
	write := func() { writeView(path, f, pic) }
	if !views.async {
		write()
		return
	}
	views.writes.Add(1)
	go func() {
		defer views.writes.Done()
		write()
	}()
}

func writeView(path string, f viewFile, pic []byte) {
	var meta bytes.Buffer
	if gob.NewEncoder(&meta).Encode(f) != nil {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return
	}
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(meta.Len()))
	_, err = out.Write(n[:])
	if err == nil {
		_, err = out.Write(meta.Bytes())
	}
	if err == nil {
		_, err = out.Write(pic)
	}
	if out.Close() != nil || err != nil {
		os.Remove(tmp)
		return
	}
	os.Rename(tmp, path)
	pruneViews(filepath.Dir(path))
}

// viewFromDisk is view key as it was last kept on the tablet, as a page one screen tall (the
// rest of it comes with its fresh drawing a moment later).
func (c *console) viewFromDisk(key string) *viewEntry {
	b, err := os.ReadFile(viewPath(key))
	if err != nil || len(b) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(b[:4]))
	if n <= 0 || 4+n > len(b) {
		return nil
	}
	var f viewFile
	if gob.NewDecoder(bytes.NewReader(b[4:4+n])).Decode(&f) != nil || f.Key != key || f.W != c.s.W || f.H != c.viewH() {
		return nil
	}
	pic := b[4+n:]
	if len(pic) != 4*f.W*f.H {
		return nil
	}
	img := &image.RGBA{Pix: pic, Stride: 4 * f.W, Rect: image.Rect(0, 0, f.W, f.H)}
	p := &page{img: img, header: f.Header, footer: f.Footer, left: f.Left}
	for _, x := range f.Buttons {
		p.buttons = append(p.buttons, button{x.ID, x.R})
	}
	for _, x := range f.Swipes {
		p.swipes = append(p.swipes, swipeZone{x.ID, x.R})
	}
	return &viewEntry{key: key, p: p, tabsOn: f.TabsOn, at: time.Now(), used: time.Now(), disk: true}
}

func pruneViews(dir string) {
	es, err := os.ReadDir(dir)
	if err != nil || len(es) <= viewDiskMax {
		return
	}
	type fi struct {
		name string
		t    time.Time
	}
	var fs []fi
	for _, e := range es {
		if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".view") {
			fs = append(fs, fi{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].t.After(fs[j].t) })
	for _, f := range fs[min(viewDiskMax, len(fs)):] {
		os.Remove(filepath.Join(dir, f.name))
	}
}
