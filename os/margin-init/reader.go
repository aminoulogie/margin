package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-fonts/dejavu/dejavusans"
	"github.com/go-fonts/dejavu/dejavusansbold"
	"github.com/go-fonts/dejavu/dejavuserif"
	"github.com/go-fonts/dejavu/dejavuserifbold"
	"github.com/go-fonts/latin-modern/lmroman10bold"
	"github.com/go-fonts/latin-modern/lmroman12regular"
	"github.com/go-fonts/liberation/liberationserifbold"
	"github.com/go-fonts/liberation/liberationserifregular"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"

	"margin-init/epub"
	"margin-init/ui"
)

// The Books reader, after Soma's (src/components/BookReader.tsx and src/lib: reader-prefs,
// lines, marks, word-capture, lookup, translate, review-queue, reading-goal):
//   - themes paper / sepia / night with Soma's colours, five fonts, text size, line spacing,
//     margins, justified text, Arabic shaped and set right to left
//   - line by line: the page dims except the lit line; a tap anywhere moves to the next
//     line, a tap in the left sixth to the line before, a long press jumps to that line; past
//     the last line the page turns
//   - long press a word to select it (drag to take in more), then: highlight in one of
//     Soma's five colours, look it up, translate it, keep it in the word book
//   - contents, the book's highlights, reading time against a daily goal
// Progress is (chapter, first word of the page or lit line), so it survives any change of
// size or font. Pages are drawn once into a cache (normal and dimmed), so moving the lit
// line only redraws two strips of the screen.

type readerTheme struct {
	name                  string
	bg, fg, strong, faint color.RGBA
	mark                  color.RGBA // the lit line (line by line)
	markA                 float64
	dark                  bool
	bold                  bool // Apple's "Bold": the body in the bold face
}

// Apple Books' six themes, in its order, then Original's dark appearance (the half-moon
// button). The lit-line inks are Soma's: yellow on light paper, blue on dark.
var readerThemes = []readerTheme{
	{"Original", rgb(0xffffff), rgb(0x000000), rgb(0x000000), rgb(0x8e8e93), color.RGBA{255, 214, 10, 255}, 0.42, false, false},
	{"Quiet", rgb(0x4a4a4d), rgb(0xd8d8da), rgb(0xffffff), rgb(0x9a9a9e), color.RGBA{120, 170, 255, 255}, 0.30, true, false},
	{"Paper", rgb(0xeeeeec), rgb(0x1d1d1f), rgb(0x000000), rgb(0x8a8a8a), color.RGBA{255, 214, 10, 255}, 0.42, false, false},
	{"Bold", rgb(0xffffff), rgb(0x000000), rgb(0x000000), rgb(0x8e8e93), color.RGBA{255, 214, 10, 255}, 0.42, false, true},
	{"Calm", rgb(0xeee2cc), rgb(0x3b2f1e), rgb(0x1f1708), rgb(0x9a8a6d), color.RGBA{214, 138, 10, 255}, 0.34, false, false},
	{"Focus", rgb(0xfffcf0), rgb(0x1c1c1e), rgb(0x000000), rgb(0x8e8e93), color.RGBA{255, 214, 10, 255}, 0.42, false, false},
	{"Night", rgb(0x000000), rgb(0xe8e6e2), rgb(0xffffff), rgb(0x77736c), color.RGBA{120, 170, 255, 255}, 0.30, true, false},
}

const themeNight = 6

func (c *console) theme() readerTheme { return readerThemes[c.lib.Prefs.Theme] }

func rgb(v uint32) color.RGBA { return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255} }

const (
	textSizeMin, textSizeMax, textSizeDefault   = 26, 60, 38    // px on this 1200-wide screen
	lineHeightMin, lineHeightMax, lineHeightDef = 1.2, 2.2, 1.7 // Soma's LINE_HEIGHT
	marginMin, marginMax, marginDef             = 24, 168, 72   // Soma's MARGIN, scaled to this screen
	readerToolbarH                              = 120
	readerFooterH                               = 80
	textTopPad                                  = 30  // between the toolbar and the first line
	dimAmount                                   = 0.8 // Soma: the page under a lit line, at 0.8 opacity
)

type readerFont struct {
	id, label string
	reg, bold []byte
}

// readerFontList: free fonts in the spirit of Soma's (which are Apple's and can't ship here).
var readerFontList = []readerFont{
	{"serif", "Serif", liberationserifregular.TTF, liberationserifbold.TTF},
	{"book", "Book", lmroman12regular.TTF, lmroman10bold.TTF},
	{"classic", "Classic", dejavuserif.TTF, dejavuserifbold.TTF},
	{"sans", "Sans", dejavusans.TTF, dejavusansbold.TTF},
	{"go", "Go", goregular.TTF, gobold.TTF},
}

func fontIndex(id string) int {
	for i, f := range readerFontList {
		if f.id == id {
			return i
		}
	}
	return 0
}

type readerPrefs struct {
	Theme        int     `json:"theme"`
	ThemeSet     int     `json:"theme_set"` // 2 = Apple's themes (before: night, paper, sepia)
	PageTurn     string  `json:"page_turn"` // "curl", "slide" or "none"
	Size         int     `json:"size"`
	Font         string  `json:"font"`
	LineHeight   float64 `json:"line_height"`
	Margin       int     `json:"margin"`
	LineFocus    bool    `json:"line_focus"`
	TranslateTo  string  `json:"translate_to"`
	GoalMinutes  int     `json:"goal_minutes"`
	BooksPerYear int     `json:"books_per_year"`
	// NoOfflineDict: don't fetch the open book's words for offline Look Up (dict.go).
	NoOfflineDict bool `json:"no_offline_dict"`
}

type bookProgress struct {
	Chapter int    `json:"chapter"`
	Word    int    `json:"word"`
	Pct     int    `json:"pct"`    // through the whole book, for the library
	Opened  string `json:"opened"` // RFC 3339, for "continue reading"
}

// library is saved in /data/condor/books.json.
type library struct {
	Prefs     readerPrefs             `json:"prefs"`
	Progress  map[string]bookProgress `json:"progress"`
	Marks     map[string][]bookMark   `json:"marks"`    // highlights, by book path
	Finished  map[string]string       `json:"finished"` // book path -> date finished
	Reading   map[string]int          `json:"reading"`  // date -> seconds read
	Bookmarks map[string][]bookmark   `json:"bookmarks"`
}

const libraryPath = marginHome + "/books.json"

func loadLibrary() *library {
	l := &library{Prefs: readerPrefs{Size: textSizeDefault}}
	if b, err := os.ReadFile(libraryPath); err == nil {
		json.Unmarshal(b, l)
	}
	if l.Progress == nil {
		l.Progress = map[string]bookProgress{}
	}
	if l.Marks == nil {
		l.Marks = map[string][]bookMark{}
	}
	if l.Finished == nil {
		l.Finished = map[string]string{}
	}
	if l.Reading == nil {
		l.Reading = map[string]int{}
	}
	if l.Bookmarks == nil {
		l.Bookmarks = map[string][]bookmark{}
	}
	p := &l.Prefs
	if p.Size == 0 {
		p.Size = textSizeDefault
	}
	p.Size = min(max(p.Size, textSizeMin), textSizeMax)
	if p.ThemeSet < 2 { // night, paper, sepia → Night, Original, Calm
		if b, err := os.ReadFile(libraryPath); err == nil && len(b) > 0 {
			p.Theme = []int{themeNight, 0, 4}[min(max(p.Theme, 0), 2)]
		} else {
			p.Theme = 0 // new: Original, as Apple Books
		}
		p.ThemeSet = 2
	}
	p.Theme = min(max(p.Theme, 0), len(readerThemes)-1)
	if p.PageTurn != "slide" && p.PageTurn != "curl" {
		p.PageTurn = "none" // instant, like an e-reader
	}
	p.Font = readerFontList[fontIndex(p.Font)].id
	if p.LineHeight == 0 {
		p.LineHeight = lineHeightDef
	}
	p.LineHeight = math.Round(min(max(p.LineHeight, lineHeightMin), lineHeightMax)*10) / 10
	if p.Margin == 0 {
		p.Margin = marginDef
	}
	p.Margin = min(max(p.Margin, marginMin), marginMax)
	if !isLanguage(p.TranslateTo) {
		p.TranslateTo = "fr" // Soma's default: a reader of English books here wants it out of English
	}
	if p.GoalMinutes == 0 {
		p.GoalMinutes = 30 // Soma's DEFAULT_GOAL_MIN
	}
	if p.BooksPerYear == 0 {
		p.BooksPerYear = 12 // Soma's DEFAULT_BOOKS_PER_YEAR
	}
	return l
}

func (l *library) save() {
	b, _ := json.MarshalIndent(l, "", "  ")
	os.MkdirAll(marginHome, 0o755)
	tmp := libraryPath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, libraryPath)
	}
}

// --- fonts -----------------------------------------------------------------------------

type readerFonts struct {
	font       string
	size, lh   int // text size and line height in px
	body, bold font.Face
}

var (
	parsedMu    sync.Mutex
	parsedFonts = map[string]*opentype.Font{}
	readerFaces = map[string]*readerFonts{}
)

func parseFont(key string, data []byte) *opentype.Font {
	parsedMu.Lock()
	defer parsedMu.Unlock()
	if f := parsedFonts[key]; f != nil {
		return f
	}
	f, err := opentype.Parse(data)
	if err != nil {
		log.Printf("font %s: %v", key, err)
		f, _ = opentype.Parse(goregular.TTF)
	}
	parsedFonts[key] = f
	return f
}

// textFace is a face of the font at size, falling back to DejaVu Sans (Arabic and more).
func textFace(key string, data []byte, bold bool, size float64) font.Face {
	primary := parseFont(key, data)
	fbKey, fbData := "dejavusans", dejavusans.TTF
	if bold {
		fbKey, fbData = "dejavusansbold", dejavusansbold.TTF
	}
	fallback := parseFont(fbKey, fbData)
	mk := func(f *opentype.Font) font.Face {
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		return face
	}
	return ui.Cache(ui.Fallback([]font.Face{mk(primary), mk(fallback)}, []*sfnt.Font{primary, fallback}))
}

// newReaderFonts makes (once) the faces for a font, size and line spacing.
func newReaderFonts(fontID string, size int, lineHeight float64) *readerFonts {
	return newReaderFontsB(fontID, size, lineHeight, false)
}

// newReaderFontsB: with boldBody, the text itself is set in the bold face (Apple's "Bold").
func newReaderFontsB(fontID string, size int, lineHeight float64, boldBody bool) *readerFonts {
	key := fmt.Sprintf("%s/%d/%.1f/%v", fontID, size, lineHeight, boldBody)
	if rf := readerFaces[key]; rf != nil {
		return rf
	}
	f := readerFontList[fontIndex(fontID)]
	rf := &readerFonts{font: f.id, size: size, lh: int(math.Round(float64(size) * lineHeight)),
		body: textFace(f.id, f.reg, false, float64(size)), bold: textFace(f.id+"-bold", f.bold, true, float64(size)*1.45)}
	if boldBody {
		rf.body = textFace(f.id+"-bold", f.bold, true, float64(size))
	}
	readerFaces[key] = rf
	return rf
}

func (c *console) readerFontsNow() *readerFonts {
	p := c.lib.Prefs
	return newReaderFontsB(p.Font, p.Size, p.LineHeight, c.theme().bold)
}

// --- layout ------------------------------------------------------------------------------

// rline is one laid-out line of a chapter, placed on its page.
type rline struct {
	tline
	heading  bool
	ornament bool // the last line of a chapter heading: a flourish under it
	block    int
	y, h     int // top and height within the text area
	word     int // index of its first word in the chapter
}

func (l rline) last() int { return l.words[len(l.words)-1].idx }

// openBook is the book being read.
type openBook struct {
	path     string
	b        *epub.Book
	chapter  int
	blocks   []epub.Block
	words    []string // the chapter's words, in order
	blockOf  []int    // words[i] is in blocks[blockOf[i]]
	pages    [][]rline
	page     int
	line     int // the lit line on the page (line by line)
	chTitle  string
	titles   []string // contents, filled in the background
	finished bool     // just turned past the last page
}

// layoutChapter wraps a chapter's paragraphs to the page and cuts them into pages.
func layoutChapter(blocks []epub.Block, f *readerFonts, width, height int) (pages [][]rline, words []string, blockOf []int) {
	var lines []rline
	extra := 0 // room for a heading's ornament
	for bi, blk := range blocks {
		ws := strings.Fields(blk.Text)
		face, gap, lh := f.body, f.lh/3, f.lh
		if blk.Heading {
			face, gap, lh = f.bold, f.lh, int(float64(f.lh)*1.3)
		}
		tls := layoutWords(face, ws, len(words), width, !blk.Heading)
		for i, tl := range tls {
			g := 0
			if i == 0 && len(lines) > 0 {
				g = gap + extra
			}
			if blk.Heading { // Apple Books: chapter titles centred
				lw := tl.words[len(tl.words)-1].x + tl.words[len(tl.words)-1].w - tl.words[0].x
				if tl.rtl {
					lw = tl.words[0].x + tl.words[0].w - tl.words[len(tl.words)-1].x
				}
				shift := (width-lw)/2 - min(tl.words[0].x, tl.words[len(tl.words)-1].x)
				for k := range tl.words {
					tl.words[k].x += shift
				}
			}
			lines = append(lines, rline{tline: tl, heading: blk.Heading, block: bi, y: g, h: lh, word: tl.words[0].idx,
				ornament: blk.Heading && i == len(tls)-1})
		}
		extra = 0
		if blk.Heading && len(tls) > 0 {
			extra = f.lh / 2
		}
		for range ws {
			blockOf = append(blockOf, bi)
		}
		words = append(words, ws...)
	}
	var pg []rline
	y := 0
	for _, l := range lines {
		gap := l.y
		if len(pg) == 0 {
			gap = 0
		}
		if len(pg) > 0 && y+gap+l.h > height {
			pages = append(pages, pg)
			pg, y, gap = nil, 0, 0
		}
		l.y = y + gap
		pg = append(pg, l)
		y = l.y + l.h
	}
	if len(pg) > 0 {
		pages = append(pages, pg)
	}
	return pages, words, blockOf
}

// textRect is the text column on the reader page (page coordinates).
func (c *console) textRect() image.Rectangle {
	m := c.lib.Prefs.Margin
	return image.Rect(m, readerToolbarH+textTopPad, c.s.W-m, c.s.H-c.barH-readerFooterH-10)
}

// loadChapter lays out chapter i and moves to the page holding word (or the last page if
// word < 0). Empty chapters are skipped in the direction of travel.
func (c *console) loadChapter(i, word int) bool {
	ob := c.book
	dir := 1
	if word < 0 {
		dir = -1
	}
	for ; i >= 0 && i < len(ob.b.Chapters); i += dir {
		x, err := ob.b.ReadFile(ob.b.Chapters[i].Path)
		if err != nil {
			continue
		}
		blocks := epub.ChapterText(x)
		tr := c.textRect()
		pages, words, blockOf := layoutChapter(blocks, c.rf, tr.Dx(), tr.Dy())
		if len(pages) == 0 {
			continue
		}
		ob.chapter, ob.pages, ob.blocks, ob.words, ob.blockOf = i, pages, blocks, words, blockOf
		ob.chTitle = epub.ChapterTitle(x, fmt.Sprintf("chapter %d", i+1))
		ob.page, ob.line = 0, 0
		if word < 0 {
			ob.page = len(pages) - 1
			ob.line = len(pages[ob.page]) - 1
		} else {
			ob.page, ob.line = findWord(pages, word)
		}
		c.invalidatePage()
		return true
	}
	return false
}

// findWord is the page and line holding a chapter's word.
func findWord(pages [][]rline, word int) (page, line int) {
	for p, pg := range pages {
		for li, l := range pg {
			if l.word <= word {
				page, line = p, li
			}
		}
	}
	return page, line
}

func (c *console) openBookAt(path string) {
	b, err := epub.Open(path)
	if err != nil {
		log.Printf("books: %s: %v", path, err)
		return
	}
	if c.book != nil {
		c.book.b.Close()
	}
	c.rd = readerUI{}
	c.rf = c.readerFontsNow()
	c.book = &openBook{path: path, b: b}
	pr := c.lib.Progress[path]
	if !c.loadChapter(min(pr.Chapter, len(b.Chapters)-1), pr.Word) && !c.loadChapter(0, 0) {
		log.Printf("books: %s has no readable text", path)
		b.Close()
		c.book = nil
		return
	}
	c.lastRead = time.Now()
	c.saveProgress() // opened now: first under Continue
	go c.loadTitles(c.book)
	c.startDictFetch() // its words, for Look Up without internet
	c.setMode(modeReader)
}

// loadTitles reads each chapter's title for the contents, in the background.
func (c *console) loadTitles(ob *openBook) {
	titles := make([]string, len(ob.b.Chapters))
	for i, ch := range ob.b.Chapters {
		x, err := ob.b.ReadFile(ch.Path)
		if err != nil {
			titles[i] = fmt.Sprintf("chapter %d", i+1)
			continue
		}
		titles[i] = epub.ChapterTitle(x, fmt.Sprintf("chapter %d", i+1))
	}
	drawMu.Lock()
	ob.titles = titles
	if c.book == ob && c.mode == modeReader && c.rd.view == "contents" {
		c.showPage()
	}
	drawMu.Unlock()
}

// turn moves one page forward (+1) or back (-1), across chapters. Caller holds drawMu.
func (c *console) turn(dir int) {
	ob := c.book
	if ob == nil {
		return
	}
	old := c.turnFrom() // the page as it is, for the page-turn animation
	c.readTick()
	c.rd.sel, c.rd.menu, c.rd.panel, c.rd.chrome = nil, menuNone, nil, false
	switch {
	case dir > 0 && ob.page < len(ob.pages)-1:
		ob.page, ob.line = ob.page+1, 0
	case dir < 0 && ob.page > 0:
		ob.page--
		ob.line = len(ob.pages[ob.page]) - 1
		if !c.lib.Prefs.LineFocus {
			ob.line = 0
		}
	case dir > 0:
		if !c.loadChapter(ob.chapter+1, 0) {
			c.markFinished()
			c.showPage()
			return
		}
	default:
		if !c.loadChapter(ob.chapter-1, -1) {
			return
		}
		if !c.lib.Prefs.LineFocus {
			ob.line = 0
		}
	}
	c.invalidatePage()
	c.saveProgress()
	if old != nil {
		c.s.hold = true
		c.showPage()
		c.s.hold = false
		c.animateTurn(old, dir)
		return
	}
	c.showPage()
}

// stepLine moves the lit line (line by line), turning the page past either end.
func (c *console) stepLine(dir int) {
	ob := c.book
	if ob == nil {
		return
	}
	next := ob.line + dir
	if next < 0 || next >= len(ob.pages[ob.page]) {
		c.turn(dir)
		return
	}
	c.readTick()
	old := ob.line
	ob.line = next
	c.saveProgress()
	c.refreshLines(old, next)
}

func (c *console) saveProgress() {
	ob := c.book
	pg := ob.pages[ob.page]
	w := pg[0].word
	if c.lib.Prefs.LineFocus && ob.line < len(pg) {
		w = pg[ob.line].word
	}
	pct := c.progressPct()
	if ob.finished {
		pct = 100
	}
	c.lib.Progress[ob.path] = bookProgress{ob.chapter, w, pct, time.Now().Format(time.RFC3339)}
	c.lib.save()
}

// relayout re-paginates after a change of size, font, spacing or margins, keeping the
// first word on the page (or the lit line).
func (c *console) relayout() {
	ob := c.book
	pg := ob.pages[ob.page]
	word := pg[0].word
	if c.lib.Prefs.LineFocus && ob.line < len(pg) {
		word = pg[ob.line].word
	}
	c.rf = c.readerFontsNow()
	c.loadChapter(ob.chapter, word)
}

func (c *console) progressPct() int {
	ob := c.book
	n := len(ob.b.Chapters)
	if n == 0 {
		return 0
	}
	return int(100 * (float64(ob.chapter) + float64(ob.page+1)/float64(len(ob.pages))) / float64(n))
}

// --- drawing -------------------------------------------------------------------------------

// pageCache holds the current page drawn once, normally and dimmed (for line by line).
type pageCache struct {
	key         string
	normal, dim *image.RGBA
}

func (c *console) invalidatePage() { c.pcache.key = "" }

func (c *console) pageKey() string {
	ob, p := c.book, c.lib.Prefs
	return fmt.Sprintf("%s|%d|%d|%d|%s|%d|%.1f|%d|%d|%v|%v", ob.path, ob.chapter, ob.page, p.Theme, p.Font, p.Size,
		p.LineHeight, p.Margin, c.marksVersion, p.LineFocus, ob.finished)
}

func blend(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 255}
}

// blendRect lays colour c at alpha a over r (text under it stays readable).
func blendRect(img *image.RGBA, r image.Rectangle, c color.RGBA, a float64) {
	r = r.Intersect(img.Rect)
	ai := int(a * 256)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y):][:4*r.Dx()]
		for i := 0; i < len(row); i += 4 {
			row[i] = uint8((int(row[i])*(256-ai) + int(c.R)*ai) >> 8)
			row[i+1] = uint8((int(row[i+1])*(256-ai) + int(c.G)*ai) >> 8)
			row[i+2] = uint8((int(row[i+2])*(256-ai) + int(c.B)*ai) >> 8)
		}
	}
}

// band is the strip a line's text occupies (for its highlight, the lit line, a selection),
// in page coordinates.
func (c *console) band(l rline) image.Rectangle {
	tr := c.textRect()
	face := c.rf.body
	if l.heading {
		face = c.rf.bold
	}
	m := face.Metrics()
	th := (m.Ascent + m.Descent).Ceil()
	top := tr.Min.Y + l.y + (l.h-th)/2 - 6
	return image.Rect(tr.Min.X, top, tr.Max.X, top+th+12)
}

// wordsRect is the box around words [from, to] of a line (both chapter word indices).
func (c *console) wordsRect(l rline, from, to int) (image.Rectangle, bool) {
	b := c.band(l)
	x0, x1 := math.MaxInt, math.MinInt
	for _, w := range l.words {
		if w.idx >= from && w.idx <= to {
			x0, x1 = min(x0, w.x), max(x1, w.x+w.w)
		}
	}
	if x0 > x1 {
		return image.Rectangle{}, false
	}
	return image.Rect(b.Min.X+x0-4, b.Min.Y, b.Min.X+x1+4, b.Max.Y), true
}

// drawText draws the page's lines (with their highlights) into img; dim fades everything
// toward the paper the way Soma's overlay does.
func (c *console) drawText(img *image.RGBA, th readerTheme, dim bool) {
	ob := c.book
	fade := func(col color.RGBA) color.RGBA {
		if dim {
			return blend(col, th.bg, dimAmount)
		}
		return col
	}
	tr := c.textRect()
	marks := c.lib.Marks[ob.path]
	for _, l := range ob.pages[ob.page] {
		for _, m := range marks {
			if m.Chapter != ob.chapter || m.End < l.word || m.Start > l.last() {
				continue
			}
			if r, ok := c.wordsRect(l, m.Start, m.End); ok {
				mc := markColour(m.Colour)
				a := mc.lightA
				if th.dark {
					a = mc.darkA
				}
				ink := mc.light
				if th.dark {
					ink = mc.dark
				}
				ui.RoundRect(img, r, 6, fade(blend(th.bg, ink, a)))
			}
		}
		face, col := c.rf.body, th.fg
		if l.heading {
			face, col = c.rf.bold, th.strong
		}
		asc := face.Metrics().Ascent.Ceil()
		top := tr.Min.Y + l.y + (l.h-(face.Metrics().Ascent+face.Metrics().Descent).Ceil())/2
		drawWords(img, face, l.tline, tr.Min.X, top+asc, fade(col))
		if l.ornament { // a flourish under the chapter title: a line, a diamond, a line
			cx, oy := tr.Min.X+tr.Dx()/2, tr.Min.Y+l.y+l.h+c.rf.lh/4
			oc := fade(blend(th.bg, th.fg, 0.55))
			line(img, cx-70, oy, cx-16, oy, 3, oc)
			line(img, cx+16, oy, cx+70, oy, 3, oc)
			for i := 0; i <= 9; i++ {
				ui.Fill(img, image.Rect(cx-i, oy-9+i, cx+i+1, oy-8+i), oc)
				ui.Fill(img, image.Rect(cx-i, oy+9-i, cx+i+1, oy+10-i), oc)
			}
		}
	}
}

// buildPage draws the page's cache: toolbar, text, footer (normal), and the text dimmed.
func (c *console) buildPage() {
	key := c.pageKey()
	if c.pcache.key == key {
		return
	}
	th := c.theme()
	w, h := c.s.W, c.s.H-c.barH
	if c.pcache.normal == nil || c.pcache.normal.Rect.Dx() != w || c.pcache.normal.Rect.Dy() != h {
		c.pcache.normal = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	img := c.pcache.normal
	ui.Fill(img, img.Rect, th.bg)
	c.drawText(img, th, false)
	c.drawFooter(img, th)
	if c.lib.Prefs.LineFocus {
		if c.pcache.dim == nil || c.pcache.dim.Rect.Dx() != w || c.pcache.dim.Rect.Dy() != h {
			c.pcache.dim = image.NewRGBA(image.Rect(0, 0, w, h))
		}
		ui.Fill(c.pcache.dim, c.pcache.dim.Rect, th.bg)
		c.drawText(c.pcache.dim, th, true)
	}
	c.pcache.key = key
}

func (c *console) drawFooter(img *image.RGBA, th readerTheme) {
	ob := c.book
	f := apple()
	h := img.Rect.Dy()
	m := c.lib.Prefs.Margin
	// Apple Books: the book's title in small grey type above the text, the page below it,
	// how much of the chapter is left at the right.
	apTextCenter(img, f.caption, c.s.W/2, 70, th.faint, clip(f.caption, ob.b.Title, c.s.W-2*m-160))
	foot := fmt.Sprintf("%d of %d", ob.page+1, len(ob.pages))
	if ob.finished {
		foot = "The End"
	}
	apTextCenter(img, f.caption, c.s.W/2, h-readerFooterH/2, th.faint, foot)
	if left := len(ob.pages) - ob.page - 1; left > 0 && !ob.finished {
		s := fmt.Sprintf("%d pages left in chapter", left)
		if left == 1 {
			s = "1 page left in chapter"
		}
		apTextRight(img, f.caption, c.s.W-m, h-readerFooterH/2+9, th.faint, s)
	}
	if c.bookmarked() != nil { // the red ribbon
		x := c.s.W - 110
		ui.Fill(img, image.Rect(x, 0, x+44, 70), rgb(0xff3b30))
		for i := 0; i < 18; i++ {
			ui.Fill(img, image.Rect(x, 70+i, x+22-i*22/18, 71+i), rgb(0xff3b30))
			ui.Fill(img, image.Rect(x+22+i*22/18, 70+i, x+44, 71+i), rgb(0xff3b30))
		}
	}
}

// bookmark is a saved place in a book.
type bookmark struct {
	ID      string `json:"id"`
	Chapter int    `json:"chapter"`
	Word    int    `json:"word"`
	Text    string `json:"text"` // the page's first words
	Added   string `json:"added"`
}

// bookmarked is the bookmark on the page on screen, or nil.
func (c *console) bookmarked() *bookmark {
	ob := c.book
	pg := ob.pages[ob.page]
	for i, b := range c.lib.Bookmarks[ob.path] {
		if b.Chapter == ob.chapter && b.Word >= pg[0].word && b.Word <= pg[len(pg)-1].last() {
			return &c.lib.Bookmarks[ob.path][i]
		}
	}
	return nil
}

// toggleBookmark adds or removes the page's bookmark.
func (c *console) toggleBookmark() {
	ob := c.book
	if b := c.bookmarked(); b != nil {
		var kept []bookmark
		for _, o := range c.lib.Bookmarks[ob.path] {
			if o.ID != b.ID {
				kept = append(kept, o)
			}
		}
		c.lib.Bookmarks[ob.path] = kept
	} else {
		w := ob.pages[ob.page][0].word
		text := strings.Join(ob.words[w:min(w+14, len(ob.words))], " ")
		c.lib.Bookmarks[ob.path] = append(c.lib.Bookmarks[ob.path], bookmark{ID: fmt.Sprintf("b%d", time.Now().UnixNano()),
			Chapter: ob.chapter, Word: w, Text: text + "…", Added: time.Now().Format("2006-01-02")})
	}
	c.lib.save()
	c.marksVersion++
}

// textRows is the band of rows the text area covers, where dimming applies.
func (c *console) textRows() image.Rectangle {
	return image.Rect(0, readerToolbarH, c.s.W, c.s.H-c.barH-readerFooterH)
}

// copyRows copies rows [r.Min.Y, r.Max.Y) of src into dst (same size images).
func copyRows(dst, src *image.RGBA, r image.Rectangle) {
	r = r.Intersect(dst.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		o := dst.PixOffset(r.Min.X, y)
		copy(dst.Pix[o:o+4*r.Dx()], src.Pix[o:o+4*r.Dx()])
	}
}

// litRect is the lit line's strip, padded a little (page coordinates).
func (c *console) litRect(li int) image.Rectangle {
	ob := c.book
	if li < 0 || li >= len(ob.pages[ob.page]) {
		return image.Rectangle{}
	}
	b := c.band(ob.pages[ob.page][li])
	return image.Rect(0, b.Min.Y-2, c.s.W, b.Max.Y+2)
}

// readerPage composes the page: cached text, the lit line, a selection, the toolbar and
// whatever is open over it (word menu, result card, settings, contents, highlights).
func (c *console) readerPage() *page {
	if c.rd.view != "" {
		return c.readerListPage()
	}
	ob := c.book
	th := c.theme()
	c.buildPage()
	img := canvas(c.s.W, c.s.H-c.barH)
	copy(img.Pix, c.pcache.normal.Pix)
	if c.lib.Prefs.LineFocus {
		lit := c.litRect(ob.line)
		rows := c.textRows()
		copyRows(img, c.pcache.dim, image.Rect(0, rows.Min.Y, c.s.W, lit.Min.Y))
		copyRows(img, c.pcache.dim, image.Rect(0, lit.Max.Y, c.s.W, rows.Max.Y))
		if !lit.Empty() {
			b := c.band(ob.pages[ob.page][ob.line])
			blendRect(img, image.Rect(b.Min.X-16, b.Min.Y, b.Max.X+16, b.Max.Y), th.mark, th.markA)
		}
	}
	if s := c.rd.sel; s != nil { // iOS selection: light blue, with a handle at each end
		var first, last image.Rectangle
		for _, l := range ob.pages[ob.page] {
			if r, ok := c.wordsRect(l, s.from, s.to); ok {
				blendRect(img, r, apBlue, 0.22)
				if first.Empty() {
					first = r
				}
				last = r
			}
		}
		if !first.Empty() && c.rd.g.selecting == false {
			ui.Fill(img, image.Rect(first.Min.X-2, first.Min.Y, first.Min.X+2, first.Max.Y), apBlue)
			ui.Circle(img, first.Min.X, first.Min.Y-8, 10, apBlue)
			ui.Fill(img, image.Rect(last.Max.X-2, last.Min.Y, last.Max.X+2, last.Max.Y), apBlue)
			ui.Circle(img, last.Max.X, last.Max.Y+8, 10, apBlue)
		}
	}
	p := &page{img: img}
	c.drawOverlays(p, th) // menu, card, settings: their buttons come first
	if c.overlayOpen() {
		return p
	}
	h := c.s.H - c.barH
	if c.rd.chrome {
		c.drawChrome(p, th)
		p.buttons = append(p.buttons, button{"chrome", img.Rect}) // a tap on the page hides it again
		return p
	}
	// Apple Books: the sides turn the page, the middle brings up the controls. Line by line
	// (Soma): the whole page steps lines, the left sixth back; the controls live in the
	// margins above and below the text.
	tr := c.textRect()
	if c.lib.Prefs.LineFocus {
		p.buttons = append(p.buttons,
			button{"chrome", image.Rect(0, 0, c.s.W, tr.Min.Y-10)},
			button{"chrome", image.Rect(0, h-readerFooterH, c.s.W, h)},
			button{"prev", image.Rect(0, 0, c.s.W/6, h)},
			button{"next", image.Rect(c.s.W/6, 0, c.s.W, h)})
	} else {
		third := c.s.W / 3
		p.buttons = append(p.buttons,
			button{"prev", image.Rect(0, 0, third, h)},
			button{"chrome", image.Rect(third, 0, c.s.W-third, h)},
			button{"next", image.Rect(c.s.W-third, 0, c.s.W, h)})
	}
	return p
}

// accent is the controls' tint on a theme: system blue (the dark-mode blue on dark paper).
func (th readerTheme) accent() color.RGBA {
	if th.dark {
		return rgb(0x0a84ff)
	}
	return apBlue
}

// drawChrome is Soma's reader controls (BookChrome.tsx), floating over the page as glass:
// top left, back, the contents and the highlights; top right, Aa, line by line and the bookmark; at the
// bottom, "Page N of M", a thin line through the book, and a strip of the chapter's pages
// between ‹ and ›.
func (c *console) drawChrome(p *page, th readerTheme) {
	ob := c.book
	img := p.img
	h := img.Rect.Dy()
	w := c.s.W
	ink := rgb(0xf5f7fa)
	pillBG := rgb(0x1e2028)
	pill := func(r image.Rectangle) {
		ui.RoundRect(img, r, r.Dy()/2, blend(th.bg, rgb(0xffffff), 0.18)) // the rim
		ui.RoundRect(img, r.Inset(2), r.Dy()/2-2, pillBG)
	}
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }

	// Top left: back, contents.
	lp := image.Rect(24, 18, 24+290, 18+100)
	pill(lp)
	iconBack(img, lp.Min.X+44, (lp.Min.Y+lp.Max.Y)/2, ink)
	iconList(img, lp.Min.X+140, (lp.Min.Y+lp.Max.Y)/2, ink)
	iconPen(img, lp.Min.X+236, (lp.Min.Y+lp.Max.Y)/2, ink) // highlights and notes
	btn("shelf", image.Rect(0, 0, lp.Min.X+95, lp.Max.Y+10))
	btn("contents", image.Rect(lp.Min.X+95, 0, lp.Min.X+190, lp.Max.Y+10))
	btn("marks", image.Rect(lp.Min.X+190, 0, lp.Max.X, lp.Max.Y+10))
	// Top right: Aa, line by line, the bookmark.
	rp := image.Rect(w-24-300, 18, w-24, 118)
	pill(rp)
	cy := (rp.Min.Y + rp.Max.Y) / 2
	iconAa(img, rp.Min.X+56, cy, ink)
	lit := blend(pillBG, ink, 0.45)
	if c.lib.Prefs.LineFocus {
		lit = th.accent()
	}
	iconLines(img, rp.Min.X+150, cy, ink, lit)
	bm := c.bookmarked()
	bmCol := ink
	if bm != nil {
		bmCol = apRed
	}
	iconBookmark(img, rp.Min.X+244, cy, bmCol, bm != nil)
	btn("settings", image.Rect(rp.Min.X, 0, rp.Min.X+100, rp.Max.Y+10))
	btn("linemode", image.Rect(rp.Min.X+100, 0, rp.Min.X+200, rp.Max.Y+10))
	btn("r:bookmark", image.Rect(rp.Min.X+200, 0, w, rp.Max.Y+10))

	// Bottom: a fade to the page's colour, "Page N of M", the line, the strip.
	bot := h - 380
	for y := bot; y < h; y++ {
		a := min(1, float64(y-bot)/110)
		glass(img, image.Rect(0, y, w, y+1), 0, th.bg, a)
	}
	pct := c.progressPct()
	n := max(len(ob.b.Chapters), 1)
	total := max(len(ob.pages)*n, 1)
	at := min(max(total*pct/100, 1), total)
	f := apple()
	apTextCenter(img, f.captionBold, w/2, h-232, blend(th.bg, ink, 0.55), fmt.Sprintf("Page %d of %d", at, total))
	ln := image.Rect(32, h-196, w-32, h-190)
	ui.RoundRect(img, ln, 3, blend(th.bg, ink, 0.18))
	ui.RoundRect(img, image.Rect(ln.Min.X, ln.Min.Y, ln.Min.X+max(6, ln.Dx()*pct/100), ln.Max.Y), 3, blend(th.bg, ink, 0.7))
	for i := 0; i < 100; i++ { // tap the line to jump
		x0, x1 := ln.Min.X+ln.Dx()*i/100, ln.Min.X+ln.Dx()*(i+1)/100
		btn(fmt.Sprintf("r:seek:%d", i), image.Rect(x0, ln.Min.Y-30, x1, ln.Max.Y+30))
	}
	// ‹ the strip ›.
	sy0, sy1 := h-150, h-40
	prev := image.Rect(24, sy0+5, 124, sy1-5)
	next := image.Rect(w-124, sy0+5, w-24, sy1-5)
	for _, r := range []image.Rectangle{prev, next} {
		ui.Circle(img, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, r.Dx()/2, blend(th.bg, rgb(0xffffff), 0.12))
		ui.Circle(img, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, r.Dx()/2-2, pillBG)
	}
	iconBack(img, prev.Min.X+42, (prev.Min.Y+prev.Max.Y)/2, ink)
	iconChevronRight(img, next.Min.X+46, (next.Min.Y+next.Max.Y)/2, ink)
	btn("prev", prev)
	btn("next", next)
	strip := image.Rect(prev.Max.X+20, sy0, next.Min.X-20, sy1)
	pill(strip)
	// The chapter's pages around this one, as little pages; this one is the page itself, small.
	tw, thh := 46, 76
	slots := (strip.Dx() - 60) / (tw + 10)
	first := max(0, min(ob.page-slots/2, len(ob.pages)-slots))
	x := strip.Min.X + (strip.Dx()-min(slots, len(ob.pages))*(tw+10)+10)/2
	ty := (strip.Min.Y+strip.Max.Y)/2 - thh/2
	for k := first; k < min(first+slots, len(ob.pages)); k++ {
		r := image.Rect(x, ty, x+tw, ty+thh)
		if k == ob.page {
			// The page on screen, shrunk.
			src := c.pcache.normal
			if src != nil {
				for yy := 0; yy < thh; yy++ {
					sy := yy * src.Rect.Dy() / thh
					for xx := 0; xx < tw; xx++ {
						sx := xx * src.Rect.Dx() / tw
						o := src.PixOffset(sx, sy)
						img.SetRGBA(r.Min.X+xx, r.Min.Y+yy, color.RGBA{src.Pix[o], src.Pix[o+1], src.Pix[o+2], 255})
					}
				}
			}
			ui.RoundRect(img, image.Rect(r.Min.X-4, r.Min.Y-4, r.Max.X+4, r.Min.Y-1), 2, ink)
			ui.RoundRect(img, image.Rect(r.Min.X-4, r.Max.Y+1, r.Max.X+4, r.Max.Y+4), 2, ink)
			ui.Fill(img, image.Rect(r.Min.X-4, r.Min.Y-4, r.Min.X-1, r.Max.Y+4), ink)
			ui.Fill(img, image.Rect(r.Max.X+1, r.Min.Y-4, r.Max.X+4, r.Max.Y+4), ink)
		} else {
			ui.Fill(img, r, blend(th.bg, ink, 0.08))
			for l := 0; l < 7; l++ { // its lines of text, suggested
				ui.Fill(img, image.Rect(r.Min.X+6, r.Min.Y+10+l*9, r.Max.X-6-(l%3)*5, r.Min.Y+12+l*9), blend(th.bg, ink, 0.22))
			}
		}
		btn(fmt.Sprintf("r:page:%d", k), r.Inset(-5))
		x += tw + 10
	}
	btn("r:bar", image.Rect(0, bot+110, w, h))
}

// seek jumps to a point through the book (0..99, in hundredths). Caller holds drawMu.
func (c *console) seek(at int) {
	ob := c.book
	n := len(ob.b.Chapters)
	pos := float64(at) / 100 * float64(n)
	ch := min(int(pos), n-1)
	frac := pos - float64(ch)
	if ch != ob.chapter && !c.loadChapter(ch, 0) {
		return
	}
	ob.page = min(int(frac*float64(len(ob.pages))), len(ob.pages)-1)
	ob.line = 0
	c.invalidatePage()
	c.saveProgress()
}

// refreshLines redraws only the strips of two lines (moving the lit line). Caller holds drawMu.
func (c *console) refreshLines(a, b int) {
	if !c.screenOn || c.mode != modeReader || c.rd.view != "" {
		return
	}
	ra, rb := c.litRect(a), c.litRect(b)
	if c.page != nil && c.pcache.key == c.pageKey() && c.pcache.dim != nil && !c.overlayOpen() {
		// The page on screen is the cached page with another line lit: the band glides from
		// the old line to the new, then the old line is put back to dim and the new one lit.
		img := c.page.img
		th := c.theme()
		if !ra.Empty() && !rb.Empty() && c.animOK() && animScale > 0 {
			c.glideLines(a, b)
			area := ra.Union(rb)
			copyRows(img, c.pcache.dim, area)
			ra = area // blit all of it: the glide drew in between
		}
		copyRows(img, c.pcache.dim, ra)
		copyRows(img, c.pcache.normal, rb)
		if line := c.book.pages[c.book.page]; b >= 0 && b < len(line) {
			bd := c.band(line[b])
			blendRect(img, image.Rect(bd.Min.X-16, bd.Min.Y, bd.Max.X+16, bd.Max.Y), th.mark, th.markA)
		}
	} else {
		c.page = c.readerPage()
	}
	for _, r := range []image.Rectangle{ra, rb} {
		if r.Empty() {
			continue
		}
		c.s.blitRGBA(c.page.img.SubImage(r).(*image.RGBA), 0, c.barH+r.Min.Y)
	}
	c.s.Flush()
}

// --- reading time ------------------------------------------------------------------------

// readTick counts the time since the last thing done in a book as reading, if it was under
// two minutes ago (longer means the tablet was put down). Caller holds drawMu.
func (c *console) readTick() {
	now := time.Now()
	d := now.Sub(c.lastRead)
	c.lastRead = now
	if d > 0 && d < 2*time.Minute {
		c.lib.Reading[now.Format("2006-01-02")] += int(d.Seconds())
		// Soma's reading log counts it too, a minute at a time (reading-clock.ts does the same
		// for a book open in Soma).
		if c.somaReadSecs += d.Seconds(); c.somaReadSecs >= 60 && soma.linked() {
			m := int(c.somaReadSecs / 60)
			c.somaReadSecs -= float64(m * 60)
			somaAddReading(somaToday(), m)
		}
	}
}

func (c *console) markFinished() {
	ob := c.book
	ob.finished = true
	if _, ok := c.lib.Finished[ob.path]; !ok {
		c.lib.Finished[ob.path] = time.Now().Format("2006-01-02")
		c.lib.save()
	}
	c.invalidatePage()
}

// readingToday is minutes read today; booksThisYear counts books finished this year.
func (l *library) readingToday() int {
	return l.Reading[time.Now().Format("2006-01-02")] / 60
}

func (l *library) booksThisYear() int {
	y, n := time.Now().Format("2006"), 0
	for _, d := range l.Finished {
		if strings.HasPrefix(d, y) {
			n++
		}
	}
	return n
}
