package main

import (
	"image"
	"image/color"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/go-fonts/dejavu/dejavusansmono"
	"github.com/go-fonts/dejavu/dejavusansmonobold"
	"golang.org/x/image/font"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"margin-init/fonts"
)

// The browser's page layout: HTML (parsed by x/net/html) laid out as a reader would, in
// one column the width of the screen. Headings, paragraphs, lists, quotes, code, tables (row
// by row), images, links and forms; no JavaScript and no style sheets (there's no engine for
// either on this tablet), so the page's own colours and columns give way to Margin's look.
// Arabic paragraphs run right to left.

type brItemKind int

const (
	brText brItemKind = iota
	brImage
	brRule  // a line (hr, a quote's bar)
	brFieldItem // a form's field or button
	brBox   // a code block's background
)

type brItem struct {
	kind       brItemKind
	x, y, w, h int // y is the top of the box, in page coordinates
	asc        int // text: baseline - y
	text       string
	face       font.Face
	role       string // "text", "link", "muted", "code", "head"
	link       int    // index in doc.links, -1 for none
	img        string
	field      int // index in doc.fields, -1
	under      bool
}

type brField struct {
	form        int
	kind        string // "text", "password", "search", "email", "url", "number", "hidden", "checkbox", "radio", "submit", "button", "image", "select", "textarea", "reset"
	name, value string
	checked     bool
	options     []brOption
	sel         int
	placeholder string
}

type brOption struct{ label, value string }

type brForm struct {
	action, method string
}

// brLink is where a tap goes: a page, a film (Playbox plays it), a magnet (the torrent
// plays), or a book (to the library).
type brLink struct {
	href string
	kind string // "page", "video", "magnet"
}

type brDoc struct {
	url     *url.URL
	title   string
	items   []brItem
	height  int
	links   []brLink
	forms   []*brForm
	fields  []*brField
	images  []string
	refresh string // <meta http-equiv=refresh>
	root    *html.Node
	text    string // plain text (text/plain pages)
	rtl     bool
}

// brImageSize gives an image's natural size if it's known (loaded), else ok=false.
type brImageSize func(src string) (w, h int, ok bool)

// brFonts are the faces at the browser's text size.
type brFonts struct {
	body, bold, small, smallBold, mono, monoBold font.Face
	h                                            [7]font.Face
	scale                                        float64
}

var (
	brFontsMu    sync.Mutex
	brFontsCache = map[float64]*brFonts{}
)

func browserFonts(scale float64) *brFonts {
	brFontsMu.Lock()
	defer brFontsMu.Unlock()
	if f := brFontsCache[scale]; f != nil {
		return f
	}
	reg := func(s float64) font.Face { return textFace("inter", fonts.InterRegular, false, s*scale) }
	semi := func(s float64) font.Face { return textFace("inter-semibold", fonts.InterSemiBold, true, s*scale) }
	bold := func(s float64) font.Face { return textFace("inter-bold", fonts.InterBold, true, s*scale) }
	f := &brFonts{scale: scale, body: reg(33), bold: semi(33), small: reg(27), smallBold: semi(27),
		mono:     textFace("dejavusansmono", dejavusansmono.TTF, false, 27*scale),
		monoBold: textFace("dejavusansmonobold", dejavusansmonobold.TTF, true, 27*scale)}
	f.h = [7]font.Face{f.body, bold(58), bold(48), semi(41), semi(37), semi(34), semi(32)}
	brFontsCache[scale] = f
	return f
}

// brStyle is the inline style in force.
type brStyle struct {
	bold, mono, small, pre, under bool
	head                          int // 1..6 inside a heading
	link                          int
	role                          string
}

type brLayout struct {
	doc      *brDoc
	f        *brFonts
	size     brImageSize
	left     int // the block's left edge (indents)
	right    int // the block's right edge
	y        int // the top of the line being filled
	x        int // where the next item goes on the line (from left)
	line     []brItem
	space    bool // a space is owed before the next word
	st       []brStyle
	lists    []brList
	rtl      bool
	form     int
	marginY  int // vertical space owed before the next block's first line
	nodes    int
	maxNodes int
}

type brList struct {
	ordered bool
	n       int
}

const (
	brMargin   = 48
	brMaxNodes = 60000
)

func (l *brLayout) style() brStyle { return l.st[len(l.st)-1] }

func (l *brLayout) face() font.Face {
	s := l.style()
	switch {
	case s.mono && s.bold:
		return l.f.monoBold
	case s.mono:
		return l.f.mono
	case s.head > 0:
		return l.f.h[s.head]
	case s.small && s.bold:
		return l.f.smallBold
	case s.small:
		return l.f.small
	case s.bold:
		return l.f.bold
	}
	return l.f.body
}

// layoutHTML lays out a parsed page width pixels wide.
func layoutHTML(doc *brDoc, width int, scale float64, size brImageSize) {
	doc.items, doc.links, doc.forms, doc.fields, doc.images = nil, nil, nil, nil, nil
	l := &brLayout{doc: doc, f: browserFonts(scale), size: size, left: brMargin, right: width - brMargin, y: 30,
		st: []brStyle{{link: -1, role: "text"}}, form: -1, maxNodes: brMaxNodes}
	body := findBody(doc.root)
	if doc.title == "" {
		doc.title = strings.TrimSpace(textOf(findFirst(doc.root, atom.Title)))
	}
	words := strings.Fields(textOf(body))
	l.rtl = isRTL(strings.Join(words[:min(len(words), 40)], " "))
	if d := attr(findFirst(doc.root, atom.Html), "dir"); d != "" {
		l.rtl = d == "rtl"
	}
	doc.rtl = l.rtl
	l.walk(body)
	l.flush()
	doc.height = l.y + 60
}

// layoutText lays out a plain text page (text/plain) as preformatted lines.
func layoutText(doc *brDoc, width int, scale float64) {
	doc.items = nil
	l := &brLayout{doc: doc, f: browserFonts(scale), left: brMargin, right: width - brMargin, y: 30,
		st: []brStyle{{link: -1, role: "text", mono: true, pre: true}}, form: -1}
	l.addPre(doc.text)
	l.flush()
	doc.height = l.y + 60
}

func findBody(n *html.Node) *html.Node {
	if b := findFirst(n, atom.Body); b != nil {
		return b
	}
	return n
}

func findFirst(n *html.Node, a atom.Atom) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findFirst(c, a); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// hidden: an element that isn't shown (hidden, display:none, screen-reader only).
func hidden(n *html.Node) bool {
	if hasAttr(n, "hidden") || strings.EqualFold(attr(n, "type"), "hidden") && n.DataAtom != atom.Input {
		return true
	}
	st := strings.ToLower(strings.ReplaceAll(attr(n, "style"), " ", ""))
	if strings.Contains(st, "display:none") || strings.Contains(st, "visibility:hidden") {
		return true
	}
	for _, c := range strings.Fields(strings.ToLower(attr(n, "class"))) {
		switch c {
		case "hidden", "sr-only", "visually-hidden", "visuallyhidden", "screen-reader-text", "screenreader", "d-none", "hide", "noprint", "mw-hidden-catlinks":
			return true
		}
	}
	return false
}

var blockAtoms = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Section: true, atom.Article: true, atom.Header: true, atom.Footer: true,
	atom.Nav: true, atom.Main: true, atom.Aside: true, atom.Figure: true, atom.Figcaption: true, atom.Address: true,
	atom.Ul: true, atom.Ol: true, atom.Li: true, atom.Dl: true, atom.Dt: true, atom.Dd: true, atom.Blockquote: true,
	atom.Pre: true, atom.Table: true, atom.Tr: true, atom.Thead: true, atom.Tbody: true, atom.Tfoot: true,
	atom.Caption: true, atom.Form: true, atom.Fieldset: true, atom.Legend: true, atom.Hr: true, atom.Center: true,
	atom.Details: true, atom.Summary: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true,
	atom.H6: true, atom.Hgroup: true, atom.Body: true, atom.Html: true, atom.Menu: true, atom.Noscript: true,
}

var skipAtoms = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Head: true, atom.Template: true, atom.Svg: true, atom.Math: true,
	atom.Canvas: true, atom.Object: true, atom.Embed: true, atom.Iframe: true, atom.Frameset: true, atom.Audio: true,
	atom.Map: true, atom.Datalist: true, atom.Dialog: true,
}

func (l *brLayout) push(s brStyle) { l.st = append(l.st, s) }
func (l *brLayout) pop()           { l.st = l.st[:len(l.st)-1] }

// block ends the line and owes some space before what follows.
func (l *brLayout) block(gap int) {
	l.flush()
	l.marginY = max(l.marginY, gap)
	l.space = false
}

func (l *brLayout) walk(n *html.Node) {
	if n == nil || l.nodes > l.maxNodes {
		return
	}
	l.nodes++
	switch n.Type {
	case html.TextNode:
		if l.style().pre {
			l.addPre(n.Data)
		} else {
			l.addText(n.Data)
		}
		return
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			l.walk(c)
		}
		return
	case html.ElementNode:
	default:
		return
	}
	if skipAtoms[n.DataAtom] || hidden(n) {
		return
	}
	s := l.style()
	switch n.DataAtom {
	case atom.Meta:
		if strings.EqualFold(attr(n, "http-equiv"), "refresh") {
			if _, u, ok := strings.Cut(strings.ToLower(attr(n, "content")), "url="); ok {
				orig := attr(n, "content")
				if i := strings.Index(strings.ToLower(orig), "url="); i >= 0 {
					u = strings.Trim(orig[i+4:], `'" `)
				}
				l.doc.refresh = l.abs(u)
			}
		}
		return
	case atom.Title, atom.Link, atom.Base:
		return
	case atom.Br:
		if len(l.line) == 0 {
			l.y += l.lineHeight(l.face())
		}
		l.flush()
		l.space = false
		return
	case atom.Hr:
		l.block(20)
		l.startLine()
		l.doc.items = append(l.doc.items, brItem{kind: brRule, x: l.left, y: l.y, w: l.right - l.left, h: 2, role: "muted", link: -1, field: -1})
		l.y += 2
		l.block(20)
		return
	case atom.Img:
		l.addImage(n)
		return
	case atom.Video:
		src := attr(n, "src")
		for c := n.FirstChild; c != nil && src == ""; c = c.NextSibling {
			if c.Type == html.ElementNode && c.DataAtom == atom.Source {
				src = attr(c, "src")
			}
		}
		if src != "" {
			l.block(16)
			l.addButton("▶  Play the video", l.addLink(l.abs(src), "video"))
			l.block(16)
		}
		return
	case atom.Input, atom.Select, atom.Textarea, atom.Button:
		l.addFieldNode(n)
		return
	}
	isBlock := blockAtoms[n.DataAtom]
	gap := 0
	switch n.DataAtom {
	case atom.P, atom.Blockquote, atom.Pre, atom.Table, atom.Figure, atom.Ul, atom.Ol, atom.Dl, atom.Form, atom.Fieldset:
		gap = 22
	case atom.H1, atom.H2:
		gap = 40
	case atom.H3, atom.H4, atom.H5, atom.H6:
		gap = 30
	case atom.Li, atom.Tr, atom.Dt, atom.Dd, atom.Div, atom.Section, atom.Article, atom.Header, atom.Footer, atom.Nav, atom.Caption, atom.Summary, atom.Figcaption:
		gap = 6
	}
	if isBlock {
		l.block(gap)
	}
	ns := s
	left, right := l.left, l.right
	var barTop int
	switch n.DataAtom {
	case atom.B, atom.Strong, atom.Th, atom.Dt, atom.Summary, atom.Legend:
		ns.bold = true
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		ns.head = int(n.Data[1] - '0')
		ns.role = "head"
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt, atom.Var:
		ns.mono = true
	case atom.Pre:
		ns.mono, ns.pre = true, true
	case atom.Small, atom.Sub, atom.Sup, atom.Figcaption, atom.Caption, atom.Cite:
		ns.small = true
		if n.DataAtom == atom.Figcaption || n.DataAtom == atom.Caption {
			ns.role = "muted"
		}
	case atom.U, atom.Ins:
		ns.under = true
	case atom.A:
		if href := strings.TrimSpace(attr(n, "href")); href != "" && !strings.HasPrefix(strings.ToLower(href), "javascript:") {
			abs := l.abs(href)
			kind := "page"
			switch {
			case strings.HasPrefix(strings.ToLower(abs), "magnet:"):
				kind = "magnet"
			case isVideoURL(abs):
				kind = "video"
			}
			ns.link = l.addLink(abs, kind)
			ns.role = "link"
		}
	case atom.Ul, atom.Ol, atom.Menu:
		l.lists = append(l.lists, brList{ordered: n.DataAtom == atom.Ol})
		if v, err := strconv.Atoi(attr(n, "start")); err == nil && n.DataAtom == atom.Ol {
			l.lists[len(l.lists)-1].n = v - 1
		}
		l.indent(56)
	case atom.Blockquote, atom.Dd:
		l.indent(44)
		barTop = l.y + l.marginY
	case atom.Li:
		l.startLine()
		marker := "•"
		if k := len(l.lists); k > 0 {
			if l.lists[k-1].ordered {
				l.lists[k-1].n++
				marker = strconv.Itoa(l.lists[k-1].n) + "."
			} else if k > 1 {
				marker = "◦"
			}
		}
		face := l.face()
		mw := font.MeasureString(face, marker).Ceil()
		mx := l.left - mw - 16
		if l.rtl {
			mx = l.right + 16
		}
		l.line = append(l.line, brItem{kind: brText, x: mx - l.left, w: mw, text: marker, face: face, role: "muted", link: -1, field: -1})
	case atom.Form:
		l.doc.forms = append(l.doc.forms, &brForm{action: l.abs(attr(n, "action")), method: strings.ToUpper(attr(n, "method"))})
		l.form = len(l.doc.forms) - 1
	case atom.Td:
		if len(l.line) > 0 {
			l.x += 28 // cells side by side, a little apart
		}
	}
	l.push(ns)
	preStart := len(l.doc.items)
	preY := l.y + l.marginY
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		l.walk(c)
	}
	l.pop()
	switch n.DataAtom {
	case atom.Ul, atom.Ol, atom.Menu:
		l.lists = l.lists[:len(l.lists)-1]
	case atom.Blockquote:
		l.flush()
		bx := l.left - 26
		if l.rtl {
			bx = l.right + 20
		}
		if l.y > barTop {
			l.doc.items = append(l.doc.items, brItem{kind: brRule, x: bx, y: barTop, w: 6, h: l.y - barTop, role: "muted", link: -1, field: -1})
		}
	case atom.Pre:
		l.flush()
		if l.y > preY {
			bg := brItem{kind: brBox, x: brMargin - 18, y: preY - 12, w: l.right - brMargin + 36, h: l.y - preY + 24, role: "code", link: -1, field: -1}
			l.doc.items = append(l.doc.items[:preStart], append([]brItem{bg}, l.doc.items[preStart:]...)...)
			l.y += 12
		}
	case atom.Form:
		l.form = -1
	}
	l.left, l.right = left, right
	if isBlock {
		l.block(gap)
	}
}

func (l *brLayout) indent(px int) {
	if l.rtl {
		l.right -= px
	} else {
		l.left += px
	}
}

func (l *brLayout) abs(href string) string {
	href = strings.TrimSpace(href)
	if l.doc.url == nil {
		return href
	}
	u, err := l.doc.url.Parse(href)
	if err != nil {
		return href
	}
	u.Fragment = ""
	return u.String()
}

func (l *brLayout) addLink(href, kind string) int {
	l.doc.links = append(l.doc.links, brLink{href: href, kind: kind})
	return len(l.doc.links) - 1
}

func isVideoURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	ext := strings.ToLower(p.Path)
	for _, e := range []string{".mp4", ".m4v", ".mkv", ".webm", ".mov", ".avi", ".m3u8", ".ts"} {
		if strings.HasSuffix(ext, e) {
			return true
		}
	}
	return false
}

func (l *brLayout) lineHeight(f font.Face) int {
	m := f.Metrics()
	return int(float64((m.Ascent + m.Descent).Ceil()) * 1.42)
}

// startLine pays the space owed before a block's first line.
func (l *brLayout) startLine() {
	if len(l.line) == 0 && l.marginY > 0 {
		l.y += l.marginY
		l.marginY = 0
	}
}

// place puts an item on the line, wrapping first if it doesn't fit.
func (l *brLayout) place(it brItem, spaceW int) {
	l.startLine()
	gap := 0
	if l.space && len(l.line) > 0 {
		gap = spaceW
	}
	if len(l.line) > 0 && l.left+l.x+gap+it.w > l.right {
		l.flush()
		l.startLine()
		gap = 0
	}
	it.x = l.x + gap
	l.x = it.x + it.w
	l.line = append(l.line, it)
	l.space = false
}

func (l *brLayout) addText(t string) {
	if t == "" {
		return
	}
	s := l.style()
	face := l.face()
	sp := font.MeasureString(face, " ").Ceil()
	if unicode.IsSpace([]rune(t)[0]) {
		l.space = true
	}
	words := strings.Fields(t)
	for i, w := range words {
		if i > 0 {
			l.space = true
		}
		d := displayWord(w)
		ww := font.MeasureString(face, d).Ceil()
		for ww > l.right-l.left && len([]rune(d)) > 1 { // a word wider than the page: broken
			r := []rune(d)
			cut := len(r) - 1
			for cut > 1 && font.MeasureString(face, string(r[:cut])).Ceil() > l.right-l.left {
				cut--
			}
			l.place(brItem{kind: brText, w: font.MeasureString(face, string(r[:cut])).Ceil(), text: string(r[:cut]), face: face, role: s.role, link: s.link, under: s.under || s.link >= 0, field: -1}, sp)
			d = string(r[cut:])
			ww = font.MeasureString(face, d).Ceil()
		}
		l.place(brItem{kind: brText, w: ww, text: d, face: face, role: s.role, link: s.link, under: s.under || s.link >= 0, field: -1}, sp)
	}
	if r := []rune(t); unicode.IsSpace(r[len(r)-1]) {
		l.space = true
	}
}

// addPre lays out text as it is: its lines kept, long ones broken at the page's edge.
func (l *brLayout) addPre(t string) {
	s := l.style()
	face := l.face()
	t = strings.ReplaceAll(strings.ReplaceAll(t, "\r\n", "\n"), "\t", "    ")
	lines := strings.Split(t, "\n")
	for i, line := range lines {
		if i > 0 {
			if len(l.line) == 0 {
				l.y += l.lineHeight(face)
			}
			l.flush()
		}
		r := []rune(line)
		for len(r) > 0 {
			cut := len(r)
			for cut > 1 && l.left+l.x+font.MeasureString(face, string(r[:cut])).Ceil() > l.right {
				cut--
			}
			part := string(r[:cut])
			w := font.MeasureString(face, part).Ceil()
			l.startLine()
			l.line = append(l.line, brItem{kind: brText, x: l.x, w: w, text: part, face: face, role: "code", link: s.link, field: -1})
			l.x += w
			r = r[cut:]
			if len(r) > 0 {
				l.flush()
			}
		}
	}
}

// addImage places an image at its size (shrunk to the page), once its size is known.
func (l *brLayout) addImage(n *html.Node) {
	src := attr(n, "src")
	if src == "" || strings.HasPrefix(src, "data:image/svg") {
		src = attr(n, "data-src")
	}
	if src == "" {
		if ss := attr(n, "srcset"); ss != "" {
			src = strings.Fields(strings.Split(ss, ",")[0] + " x")[0]
		}
	}
	if src == "" {
		return
	}
	abs := src
	if !strings.HasPrefix(src, "data:") {
		abs = l.abs(src)
	}
	if strings.HasSuffix(strings.ToLower(strings.Split(abs, "?")[0]), ".svg") {
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			l.addText(" " + alt + " ")
		}
		return
	}
	aw, _ := strconv.Atoi(strings.TrimSuffix(attr(n, "width"), "px"))
	ah, _ := strconv.Atoi(strings.TrimSuffix(attr(n, "height"), "px"))
	if (aw > 0 && aw < 12) || (ah > 0 && ah < 12) { // tracking pixels, spacers
		return
	}
	l.doc.images = append(l.doc.images, abs)
	w, h := aw, ah // the size the page asks for, else the picture's own
	if aw <= 0 || ah <= 0 {
		nw, nh, ok := 0, 0, false
		if l.size != nil {
			nw, nh, ok = l.size(abs)
		}
		if !ok || nw <= 0 || nh <= 0 {
			return // laid out again when it arrives
		}
		switch {
		case aw > 0:
			w, h = aw, nh*aw/nw
		case ah > 0:
			w, h = nw*ah/nh, ah
		default:
			w, h = nw, nh
		}
	}
	if w < 12 || h < 12 {
		return
	}
	sc := 1.5 * l.f.scale // pages are made for screens with smaller pixels than these
	dw, dh := int(float64(w)*sc), int(float64(h)*sc)
	if maxW := l.right - l.left; dw > maxW {
		dh, dw = dh*maxW/dw, maxW
	}
	if dh > 1400 {
		dw, dh = dw*1400/dh, 1400
	}
	if dw < 8 || dh < 8 {
		return
	}
	big := dw > (l.right-l.left)/2
	if big {
		l.block(12)
	}
	l.place(brItem{kind: brImage, w: dw, h: dh, img: abs, link: l.style().link, field: -1}, 8)
	if big {
		l.block(12)
	}
}

// addButton places a button that follows a link.
func (l *brLayout) addButton(label string, link int) {
	face := l.f.bold
	w := font.MeasureString(face, label).Ceil() + 60
	l.place(brItem{kind: brFieldItem, w: w, h: 80, text: label, face: face, role: "button", link: link, field: -1}, 16)
}

// addFieldNode places a form's input, select, text area or button.
func (l *brLayout) addFieldNode(n *html.Node) {
	fd := &brField{form: l.form, name: attr(n, "name"), placeholder: attr(n, "placeholder")}
	switch n.DataAtom {
	case atom.Input:
		fd.kind = strings.ToLower(attr(n, "type"))
		if fd.kind == "" {
			fd.kind = "text"
		}
		fd.value = attr(n, "value")
		fd.checked = hasAttr(n, "checked")
	case atom.Textarea:
		fd.kind, fd.value = "textarea", textOf(n)
	case atom.Select:
		fd.kind = "select"
		var walk func(*html.Node)
		walk = func(c *html.Node) {
			for ; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.DataAtom == atom.Option {
					v := attr(c, "value")
					label := strings.TrimSpace(textOf(c))
					if !hasAttr(c, "value") {
						v = label
					}
					if hasAttr(c, "selected") {
						fd.sel = len(fd.options)
					}
					fd.options = append(fd.options, brOption{label, v})
				} else if c.Type == html.ElementNode {
					walk(c.FirstChild)
				}
			}
		}
		walk(n.FirstChild)
	case atom.Button:
		fd.kind = strings.ToLower(attr(n, "type"))
		if fd.kind == "" {
			fd.kind = "submit"
		}
		fd.value = attr(n, "value")
		fd.placeholder = strings.Join(strings.Fields(textOf(n)), " ")
	}
	l.doc.fields = append(l.doc.fields, fd)
	idx := len(l.doc.fields) - 1
	if fd.kind == "hidden" {
		return
	}
	face := l.f.body
	var w, h int
	label := ""
	full := l.right - l.left
	switch fd.kind {
	case "submit", "button", "reset", "image":
		label = fd.value
		if fd.kind == "image" || label == "" {
			label = fd.placeholder
		}
		if label == "" {
			label = map[string]string{"reset": "Reset"}[fd.kind]
			if label == "" {
				label = "Submit"
			}
		}
		face = l.f.bold
		w, h = min(font.MeasureString(face, label).Ceil()+60, full), 80
	case "checkbox", "radio":
		w, h = 52, 52
	case "select":
		longest := 0
		for _, o := range fd.options {
			longest = max(longest, font.MeasureString(face, o.label).Ceil())
		}
		w, h = min(longest+100, full), 80
	case "textarea":
		rows, _ := strconv.Atoi(attr(n, "rows"))
		w, h = full, max(min(rows, 8), 3)*44+30
	case "file", "color", "range", "date", "time", "datetime-local", "month", "week":
		return // not here
	default: // text, search, email, password, url, number, tel
		size, _ := strconv.Atoi(attr(n, "size"))
		w, h = full, 80
		if size > 0 && size < 40 {
			w = min(max(size*20+60, 260), full)
		}
	}
	if fd.kind == "textarea" {
		l.block(10)
	}
	l.place(brItem{kind: brFieldItem, w: w, h: h, text: label, face: face, role: fd.kind, link: -1, field: idx}, 16)
	if fd.kind == "textarea" {
		l.block(10)
	}
}

// flush ends the line: its items get their place on the page, baselines lined up.
func (l *brLayout) flush() {
	if len(l.line) == 0 {
		l.x = 0
		return
	}
	asc, desc := 0, 0
	for _, it := range l.line {
		switch it.kind {
		case brText:
			m := it.face.Metrics()
			lh := l.lineHeight(it.face)
			a := m.Ascent.Ceil() + (lh-(m.Ascent+m.Descent).Ceil())/2
			asc, desc = max(asc, a), max(desc, lh-a)
		default:
			asc = max(asc, it.h+6)
			desc = max(desc, 10)
		}
	}
	width := l.right - l.left
	for _, it := range l.line {
		it.x += l.left
		if l.rtl && it.kind != brRule {
			it.x = l.left + width - (it.x - l.left) - it.w // the line runs right to left
		}
		switch it.kind {
		case brText:
			it.asc = it.face.Metrics().Ascent.Ceil()
			it.y = l.y + asc - it.asc
			it.h = (it.face.Metrics().Ascent + it.face.Metrics().Descent).Ceil()
		default:
			it.y = l.y + asc - it.h - 6
		}
		l.doc.items = append(l.doc.items, it)
	}
	l.y += asc + desc
	l.line, l.x = l.line[:0], 0
}

// brColour is an item's colour for its role.
func brColour(role string) color.RGBA {
	switch role {
	case "link":
		return apBlue
	case "muted":
		return apSecondary
	}
	return apLabel
}

// brItemsIn are the items that show in the rows [y0, y1) of the page.
func brItemsIn(doc *brDoc, y0, y1 int) []brItem {
	var out []brItem
	for _, it := range doc.items {
		if it.y < y1 && it.y+max(it.h, 1) > y0 {
			out = append(out, it)
		}
	}
	return out
}

// brLinkAt is the link under a page point, -1 if none.
func brLinkAt(doc *brDoc, p image.Point) int {
	for _, it := range doc.items {
		if it.link >= 0 && p.In(image.Rect(it.x-8, it.y-8, it.x+it.w+8, it.y+it.h+8)) {
			return it.link
		}
	}
	return -1
}
