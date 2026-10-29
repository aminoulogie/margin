package ui

import (
	"image"
	"image/draw"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// The tablet's 2013 Atom is 10-20x slower than a PC, and a page is 2.2 million pixels, so the
// hot paths here avoid per-pixel interface calls: rows are filled with copy(), and glyphs are
// rasterized once per face and reused.

// CachedFace wraps a font.Face and keeps each glyph's coverage mask after its first use.
// Glyphs are drawn at whole-pixel positions (the faces use full hinting, so advances are
// whole pixels anyway).
//
// The same face is used by every page that asks for its font and size, from more than one
// goroutine, so every call goes through mu (the opentype face under it isn't safe for
// concurrent use); advances and kerning are kept too (measuring text asked for them per rune).
type CachedFace struct {
	font.Face
	mu      sync.Mutex
	glyphs  map[rune]cachedGlyph
	advance map[rune]cachedAdvance
	kern    map[[2]rune]fixed.Int26_6
	metrics *font.Metrics
}

type cachedAdvance struct {
	a  fixed.Int26_6
	ok bool
}

type cachedGlyph struct {
	dr      image.Rectangle // relative to the dot
	mask    *image.Alpha    // origin at dr.Min
	advance fixed.Int26_6
	ok      bool
}

// Cache wraps f with a glyph cache.
func Cache(f font.Face) *CachedFace {
	if cf, ok := f.(*CachedFace); ok {
		return cf
	}
	return &CachedFace{Face: f, glyphs: map[rune]cachedGlyph{}, advance: map[rune]cachedAdvance{}, kern: map[[2]rune]fixed.Int26_6{}}
}

// GlyphAdvance implements font.Face.
func (f *CachedFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, hit := f.advance[r]
	if !hit {
		a.a, a.ok = f.Face.GlyphAdvance(r)
		f.advance[r] = a
	}
	return a.a, a.ok
}

// Kern implements font.Face.
func (f *CachedFace) Kern(r0, r1 rune) fixed.Int26_6 {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, hit := f.kern[[2]rune{r0, r1}]
	if !hit {
		k = f.Face.Kern(r0, r1)
		f.kern[[2]rune{r0, r1}] = k
	}
	return k
}

// GlyphBounds implements font.Face.
func (f *CachedFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Face.GlyphBounds(r)
}

// Metrics implements font.Face.
func (f *CachedFace) Metrics() font.Metrics {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.metrics == nil {
		m := f.Face.Metrics()
		f.metrics = &m
	}
	return *f.metrics
}

// Glyph implements font.Face.
func (f *CachedFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	f.mu.Lock()
	g, hit := f.glyphs[r]
	if !hit {
		dr, mask, mp, adv, ok := f.Face.Glyph(fixed.Point26_6{}, r)
		g = cachedGlyph{dr: dr, advance: adv, ok: ok}
		if ok {
			g.mask = image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
			draw.Draw(g.mask, g.mask.Rect, mask, mp, draw.Src)
		}
		f.glyphs[r] = g
	}
	f.mu.Unlock()
	if !g.ok {
		return image.Rectangle{}, nil, image.Point{}, g.advance, false
	}
	return g.dr.Add(image.Pt(dot.X.Round(), dot.Y.Round())), g.mask, image.Point{}, g.advance, true
}
