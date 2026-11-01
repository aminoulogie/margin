package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Home's store rows (Popular Free Books and the rows by topic) are kept on the tablet, with
// the books in them. Covers are kept for good already (covers.go), but the lists were asked
// for again at every start: on turning the tablet on, Wi-Fi takes half a minute to come back,
// and until then (and gutendex's answer) Home's rows said "Loading…" where the covers were.
// Now they show the books and covers from last time at once, even with no Wi-Fi, and are
// asked for again once a day, in the background; a new list replaces the old only when it
// has come.
//
// At start, the covers Home shows first (the first few of every row: what's on screen
// without scrolling it sideways) are read from the tablet into memory in the background, so
// Home's first drawing doesn't wait on the flash for them.

var homeListsPath = marginHome + "/home-lists.json"

const (
	homeListsFresh  = 24 * time.Hour // a kept row older than this is fetched again
	homeWarmPerRow  = 8              // covers read ahead per row at start (~6 fit across)
	homeKeptVersion = 1
)

type keptHome struct {
	V       int                 `json:"v"`
	Pop     keptList            `json:"pop"`
	Shelves map[string]keptList `json:"shelves"` // by shelfKey: rows can be reordered or added
}

type keptList struct {
	At    time.Time  `json:"at"`
	Items []keptItem `json:"items"`
}

type keptItem struct {
	Key       string      `json:"key,omitempty"`
	Title     string      `json:"title"`
	Author    string      `json:"author,omitempty"`
	Cover     string      `json:"cover,omitempty"`
	Summary   string      `json:"summary,omitempty"`
	Subjects  []string    `json:"subjects,omitempty"`
	Langs     []string    `json:"langs,omitempty"`
	Year      int         `json:"year,omitempty"`
	Downloads int         `json:"downloads,omitempty"`
	OLWork    string      `json:"olWork,omitempty"`
	Offers    []keptOffer `json:"offers,omitempty"`
}

type keptOffer struct {
	Src   int      `json:"src"`
	Full  bool     `json:"full,omitempty"`
	URLs  []string `json:"urls,omitempty"`
	IA    string   `json:"ia,omitempty"`
	Price string   `json:"price,omitempty"`
	Note  string   `json:"note,omitempty"`
	Rank  int      `json:"rank,omitempty"`
}

func shelfKey(topic, lang string) string { return topic + "|" + lang }

func keepItems(items []*storeItem) []keptItem {
	out := make([]keptItem, 0, len(items))
	for _, it := range items {
		k := keptItem{Key: it.key, Title: it.title, Author: it.author, Cover: it.cover, Summary: it.summary,
			Subjects: it.subjects, Langs: it.langs, Year: it.year, Downloads: it.downloads, OLWork: it.olWork}
		for _, o := range it.offers {
			k.Offers = append(k.Offers, keptOffer{o.src, o.full, o.urls, o.iaID, o.price, o.note, o.rank})
		}
		out = append(out, k)
	}
	return out
}

func unkeepItems(kept []keptItem) []*storeItem {
	out := make([]*storeItem, 0, len(kept))
	for _, k := range kept {
		if k.Title == "" {
			continue
		}
		it := &storeItem{key: k.Key, title: k.Title, author: k.Author, cover: k.Cover, summary: k.Summary,
			subjects: k.Subjects, langs: k.Langs, year: k.Year, downloads: k.Downloads, olWork: k.OLWork}
		for _, o := range k.Offers {
			if o.Src < 0 || o.Src >= numSources {
				continue
			}
			it.offers = append(it.offers, storeOffer{src: o.Src, full: o.Full, urls: o.URLs, iaID: o.IA,
				price: o.Price, note: o.Note, rank: o.Rank})
		}
		out = append(out, it)
	}
	return out
}

// homeRestore puts the rows kept on the tablet back on Home (once) and starts reading their
// first covers into memory. Caller holds drawMu.
func (c *console) homeRestore() {
	if c.homeKept {
		return
	}
	c.homeKept = true
	if c.homeShelf == nil {
		c.homeShelf = map[int]*homeShelfState{}
	}
	var kh keptHome
	if b, err := os.ReadFile(homeListsPath); err == nil {
		if err := json.Unmarshal(b, &kh); err != nil || kh.V != homeKeptVersion {
			log.Printf("home: kept rows not read (version %d, %v)", kh.V, err)
			kh = keptHome{}
		}
	}
	var warm []coverKey
	firsts := func(items []*storeItem) {
		for i, it := range items {
			if i == homeWarmPerRow {
				break
			}
			if it.cover != "" {
				warm = append(warm, coverKey{it.cover, rowCW, rowCH})
			}
		}
	}
	if len(c.homePop) == 0 {
		if items := unkeepItems(kh.Pop.Items); len(items) > 0 {
			c.homePop, c.homePopAt = items, kh.Pop.At
			firsts(items)
		}
	}
	for si, sh := range homeShelves {
		kl, ok := kh.Shelves[shelfKey(sh.topic, sh.lang)]
		if !ok || c.homeShelf[si] != nil {
			continue
		}
		if items := unkeepItems(kl.Items); len(items) > 0 {
			c.homeShelf[si] = &homeShelfState{items: items, at: kl.At}
			firsts(items)
		}
	}
	// The library's own books (Continue, Want to Read): their covers are inside them.
	if len(c.shelf) == 0 {
		c.shelf = findBooks()
	}
	for k, i := range c.readingNow() {
		if k == 3 {
			break
		}
		if cv := c.shelf[i].cover; cv != "" {
			warm = append(warm, coverKey{cv, 72, 108})
		}
	}
	for k, i := range c.wantToRead() {
		if k == homeWarmPerRow {
			break
		}
		if cv := c.shelf[i].cover; cv != "" {
			warm = append(warm, coverKey{cv, rowCW, rowCH})
		}
	}
	cc := covers
	go func() {
		t := time.Now()
		n := 0
		for _, k := range warm {
			if cc.warm(k) {
				n++
			}
		}
		log.Printf("home: %d of %d covers on screen read ahead in %v", n, len(warm), time.Since(t).Round(time.Millisecond))
	}()
}

var homeSave struct {
	sync.Mutex
	gen, written int
}

// saveHomeLists keeps Home's rows on the tablet (written in the background: the rows have
// summaries, a few hundred kilobytes). Caller holds drawMu.
func (c *console) saveHomeLists() {
	kh := keptHome{V: homeKeptVersion, Shelves: map[string]keptList{}}
	if len(c.homePop) > 0 {
		kh.Pop = keptList{At: c.homePopAt, Items: keepItems(c.homePop)}
	}
	for si, st := range c.homeShelf {
		if si < len(homeShelves) && st != nil && len(st.items) > 0 {
			sh := homeShelves[si]
			kh.Shelves[shelfKey(sh.topic, sh.lang)] = keptList{At: st.at, Items: keepItems(st.items)}
		}
	}
	b, err := json.Marshal(kh)
	if err != nil {
		return
	}
	homeSave.Lock()
	homeSave.gen++
	gen := homeSave.gen
	homeSave.Unlock()
	path := homeListsPath
	write := func() {
		homeSave.Lock()
		defer homeSave.Unlock()
		if gen < homeSave.written { // a newer list is already on the tablet
			return
		}
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path+".new", b, 0o644); err != nil {
			log.Printf("home: keeping the rows: %v", err)
			return
		}
		if os.Rename(path+".new", path) == nil {
			homeSave.written = gen
		}
	}
	if homeSaveAsync {
		go write()
	} else {
		write()
	}
}

var homeSaveAsync = true // tests write at once
