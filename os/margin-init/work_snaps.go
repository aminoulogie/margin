package main

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Pages remembered across restarts. The first time a workstation page is opened after Margin
// starts, it has to be drawn from Soma's records, which on the tablet's Atom takes a moment
// (Reports most). So each page's first screen is kept on the tablet as a picture
// (/data/condor/soma/snaps), and that first opening shows the picture at once while the page
// is drawn fresh behind it: the page appears on the touch, and its own drawing replaces the
// picture a moment later with whatever changed since. After that first time the pages kept
// in memory (wkPageCache) do the work, so the picture is only ever shown once per page per
// start, and never over a page that was just changed.

const snapMax = 40 // pictures kept on the tablet (one per page, tab and choice seen)

var snaps struct {
	sync.Mutex
	shown map[string]bool      // views drawn (or their picture shown) since Margin started
	saved map[string]time.Time // when each view's picture was last written
}

// snapWrites are the pictures being written (tests wait for them before their folder goes).
var snapWrites sync.WaitGroup

// snapAsync: pictures are written off the touch path (tests write them at once).
var snapAsync = true

func snapDir() string { return filepath.Join(somaDir, "snaps") }

// snapKey names the view on screen: the page, the page within it and its choices (tabs,
// chips, segments), the accent; not the records it shows. "" when the screen isn't a plain
// page at its top (a sheet, typing, scrolled, an undo offer).
func (c *console) snapKey() string {
	su := &c.somaUI
	if su.typing || su.sheet != "" || su.undo != nil || c.scrollY() != 0 || c.drag.on {
		return ""
	}
	var sel []string
	for k, v := range su.workSel {
		if strings.HasPrefix(k, su.work) {
			sel = append(sel, k+"="+v)
		}
	}
	sort.Strings(sel)
	key := fmt.Sprintf("%s|%s|%s|%s|%v|%d|%d", su.work, su.workSub, su.workProj, strings.Join(sel, ","), wkLime, c.barH, c.s.W)
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:10])
}

// snapShow shows the view's picture from the last time, if this is its first drawing since
// Margin started and there is one. Caller holds drawMu.
func (c *console) snapShow(key string) {
	if key == "" || !c.screenOn {
		return
	}
	snaps.Lock()
	if snaps.shown == nil {
		snaps.shown = map[string]bool{}
	}
	first := !snaps.shown[key]
	snaps.shown[key] = true
	snaps.Unlock()
	if !first {
		return
	}
	img := readSnap(filepath.Join(snapDir(), key+".raw"), c.s.W, c.viewH())
	if img == nil {
		return
	}
	c.s.blitRGBA(img, 0, c.barH)
	c.s.Flush()
}

// snapSave writes the view's first screen from page p as its picture, in the background (at
// most once a minute a view: the pictures are only for the first moment after a start).
func (c *console) snapSave(key string, p *page) {
	if key == "" || p == nil || !soma.linked() {
		return
	}
	snaps.Lock()
	if snaps.saved == nil {
		snaps.saved = map[string]time.Time{}
	}
	if time.Since(snaps.saved[key]) < time.Minute {
		snaps.Unlock()
		return
	}
	snaps.saved[key] = time.Now()
	snaps.Unlock()
	// What blitPage shows at the top: the page's first rows, and its fixed band at the bottom.
	vh, W := c.viewH(), c.s.W
	src := p.img
	ih := src.Rect.Dy()
	pic := image.NewRGBA(image.Rect(0, 0, W, vh))
	rows := min(ih, vh)
	fh := 0
	if ih > vh {
		fh = p.footer
	}
	for y := 0; y < rows-fh; y++ {
		copy(pic.Pix[y*pic.Stride:][:4*W], src.Pix[src.PixOffset(0, y):][:4*W])
	}
	for y := 0; y < fh; y++ {
		copy(pic.Pix[(vh-fh+y)*pic.Stride:][:4*W], src.Pix[src.PixOffset(0, ih-fh+y):][:4*W])
	}
	if rows < vh { // a short page: the rest as its last row
		for y := rows; y < vh; y++ {
			copy(pic.Pix[y*pic.Stride:][:4*W], pic.Pix[(rows-1)*pic.Stride:][:4*W])
		}
	}
	path := filepath.Join(snapDir(), key+".raw")
	if !snapAsync {
		writeSnap(path, pic)
		return
	}
	snapWrites.Add(1)
	go func() {
		defer snapWrites.Done()
		writeSnap(path, pic)
	}()
}

// The picture on disk: width and height (4 bytes each), then RGBA rows.
func writeSnap(path string, img *image.RGBA) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[:4], uint32(img.Rect.Dx()))
	binary.LittleEndian.PutUint32(hdr[4:], uint32(img.Rect.Dy()))
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	_, err = f.Write(hdr[:])
	if err == nil {
		_, err = f.Write(img.Pix)
	}
	if f.Close() != nil || err != nil {
		os.Remove(tmp)
		return
	}
	os.Rename(tmp, path)
	pruneSnaps(filepath.Dir(path))
}

func readSnap(path string, w, h int) *image.RGBA {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var hdr [8]byte
	if _, err := f.Read(hdr[:]); err != nil {
		return nil
	}
	if int(binary.LittleEndian.Uint32(hdr[:4])) != w || int(binary.LittleEndian.Uint32(hdr[4:])) != h {
		return nil // drawn for another screen
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	n := 0
	for n < len(img.Pix) {
		m, err := f.Read(img.Pix[n:])
		if m <= 0 || err != nil && n+m < len(img.Pix) {
			return nil
		}
		n += m
	}
	return img
}

// pruneSnaps keeps the newest snapMax pictures.
func pruneSnaps(dir string) {
	es, err := os.ReadDir(dir)
	if err != nil || len(es) <= snapMax {
		return
	}
	type fi struct {
		name string
		t    time.Time
	}
	var fs []fi
	for _, e := range es {
		if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".raw") {
			fs = append(fs, fi{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].t.After(fs[j].t) })
	for _, f := range fs[min(snapMax, len(fs)):] {
		os.Remove(filepath.Join(dir, f.name))
	}
}
