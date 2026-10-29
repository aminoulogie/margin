package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeWeb(t *testing.T) *httptest.Server {
	var pic bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 300, 150))
	for i := range im.Pix {
		im.Pix[i] = 200
	}
	im.Set(1, 1, color.RGBA{255, 0, 0, 255})
	png.Encode(&pic, im)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Fake Home</title><style>p{}</style><script>bad()</script></head><body>
<h1>Hello web</h1><p>Some <b>bold</b> text and a <a href="/second">second page</a>.</p>
<img src="/pic.png"><ul><li>one</li><li>two</li></ul><pre>code  line</pre>
<div hidden>SECRET</div>
<form action="/search" method="get"><input name="q" placeholder="Search"><input type="submit" value="Go"></form>
<p dir="rtl">مرحبا بالعالم</p></body></html>`)
	})
	mux.HandleFunc("/second", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><title>Second</title><body><p>Page two</p></body></html>`)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><title>Results</title><body><p>You searched %s</p></body></html>`, r.URL.Query().Get("q"))
	})
	mux.HandleFunc("/pic.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pic.Bytes())
	})
	srv := httptest.NewServer(mux)
	old, oldPath := brClient, brSavePath
	brClient = func() *http.Client { return srv.Client() }
	brSavePath = t.TempDir() + "/browser.json"
	t.Cleanup(func() { srv.Close(); brClient, brSavePath = old, oldPath })
	return srv
}

func waitBrowser(t *testing.T, c *console, ok func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		drawMu.Lock()
		done := ok()
		drawMu.Unlock()
		if done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("browser: timed out (err %q)", c.br.err)
}

func hasText(doc *brDoc, s string) bool {
	var all []string
	for _, it := range doc.items {
		if it.kind == brText {
			all = append(all, it.text)
		}
	}
	return strings.Contains(strings.Join(all, " "), s)
}

func TestBrowserPagesLinksFormsHistory(t *testing.T) {
	srv := fakeWeb(t)
	c := testConsole(t)
	drawMu.Lock()
	c.setMode(modeBrowser)
	if c.page == nil {
		t.Fatal("no start page")
	}
	shot(t, c, "browser-start")
	c.brGo(srv.URL + "/")
	drawMu.Unlock()
	waitBrowser(t, c, func() bool { return c.br.cur.doc != nil })
	drawMu.Lock()
	doc := c.br.cur.doc
	if doc.title != "Fake Home" || !hasText(doc, "Hello") || !hasText(doc, "bold") || hasText(doc, "SECRET") || hasText(doc, "bad()") {
		t.Fatalf("page laid out wrong: title %q", doc.title)
	}
	if !hasText(doc, "code  line") {
		t.Fatal("pre text should keep its spaces")
	}
	drawMu.Unlock()
	// The picture arrives and the page is laid out again with it.
	waitBrowser(t, c, func() bool {
		for _, it := range c.br.cur.doc.items {
			if it.kind == brImage {
				return true
			}
		}
		return false
	})
	drawMu.Lock()
	c.showPage()
	shot(t, c, "browser-page")
	// A link.
	tapButton(t, c, "br:a:0")
	drawMu.Unlock()
	waitBrowser(t, c, func() bool { return c.br.cur.doc != nil && c.br.cur.doc.title == "Second" })
	drawMu.Lock()
	tapButton(t, c, "br:back")
	if c.br.cur.doc == nil || c.br.cur.doc.title != "Fake Home" {
		t.Fatal("back should show the first page again")
	}
	// The form: type, then Go.
	c.showPage()
	field := -1
	for i, fd := range c.br.cur.doc.fields {
		if fd.name == "q" {
			field = i
		}
	}
	tapButton(t, c, fmt.Sprintf("br:f:%d", field))
	if !c.br.typing {
		t.Fatal("tapping the field should start typing")
	}
	c.brKey([]byte("films"))
	c.brKey([]byte("\r"))
	drawMu.Unlock()
	waitBrowser(t, c, func() bool { return c.br.cur.doc != nil && c.br.cur.doc.title == "Results" })
	drawMu.Lock()
	defer drawMu.Unlock()
	if !hasText(c.br.cur.doc, "films") {
		t.Fatal("the search should reach the server")
	}
	// Favourite, and the address bar's search.
	c.showPage()
	tapButton(t, c, "br:fav")
	if n := len(c.br.saved.Favorites); n != len(brDefaultFavs)+1 {
		t.Fatalf("favourites: %d", n)
	}
	if brAddress("cats and dogs") != brSearch+"cats+and+dogs" || brAddress("example.com") != "https://example.com" {
		t.Fatal("address bar: search or site")
	}
	shot(t, c, "browser-results")
}

func TestBrowserLongPageInParts(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < 900; i++ {
		fmt.Fprintf(&b, "<p>Paragraph %d with some words to fill the line.</p>", i)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(b.String() + "</body></html>")) }))
	defer srv.Close()
	old := brClient
	brClient = func() *http.Client { return srv.Client() }
	defer func() { brClient = old }()
	c := testConsole(t)
	drawMu.Lock()
	c.setMode(modeBrowser)
	c.brGo(srv.URL)
	drawMu.Unlock()
	waitBrowser(t, c, func() bool { return c.br.cur.doc != nil })
	drawMu.Lock()
	defer drawMu.Unlock()
	c.showPage()
	if c.page.img.Rect.Dy() > brPartH+brTopH+400 {
		t.Fatalf("a part is %d rows", c.page.img.Rect.Dy())
	}
	tapButton(t, c, "br:part+")
	if c.br.cur.part != 1 {
		t.Fatal("Continue should show the next part")
	}
}
