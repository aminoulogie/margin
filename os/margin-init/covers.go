package main

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // some books' covers

	"margin-init/epub"
	"margin-init/ui"
)

// Book covers: fetched once and kept on the tablet for good, twice: the image as it came
// (/data/condor/covers/<hash>) and, once scaled to a size it's drawn at, that too, ready to
// show (/data/condor/covers/s/<hash>-<w>x<h>: raw pixels, no decoding). A cover seen once
// appears at once from then on, everywhere, even after a restart. In memory, the most
// recently drawn covers are kept, up to coverMemBytes (all of Home's, and more).
//
// A cover that couldn't be fetched because there was no internet (just after turning the
// tablet on, Wi-Fi is still joining) is asked for again by itself a minute later, then after
// longer and longer waits, and shows up when it comes. One the server says doesn't exist, or
// that isn't a picture, isn't asked for again: that is noted on the tablet (<hash>.none) for
// a month.

var coverDir = marginHome + "/covers"

type coverKey struct {
	url  string
	w, h int
}

type coverCache struct {
	mu      sync.Mutex
	imgs    map[coverKey]*list.Element // values: *coverEntry
	lru     *list.List                 // most recently drawn at the front
	bytes   int
	pending map[coverKey]bool
	failed  map[string]*coverFail
	sem     chan struct{}
	loaded  func(url string) // called (without locks) when a cover is ready
}

type coverEntry struct {
	k   coverKey
	img *image.RGBA
}

// coverFail is a cover that couldn't be had.
type coverFail struct {
	at   time.Time
	n    int  // failures in a row
	gone bool // the server answered: no such picture (or not a picture). Not asked again
}

const (
	coverMemBytes = 48 << 20
	coverTries    = 10                  // fetches of a cover before giving up until a restart
	coverNoneFor  = 30 * 24 * time.Hour // a cover the server said doesn't exist isn't asked for
)

// coverRetryBase is the wait after a cover's first failure; it doubles after each (to 15 min).
var coverRetryBase = time.Minute

func coverRetry(n int) time.Duration {
	d := coverRetryBase
	for i := 1; i < n && d < 15*time.Minute; i++ {
		d *= 2
	}
	return min(d, 15*time.Minute)
}

var covers = newCoverCache()

func newCoverCache() *coverCache {
	return &coverCache{imgs: map[coverKey]*list.Element{}, lru: list.New(), pending: map[coverKey]bool{},
		failed: map[string]*coverFail{}, sem: make(chan struct{}, 3)}
}

// waiting: url failed and isn't to be asked for again yet. Caller holds cc.mu.
func (cc *coverCache) waiting(url string) bool {
	f := cc.failed[url]
	return f != nil && (f.gone || time.Since(f.at) < coverRetry(f.n))
}

// warm reads a cover already on the tablet into memory, without fetching anything (at start,
// for what Home shows first). True if it's in memory now.
func (cc *coverCache) warm(k coverKey) bool {
	if k.url == "" {
		return false
	}
	cc.mu.Lock()
	in := cc.imgs[k] != nil
	cc.mu.Unlock()
	if in {
		return true
	}
	img := readScaled(coverDir, k)
	if img == nil {
		return false
	}
	cc.mu.Lock()
	cc.keepCold(k, img)
	cc.mu.Unlock()
	return true
}

// get returns the cover scaled to fill w x h, or nil while it loads (or if it can't). A cover
// already scaled on the tablet is read at once (a few milliseconds), so pages draw with it.
func (cc *coverCache) get(url string, w, h int) *image.RGBA {
	if url == "" {
		return nil
	}
	k := coverKey{url, w, h}
	cc.mu.Lock()
	if e := cc.imgs[k]; e != nil {
		cc.lru.MoveToFront(e)
		img := e.Value.(*coverEntry).img
		cc.mu.Unlock()
		return img
	}
	if cc.pending[k] || cc.waiting(url) {
		cc.mu.Unlock()
		return nil
	}
	cc.mu.Unlock()
	dir := coverDir // the folder when asked, even if it changes while this loads
	if img := readScaled(dir, k); img != nil {
		cc.mu.Lock()
		cc.keep(k, img)
		cc.mu.Unlock()
		return img
	}
	none := noneKnown(dir, url)
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if none {
		cc.failed[url] = &coverFail{at: time.Now(), gone: true}
		return nil
	}
	if cc.pending[k] || cc.waiting(url) {
		return nil
	}
	cc.pending[k] = true
	go cc.load(dir, k)
	return nil
}

// keep puts a cover in memory, dropping the least recently drawn beyond the budget. Caller
// holds cc.mu.
func (cc *coverCache) keep(k coverKey, img *image.RGBA) {
	if e := cc.imgs[k]; e != nil {
		cc.lru.MoveToFront(e)
		return
	}
	cc.imgs[k] = cc.lru.PushFront(&coverEntry{k, img})
	cc.add(img)
}

// keepCold puts a cover read ahead in memory as the least recently drawn: it never pushes
// out one that was drawn, and is the first to go. Caller holds cc.mu.
func (cc *coverCache) keepCold(k coverKey, img *image.RGBA) {
	if cc.imgs[k] != nil {
		return
	}
	if cc.bytes+len(img.Pix) > coverMemBytes {
		return // memory is full of covers drawn: they matter more
	}
	cc.imgs[k] = cc.lru.PushBack(&coverEntry{k, img})
	cc.add(img)
}

func (cc *coverCache) add(img *image.RGBA) {
	cc.bytes += len(img.Pix)
	for cc.bytes > coverMemBytes && cc.lru.Len() > 1 {
		old := cc.lru.Back().Value.(*coverEntry)
		cc.lru.Remove(cc.lru.Back())
		delete(cc.imgs, old.k)
		cc.bytes -= len(old.img.Pix)
	}
}

func (cc *coverCache) load(dir string, k coverKey) {
	cc.sem <- struct{}{}
	img, err := loadCover(dir, k.url, k.w, k.h)
	<-cc.sem
	cc.mu.Lock()
	delete(cc.pending, k)
	if err != nil {
		f := cc.failed[k.url]
		if f == nil {
			f = &coverFail{}
			cc.failed[k.url] = f
		}
		f.at, f.n = time.Now(), f.n+1
		f.gone = coverGone(err) || f.n >= coverTries
		if coverGone(err) {
			markNone(dir, k.url)
		}
		if f.n == 1 || f.gone { // not every retry: while offline they'd fill init.log
			log.Printf("store: cover %s: %v", k.url, err)
		}
		if !f.gone { // no internet, or the server had a bad moment: by itself, later
			time.AfterFunc(coverRetry(f.n), func() { cc.get(k.url, k.w, k.h) })
		}
	} else {
		delete(cc.failed, k.url)
		cc.keep(k, img)
		writeScaled(dir, k, img)
	}
	loaded := cc.loaded
	cc.mu.Unlock()
	if loaded != nil && err == nil {
		loaded(k.url)
	}
}

// coverNotPicture: what came for a cover isn't one (or is a 1x1 "no cover" placeholder).
type coverNotPicture struct{ msg string }

func (e *coverNotPicture) Error() string { return e.msg }

// coverGone: err says there's no cover to have at that address, now or later (the server
// answered "not found", or sent something that isn't a cover), unlike a network failure or
// a server's bad moment (5xx, "too many requests"), which are tried again.
func coverGone(err error) bool {
	var np *coverNotPicture
	if errors.As(err, &np) {
		return true
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		code := strings.SplitN(se.status, " ", 2)[0]
		return strings.HasPrefix(code, "4") && code != "408" && code != "429"
	}
	return false
}

func noneFile(dir, url string) string { return filepath.Join(dir, hash(url)+".none") }

func markNone(dir, url string) {
	os.MkdirAll(dir, 0o755)
	os.WriteFile(noneFile(dir, url), nil, 0o644)
}

// noneKnown: the server said, less than a month ago, that url has no cover.
func noneKnown(dir, url string) bool {
	fi, err := os.Stat(noneFile(dir, url))
	return err == nil && time.Since(fi.ModTime()) < coverNoneFor
}

// scaledFile is where a cover scaled to w x h is kept.
func scaledFile(dir string, k coverKey) string {
	return filepath.Join(dir, "s", fmt.Sprintf("%s-%dx%d", hash(k.url), k.w, k.h))
}

func readScaled(dir string, k coverKey) *image.RGBA {
	b, err := os.ReadFile(scaledFile(dir, k))
	if err != nil || len(b) != 4*k.w*k.h {
		return nil
	}
	return &image.RGBA{Pix: b, Stride: 4 * k.w, Rect: image.Rect(0, 0, k.w, k.h)}
}

func writeScaled(dir string, k coverKey, img *image.RGBA) {
	f := scaledFile(dir, k)
	os.MkdirAll(filepath.Dir(f), 0o755)
	if os.WriteFile(f+".new", img.Pix, 0o644) == nil {
		os.Rename(f+".new", f)
	}
}

func loadCover(dir, url string, w, h int) (*image.RGBA, error) {
	file := filepath.Join(dir, hash(url))
	b, err := os.ReadFile(file)
	if strings.HasPrefix(url, "epub:") { // a book on the tablet: the cover is inside it
		path, inside, _ := strings.Cut(strings.TrimPrefix(url, "epub:"), "#")
		eb, err := epub.Open(path)
		if err != nil {
			return nil, err
		}
		b, err = eb.ReadFile(inside)
		eb.Close()
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := webGetCtx(ctx, url)
		if err != nil {
			return nil, err
		}
		b, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		os.MkdirAll(dir, 0o755)
		os.WriteFile(file, b, 0o644)
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		os.Remove(file)
		return nil, &coverNotPicture{fmt.Sprintf("decode: %v", err)}
	}
	sb := src.Bounds()
	if sb.Dx() < 8 || sb.Dy() < 8 { // 1x1 "no cover" placeholders
		return nil, &coverNotPicture{fmt.Sprintf("no cover (%dx%d)", sb.Dx(), sb.Dy())}
	}
	// Fill w x h, cropping the longer side (covers are close to 2:3 anyway).
	crop := sb
	if sb.Dx()*h > sb.Dy()*w {
		cw := sb.Dy() * w / h
		crop.Min.X += (sb.Dx() - cw) / 2
		crop.Max.X = crop.Min.X + cw
	} else {
		ch := sb.Dx() * h / w
		crop.Min.Y += (sb.Dy() - ch) / 2
		crop.Max.Y = crop.Min.Y + ch
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(dst, dst.Rect, src, crop, xdraw.Src, nil)
	return dst, nil
}

// coverColors are the backgrounds of covers drawn for books without one.
var coverColors = []color.RGBA{
	{0x3b, 0x4a, 0x6b, 255}, {0x6b, 0x3b, 0x4a, 255}, {0x3b, 0x6b, 0x5a, 255},
	{0x6b, 0x5a, 0x3b, 255}, {0x55, 0x3b, 0x6b, 255}, {0x2f, 0x5d, 0x6e, 255},
}

// placeholderColour is the made-up cover's colour for a book (the same every time).
func placeholderColour(key string) color.RGBA {
	h := 0
	for _, ch := range key {
		h = h*31 + int(ch)
	}
	return coverColors[(h&0x7fffffff)%len(coverColors)]
}

// drawCover draws a book's cover in r: the real one if loaded, else a made-up one with the
// title and author (shown while loading, and for books that have none).
func (c *console) drawCover(img *image.RGBA, r image.Rectangle, it *storeItem) {
	if cv := covers.get(it.cover, r.Dx(), r.Dy()); cv != nil {
		xdraw.Draw(img, r, cv, image.Point{}, xdraw.Src)
		return
	}
	bg := placeholderColour(it.key)
	ui.Fill(img, r, bg)
	ui.Fill(img, image.Rect(r.Min.X+r.Dx()/12, r.Min.Y, r.Min.X+r.Dx()/12+6, r.Max.Y), blend(bg, pgDark, 0.35))
	if r.Dx() < 120 { // too small for a title: the colour and the spine say "a book"
		return
	}
	face, small := apple().captionBold, apple().caption
	if r.Dx() > 400 {
		face = apple().headline
	}
	lh := face.Metrics().Height.Ceil() + 4
	pad := r.Dx()/12 + 22
	y := r.Min.Y + r.Dy()/6
	for i, l := range wrapText(face, it.title, r.Dx()-pad-18) {
		if i == 5 || y+lh > r.Max.Y-70 {
			break
		}
		y += lh
		ui.DrawText(img, face, r.Min.X+pad, y, pgText, visual(l))
	}
	ui.DrawText(img, small, r.Min.X+pad, r.Max.Y-36, blend(pgText, bg, 0.3), visual(clip(small, it.author, r.Dx()-pad-18)))
}
