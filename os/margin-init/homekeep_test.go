package main

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"margin-init/ui"
)

// keptBook is a store book as Home's rows hold them.
func keptBook(n int, cover string) *storeItem {
	return &storeItem{key: "k" + string(rune('a'+n)), title: "Kept Book " + string(rune('A'+n)), author: "Someone",
		cover: cover, summary: "A summary.", langs: []string{"en"}, downloads: 100 - n,
		offers: []storeOffer{{src: srcGutenberg, full: true, rank: n, urls: []string{"https://example.org/" + string(rune('a'+n)) + ".epub"}}}}
}

// putScaled puts a cover on the tablet already scaled to w x h, in one colour.
func putScaled(url string, w, h int, col color.RGBA) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	ui.Fill(img, img.Rect, col)
	writeScaled(coverDir, coverKey{url, w, h}, img)
}

// After a restart, with no internet, Home shows the rows it had, with their covers, at once,
// and doesn't ask for lists fetched less than a day ago.
func TestHomeRowsKeptAcrossRestart(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	fetched := time.Now().Add(-time.Hour)
	c.homePop, c.homePopAt = []*storeItem{keptBook(0, "https://example.org/pop0.jpg"), keptBook(1, "")}, fetched
	c.homeShelf = map[int]*homeShelfState{2: {items: []*storeItem{keptBook(2, "https://example.org/sf.jpg")}, at: fetched}}
	c.saveHomeLists()

	// A restart: nothing in memory, the covers only on the tablet.
	c.homePop, c.homePopAt, c.homeShelf, c.homeKept = nil, time.Time{}, nil, false
	covers = newCoverCache()
	putScaled("https://example.org/pop0.jpg", rowCW, rowCH, color.RGBA{230, 20, 20, 255})
	homeLoads = true // it would ask, if the lists were old (they aren't)
	t.Cleanup(func() { homeLoads = false })
	c.mode = modeBooksHome
	c.showPage()

	if len(c.homePop) != 2 || c.homePop[0].title != "Kept Book A" || !c.homePopAt.Equal(fetched) {
		t.Fatalf("popular row after a restart: %d books, fetched %v", len(c.homePop), c.homePopAt)
	}
	it := c.homePop[0]
	if it.cover != "https://example.org/pop0.jpg" || it.summary != "A summary." || !it.full() ||
		len(it.offers[0].urls) != 1 || it.downloads != 100 {
		t.Fatalf("a kept book lost something: %+v", it)
	}
	if st := c.homeShelf[2]; st == nil || len(st.items) != 1 || st.items[0].title != "Kept Book C" {
		t.Fatalf("the science fiction row wasn't kept: %+v", st)
	}
	if c.homePopLoading {
		t.Fatal("a list fetched an hour ago was asked for again")
	}
	for si, st := range c.homeShelf {
		if si == 2 && st.loading {
			t.Fatal("a row fetched an hour ago was asked for again")
		}
	}
	var btn *button
	for _, b := range c.page.rowBtns["home:pop"] {
		if b.id == "home:pop:0" {
			btn = &b
		}
	}
	if btn == nil {
		t.Fatal("Home's popular row shows no books after a restart")
	}
	mid := btn.r.Min.Add(image.Pt(rowCW/2, rowCH/2))
	if p := c.page.img.RGBAAt(mid.X, mid.Y); p.R != 230 || p.G != 20 {
		t.Fatalf("the kept cover isn't drawn: %v", p)
	}
}

// A row older than a day is asked for again, and keeps its books on show until the new ones
// have come; if they don't come (no internet), it keeps them.
func TestHomeOldRowsRefreshedInBackground(t *testing.T) {
	f := newFakeLibraries(t, false)
	c := testConsole(t)
	homeLoads = true
	t.Cleanup(func() {
		drawMu.Lock()
		homeLoads = false
		drawMu.Unlock()
		homeLoading.Wait()
	})
	drawMu.Lock()
	old := time.Now().Add(-48 * time.Hour)
	c.homePop, c.homePopAt, c.homeKept = []*storeItem{keptBook(0, "")}, old, true
	c.loadPopular()
	if !c.homePopLoading || len(c.homePop) != 1 {
		t.Fatalf("a two-day-old list: loading %v, %d books on show", c.homePopLoading, len(c.homePop))
	}
	drawMu.Unlock()
	waitFor(t, "the new list", func() bool { return !c.homePopLoading })
	drawMu.Lock()
	if len(c.homePop) != 2 || c.homePop[0].title == "Kept Book A" || time.Since(c.homePopAt) > time.Minute {
		t.Fatalf("after the fetch: %d books, first %q", len(c.homePop), c.homePop[0].title)
	}
	drawMu.Unlock()
	if len(f.asked("/books/")) == 0 {
		t.Fatal("gutendex wasn't asked")
	}
	// What's on the tablet is the new list.
	drawMu.Lock()
	c.homePop, c.homeKept = nil, false
	c.homeRestore()
	if len(c.homePop) != 2 || c.homePop[0].title == "Kept Book A" {
		t.Fatalf("the new list wasn't kept: %d books", len(c.homePop))
	}

	// No internet: the old list stays. The server is closed without drawMu: a request still on
	// its way (a background load) takes drawMu in the fake server, and Close waits for it.
	drawMu.Unlock()
	f.srv.Close()
	drawMu.Lock()
	c.homePop, c.homePopAt = []*storeItem{keptBook(0, "")}, old
	c.loadPopular()
	drawMu.Unlock()
	waitFor(t, "the failed fetch", func() bool { return !c.homePopLoading })
	drawMu.Lock()
	defer drawMu.Unlock()
	if len(c.homePop) != 1 || c.homePop[0].title != "Kept Book A" {
		t.Fatalf("with no internet the row lost its books: %d", len(c.homePop))
	}
}

// coverServer serves a cover after failing the first `fails` times with status `code`.
func coverServer(t *testing.T, code, fails int) (*httptest.Server, *atomic.Int32) {
	var asked atomic.Int32
	src := image.NewRGBA(image.Rect(0, 0, 100, 150))
	ui.Fill(src, src.Rect, color.RGBA{30, 200, 60, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if int(asked.Add(1)) <= fails {
			w.WriteHeader(code)
			return
		}
		png.Encode(w, src)
	}))
	t.Cleanup(srv.Close)
	old := webClient
	webClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { webClient = old })
	return srv, &asked
}

// A cover that failed for want of internet (here the server's bad moment, 503) is asked for
// again by itself, and drawn when it comes; nothing has to ask for it again.
func TestCoverRetriedAfterNetworkFailure(t *testing.T) {
	testConsole(t)
	srv, asked := coverServer(t, http.StatusServiceUnavailable, 2)
	old := coverRetryBase
	coverRetryBase = 30 * time.Millisecond
	t.Cleanup(func() { coverRetryBase = old })
	got := make(chan string, 4)
	covers.loaded = func(u string) { got <- u }
	url := srv.URL + "/c.png"
	if covers.get(url, 60, 90) != nil {
		t.Fatal("not on the tablet yet: it loads in the background")
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatalf("the cover never came (asked %d times)", asked.Load())
	}
	if n := asked.Load(); n != 3 {
		t.Fatalf("asked %d times, want 3 (two failures, then the cover)", n)
	}
	if covers.get(url, 60, 90) == nil {
		t.Fatal("loaded, but not in memory")
	}
	covers.mu.Lock()
	defer covers.mu.Unlock()
	if covers.failed[url] != nil {
		t.Fatal("a cover that came is still noted as failed")
	}
}

// A cover the server says doesn't exist isn't asked for again, even after a restart.
func TestCoverNotFoundRemembered(t *testing.T) {
	testConsole(t)
	srv, asked := coverServer(t, http.StatusNotFound, 1000)
	url := srv.URL + "/none.png"
	covers.get(url, 60, 90)
	waitFor(t, "the 404", func() bool {
		covers.mu.Lock()
		defer covers.mu.Unlock()
		return covers.failed[url] != nil
	})
	covers.mu.Lock()
	f := *covers.failed[url]
	covers.mu.Unlock()
	if !f.gone {
		t.Fatal("a 404 is to be tried again")
	}
	if _, err := os.Stat(noneFile(coverDir, url)); err != nil {
		t.Fatalf("not noted on the tablet: %v", err)
	}
	covers = newCoverCache() // a restart
	for i := 0; i < 3; i++ {
		if covers.get(url, 60, 90) != nil {
			t.Fatal("a cover from nowhere")
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := asked.Load(); n != 1 {
		t.Fatalf("asked %d times for a cover that doesn't exist", n)
	}
}

// Network failures and server hiccups are tried again; "not found" and non-pictures aren't.
func TestCoverGone(t *testing.T) {
	for _, tc := range []struct {
		err  error
		gone bool
	}{
		{&httpStatusError{"u", "404 Not Found"}, true},
		{&httpStatusError{"u", "410 Gone"}, true},
		{&httpStatusError{"u", "429 Too Many Requests"}, false},
		{&httpStatusError{"u", "502 Bad Gateway"}, false},
		{&coverNotPicture{"decode: bad"}, true},
		{os.ErrDeadlineExceeded, false},
	} {
		if got := coverGone(tc.err); got != tc.gone {
			t.Errorf("coverGone(%v) = %v", tc.err, got)
		}
	}
	if coverRetry(1) != coverRetryBase || coverRetry(2) != 2*coverRetryBase || coverRetry(30) != 15*time.Minute {
		t.Errorf("waits: %v %v %v", coverRetry(1), coverRetry(2), coverRetry(30))
	}
}

// Read ahead at start: covers on the tablet go to memory without drawing; they never push
// out covers that were drawn.
func TestCoverWarm(t *testing.T) {
	testConsole(t)
	putScaled("w1", rowCW, rowCH, color.RGBA{1, 2, 3, 255})
	if !covers.warm(coverKey{"w1", rowCW, rowCH}) {
		t.Fatal("a cover on the tablet wasn't read ahead")
	}
	if covers.warm(coverKey{"absent", rowCW, rowCH}) {
		t.Fatal("read ahead a cover that isn't on the tablet")
	}
	covers.mu.Lock()
	defer covers.mu.Unlock()
	if covers.imgs[coverKey{"w1", rowCW, rowCH}] == nil {
		t.Fatal("not in memory")
	}
	// Memory full of drawn covers: a read-ahead one doesn't get in.
	big := func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 1024, 2048)) }
	for i := 0; covers.bytes+8<<20 <= coverMemBytes; i++ {
		covers.keep(coverKey{string(rune('a' + i)), 1024, 2048}, big())
	}
	n := covers.lru.Len()
	covers.keepCold(coverKey{"cold", 1024, 2048}, big())
	if covers.lru.Len() != n || covers.imgs[coverKey{"cold", 1024, 2048}] != nil {
		t.Fatal("a cover read ahead pushed out one that was drawn")
	}
}

// The kept rows file is read back even when the rows are in another order.
func TestHomeKeptByTopic(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	c.homeShelf = map[int]*homeShelfState{0: {items: []*storeItem{keptBook(0, "")}, at: time.Now()}}
	c.saveHomeLists()
	b, err := os.ReadFile(homeListsPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := shelfKey(homeShelves[0].topic, homeShelves[0].lang); !strings.Contains(string(b), `"`+want+`"`) {
		t.Fatalf("kept rows aren't keyed by topic: %s", b)
	}
}
