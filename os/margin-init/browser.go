package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"margin-init/ui"
)

// The Browser tab: a web browser in Margin's look. Pages are fetched by Go (through
// Margin's proxy, Alpine's certificates) and laid out by browser_layout.go in one readable
// column; no JavaScript. A toolbar with back, forward, reload, the address (type a site or
// a search: DuckDuckGo), text size and a star for favourites. Long pages come in parts of a
// few screens (the tablet draws a part at a time). Films and magnet links open in
// Playbox's player; EPUBs go to Books; other files to Download.

type browserUI struct {
	typing  bool
	editing string
	field   string // "url" or "f:<n>" (a form's field)
	cur     *brPage
	back    []*brPage
	fwd     []*brPage
	loading string // the address loading
	err     string
	gen     int
	scale   float64
	saved   *brSaved
	msg     string
}

// brPage is a page in the history: its address, its layout once loaded, where it was read.
type brPage struct {
	url    string
	doc    *brDoc
	part   int // which part of a long page is shown
	scroll int
	method string
	body   string
}

const (
	brPartH   = 6500 // rows of a page drawn at a time
	brToolH   = 104  // the toolbar under the tab bar
	brTopH    = tabsH + brToolH
	brStart   = "about:start"
	brSearch  = "https://html.duckduckgo.com/html/?q="
	brUA      = "Mozilla/5.0 (Linux; Android 4.2.2; Condor TRA-901G) AppleWebKit/537.36 (KHTML, like Gecko) Mobile Safari/537.36 Margin"
	brMaxPage = 8 << 20
)

type brSaved struct {
	Favorites []brFav `json:"favorites"`
	History   []brFav `json:"history"`
	Scale     float64 `json:"scale,omitempty"`
}

type brFav struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

var brDefaultFavs = []brFav{
	{"DuckDuckGo", "https://html.duckduckgo.com/html/"},
	{"Wikipedia", "https://en.m.wikipedia.org/"},
	{"Wikipédia", "https://fr.m.wikipedia.org/"},
	{"ويكيبيديا", "https://ar.m.wikipedia.org/"},
	{"Hacker News", "https://news.ycombinator.com/"},
	{"CNN Lite", "https://lite.cnn.com/"},
	{"NPR Text", "https://text.npr.org/"},
	{"Project Gutenberg", "https://www.gutenberg.org/"},
}

var brSavePath = marginHome + "/browser.json"

func (c *console) brData() *brSaved {
	if c.br.saved == nil {
		s := &brSaved{}
		if b, err := os.ReadFile(brSavePath); err == nil {
			json.Unmarshal(b, s)
		} else {
			s.Favorites = append([]brFav(nil), brDefaultFavs...)
		}
		c.br.saved = s
		if s.Scale >= 0.8 && s.Scale <= 1.5 {
			c.br.scale = s.Scale
		}
	}
	if c.br.scale == 0 {
		c.br.scale = 1
	}
	return c.br.saved
}

func (c *console) brSave() {
	b, _ := json.MarshalIndent(c.br.saved, "", "  ")
	os.WriteFile(brSavePath, b, 0o644)
}

// brClient keeps cookies (logins, consent) for the session. Tests replace it.
var brClient = sync.OnceValue(func() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 60 * time.Second, Transport: &http.Transport{
		Proxy:                 http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
		TLSClientConfig:       &tls.Config{RootCAs: alpineRoots()},
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 40 * time.Second,
	}}
})

// brAddress turns what was typed into an address: a site, or a search.
func brAddress(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return brStart
	case strings.HasPrefix(s, "about:"), strings.HasPrefix(s, "magnet:"):
		return s
	case strings.Contains(s, "://"):
		return s
	case !strings.Contains(s, " ") && strings.Contains(s, ".") && !strings.HasSuffix(s, "."):
		return "https://" + s
	}
	return brSearch + url.QueryEscape(s)
}

// --- loading -----------------------------------------------------------------------------------

// brGo opens an address (a new page in the history). Caller holds drawMu.
func (c *console) brGo(addr string) {
	addr = brAddress(addr)
	if strings.HasPrefix(addr, "magnet:") {
		c.brPlayMagnet(addr)
		return
	}
	if isVideoURL(addr) {
		c.openVideo(videoReq{title: path.Base(addr), direct: addr, item: metaItem{ID: "web:" + addr, Name: path.Base(addr)}})
		return
	}
	if c.br.cur != nil {
		c.br.cur.scroll = c.scrollY()
		c.br.back = append(c.br.back, c.br.cur)
		if len(c.br.back) > 30 {
			c.br.back = c.br.back[1:]
		}
	}
	c.br.fwd = nil
	c.br.cur = &brPage{url: addr}
	c.setScroll(modeBrowser, 0)
	c.brLoad(c.br.cur)
}

// brLoad fetches a page in the background. Caller holds drawMu.
func (c *console) brLoad(pg *brPage) {
	br := &c.br
	br.err = ""
	if pg.url == brStart {
		br.loading = ""
		return
	}
	br.gen++
	gen := br.gen
	br.loading = pg.url
	scale := br.scale
	w := c.s.W
	go func() {
		doc, kind, err := brFetch(pg, w, scale)
		drawMu.Lock()
		defer drawMu.Unlock()
		if gen != br.gen {
			return
		}
		br.loading = ""
		switch {
		case err != nil:
			br.err = err.Error()
		case kind == "video":
			c.brBackOff(pg)
			c.openVideo(videoReq{title: path.Base(pg.url), direct: pg.url, item: metaItem{ID: "web:" + pg.url, Name: path.Base(pg.url)}})
			return
		case kind != "page":
			c.brBackOff(pg)
			c.brDownload(pg.url, kind)
		default:
			pg.doc = doc
			pg.url = doc.url.String()
			c.brRemember(doc)
			c.brLoadImages(pg)
			if doc.refresh != "" && doc.refresh != pg.url && len(doc.items) < 40 {
				ref := doc.refresh
				time.AfterFunc(500*time.Millisecond, func() {
					drawMu.Lock()
					defer drawMu.Unlock()
					if c.br.cur == pg {
						c.br.cur = nil
						c.brGo(ref)
						c.brRedraw()
					}
				})
			}
		}
		c.brRedraw()
	}()
}

// brBackOff takes a page that turned out to be a file off the history.
func (c *console) brBackOff(pg *brPage) {
	if c.br.cur == pg && len(c.br.back) > 0 {
		c.br.cur = c.br.back[len(c.br.back)-1]
		c.br.back = c.br.back[:len(c.br.back)-1]
		c.setScroll(modeBrowser, c.br.cur.scroll)
	}
}

// brFetch gets a page and lays it out. kind is "page", or what else it was ("video",
// "epub", "file").
func brFetch(pg *brPage, width int, scale float64) (*brDoc, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	method := pg.method
	if method == "" {
		method = "GET"
	}
	var body io.Reader
	if pg.body != "" {
		body = strings.NewReader(pg.body)
	}
	req, err := http.NewRequestWithContext(ctx, method, pg.url, body)
	if err != nil {
		return nil, "", fmt.Errorf("That address isn't right.")
	}
	req.Header.Set("User-Agent", brUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en,fr;q=0.9,ar;q=0.8")
	if pg.body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := brClient().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("Can't open the page: %s. Is Wi-Fi on?", shortErr(err))
	}
	defer resp.Body.Close()
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	final := resp.Request.URL
	switch {
	case strings.HasPrefix(ct, "video/") || isVideoURL(final.String()) && !strings.Contains(ct, "html"):
		return nil, "video", nil
	case strings.Contains(ct, "epub") || strings.HasSuffix(strings.ToLower(final.Path), ".epub"):
		return nil, "epub", nil
	case strings.Contains(ct, "html") || strings.Contains(ct, "xml") && !strings.Contains(ct, "zip") || ct == "":
	case strings.HasPrefix(ct, "text/"):
	case strings.HasPrefix(ct, "image/"):
		doc := &brDoc{url: final}
		doc.root, _ = html.Parse(strings.NewReader(`<html><body><img src="` + html.EscapeString(final.String()) + `"></body></html>`))
		layoutHTML(doc, width, scale, brImageSizeOf)
		doc.title = path.Base(final.Path)
		return doc, "page", nil
	default:
		return nil, "file", nil
	}
	r, err := charset.NewReader(io.LimitReader(resp.Body, brMaxPage), resp.Header.Get("Content-Type"))
	if err != nil {
		r = io.LimitReader(resp.Body, brMaxPage)
	}
	doc := &brDoc{url: final}
	if resp.StatusCode >= 400 {
		doc.title = resp.Status
	}
	if strings.HasPrefix(ct, "text/") && !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") {
		b, _ := io.ReadAll(r)
		doc.text = string(b)
		layoutText(doc, width, scale)
		return doc, "page", nil
	}
	doc.root, err = html.ParseWithOptions(r, html.ParseOptionEnableScripting(false))
	if err != nil {
		return nil, "", fmt.Errorf("Can't read the page: %v", err)
	}
	if base := findBase(doc.root); base != "" {
		if u, err := final.Parse(base); err == nil {
			doc.url = u
		}
	}
	layoutHTML(doc, width, scale, brImageSizeOf)
	doc.url = final
	return doc, "page", nil
}

func findBase(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.ElementNode && n.Data == "base" {
		return attr(n, "href")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBase(c); b != "" {
			return b
		}
	}
	return ""
}

// brRemember keeps the page in the history list (the start page shows it).
func (c *console) brRemember(doc *brDoc) {
	s := c.brData()
	u := doc.url.String()
	title := doc.title
	if title == "" {
		title = doc.url.Host
	}
	kept := []brFav{{strings.TrimSpace(title), u}}
	for _, h := range s.History {
		if h.URL != u && len(kept) < 40 {
			kept = append(kept, h)
		}
	}
	s.History = kept
	go func(b brSaved) {
		data, _ := json.MarshalIndent(b, "", "  ")
		os.WriteFile(brSavePath, data, 0o644)
	}(*s)
}

// brRedraw shows the browser again if it's on screen. Caller holds drawMu.
func (c *console) brRedraw() {
	if c.mode == modeBrowser && c.screenOn && !c.locked && !c.br.typing {
		c.showPage()
	}
}

// --- images --------------------------------------------------------------------------------------

type brImg struct {
	img    image.Image
	w, h   int
	scaled *image.RGBA
	failed bool
}

var brImgs = struct {
	sync.Mutex
	m     map[string]*brImg
	order []string
	sem   chan struct{}
}{m: map[string]*brImg{}, sem: make(chan struct{}, 4)}

func brImageSizeOf(src string) (int, int, bool) {
	brImgs.Lock()
	defer brImgs.Unlock()
	if im := brImgs.m[src]; im != nil && im.img != nil {
		return im.w, im.h, true
	}
	return 0, 0, false
}

// brLoadImages fetches the page's pictures; each that arrives lays the page out again.
// Caller holds drawMu.
func (c *console) brLoadImages(pg *brPage) {
	srcs := pg.doc.images
	if len(srcs) > 60 {
		srcs = srcs[:60]
	}
	for _, src := range srcs {
		brImgs.Lock()
		if brImgs.m[src] != nil {
			brImgs.Unlock()
			continue
		}
		brImgs.m[src] = &brImg{}
		brImgs.order = append(brImgs.order, src)
		for len(brImgs.order) > 150 {
			delete(brImgs.m, brImgs.order[0])
			brImgs.order = brImgs.order[1:]
		}
		brImgs.Unlock()
		go func(src string) {
			brImgs.sem <- struct{}{}
			img, err := brFetchImage(src)
			<-brImgs.sem
			brImgs.Lock()
			im := brImgs.m[src]
			if im == nil {
				im = &brImg{}
				brImgs.m[src] = im
			}
			if err != nil {
				im.failed = true
			} else {
				im.img, im.w, im.h = img, img.Bounds().Dx(), img.Bounds().Dy()
			}
			brImgs.Unlock()
			if err != nil {
				return
			}
			drawMu.Lock()
			defer drawMu.Unlock()
			c.brImageArrived(pg)
		}(src)
	}
}

func brFetchImage(src string) (image.Image, error) {
	var b []byte
	if strings.HasPrefix(src, "data:") {
		_, data, ok := strings.Cut(src, ",")
		if !ok || !strings.Contains(src[:min(len(src), 60)], "base64") {
			return nil, fmt.Errorf("data url")
		}
		var err error
		if b, err = base64.StdEncoding.DecodeString(data); err != nil {
			return nil, err
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", src, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", brUA)
		resp, err := brClient().Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("%s", resp.Status)
		}
		if b, err = io.ReadAll(io.LimitReader(resp.Body, 6<<20)); err != nil {
			return nil, err
		}
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	// Big pictures are kept at the page's width at most (memory).
	if bw := img.Bounds().Dx(); bw > 1104 {
		bh := img.Bounds().Dy() * 1104 / bw
		dst := image.NewRGBA(image.Rect(0, 0, 1104, max(bh, 1)))
		xdraw.ApproxBiLinear.Scale(dst, dst.Rect, img, img.Bounds(), xdraw.Src, nil)
		img = dst
	}
	return img, nil
}

// brImageArrived lays the page out again with the picture, a moment later (they come in
// bunches). Caller holds drawMu.
func (c *console) brImageArrived(pg *brPage) {
	if pg.doc == nil || pg.doc.root == nil {
		return
	}
	layoutHTML(pg.doc, c.s.W, c.br.scale, brImageSizeOf)
	c.redrawSoon(func() {
		if c.br.cur == pg {
			c.brRedraw()
		}
	})
}

// drawBrImage draws a picture scaled to r (kept scaled for the next time).
func drawBrImage(img *image.RGBA, r image.Rectangle, src string) {
	brImgs.Lock()
	im := brImgs.m[src]
	brImgs.Unlock()
	if im == nil || im.img == nil {
		ui.RoundRect(img, r, 8, apCard2)
		return
	}
	if im.scaled == nil || im.scaled.Rect.Dx() != r.Dx() || im.scaled.Rect.Dy() != r.Dy() {
		sc := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		ui.Fill(sc, sc.Rect, apBG)
		xdraw.ApproxBiLinear.Scale(sc, sc.Rect, im.img, im.img.Bounds(), xdraw.Over, nil)
		brImgs.Lock()
		im.scaled = sc
		brImgs.Unlock()
	}
	xdraw.Draw(img, r, im.scaled, image.Point{}, xdraw.Src)
}

// --- the page ------------------------------------------------------------------------------------

func (c *console) browserPage() *page {
	f := apple()
	br := &c.br
	c.brData()
	w := c.s.W
	pg := br.cur
	var doc *brDoc
	if pg != nil {
		doc = pg.doc
	}
	h := c.viewH()
	partTop, parts := 0, 1
	if doc != nil {
		parts = max((doc.height+brPartH-1)/brPartH, 1)
		pg.part = min(pg.part, parts-1)
		partTop = pg.part * brPartH
		h = max(h, brTopH+min(doc.height-partTop, brPartH)+260)
	} else if pg == nil || pg.url == brStart {
		h = max(h, 2600)
	}
	img := canvas(w, h)
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	c.booksTabs(p, "tab:browser")
	p.header = brTopH
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }

	// The toolbar: back, forward, reload, the address, text size, favourite.
	y0 := tabsH
	ui.Fill(img, image.Rect(0, y0, w, brTopH), apBG)
	cy := y0 + brToolH/2
	backCol, fwdCol := apBlue, apBlue
	if len(br.back) == 0 {
		backCol = apSecondary
	}
	if len(br.fwd) == 0 {
		fwdCol = apSecondary
	}
	iconBack(img, 36, cy, backCol)
	btn("br:back", image.Rect(0, y0, 80, brTopH))
	line(img, 112, cy-18, 128, cy, 5, fwdCol)
	line(img, 128, cy, 112, cy+18, 5, fwdCol)
	btn("br:fwd", image.Rect(80, y0, 160, brTopH))
	ar := image.Rect(170, y0+14, w-200, brTopH-14)
	ui.RoundRect(img, ar, ar.Dy()/2, apCard2)
	addr, col := "Search or enter website", apSecondary
	switch {
	case br.typing && br.field == "url":
		addr, col = withCursor(br.editing, c.fieldBack), apLabel
	case br.loading != "":
		addr, col = "Loading "+brHost(br.loading)+"…", apSecondary
	case pg != nil && pg.url != brStart:
		addr, col = brHost(pg.url), apLabel
		if doc != nil && doc.title != "" {
			addr = brHost(pg.url) + " · " + doc.title
		}
	}
	for addr != "" && ui.TextWidth(f.callout, addr) > ar.Dx()-60 {
		r := []rune(addr)
		if br.typing {
			addr = string(r[1:])
		} else {
			addr = string(r[:len(r)-2]) + "…"
			if len(r) < 4 {
				break
			}
		}
	}
	apText(img, f.callout, ar.Min.X+30, cy+11, col, addr)
	btn("br:type:url", ar)
	if br.loading != "" {
		apText(img, f.headline, w-185, cy+12, apBlue, "✕")
		btn("br:stop", image.Rect(w-200, y0, w-130, brTopH))
	} else {
		ring(img, w-165, cy, 15, 4, 0.8, apBlue, apBlue)
		btn("br:reload", image.Rect(w-200, y0, w-130, brTopH))
	}
	apTextCenter(img, f.headline, w-95, cy, apBlue, "Aa")
	btn("br:size", image.Rect(w-130, y0, w-62, brTopH))
	isFav := false
	if pg != nil {
		for _, fv := range br.saved.Favorites {
			isFav = isFav || fv.URL == pg.url
		}
	}
	star := "☆"
	if isFav {
		star = "★"
	}
	apTextCenter(img, f.title, w-34, cy, apBlue, star)
	btn("br:fav", image.Rect(w-62, y0, w, brTopH))
	ui.Fill(img, image.Rect(0, brTopH-1, w, brTopH), apSeparator)

	y := brTopH
	switch {
	case br.err != "":
		apText(img, f.title, pageGutter, y+90, apLabel, "Can't Open the Page")
		drawParagraphs(img, f.callout, br.err, pageGutter, y+130, w-2*pageGutter, 44, y+400, apSecondary)
		r := image.Rect(pageGutter, y+300, pageGutter+300, y+396)
		ui.RoundRect(img, r, 48, apBlue)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apOnBlue, "Try Again")
		btn("br:reload", r)
	case pg == nil || pg.url == brStart:
		c.brStartPage(p, y)
	case doc == nil:
		apText(img, f.body, pageGutter, y+90, apSecondary, "Loading…")
	default:
		if pg.part > 0 {
			r := image.Rect(pageGutter, y+20, w-pageGutter, y+110)
			ui.RoundRect(img, r, radiusCard, apCard2)
			apTextCenter(img, f.headline, w/2, y+65, apBlue, fmt.Sprintf("↑  Part %d of %d", pg.part, parts))
			btn("br:part-", r)
			y += 130
		}
		c.brDrawDoc(p, doc, partTop, y)
		y += min(doc.height-partTop, brPartH)
		if pg.part < parts-1 {
			r := image.Rect(pageGutter, y+20, w-pageGutter, y+130)
			ui.RoundRect(img, r, radiusCard, apBlue)
			apTextCenter(img, f.headline, w/2, y+75, apOnBlue, fmt.Sprintf("Continue  ↓  part %d of %d", pg.part+2, parts))
			btn("br:part+", r)
		}
	}
	if br.msg != "" {
		apText(img, f.caption, pageGutter, h-30, apSecondary, clip(f.caption, br.msg, w-2*pageGutter))
	}
	return p
}

func brHost(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	return strings.TrimPrefix(p.Host, "www.")
}

// brStartPage: favourites and the pages seen lately.
func (c *console) brStartPage(p *page, y int) {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	apText(img, f.largeTitle, mx, y+110, apLabel, "Browser")
	apText(img, f.callout, mx, y+160, apSecondary, "Type a website or a search in the bar above.")
	y += 220
	apText(img, f.title, mx, y+40, apLabel, "Favourites")
	y += 70
	favs := c.br.saved.Favorites
	cols := 4
	cw := (w - 2*mx - (cols-1)*24) / cols
	for i, fv := range favs {
		x := mx + (i%cols)*(cw+24)
		ty := y + (i/cols)*190
		r := image.Rect(x, ty, x+cw, ty+170)
		ui.RoundRect(img, r, radiusCard, apCard)
		ir := image.Rect(x+cw/2-40, ty+20, x+cw/2+40, ty+100)
		ui.RoundRect(img, ir, 20, placeholderColour(fv.URL))
		initial := []rune(strings.ToUpper(fv.Title + "?"))[0]
		apTextCenter(img, f.title, (ir.Min.X+ir.Max.X)/2, (ir.Min.Y+ir.Max.Y)/2, rgb(0xffffff), string(initial))
		apTextCenter(img, f.caption, x+cw/2, ty+135, apLabel, clip(f.caption, fv.Title, cw-20))
		p.buttons = append(p.buttons, button{"br:open:fav:" + strconv.Itoa(i), r})
	}
	y += (len(favs)+cols-1)/cols*190 + 40
	if hist := c.br.saved.History; len(hist) > 0 {
		apText(img, f.title, mx, y+40, apLabel, "Recently Visited")
		apTextRight(img, f.body, w-mx, y+40, apBlue, "Clear")
		p.buttons = append(p.buttons, button{"br:clearhist", image.Rect(w-mx-160, y, w, y+70)})
		y += 70
		for i, h := range hist[:min(len(hist), 12)] {
			r := image.Rect(mx, y, w-mx, y+100)
			ui.RoundRect(img, r, radiusCard, apCard)
			apText(img, f.headline, r.Min.X+30, r.Min.Y+44, apLabel, clip(f.headline, h.Title, r.Dx()-60))
			apText(img, f.caption, r.Min.X+30, r.Min.Y+80, apSecondary, clip(f.caption, h.URL, r.Dx()-60))
			p.buttons = append(p.buttons, button{"br:open:hist:" + strconv.Itoa(i), r})
			y += 112
		}
	}
}

// brDrawDoc draws the page's rows [top, top+brPartH) at y on the canvas.
func (c *console) brDrawDoc(p *page, doc *brDoc, top, y int) {
	img := p.img
	off := y - top
	for _, it := range brItemsIn(doc, top, top+brPartH) {
		r := image.Rect(it.x, it.y+off, it.x+it.w, it.y+off+it.h)
		switch it.kind {
		case brText:
			col := brColour(it.role)
			ui.DrawText(img, it.face, it.x, it.y+off+it.asc, col, it.text)
			if it.under && it.role == "link" {
				ui.Fill(img, image.Rect(it.x, it.y+off+it.asc+5, it.x+it.w, it.y+off+it.asc+7), blend(apBG, col, 0.5))
			}
		case brImage:
			drawBrImage(img, r, it.img)
		case brRule:
			ui.Fill(img, r, apSeparator)
		case brBox:
			ui.RoundRect(img, r, 16, apCard)
		case brFieldItem:
			c.brDrawField(img, doc, it, r)
		}
		if it.link >= 0 {
			p.buttons = append(p.buttons, button{"br:a:" + strconv.Itoa(it.link), r.Inset(-6)})
		}
		if it.field >= 0 {
			p.buttons = append(p.buttons, button{"br:f:" + strconv.Itoa(it.field), r})
		}
	}
}

func (c *console) brDrawField(img *image.RGBA, doc *brDoc, it brItem, r image.Rectangle) {
	f := apple()
	if it.field < 0 { // a button that follows a link (a video)
		ui.RoundRect(img, r, r.Dy()/2, apBlue)
		apTextCenter(img, it.face, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apOnBlue, it.text)
		return
	}
	fd := doc.fields[it.field]
	switch fd.kind {
	case "submit", "button", "reset", "image":
		ui.RoundRect(img, r, r.Dy()/2, apBlue)
		apTextCenter(img, it.face, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apOnBlue, it.text)
	case "checkbox", "radio":
		rad := 10
		if fd.kind == "radio" {
			rad = r.Dx() / 2
		}
		ui.RoundRect(img, r, rad, apCard2)
		if fd.checked {
			ui.RoundRect(img, r.Inset(10), rad/2, apBlue)
		}
	case "select":
		ui.RoundRect(img, r, 18, apCard2)
		label := ""
		if fd.sel < len(fd.options) {
			label = fd.options[fd.sel].label
		}
		apText(img, f.callout, r.Min.X+24, (r.Min.Y+r.Max.Y)/2+11, apLabel, clip(f.callout, label, r.Dx()-80))
		apText(img, f.callout, r.Max.X-46, (r.Min.Y+r.Max.Y)/2+11, apBlue, "▾")
	default:
		ui.RoundRect(img, r, 18, apCard2)
		text, col := fd.value, apLabel
		if fd.kind == "password" {
			text = strings.Repeat("•", len([]rune(text)))
		}
		if c.br.typing && c.br.field == "f:"+strconv.Itoa(it.field) {
			text = withCursor(c.br.editing, c.fieldBack)
			if fd.kind == "password" {
				text = strings.Repeat("•", len([]rune(c.br.editing))) + "|"
			}
		} else if text == "" {
			text, col = fd.placeholder, apSecondary
		}
		lines := []string{text}
		if fd.kind == "textarea" {
			lines = wrapText(f.callout, text, r.Dx()-48)
		}
		for i, l := range lines {
			ly := r.Min.Y + 52 + i*44
			if fd.kind != "textarea" {
				ly = (r.Min.Y+r.Max.Y)/2 + 11
				for l != "" && ui.TextWidth(f.callout, l) > r.Dx()-48 {
					l = string([]rune(l)[1:])
				}
			}
			if ly > r.Max.Y-10 {
				break
			}
			apText(img, f.callout, r.Min.X+24, ly, col, l)
		}
	}
}

// --- taps and typing ---------------------------------------------------------------------------

func (c *console) browserTap(id string) bool {
	if !strings.HasPrefix(id, "br:") {
		return false
	}
	br := &c.br
	if br.typing && id != "br:type:"+br.field && id != "br:f:"+strings.TrimPrefix(br.field, "f:") {
		c.brDone(false)
	}
	pg := br.cur
	switch {
	case id == "br:type:url":
		if !br.typing || br.field != "url" {
			cur := ""
			if pg != nil && pg.url != brStart {
				cur = pg.url
			}
			br.typing, br.field, br.editing, c.fieldBack = true, "url", cur, 0
		}
		c.skb.visible = true
	case id == "br:back":
		if len(br.back) > 0 {
			if pg != nil {
				pg.scroll = c.scrollY()
				br.fwd = append(br.fwd, pg)
			}
			br.cur = br.back[len(br.back)-1]
			br.back = br.back[:len(br.back)-1]
			c.brShow(br.cur)
		}
	case id == "br:fwd":
		if len(br.fwd) > 0 {
			if pg != nil {
				pg.scroll = c.scrollY()
				br.back = append(br.back, pg)
			}
			br.cur = br.fwd[len(br.fwd)-1]
			br.fwd = br.fwd[:len(br.fwd)-1]
			c.brShow(br.cur)
		}
	case id == "br:reload":
		if pg != nil {
			pg.doc = nil
			c.brLoad(pg)
		}
	case id == "br:stop":
		br.gen++
		br.loading = ""
	case id == "br:size":
		sizes := []float64{0.9, 1, 1.15, 1.3}
		next := 1
		for i, s := range sizes {
			if s == br.scale {
				next = (i + 1) % len(sizes)
			}
		}
		br.scale = sizes[next]
		br.saved.Scale = br.scale
		c.brSave()
		if pg != nil && pg.doc != nil {
			if pg.doc.root != nil {
				layoutHTML(pg.doc, c.s.W, br.scale, brImageSizeOf)
			} else if pg.doc.text != "" {
				layoutText(pg.doc, c.s.W, br.scale)
			}
		}
		c.toastMsg(fmt.Sprintf("Text %d%%", int(br.scale*100)))
	case id == "br:fav":
		if pg != nil && pg.url != brStart {
			s := br.saved
			for i, fv := range s.Favorites {
				if fv.URL == pg.url {
					s.Favorites = append(s.Favorites[:i], s.Favorites[i+1:]...)
					c.brSave()
					c.toastMsg("Removed from Favourites")
					c.showPage()
					return true
				}
			}
			title := brHost(pg.url)
			if pg.doc != nil && pg.doc.title != "" {
				title = pg.doc.title
			}
			s.Favorites = append(s.Favorites, brFav{title, pg.url})
			c.brSave()
			c.toastMsg("Added to Favourites")
		}
	case id == "br:clearhist":
		br.saved.History = nil
		c.brSave()
	case strings.HasPrefix(id, "br:open:"):
		what, n, _ := strings.Cut(strings.TrimPrefix(id, "br:open:"), ":")
		i, _ := strconv.Atoi(n)
		list := br.saved.Favorites
		if what == "hist" {
			list = br.saved.History
		}
		if i >= 0 && i < len(list) {
			c.brGo(list[i].URL)
		}
	case id == "br:part+", id == "br:part-":
		if pg != nil {
			if id == "br:part+" {
				pg.part++
			} else {
				pg.part = max(pg.part-1, 0)
			}
			c.setScroll(modeBrowser, 0)
		}
	case strings.HasPrefix(id, "br:a:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "br:a:"))
		if pg != nil && pg.doc != nil && i >= 0 && i < len(pg.doc.links) {
			ln := pg.doc.links[i]
			switch ln.kind {
			case "magnet":
				c.brPlayMagnet(ln.href)
				return true
			case "video":
				c.openVideo(videoReq{title: path.Base(ln.href), direct: ln.href, item: metaItem{ID: "web:" + ln.href, Name: path.Base(ln.href)}})
				return true
			}
			if strings.HasPrefix(ln.href, "mailto:") || strings.HasPrefix(ln.href, "tel:") {
				c.toastMsg("Can't open " + strings.SplitN(ln.href, ":", 2)[0] + " links here")
				break
			}
			c.brGo(ln.href)
		}
	case strings.HasPrefix(id, "br:f:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "br:f:"))
		c.brFieldTap(i)
		return true
	}
	c.showPage()
	return true
}

// brShow puts a page from the history back on screen. Caller holds drawMu.
func (c *console) brShow(pg *brPage) {
	c.br.err, c.br.loading = "", ""
	c.br.gen++
	c.setScroll(modeBrowser, pg.scroll)
	if pg.doc == nil {
		c.brLoad(pg)
	}
}

func (c *console) brFieldTap(i int) {
	br := &c.br
	pg := br.cur
	if pg == nil || pg.doc == nil || i < 0 || i >= len(pg.doc.fields) {
		return
	}
	fd := pg.doc.fields[i]
	switch fd.kind {
	case "submit", "image", "button":
		if fd.kind != "button" || fd.form >= 0 {
			c.brSubmit(fd.form, fd)
			return
		}
	case "reset":
	case "checkbox":
		fd.checked = !fd.checked
	case "radio":
		for _, o := range pg.doc.fields {
			if o.kind == "radio" && o.name == fd.name && o.form == fd.form {
				o.checked = false
			}
		}
		fd.checked = true
	case "select":
		if len(fd.options) > 0 {
			fd.sel = (fd.sel + 1) % len(fd.options)
		}
	default:
		key := "f:" + strconv.Itoa(i)
		if !br.typing || br.field != key {
			br.typing, br.field, br.editing, c.fieldBack = true, key, fd.value, 0
		}
		c.skb.visible = true
	}
	c.showPage()
}

// brSubmit sends a form (GET or POST), with the button pressed. Caller holds drawMu.
func (c *console) brSubmit(form int, pressed *brField) {
	pg := c.br.cur
	if pg == nil || pg.doc == nil {
		return
	}
	doc := pg.doc
	v := url.Values{}
	for _, fd := range doc.fields {
		if fd.form != form || fd.name == "" {
			continue
		}
		switch fd.kind {
		case "submit", "image", "button", "reset":
			if fd == pressed {
				v.Add(fd.name, fd.value)
			}
		case "checkbox", "radio":
			if fd.checked {
				val := fd.value
				if val == "" {
					val = "on"
				}
				v.Add(fd.name, val)
			}
		case "select":
			if fd.sel < len(fd.options) {
				v.Add(fd.name, fd.options[fd.sel].value)
			}
		default:
			v.Add(fd.name, fd.value)
		}
	}
	action, method := doc.url.String(), "GET"
	if form >= 0 && form < len(doc.forms) {
		if doc.forms[form].action != "" {
			action = doc.forms[form].action
		}
		if doc.forms[form].method == "POST" {
			method = "POST"
		}
	}
	if method == "GET" {
		u, err := url.Parse(action)
		if err != nil {
			return
		}
		u.RawQuery = v.Encode()
		c.brGo(u.String())
		return
	}
	if pg != nil {
		pg.scroll = c.scrollY()
		c.br.back = append(c.br.back, pg)
	}
	c.br.fwd = nil
	c.br.cur = &brPage{url: action, method: "POST", body: v.Encode()}
	c.setScroll(modeBrowser, 0)
	c.brLoad(c.br.cur)
	c.showPage()
}

// brKey takes the keyboard's output while typing in the browser. Caller holds drawMu.
func (c *console) brKey(b []byte) {
	br := &c.br
	if len(b) == 0 {
		return
	}
	var done bool
	if br.editing, done = editLine(br.editing, &c.fieldBack, b, 2000); done {
		c.brDone(true)
		c.showPage()
		return
	}
	c.fieldTyped()
}

// brDone finishes typing: the address opens, a form's text is kept (and Enter sends it).
func (c *console) brDone(enter bool) {
	br := &c.br
	text := br.editing
	br.typing, br.editing = false, ""
	c.skb.visible = false
	if br.field == "url" {
		if enter {
			c.brGo(text)
		}
		return
	}
	i, _ := strconv.Atoi(strings.TrimPrefix(br.field, "f:"))
	if pg := br.cur; pg != nil && pg.doc != nil && i >= 0 && i < len(pg.doc.fields) {
		fd := pg.doc.fields[i]
		fd.value = text
		if enter && fd.kind != "textarea" {
			c.brSubmit(fd.form, nil)
		}
	}
}

// --- films, magnets, files ------------------------------------------------------------------------

// brPlayMagnet plays a magnet link with Playbox's torrent engine. Caller holds drawMu.
func (c *console) brPlayMagnet(magnet string) {
	j, err := engine.startMagnet(magnet)
	if err != nil {
		c.toastMsg(shortErr(err))
		return
	}
	name := "Torrent"
	if u, err := url.Parse(magnet); err == nil && u.Query().Get("dn") != "" {
		name = u.Query().Get("dn")
	}
	c.openVideo(videoReq{title: name, job: j, item: metaItem{ID: "magnet:" + j.hash, Name: name}})
}

// brDownload saves a file the browser can't show: an EPUB to Books, the rest to Download.
// Caller holds drawMu.
func (c *console) brDownload(u, kind string) {
	name := path.Base(strings.Split(u, "?")[0])
	if name == "" || name == "/" || name == "." {
		name = "download"
	}
	dir := "/data/media/0/Download"
	if kind == "epub" {
		dir = storeDir
		if !strings.HasSuffix(strings.ToLower(name), ".epub") {
			name += ".epub"
		}
	}
	c.toastMsg("Downloading " + name)
	go func() {
		dst := filepath.Join(dir, safeFileName(name))
		var err error
		if kind == "epub" {
			err = fetchEPUB(u, dst, func(int64) {})
		} else {
			err = brSaveFile(u, dst)
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		if err != nil {
			log.Printf("browser: download %s: %v", u, err)
			c.br.msg = "Download failed: " + shortErr(err)
		} else if kind == "epub" {
			c.br.msg = "Saved to Books: " + filepath.Base(dst)
		} else {
			c.br.msg = "Saved to Download: " + filepath.Base(dst)
		}
		c.brRedraw()
	}()
}

func brSaveFile(u, dst string) error {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", brUA)
	resp, err := streamClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s", resp.Status)
	}
	os.MkdirAll(filepath.Dir(dst), 0o755)
	f, err := os.Create(dst + ".part")
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 4<<30)); err != nil {
		f.Close()
		os.Remove(dst + ".part")
		return err
	}
	f.Close()
	return os.Rename(dst+".part", dst)
}
