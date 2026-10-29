package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"margin-init/ui"
)

// The Playbox tab: Playbox (the iPhone app) as a tab of Margin, in Margin's look. Its own
// row under the tab bar is the app's tab bar: Discover (the catalogs, a search), Library
// (Continue Watching, downloads, saved), Add-ons (the add-on store) and Settings. A film's
// page has Quick Play, subtitles and every source sorted by seeders; a series' page has its
// seasons, and an episode its own page with its sources. Play opens the player at once
// (video.go); a torrent fills in while it plays.

type pbSection struct {
	id, title string
	items     []metaItem
}

type pbUI struct {
	view        string // "discover", "library", "addons", "settings"
	disc        []pbSection
	discLoading bool
	discAt      time.Time
	discErr     string
	query       string // the search shown ("" = the catalogs)
	results     []pbSection
	searching   bool
	searchErr   string
	gen         int // bumped by each load: late answers to replaced loads are dropped
	typing      bool
	field       string // "search", "addonurl", "addonfind"
	editing     string
	addonURL    string
	addonFind   string
	sel         *pbDetail
	items       []metaItem // the posters on the page, by button number
	libEdit     bool
	msg         string
	soundTest   string
	installing  bool // the video player (ffmpeg) is being installed
	httpDL      map[string]*httpDownload
}

// pbDetail is a film's or series' page.
type pbDetail struct {
	item    metaItem // as it was tapped
	meta    *metaItem
	metaErr string
	season  int
	episode string // the episode open ("" = the series' page)
	streams []*addonStream
	subs    []addonSubtitle
	loading int // add-ons still answering
	loaded  bool
	sub     string // the subtitle language chosen ("" = off)
	sort    string
	quality string // "" = all
	limit   int    // torrents shown
	epLimit int
	unsupp  bool // the unavailable sources are shown
	errs    map[string]string
	from    string // the view it was opened from
	gen     int
	seriesY int   // the series page's scroll, kept while an episode is open
	warming bool  // pbPrewarm is starting it
	warm    *tjob // the best torrent, connecting while the page is read (pbPrewarm)
}

func (d *pbDetail) info() metaItem {
	if d.meta != nil {
		m := *d.meta
		if m.Poster == "" {
			m.Poster = d.item.Poster
		}
		if m.Name == "" {
			m.Name = d.item.Name
		}
		if m.Type == "" {
			m.Type = d.item.Type
		}
		return m
	}
	return d.item
}

// --- settings ------------------------------------------------------------------------------

type pbSettings struct {
	PosterCols int    `json:"posterColumns"`
	NoGestures bool   `json:"noVerticalGestures,omitempty"`
	Light      bool   `json:"lightPicture,omitempty"` // half the resolution: smoother on this processor
	SubLang    string `json:"subtitleLanguage,omitempty"`
	Flip       bool   `json:"flipPicture,omitempty"` // the picture turned the other way up
	Volume     int    `json:"volume"`                // percent, 0 to 150
	Sort       string `json:"sourceSort,omitempty"`
	SortChosen bool   `json:"sourceSortChosen,omitempty"` // picked by hand since "Starts Fastest" came
}

var pbClientOnce sync.Once

var (
	pbCfgMu sync.Mutex
	pbCfg   *pbSettings
)

func pbSettingsNow() *pbSettings {
	pbCfgMu.Lock()
	defer pbCfgMu.Unlock()
	if pbCfg == nil {
		pbCfg = &pbSettings{PosterCols: 5, Volume: 100}
		if b, err := os.ReadFile(filepath.Join(playboxDir, "settings.json")); err == nil {
			json.Unmarshal(b, pbCfg)
		}
		pbCfg.PosterCols = min(max(pbCfg.PosterCols, 2), 8)
		pbCfg.Volume = min(max(pbCfg.Volume, 0), 150)
	}
	return pbCfg
}

func (s *pbSettings) save() {
	b, _ := json.MarshalIndent(s, "", "  ")
	os.MkdirAll(playboxDir, 0o755)
	os.WriteFile(filepath.Join(playboxDir, "settings.json"), b, 0o644)
}

// posterSize is a poster's size for the chosen number per row (Playbox's PosterLayout).
func (c *console) posterSize() (w, h, gap int) {
	n := pbSettingsNow().PosterCols
	gap = map[bool]int{true: 18, false: 28}[n >= 6]
	w = (c.s.W - 2*pageGutter - gap*(n-1)) / n
	return w, w * 3 / 2, gap
}

// --- loading -------------------------------------------------------------------------------

// pbRedraw shows the Playbox page again if it's on screen (an answer came in). Caller holds
// drawMu.
func (c *console) pbRedraw() {
	if c.mode == modePlaybox && c.screenOn && !c.locked && !c.pb.typing {
		c.showPage()
	}
}

// discoverSources are Discover's rows: Cinemeta's four, then every installed add-on's
// catalogs that list without a search.
func discoverSources() []struct{ title, base, typ, id string } {
	srcs := []struct{ title, base, typ, id string }{
		{"Popular Movies", cinemetaBase, "movie", "top"},
		{"Popular Series", cinemetaBase, "series", "top"},
		{"Top Rated Movies", cinemetaBase, "movie", "imdbRating"},
		{"Top Rated Series", cinemetaBase, "series", "imdbRating"},
	}
	for _, a := range addons.all() {
		if a.Manifest.ID == "com.linvo.cinemeta" || a.Base == cinemetaBase {
			continue
		}
		for _, cat := range a.Manifest.Catalogs {
			if cat.needsExtra() {
				continue
			}
			name := cat.Name
			if name == "" {
				name = a.Manifest.Name
			}
			srcs = append(srcs, struct{ title, base, typ, id string }{name + " · " + strings.Title(cat.Type), a.Base, cat.Type, cat.ID})
		}
	}
	return srcs
}

// loadDiscover fetches Discover's rows, all at once. Caller holds drawMu.
func (c *console) loadDiscover() {
	pb := &c.pb
	if pb.discLoading {
		return
	}
	pb.discLoading, pb.discErr = true, ""
	pb.gen++
	gen := pb.gen
	srcs := discoverSources()
	go func() {
		out := make([][]metaItem, len(srcs))
		var wg sync.WaitGroup
		var errMu sync.Mutex
		var lastErr error
		for i, s := range srcs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				items, err := fetchCatalog(ctx, s.base, s.typ, s.id)
				if err != nil {
					errMu.Lock()
					lastErr = err
					errMu.Unlock()
				}
				out[i] = items
			}()
		}
		wg.Wait()
		drawMu.Lock()
		defer drawMu.Unlock()
		pb.discLoading = false
		var secs []pbSection
		for i, items := range out {
			if len(items) > 0 {
				secs = append(secs, pbSection{id: fmt.Sprint(i), title: srcs[i].title, items: items})
			}
		}
		pb.disc, pb.discAt = secs, time.Now()
		if len(secs) == 0 {
			pb.discErr = "No catalog data was returned. Check your connection."
			if lastErr != nil {
				pb.discErr += " (" + shortErr(lastErr) + ")"
			}
		}
		if gen == pb.gen || pb.query == "" {
			c.pbRedraw()
		}
	}()
}

// pbSearch asks Cinemeta and every add-on catalog that can search, then merges, removes
// repeats and ranks the results (CatalogView.search). Caller holds drawMu.
func (c *console) pbSearch(q string) {
	pb := &c.pb
	q = strings.TrimSpace(q)
	pb.query, pb.results, pb.searchErr = q, nil, ""
	c.setScroll(modePlaybox, 0)
	if q == "" {
		return
	}
	pb.searching = true
	pb.gen++
	gen := pb.gen
	type src struct{ base, typ, id string }
	srcs := []src{{cinemetaBase, "movie", "top"}, {cinemetaBase, "series", "top"}}
	for _, a := range addons.all() {
		if a.Manifest.ID == "com.linvo.cinemeta" || a.Base == cinemetaBase {
			continue
		}
		for _, cat := range a.Manifest.Catalogs {
			if cat.supportsSearch() {
				srcs = append(srcs, src{a.Base, cat.Type, cat.ID})
			}
		}
	}
	go func() {
		out := make([][]metaItem, len(srcs))
		var wg sync.WaitGroup
		for i, s := range srcs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				out[i], _ = fetchSearch(ctx, s.base, s.typ, s.id, q)
			}()
		}
		wg.Wait()
		type ranked struct {
			score, order int
			it           metaItem
		}
		var all []ranked
		seen := map[string]bool{}
		for si, items := range out {
			for k, it := range items {
				if key := it.Type + ":" + it.ID; !seen[key] {
					seen[key] = true
					all = append(all, ranked{relevance(it.Name, q), si*1000 + k, it})
				}
			}
		}
		sort.SliceStable(all, func(i, j int) bool {
			if all[i].score != all[j].score {
				return all[i].score > all[j].score
			}
			return all[i].order < all[j].order
		})
		var movies, series, other []metaItem
		for _, r := range all {
			switch r.it.Type {
			case "movie":
				movies = append(movies, r.it)
			case "series":
				series = append(series, r.it)
			default:
				other = append(other, r.it)
			}
		}
		var res []pbSection
		for _, s := range []pbSection{{"search-movie", "Movies", movies}, {"search-series", "Series", series}, {"search-other", "More Results", other}} {
			if len(s.items) > 0 {
				res = append(res, s)
			}
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		if gen != pb.gen || pb.query != q {
			return
		}
		pb.searching, pb.results = false, res
		if len(res) == 0 {
			pb.searchErr = "No matching titles were found for “" + q + "”."
		}
		c.pbRedraw()
	}()
}

// pbOpen opens a film's or series' page. Caller holds drawMu.
func (c *console) pbOpen(it metaItem) {
	pb := &c.pb
	if pb.sel != nil {
		c.pbDropWarm(pb.sel)
	}
	pb.gen++
	d := &pbDetail{item: it, from: pb.view, gen: pb.gen, sort: pbSettingsNow().Sort, limit: 12, epLimit: 30, errs: map[string]string{}}
	if d.sort == "" || d.sort == "seeders" && !pbSettingsNow().SortChosen {
		d.sort = "fast"
	}
	pb.sel = d
	c.setScroll(modePlaybox, 0)
	base := metaBase(it.Type, it.ID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		m, err := fetchMeta(ctx, base, it.Type, it.ID)
		cancel()
		drawMu.Lock()
		defer drawMu.Unlock()
		if pb.sel != d {
			return
		}
		if err != nil || m.ID == "" {
			d.metaErr = "Couldn't load the details."
			if err != nil {
				d.metaErr += " (" + shortErr(err) + ")"
			}
		} else {
			d.meta = &m
		}
		if it.Type == "series" {
			if seasons := seasonsOf(d.info().Videos); len(seasons) > 0 {
				d.season = seasons[0]
				for _, s := range seasons {
					if s > 0 { // the first real season, not the specials
						d.season = s
						break
					}
				}
				// Back to the episode last watched.
				if e, ok := pbLib.recentEntry(it.ID); ok && e.Episode != "" {
					for _, v := range d.info().Videos {
						if v.ID == e.Episode {
							d.season = v.Season
						}
					}
				}
			}
			c.pbRedraw()
			return
		}
		c.pbRedraw()
	}()
	if it.Type != "series" {
		c.pbLoadSources(d, it.ID)
	}
}

func seasonsOf(videos []metaVideo) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range videos {
		if !seen[v.Season] {
			seen[v.Season] = true
			out = append(out, v.Season)
		}
	}
	sort.Ints(out)
	return out
}

func episodesOf(videos []metaVideo, season int) []metaVideo {
	var out []metaVideo
	for _, v := range videos {
		if v.Season == season {
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].episodeNo() < out[j].episodeNo() })
	return out
}

// pbLoadSources asks every add-on that serves streams or subtitles for this id, and shows
// the sources as they come. Caller holds drawMu.
func (c *console) pbLoadSources(d *pbDetail, id string) {
	d.streams, d.subs, d.loaded = nil, nil, false
	d.sub = ""
	typ := d.item.Type
	type ask struct {
		base, name string
		streams    bool
	}
	var asks []ask
	for _, a := range addons.all() {
		if a.Manifest.has("stream", typ, id) {
			asks = append(asks, ask{a.Base, a.Manifest.Name, true})
		}
		if a.Manifest.has("subtitles", typ, id) {
			asks = append(asks, ask{a.Base, a.Manifest.Name, false})
		}
	}
	d.loading = len(asks)
	if d.loading == 0 {
		d.loaded = true
		return
	}
	pb := &c.pb
	for _, a := range asks {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			var streams []*addonStream
			var subs []addonSubtitle
			var err error
			if a.streams {
				streams, err = fetchStreams(ctx, a.base, typ, id)
				for _, s := range streams {
					s.Addon = a.name
				}
			} else {
				subs, err = fetchSubtitles(ctx, a.base, typ, id)
			}
			if err != nil {
				log.Printf("playbox: %s %s: %v", a.name, id, err)
			}
			drawMu.Lock()
			defer drawMu.Unlock()
			if pb.sel != d || (d.episode != "" && d.episode != id) || (d.episode == "" && d.item.Type == "series") {
				return
			}
			for _, s := range streams { // one list from every add-on, the same torrent once
				d.streams = mergeStream(d.streams, s)
			}
			sortStreams(d.streams)
			c.pbPrewarm(d)
			subSeen := map[string]bool{}
			for _, s := range d.subs {
				subSeen[s.key()] = true
			}
			for _, s := range subs {
				if !subSeen[s.key()] && s.URL != "" {
					subSeen[s.key()] = true
					d.subs = append(d.subs, s)
				}
			}
			d.loading--
			if d.loading == 0 {
				d.loaded = true
				if want := pbSettingsNow().SubLang; want != "" && d.sub == "" {
					for _, l := range subLangs(d.subs) {
						if l == want {
							d.sub = want
						}
					}
				}
			}
			c.pbRedraw()
		}()
	}
}

// subLangs are the subtitle languages offered, the tablet's own first (Arabic, French,
// English), then the rest alphabetically.
func subLangs(subs []addonSubtitle) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range subs {
		l := strings.ToLower(s.Lang)
		if l == "" {
			l = "und"
		}
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	first := map[string]int{"ara": 1, "ar": 1, "fre": 2, "fra": 2, "fr": 2, "eng": 3, "en": 3}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := first[out[i]], first[out[j]]
		if (a > 0) != (b > 0) {
			return a > 0
		}
		if a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}

// subsFor are the subtitle files in a language, the add-on's best first.
func subsFor(subs []addonSubtitle, lang string) []addonSubtitle {
	var out []addonSubtitle
	for _, s := range subs {
		l := strings.ToLower(s.Lang)
		if l == "" {
			l = "und"
		}
		if l == lang {
			out = append(out, s)
		}
	}
	return out
}

var langNames = map[string]string{"ara": "العربية", "ar": "العربية", "fre": "Français", "fra": "Français", "fr": "Français",
	"eng": "English", "en": "English", "spa": "Español", "es": "Español", "ger": "Deutsch", "deu": "Deutsch", "de": "Deutsch",
	"ita": "Italiano", "it": "Italiano", "por": "Português", "pob": "Português (BR)", "pt": "Português", "tur": "Türkçe", "tr": "Türkçe",
	"rus": "Русский", "ru": "Русский", "dut": "Nederlands", "nld": "Nederlands", "pol": "Polski", "rum": "Română", "gre": "Ελληνικά",
	"heb": "עברית", "per": "فارسی", "fas": "فارسی", "chi": "中文", "zho": "中文", "jpn": "日本語", "kor": "한국어", "hin": "हिन्दी",
	"swe": "Svenska", "nor": "Norsk", "dan": "Dansk", "fin": "Suomi", "hun": "Magyar", "cze": "Čeština", "ces": "Čeština",
	"und": "Other"}

func langName(l string) string {
	if n := langNames[strings.ToLower(l)]; n != "" {
		return n
	}
	return strings.ToUpper(l)
}

// --- pages ---------------------------------------------------------------------------------

func (c *console) playboxPage() *page {
	pb := &c.pb
	if pb.view == "" {
		pb.view = "discover"
	}
	addons.load()
	addons.mu.Lock()
	addons.changed = func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if pb.view == "discover" && !pb.discLoading { // new catalogs
			pb.discAt = time.Time{}
		}
		c.pbRedraw()
	}
	addons.mu.Unlock()
	pbChanged = func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		c.pbRedraw()
	}
	pb.items = pb.items[:0]
	pbClientOnce.Do(func() { go engine.client() }) // started early, never while the screen waits
	if pb.sel != nil {
		if pb.sel.episode != "" {
			return c.pbEpisodePage()
		}
		return c.pbDetailPage()
	}
	switch pb.view {
	case "library":
		return c.pbLibraryPage()
	case "addons":
		return c.pbAddonsPage()
	case "settings":
		return c.pbSettingsPage()
	}
	return c.pbDiscoverPage()
}

// pbItemID registers a poster's button.
func (c *console) pbItemID(it metaItem) string {
	c.pb.items = append(c.pb.items, it)
	return "pb:item:" + strconv.Itoa(len(c.pb.items)-1)
}

func posterItem(it metaItem) *storeItem {
	sub := it.ReleaseInfo
	if sub == "" {
		sub = strings.Title(it.Type)
	}
	return &storeItem{key: it.ID, title: it.Name, author: sub, cover: it.Poster}
}

// pbRowH is the height a poster row takes with its title.
func (c *console) pbRowH() int {
	_, h, _ := c.posterSize()
	return h + 200
}

// pbRow draws a section's title and its posters in a row that scrolls sideways. Returns the
// y under it.
func (c *console) pbRow(p *page, id, title string, y int, items []metaItem) int {
	f := apple()
	pw, ph, gap := c.posterSize()
	apText(p.img, f.title, pageGutter, y+56, apLabel, title)
	y += 84
	band := image.Rect(0, y-10, c.s.W, y+ph+110)
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = c.pbItemID(it)
	}
	contentW := 2*pageGutter + len(items)*(pw+gap) - gap
	paint := func(img *image.RGBA) {
		ui.Fill(img, image.Rect(img.Rect.Min.X, band.Min.Y, img.Rect.Max.X, band.Max.Y), apBG)
		off := c.rowOffset(id)
		var btns []button
		for k, it := range items {
			x := pageGutter + k*(pw+gap) - off
			if x+pw+40 < img.Rect.Min.X || x-40 > img.Rect.Max.X {
				continue
			}
			r := image.Rect(x, y, x+pw, y+ph)
			c.drawPoster(img, r, it)
			apText(img, f.captionBold, x, r.Max.Y+38, apLabel, clip(f.captionBold, it.Name, pw))
			sub := strings.Title(it.Type)
			if it.ReleaseInfo != "" {
				sub = it.ReleaseInfo + " · " + sub
			}
			apText(img, f.caption, x, r.Max.Y+72, apSecondary, clip(f.caption, sub, pw))
			btns = append(btns, button{ids[k], image.Rect(x, y, x+pw, r.Max.Y+84)})
		}
		p.setRowButtons(id, btns)
	}
	paint(p.img)
	p.addRow(id, band, contentW, paint)
	r := &p.rows[len(p.rows)-1]
	r.covers = map[string]bool{}
	for _, it := range items {
		if it.Poster != "" {
			r.covers[it.Poster] = true
		}
	}
	return y + ph + 116
}

// drawPoster draws a poster with its rating, as Playbox's cards.
func (c *console) drawPoster(img *image.RGBA, r image.Rectangle, it metaItem) {
	shadowRect(img, r)
	c.drawCover(img, r, posterItem(it))
	if it.IMDbRating != "" && r.Dx() >= 140 {
		f := apple()
		label := "★ " + it.IMDbRating
		w := ui.TextWidth(f.captionBold, label) + 24
		br := image.Rect(r.Max.X-w-10, r.Min.Y+10, r.Max.X-10, r.Min.Y+50)
		ui.RoundRect(img, br, 20, rgb(0x000000))
		apTextCenter(img, f.captionBold, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2, rgb(0xffd60a), label)
	}
}

// pbField draws a text field (a search, an address) and registers it.
func (c *console) pbField(p *page, id, placeholder, value string, r image.Rectangle, search bool) {
	f := apple()
	ui.RoundRect(p.img, r, r.Dy()/2, apCard2)
	x := r.Min.X + 30
	if search {
		iconMagnifier(p.img, r.Min.X+40, (r.Min.Y+r.Max.Y)/2, apSecondary)
		x += 48
	}
	text, col := value, apLabel
	if c.pb.typing && c.pb.field == id {
		text = withCursor(c.pb.editing, c.fieldBack)
	} else if text == "" {
		text, col = placeholder, apSecondary
	}
	for text != "" && ui.TextWidth(f.body, text) > r.Max.X-x-30 {
		text = string([]rune(text)[1:])
	}
	apText(p.img, f.body, x, (r.Min.Y+r.Max.Y)/2+12, col, text)
	p.buttons = append(p.buttons, button{"pb:type:" + id, r})
}

func (c *console) pbDiscoverPage() *page {
	f := apple()
	pb := &c.pb
	w, mx := c.s.W, pageGutter
	if pb.query == "" && !pb.discLoading && (pb.disc == nil || time.Since(pb.discAt) > 30*time.Minute) {
		if pb.discErr == "" || time.Since(pb.discAt) > 20*time.Second {
			c.loadDiscover()
		}
	}
	secs := pb.disc
	if pb.query != "" {
		secs = pb.results
	}
	h := 420 + len(secs)*c.pbRowH() + 120
	hero := pb.query == "" && len(secs) > 0 && len(secs[0].items) > 0
	if hero {
		h += 640
	}
	img := canvas(w, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	c.booksTabs(p, "pbt:discover")
	apText(img, f.largeTitle, mx, 238, apLabel, "Playbox")
	c.pbField(p, "search", "Movies and series", pb.query, image.Rect(mx, 270, w-mx, 360), true)
	y := 400
	if pb.query != "" && !(pb.typing && pb.field == "search") {
		apTextRight(img, f.body, w-mx-30, 330, apBlue, "Clear")
		p.buttons = append(p.buttons, button{"pb:clear", image.Rect(w-mx-200, 270, w-mx, 360)})
	}
	switch {
	case pb.query != "" && pb.searching:
		apText(img, f.body, mx, y+60, apSecondary, "Searching…")
	case pb.query != "" && pb.searchErr != "":
		apText(img, f.title, mx, y+70, apLabel, "No Titles Found")
		drawParagraphs(img, f.callout, pb.searchErr, mx, y+110, w-2*mx, 44, y+300, apSecondary)
	case pb.query == "" && len(secs) == 0 && pb.discLoading:
		apText(img, f.body, mx, y+60, apSecondary, "Loading the catalogs…")
	case pb.query == "" && len(secs) == 0:
		apText(img, f.title, mx, y+70, apLabel, "Couldn't Load Discover")
		drawParagraphs(img, f.callout, pb.discErr, mx, y+110, w-2*mx, 44, y+300, apSecondary)
		r := image.Rect(mx, y+220, mx+300, y+316)
		ui.RoundRect(img, r, 48, apBlue)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apOnBlue, "Try Again")
		p.buttons = append(p.buttons, button{"pb:retry", r})
	}
	if hero {
		it := secs[0].items[0]
		r := image.Rect(mx, y, w-mx, y+600)
		c.drawHero(img, r, it)
		p.buttons = append(p.buttons, button{c.pbItemID(it), r})
		y += 640
	}
	for _, s := range secs {
		y = c.pbRow(p, "pb:row:"+pb.query+":"+s.id, s.title, y, s.items)
	}
	return p
}

// drawHero is Discover's big card: the poster (or the film's backdrop) filling it, darker
// towards the bottom, with the title and rating.
func (c *console) drawHero(img *image.RGBA, r image.Rectangle, it metaItem) {
	f := apple()
	shadow(img, r, radiusCard, 0.12)
	card := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	pic := it.Background
	if pic == "" {
		pic = it.Poster
	}
	if cv := covers.get(pic, r.Dx(), r.Dy()); cv != nil {
		copy(card.Pix, cv.Pix)
	} else {
		ui.Fill(card, card.Rect, placeholderColour(it.ID))
	}
	for y := r.Dy() / 2; y < r.Dy(); y++ { // to black at the bottom
		a := float64(y-r.Dy()/2) / float64(r.Dy()/2) * 0.9
		blendRect(card, image.Rect(0, y, r.Dx(), y+1), rgb(0x000000), a)
	}
	apText(card, f.largeTitle, 40, r.Dy()-150, rgb(0xffffff), clip(f.largeTitle, it.Name, r.Dx()-80))
	var meta []string
	if it.IMDbRating != "" {
		meta = append(meta, "★ "+it.IMDbRating)
	}
	meta = append(meta, strings.Title(it.Type))
	if it.ReleaseInfo != "" {
		meta = append(meta, it.ReleaseInfo)
	}
	apText(card, f.callout, 40, r.Dy()-96, rgb(0xe5e5ea), strings.Join(meta, "   "))
	apText(card, f.headline, 40, r.Dy()-40, rgb(0xffffff), "▶  View details")
	// The card's rounded corners: what's outside them is the page's colour.
	rr := 26
	for y := 0; y < rr; y++ {
		for x := 0; x < rr; x++ {
			dx, dy := rr-x, rr-y
			if dx*dx+dy*dy > rr*rr {
				for _, pt := range [][2]int{{x, y}, {r.Dx() - 1 - x, y}, {x, r.Dy() - 1 - y}, {r.Dx() - 1 - x, r.Dy() - 1 - y}} {
					card.SetRGBA(pt[0], pt[1], apBG)
				}
			}
		}
	}
	for y := 0; y < r.Dy(); y++ {
		copy(img.Pix[img.PixOffset(r.Min.X, r.Min.Y+y):][:4*r.Dx()], card.Pix[y*card.Stride:][:4*r.Dx()])
	}
}

// --- a film's or series' page ----------------------------------------------------------------

func (c *console) pbDetailPage() *page {
	f := apple()
	pb := &c.pb
	d := pb.sel
	it := d.info()
	w, mx := c.s.W, pageGutter
	h := 2400
	var eps []metaVideo
	if it.Type == "series" {
		eps = episodesOf(it.Videos, d.season)
		h += min(len(eps), d.epLimit)*170 + 400
	} else {
		h += c.pbSourcesHeight(d)
	}
	img := canvas(w, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	c.booksTabs(p, "pbt:"+d.from)
	y := c.pbHeader(p, d, it)

	if it.Type == "series" {
		y += 20
		if d.metaErr != "" {
			drawParagraphs(img, f.callout, d.metaErr, mx, y, w-2*mx, 44, y+200, apRed)
			y += 120
		}
		if len(it.Videos) == 0 && d.meta == nil && d.metaErr == "" {
			apText(img, f.body, mx, y+50, apSecondary, "Loading the episodes…")
			y += 100
		} else if len(it.Videos) > 0 {
			apText(img, f.title, mx, y+50, apLabel, "Episodes")
			y += 80
			var ids, labels []string
			for _, s := range seasonsOf(it.Videos) {
				ids = append(ids, "pb:season:"+strconv.Itoa(s))
				if s == 0 {
					labels = append(labels, "Specials")
				} else {
					labels = append(labels, "Season "+strconv.Itoa(s))
				}
			}
			c.capsuleRow(p, "pb:seasons", y, ids, labels, "pb:season:"+strconv.Itoa(d.season))
			y += 100
			last, _ := pbLib.recentEntry(it.ID)
			for i, ep := range eps {
				if i >= d.epLimit {
					r := image.Rect(mx, y, w-mx, y+96)
					ui.RoundRect(img, r, radiusCard, apCard2)
					apTextCenter(img, f.headline, w/2, y+48, apBlue, fmt.Sprintf("Show %d more", len(eps)-d.epLimit))
					p.buttons = append(p.buttons, button{"pb:epmore", r})
					y += 120
					break
				}
				y = c.pbEpisodeRow(p, ep, y, last.Episode == ep.ID)
			}
		}
	} else {
		y = c.pbSourcesBlock(p, d, it, y)
	}
	end := y + 80
	if end < img.Rect.Dy() && img.Rect.Dy() > c.viewH() {
		p.img = img.SubImage(image.Rect(0, 0, w, max(end, c.viewH()))).(*image.RGBA)
	}
	return p
}

// pbHeader draws the top of a film's page: back, the backdrop, the poster, the title and
// what's known, and Save. Returns the y under it.
func (c *console) pbHeader(p *page, d *pbDetail, it metaItem) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	top := marginTabsH
	if it.Background != "" { // the film's backdrop across the top, fading into the page
		br := image.Rect(0, top, w, top+620)
		if cv := covers.get(it.Background, br.Dx(), br.Dy()); cv != nil {
			for y := 0; y < br.Dy(); y++ {
				copy(img.Pix[img.PixOffset(0, br.Min.Y+y):][:4*w], cv.Pix[y*cv.Stride:][:4*w])
			}
			blendRect(img, br, apBG, 0.35)
			for y := br.Dy() / 3; y < br.Dy(); y++ {
				a := float64(y-br.Dy()/3) / float64(br.Dy()*2/3)
				blendRect(img, image.Rect(0, br.Min.Y+y, w, br.Min.Y+y+1), apBG, a)
			}
		}
	}
	back := map[string]string{"discover": "Discover", "library": "Library", "addons": "Add-ons", "settings": "Settings"}[d.from]
	if back == "" {
		back = "Discover"
	}
	iconBack(img, mx, top+50, apBlue)
	apText(img, f.body, mx+30, top+62, apBlue, back)
	p.buttons = append(p.buttons, button{"pb:back", image.Rect(0, top, 380, top+110)})
	saved := pbLib.contains(it.ID)
	br := image.Rect(w-mx-90, top+10, w-mx, top+100)
	iconBookmark(img, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2, apBlue, saved)
	p.buttons = append(p.buttons, button{"pb:save", br})

	pr := image.Rect(mx, top+130, mx+340, top+130+510)
	c.drawPoster(img, pr, it)
	x, tw := pr.Max.X+44, w-mx-(pr.Max.X+44)
	y := pr.Min.Y + 10
	for i, l := range wrapText(f.title, it.Name, tw) {
		if i == 3 {
			break
		}
		y += 54
		apText(img, f.title, x, y, apLabel, l)
	}
	if it.IMDbRating != "" {
		y += 52
		apText(img, f.headline, x, y, rgb(0xffd60a), "★ "+it.IMDbRating+"/10 IMDb")
	}
	var meta []string
	for _, s := range []string{it.ReleaseInfo, it.Runtime} {
		if s != "" {
			meta = append(meta, s)
		}
	}
	if len(it.Genres) > 0 {
		meta = append(meta, strings.Join(it.Genres[:min(len(it.Genres), 3)], ", "))
	}
	if len(meta) > 0 {
		y += 48
		for i, l := range wrapText(f.callout, strings.Join(meta, " · "), tw) {
			if i == 2 {
				break
			}
			if i > 0 {
				y += 42
			}
			apText(img, f.callout, x, y, apSecondary, l)
		}
	}
	y += 40
	// Save, as a capsule too (the bookmark at the top does the same).
	label := "＋ Library"
	if saved {
		label = "✓ In Library"
	}
	sr := image.Rect(x, y, x+min(tw, 320), y+80)
	ui.RoundRect(img, sr, 40, apCard2)
	apTextCenter(img, f.headline, (sr.Min.X+sr.Max.X)/2, (sr.Min.Y+sr.Max.Y)/2, apBlue, label)
	p.buttons = append(p.buttons, button{"pb:save", sr})
	y = max(y+80, pr.Max.Y) + 40
	if it.Description != "" {
		apText(img, f.title, mx, y+44, apLabel, "About")
		y = drawParagraphs(img, f.callout, it.Description, mx, y+70, w-2*mx, 44, y+70+44*9, apLabel) + 10
	} else if d.meta == nil && d.metaErr == "" {
		apText(img, f.callout, mx, y+40, apSecondary, "Loading…")
		y += 70
	}
	return y
}

func (c *console) pbEpisodeRow(p *page, ep metaVideo, y int, last bool) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	r := image.Rect(mx, y, w-mx, y+150)
	ui.RoundRect(img, r, radiusCard, apCard)
	tr := image.Rect(r.Min.X+16, r.Min.Y+15, r.Min.X+16+213, r.Min.Y+135)
	if cv := covers.get(ep.Thumbnail, tr.Dx(), tr.Dy()); cv != nil {
		for k := 0; k < tr.Dy(); k++ {
			copy(img.Pix[img.PixOffset(tr.Min.X, tr.Min.Y+k):][:4*tr.Dx()], cv.Pix[k*cv.Stride:][:4*tr.Dx()])
		}
	} else {
		ui.RoundRect(img, tr, 12, apCard2)
		iconPlayTriangle(img, (tr.Min.X+tr.Max.X)/2, (tr.Min.Y+tr.Max.Y)/2, 18, apSecondary)
	}
	x, tw := tr.Max.X+28, r.Max.X-tr.Max.X-28-70
	apText(img, f.headline, x, r.Min.Y+52, apLabel, clip(f.headline, ep.label(), tw))
	var meta []string
	if ep.Runtime != "" {
		meta = append(meta, ep.Runtime)
	}
	if len(ep.Released) >= 10 {
		meta = append(meta, ep.Released[:10])
	}
	if last {
		meta = append(meta, "Watched last")
	}
	apText(img, f.caption, x, r.Min.Y+92, apSecondary, clip(f.caption, strings.Join(meta, " · "), tw))
	if ep.Overview != "" {
		apText(img, f.caption, x, r.Min.Y+128, apSecondary, clip(f.caption, ep.Overview, tw))
	}
	iconChevronRight(img, r.Max.X-44, (r.Min.Y+r.Max.Y)/2, apSecondary)
	p.buttons = append(p.buttons, button{"pb:ep:" + ep.ID, r})
	return y + 166
}

// pbEpisodePage is an episode's page: what it is, Quick Play, subtitles and sources.
func (c *console) pbEpisodePage() *page {
	f := apple()
	d := c.pb.sel
	it := d.info()
	var ep metaVideo
	for _, v := range it.Videos {
		if v.ID == d.episode {
			ep = v
		}
	}
	w, mx := c.s.W, pageGutter
	h := 1700 + c.pbSourcesHeight(d)
	img := canvas(w, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	c.booksTabs(p, "pbt:"+d.from)
	top := marginTabsH
	iconBack(img, mx, top+50, apBlue)
	apText(img, f.body, mx+30, top+62, apBlue, clip(f.body, it.Name, 700))
	p.buttons = append(p.buttons, button{"pb:epback", image.Rect(0, top, 760, top+110)})
	tr := image.Rect(mx, top+130, mx+560, top+130+315)
	shadowRect(img, tr)
	if cv := covers.get(ep.Thumbnail, tr.Dx(), tr.Dy()); cv != nil {
		for k := 0; k < tr.Dy(); k++ {
			copy(img.Pix[img.PixOffset(tr.Min.X, tr.Min.Y+k):][:4*tr.Dx()], cv.Pix[k*cv.Stride:][:4*tr.Dx()])
		}
	} else {
		c.drawCover(img, tr, posterItem(it))
	}
	x, tw := tr.Max.X+40, w-mx-(tr.Max.X+40)
	y := tr.Min.Y + 40
	season := fmt.Sprintf("Season %d · Episode %d", ep.Season, ep.episodeNo())
	if ep.Season == 0 {
		season = fmt.Sprintf("Special %d", ep.episodeNo())
	}
	apText(img, f.callout, x, y, apSecondary, season)
	title := ep.Title
	if title == "" {
		title = ep.Name
	}
	for i, l := range wrapText(f.title, title, tw) {
		if i == 3 {
			break
		}
		y += 54
		apText(img, f.title, x, y, apLabel, l)
	}
	var meta []string
	if ep.Runtime != "" {
		meta = append(meta, ep.Runtime)
	}
	if len(ep.Released) >= 10 {
		meta = append(meta, ep.Released[:10])
	}
	if len(meta) > 0 {
		y += 46
		apText(img, f.callout, x, y, apSecondary, strings.Join(meta, " · "))
	}
	y = tr.Max.Y + 40
	if ep.Overview != "" {
		y = drawParagraphs(img, f.callout, ep.Overview, mx, y, w-2*mx, 44, y+44*6, apLabel) + 10
	}
	y = c.pbSourcesBlock(p, d, it, y)
	end := y + 80
	if end < img.Rect.Dy() && img.Rect.Dy() > c.viewH() {
		p.img = img.SubImage(image.Rect(0, 0, w, max(end, c.viewH()))).(*image.RGBA)
	}
	return p
}

// pbSourcesHeight is about how tall the sources block will be.
func (c *console) pbSourcesHeight(d *pbDetail) int {
	direct, torrents, unsupp := c.pbSplit(d)
	n := len(direct) + min(len(torrents), d.limit)
	h := 900 + n*230
	if d.unsupp {
		h += len(unsupp) * 100
	}
	return h
}

// pbSplit sorts and filters the sources into direct, torrent and unavailable ones.
func (c *console) pbSplit(d *pbDetail) (direct, torrents, unsupp []*addonStream) {
	for _, s := range d.streams {
		switch {
		case s.playableURL() != "":
			direct = append(direct, s)
		case s.isTorrent():
			if d.quality == "" || s.quality() == d.quality {
				torrents = append(torrents, s)
			}
		default:
			unsupp = append(unsupp, s)
		}
	}
	sortTorrents(torrents, d.sort)
	return
}

// pbSourcesBlock: Quick Play, subtitles, the sources. Returns the y under it.
func (c *console) pbSourcesBlock(p *page, d *pbDetail, it metaItem, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	id := it.ID
	if d.episode != "" {
		id = d.episode
	}
	direct, torrents, unsupp := c.pbSplit(d)
	y += 20
	// Quick Play: a direct stream if there's one, else the best torrent for this tablet.
	if pick := quickPick(d.streams); pick != nil {
		r := image.Rect(mx, y, w-mx, y+130)
		ui.RoundRect(img, r, 34, apBlue)
		iconPlayTriangle(img, r.Min.X+70, (r.Min.Y+r.Max.Y)/2, 26, apOnBlue)
		title := "Quick Play"
		if pos := pbLib.resume(it.ID, d.episode); pos > 30 {
			title = "Resume from " + clockText(pos)
		}
		apText(img, f.headline, r.Min.X+124, r.Min.Y+56, apOnBlue, title)
		sub := "Direct stream · " + pick.quality()
		if pick.playableURL() == "" {
			sub = "Torrent · " + pick.quality()
			if n := pick.seeders(); n >= 0 {
				sub += " · " + groupDigits(n) + " seeders"
			} else {
				sub += " · Seeders unknown"
			}
		}
		apText(img, f.callout, r.Min.X+124, r.Min.Y+100, blend(apOnBlue, apBlue, 0.25), clip(f.callout, sub, r.Dx()-160))
		p.buttons = append(p.buttons, button{"pb:quick", r})
		y += 160
	}
	// Subtitles, by language.
	if langs := subLangs(d.subs); len(langs) > 0 {
		apText(img, f.headline, mx, y+40, apLabel, "Subtitles")
		y += 66
		ids, labels := []string{"pb:sub:"}, []string{"Off"}
		for _, l := range langs {
			ids = append(ids, "pb:sub:"+l)
			labels = append(labels, fmt.Sprintf("%s (%d)", langName(l), len(subsFor(d.subs, l))))
		}
		c.capsuleRow(p, "pb:subs:"+id, y, ids, labels, "pb:sub:"+d.sub)
		y += 96
	}
	apText(img, f.title, mx, y+50, apLabel, map[bool]string{true: "Play", false: "Sources"}[len(d.streams) > 0])
	y += 74
	switch {
	case d.loading > 0 && len(d.streams) == 0:
		apText(img, f.body, mx, y+40, apSecondary, "Finding sources…")
		return y + 80
	case len(d.streams) == 0 && d.loaded:
		apText(img, f.headline, mx, y+40, apLabel, "No sources")
		msg := "No installed add-on returned a source for this title."
		if !addons.isInstalled(featuredAddons[1].manifest) {
			msg += " Install Torrentio in Add-ons for torrent sources."
		}
		return drawParagraphs(img, f.callout, msg, mx, y+60, w-2*mx, 44, y+300, apSecondary) + 20
	case len(direct) == 0 && len(torrents) == 0 && d.quality == "" && d.loaded:
		apText(img, f.headline, mx, y+40, apLabel, "No playable source available")
		y = drawParagraphs(img, f.callout, fmt.Sprintf("%d source(s) were found, but they are not directly playable.", len(unsupp)), mx, y+60, w-2*mx, 44, y+300, apSecondary) + 20
	}
	if d.loading > 0 {
		apText(img, f.caption, mx, y+20, apSecondary, fmt.Sprintf("Still asking %d add-on(s)…", d.loading))
		y += 40
	}
	nTorrents := 0
	for _, s := range d.streams {
		if s.isTorrent() {
			nTorrents++
		}
	}
	if nTorrents > 0 {
		var ids, labels []string
		for _, s := range sourceSorts {
			ids, labels = append(ids, "pb:sort:"+s.id), append(labels, s.label)
		}
		c.capsuleRow(p, "pb:sorts", y, ids, labels, "pb:sort:"+d.sort)
		y += 90
		ids, labels = []string{"pb:q:"}, []string{"All qualities"}
		for _, q := range []string{"4K", "1080p", "720p", "480p", "Auto"} {
			ids, labels = append(ids, "pb:q:"+q), append(labels, q)
		}
		c.capsuleRow(p, "pb:quals", y, ids, labels, "pb:q:"+d.quality)
		y += 100
	}
	for _, s := range direct {
		y = c.pbSourceRow(p, d, s, y)
	}
	for i, s := range torrents {
		if i >= d.limit {
			r := image.Rect(mx, y, w-mx, y+96)
			ui.RoundRect(img, r, radiusCard, apCard2)
			apTextCenter(img, f.headline, w/2, y+48, apBlue, fmt.Sprintf("Show %d more", len(torrents)-d.limit))
			p.buttons = append(p.buttons, button{"pb:more", r})
			y += 120
			break
		}
		y = c.pbSourceRow(p, d, s, y)
	}
	if nTorrents > 0 && len(torrents) == 0 {
		apText(img, f.callout, mx, y+40, apSecondary, "No torrent in "+d.quality+".")
		y += 80
	}
	if len(unsupp) > 0 {
		r := image.Rect(mx, y, w-mx, y+90)
		ui.RoundRect(img, r, radiusCard, apCard)
		chev := "▸"
		if d.unsupp {
			chev = "▾"
		}
		apText(img, f.callout, r.Min.X+30, r.Min.Y+56, apSecondary, fmt.Sprintf("%s  Unavailable sources (%d)", chev, len(unsupp)))
		p.buttons = append(p.buttons, button{"pb:unsupp", r})
		y += 106
		if d.unsupp {
			for _, s := range unsupp {
				t := s.title()
				if t == "" {
					t = s.Name
				}
				apText(img, f.caption, mx+30, y+30, apLabel, clip(f.caption, strings.ReplaceAll(t, "\n", " "), w-2*mx-60))
				apText(img, f.caption, mx+30, y+66, apSecondary, s.status())
				y += 96
			}
		}
	}
	return y
}

// pbSourceRow draws one source with Play and Download (or Stop, or Saved). Returns the y
// under it.
func (c *console) pbSourceRow(p *page, d *pbDetail, s *addonStream, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	key := s.key()
	idx := c.pbStreamIndex(d, s)
	lines := strings.Split(strings.TrimSpace(s.title()), "\n")
	name := strings.TrimSpace(lines[0])
	if name == "" {
		name = strings.ReplaceAll(s.Name, "\n", " ")
	}
	j := engine.jobFor(key)
	dl := c.pb.httpDL[key]
	status, statusCol := "", apBlue
	switch {
	case j != nil:
		st := engine.status(j)
		status = torrentLine(st, j.download)
		if st.State == "failed" {
			statusCol = apRed
		}
		if st.State == "saved" {
			statusCol = rgb(0x34c759)
		}
	case dl != nil:
		status, statusCol = dl.line()
	case d.errs[key] != "":
		status, statusCol = d.errs[key], apRed
	}
	hgt := 200
	if status != "" {
		hgt += 40
	}
	r := image.Rect(mx, y, w-mx, y+hgt)
	ui.RoundRect(img, r, radiusCard, apCard)
	tw := r.Dx() - 260
	x := r.Min.X + 30
	apText(img, f.headline, x, r.Min.Y+50, apLabel, s.quality())
	if s.Addon != "" {
		apText(img, f.caption, x+ui.TextWidth(f.headline, s.quality())+18, r.Min.Y+50, apSecondary, clip(f.caption, s.Addon, tw-200))
	}
	apText(img, f.caption, x, r.Min.Y+88, apLabel, clip(f.caption, name, tw))
	apText(img, f.caption, x, r.Min.Y+124, apSecondary, clip(f.caption, s.summary(), tw))
	info := ""
	if s.isTorrent() {
		if n := s.seeders(); n >= 0 {
			info = groupDigits(n) + " seeders"
		} else {
			info = "Seeders unknown"
		}
		if s.heavy() {
			info += " · heavy for this tablet"
		}
	} else {
		info = "Direct stream"
	}
	apText(img, f.caption, x, r.Min.Y+160, apSecondary, clip(f.caption, info, tw))
	if status != "" {
		apText(img, f.caption, x, r.Min.Y+200, statusCol, clip(f.caption, status, r.Dx()-60))
	}
	// Play, and Download (Stop while it runs, a tick once saved).
	cy := r.Min.Y + 100
	pr := image.Rect(r.Max.X-220, cy-50, r.Max.X-120, cy+50)
	iconCircleButton(img, pr, apBlue, "play")
	p.buttons = append(p.buttons, button{"pb:play:" + idx, pr})
	dr := image.Rect(r.Max.X-110, cy-50, r.Max.X-10, cy+50)
	switch {
	case (j != nil && j.download && engine.status(j).State != "saved" && engine.status(j).State != "failed") || (dl != nil && dl.running()):
		iconCircleButton(img, dr, apRed, "stop")
		p.buttons = append(p.buttons, button{"pb:stop:" + idx, dr})
	case (j != nil && engine.status(j).State == "saved") || (dl != nil && dl.done()):
		iconCircleButton(img, dr, rgb(0x34c759), "check")
	default:
		iconCircleButton(img, dr, apCard2, "down")
		p.buttons = append(p.buttons, button{"pb:dl:" + idx, dr})
	}
	return y + hgt + 16
}

// pbStreamIndex is a source's number in d.streams (its buttons carry it).
func (c *console) pbStreamIndex(d *pbDetail, s *addonStream) string {
	for i, x := range d.streams {
		if x == s {
			return strconv.Itoa(i)
		}
	}
	return "-1"
}

func groupDigits(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// clockText is seconds as 1:02:03 or 4:05.
func clockText(sec float64) string {
	t := int(max(sec, 0))
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

// iconPlayTriangle is a filled triangle pointing right.
func iconPlayTriangle(img *image.RGBA, cx, cy, r int, col color.RGBA) {
	for dy := -r; dy <= r; dy++ {
		w := (r - abs(dy)) * 2 * 87 / 100
		ui.Fill(img, image.Rect(cx-r*6/10, cy+dy, cx-r*6/10+w, cy+dy+1), col)
	}
}

// iconCircleButton is a round button with a symbol: play, down, stop, check, pause, back10,
// fwd10.
func iconCircleButton(img *image.RGBA, r image.Rectangle, bg color.RGBA, what string) {
	cx, cy, rad := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, min(r.Dx(), r.Dy())/2-4
	ui.Circle(img, cx, cy, rad, bg)
	fg := rgb(0xffffff)
	if bg == apCard2 {
		fg = apBlue
	}
	switch what {
	case "play":
		iconPlayTriangle(img, cx+4, cy, rad/2, fg)
	case "down":
		line(img, cx, cy-rad/2, cx, cy+rad/3, 6, fg)
		line(img, cx-rad/3, cy, cx, cy+rad/3+2, 6, fg)
		line(img, cx+rad/3, cy, cx, cy+rad/3+2, 6, fg)
		ui.Fill(img, image.Rect(cx-rad/2, cy+rad/2, cx+rad/2, cy+rad/2+5), fg)
	case "stop":
		ui.RoundRect(img, image.Rect(cx-rad/3, cy-rad/3, cx+rad/3, cy+rad/3), 4, fg)
	case "check":
		iconCheck(img, cx-14, cy, fg)
	case "pause":
		ui.RoundRect(img, image.Rect(cx-rad/3, cy-rad/2, cx-rad/3+rad/4, cy+rad/2), 3, fg)
		ui.RoundRect(img, image.Rect(cx+rad/3-rad/4, cy-rad/2, cx+rad/3, cy+rad/2), 3, fg)
	case "trash":
		iconTrash(img, cx, cy, fg)
	}
}

// --- the library -----------------------------------------------------------------------------

func (c *console) pbLibraryPage() *page {
	f := apple()
	pb := &c.pb
	w, mx := c.s.W, pageGutter
	saved, recent, _ := pbLib.lists()
	downloads := pbLib.downloads()
	running := engine.downloadsRunning()
	var httpRunning []*httpDownload
	for _, d := range pb.httpDL {
		if d.running() {
			httpRunning = append(httpRunning, d)
		}
	}
	n := pbSettingsNow().PosterCols
	pw, ph, gap := c.posterSize()
	cellH := ph + 130
	rows := func(k int) int { return (k + n - 1) / n }
	h := 420 + (len(running)+len(httpRunning))*150 + rows(len(recent))*cellH + len(downloads)*150 + rows(len(saved))*cellH + 900
	img := canvas(w, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	c.booksTabs(p, "pbt:library")
	apText(img, f.largeTitle, mx, 238, apLabel, "Library")
	editLabel := "Edit"
	if pb.libEdit {
		editLabel = "Done"
	}
	apTextRight(img, f.body, w-mx, 230, apBlue, editLabel)
	p.buttons = append(p.buttons, button{"pb:libedit", image.Rect(w-mx-200, 160, w, 270)})
	y := 290
	section := func(title string) {
		apText(img, f.title, mx, y+50, apLabel, title)
		y += 80
	}
	grid := func(entries []pbEntry, progress bool) {
		for i, e := range entries {
			col, row := i%n, i/n
			x, yy := mx+col*(pw+gap), y+row*cellH
			r := image.Rect(x, yy, x+pw, yy+ph)
			c.drawPoster(img, r, e.item())
			id := c.pbItemID(e.item())
			ty := r.Max.Y + 38
			if progress {
				bar := image.Rect(x, r.Max.Y+14, x+pw, r.Max.Y+22)
				ui.RoundRect(img, bar, 4, apCard2)
				ui.RoundRect(img, image.Rect(x, bar.Min.Y, x+max(int(float64(pw)*e.Progress), 8), bar.Max.Y), 4, apBlue)
				ty += 18
			}
			apText(img, f.captionBold, x, ty, apLabel, clip(f.captionBold, e.Name, pw))
			sub := strings.Title(e.Type)
			if progress && e.Duration > 0 {
				sub = clockText(e.Duration-e.Position) + " left"
				if e.EpLabel != "" {
					sub = e.EpLabel + " · " + sub
				}
			}
			apText(img, f.caption, x, ty+34, apSecondary, clip(f.caption, sub, pw))
			if pb.libEdit {
				xr := image.Rect(x-14, yy-14, x+52, yy+52)
				ui.Circle(img, x+19, yy+19, 30, apRed)
				ui.Fill(img, image.Rect(x+5, yy+16, x+33, yy+22), rgb(0xffffff))
				what := "saved"
				if progress {
					what = "recent"
				}
				p.buttons = append(p.buttons, button{"pb:rm:" + what + ":" + e.ID, xr})
			} else {
				p.buttons = append(p.buttons, button{id, image.Rect(x, yy, x+pw, ty+40)})
			}
		}
		y += rows(len(entries)) * cellH
	}
	if len(running)+len(httpRunning) > 0 {
		section("Downloading")
		for _, j := range running {
			st := engine.status(j)
			name := j.name
			if name == "" {
				name = st.Name
			}
			frac := 0.0
			if st.Size > 0 {
				frac = float64(st.Done) / float64(st.Size)
			}
			y = c.pbProgressRow(p, name, torrentLine(st, true), frac, "pb:jstop:"+j.hash, y)
		}
		for _, d := range httpRunning {
			line, _ := d.line()
			y = c.pbProgressRow(p, d.name, line, d.frac(), "pb:hstop:"+d.key, y)
		}
		y += 20
	}
	if len(recent) > 0 {
		section("Continue Watching")
		grid(recent, true)
	}
	if len(downloads) > 0 {
		section("Downloaded")
		for i, dl := range downloads {
			r := image.Rect(mx, y, w-mx, y+134)
			ui.RoundRect(img, r, radiusCard, apCard)
			pr := image.Rect(r.Min.X+16, r.Min.Y+12, r.Min.X+16+73, r.Min.Y+122)
			c.drawCover(img, pr, &storeItem{key: dl.Path, title: dl.Name, cover: dl.Poster})
			apText(img, f.headline, pr.Max.X+28, r.Min.Y+56, apLabel, clip(f.headline, dl.Name, r.Dx()-300))
			size := ""
			if fi, err := os.Stat(dl.Path); err == nil {
				size = humanBytes(float64(fi.Size()))
			}
			apText(img, f.caption, pr.Max.X+28, r.Min.Y+96, apSecondary, clip(f.caption, size+" · "+filepath.Base(dl.Path), r.Dx()-300))
			br := image.Rect(r.Max.X-110, r.Min.Y+17, r.Max.X-10, r.Min.Y+117)
			if pb.libEdit {
				iconCircleButton(img, br, apRed, "trash")
				p.buttons = append(p.buttons, button{"pb:rmdl:" + strconv.Itoa(i), br})
			} else {
				iconCircleButton(img, br, apBlue, "play")
				p.buttons = append(p.buttons, button{"pb:playdl:" + strconv.Itoa(i), r})
			}
			y += 150
		}
		y += 20
	}
	section("My Library")
	if len(saved) == 0 {
		apText(img, f.headline, mx, y+30, apLabel, "Your library is empty")
		apText(img, f.callout, mx, y+80, apSecondary, "Save a movie or series from its details page.")
		y += 140
	} else {
		grid(saved, false)
	}
	end := y + 100
	if end < img.Rect.Dy() && img.Rect.Dy() > c.viewH() {
		p.img = img.SubImage(image.Rect(0, 0, w, max(end, c.viewH()))).(*image.RGBA)
	}
	return p
}

func (c *console) pbProgressRow(p *page, name, line string, frac float64, stopID string, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, pageGutter
	r := image.Rect(mx, y, w-mx, y+134)
	ui.RoundRect(img, r, radiusCard, apCard)
	apText(img, f.headline, r.Min.X+30, r.Min.Y+48, apLabel, clip(f.headline, name, r.Dx()-180))
	bar := image.Rect(r.Min.X+30, r.Min.Y+70, r.Max.X-150, r.Min.Y+80)
	ui.RoundRect(img, bar, 5, apCard2)
	ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(int(float64(bar.Dx())*frac), 10), bar.Max.Y), 5, apBlue)
	apText(img, f.caption, r.Min.X+30, r.Min.Y+116, apSecondary, clip(f.caption, line, r.Dx()-180))
	br := image.Rect(r.Max.X-120, r.Min.Y+17, r.Max.X-20, r.Min.Y+117)
	iconCircleButton(img, br, apRed, "stop")
	p.buttons = append(p.buttons, button{stopID, br})
	return y + 150
}

// --- the add-on store -------------------------------------------------------------------------

func (c *console) pbAddonsPage() *page {
	f := apple()
	pb := &c.pb
	w, mx := c.s.W, pageGutter
	list := addons.all()
	h := 1900 + len(list)*130
	img := canvas(w, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	c.booksTabs(p, "pbt:addons")
	apText(img, f.largeTitle, mx, 238, apLabel, "Add-on Store")
	y := 290
	apText(img, f.title, mx, y+50, apLabel, "Featured")
	y += 80
	c.pbField(p, "addonfind", "Search add-ons", pb.addonFind, image.Rect(mx, y, w-mx, y+86), true)
	y += 110
	addons.mu.Lock()
	busy, msg := addons.busy, addons.msg
	addons.mu.Unlock()
	q := strings.ToLower(strings.TrimSpace(pb.addonFind))
	if pb.typing && pb.field == "addonfind" {
		q = strings.ToLower(strings.TrimSpace(pb.editing))
	}
	for _, fa := range featuredAddons {
		if q != "" && !strings.Contains(strings.ToLower(fa.name+" "+fa.summary+" "+fa.category), q) {
			continue
		}
		r := image.Rect(mx, y, w-mx, y+150)
		ui.RoundRect(img, r, radiusCard, apCard)
		ir := image.Rect(r.Min.X+24, r.Min.Y+35, r.Min.X+104, r.Min.Y+115)
		ui.RoundRect(img, ir, 20, map[string]color.RGBA{"Catalogs": rgb(0x5e5ce6), "Subtitles": rgb(0xff9f0a), "Discovery": rgb(0x30d158), "Community": rgb(0x0a84ff)}[fa.category])
		apTextCenter(img, f.title, (ir.Min.X+ir.Max.X)/2, (ir.Min.Y+ir.Max.Y)/2, rgb(0xffffff), fa.name[:1])
		x := ir.Max.X + 26
		apText(img, f.headline, x, r.Min.Y+46, apLabel, fa.name)
		apText(img, f.caption, x, r.Min.Y+84, apSecondary, fa.category)
		apText(img, f.caption, x, r.Min.Y+122, apLabel, clip(f.caption, fa.summary, r.Dx()-440))
		br := image.Rect(r.Max.X-230, r.Min.Y+40, r.Max.X-24, r.Min.Y+110)
		installed := addons.isInstalled(fa.manifest)
		label, bg, fg := "Install", apBlue, apOnBlue
		switch {
		case installed:
			label, bg, fg = "Installed", apCard2, apSecondary
		case busy == addonBase(fa.manifest):
			label, bg, fg = "Installing…", apCard2, apSecondary
		}
		ui.RoundRect(img, br, 35, bg)
		apTextCenter(img, f.headline, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2, fg, label)
		if !installed && busy == "" {
			p.buttons = append(p.buttons, button{"pb:install:" + fa.id, br})
		}
		y += 166
	}
	y += 20
	apText(img, f.title, mx, y+50, apLabel, "Install from URL")
	y += 80
	c.pbField(p, "addonurl", "https://example.com/manifest.json", pb.addonURL, image.Rect(mx, y, w-mx-260, y+86), false)
	ir := image.Rect(w-mx-240, y, w-mx, y+86)
	ui.RoundRect(img, ir, 43, apBlue)
	apTextCenter(img, f.headline, (ir.Min.X+ir.Max.X)/2, (ir.Min.Y+ir.Max.Y)/2, apOnBlue, "Install")
	p.buttons = append(p.buttons, button{"pb:installurl", ir})
	y += 110
	if msg != "" {
		y = drawParagraphs(img, f.callout, msg, mx, y, w-2*mx, 44, y+200, apRed) + 10
	}
	y += 20
	apText(img, f.title, mx, y+50, apLabel, "Installed")
	y += 80
	if len(list) == 0 {
		apText(img, f.callout, mx, y+40, apSecondary, "No add-ons installed yet.")
		y += 80
	}
	for i, a := range list {
		r := image.Rect(mx, y, w-mx, y+114)
		ui.RoundRect(img, r, radiusCard, apCard)
		apText(img, f.headline, r.Min.X+30, r.Min.Y+48, apLabel, clip(f.headline, a.Manifest.Name, r.Dx()-260))
		apText(img, f.caption, r.Min.X+30, r.Min.Y+88, apSecondary, clip(f.caption, a.Base, r.Dx()-260))
		br := image.Rect(r.Max.X-200, r.Min.Y+22, r.Max.X-20, r.Min.Y+92)
		ui.RoundRect(img, br, 35, apCard2)
		apTextCenter(img, f.headline, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2, apRed, "Remove")
		p.buttons = append(p.buttons, button{"pb:uninstall:" + strconv.Itoa(i), br})
		y += 130
	}
	end := y + 100
	if end < img.Rect.Dy() && img.Rect.Dy() > c.viewH() {
		p.img = img.SubImage(image.Rect(0, 0, w, max(end, c.viewH()))).(*image.RGBA)
	}
	return p
}

// --- settings --------------------------------------------------------------------------------

func (c *console) pbSettingsPage() *page {
	f := apple()
	pb := &c.pb
	cfg := pbSettingsNow()
	w, mx := c.s.W, pageGutter
	img := canvas(w, max(3600, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	c.booksTabs(p, "pbt:settings")
	apText(img, f.largeTitle, mx, 238, apLabel, "Settings")
	y := 290
	head := func(t string) {
		apText(img, f.caption, mx+30, y+50, apSecondary, strings.ToUpper(t))
		y += 70
	}
	foot := func(t string) {
		y = drawParagraphs(img, f.caption, t, mx+30, y+14, w-2*mx-60, 36, y+200, apSecondary) + 20
	}
	gw := w - 2*mx
	head("Posters")
	rows := groupRows(img, mx, y, gw, 1, 110)
	apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+68, apLabel, fmt.Sprintf("Posters per row: %d", cfg.PosterCols))
	for k, b := range []struct{ id, label string }{{"pb:cols-", "−"}, {"pb:cols+", "+"}} {
		br := image.Rect(rows[0].Max.X-250+k*120, rows[0].Min.Y+20, rows[0].Max.X-140+k*120, rows[0].Max.Y-20)
		ui.RoundRect(img, br, 35, apCard2)
		apTextCenter(img, f.title, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2-2, apBlue, b.label)
		p.buttons = append(p.buttons, button{b.id, br})
	}
	y = rows[0].Max.Y + 20
	pw, ph, gap := c.posterSize()
	ph = min(ph, 200)
	for k := 0; k < cfg.PosterCols; k++ { // a preview of the row
		x := mx + k*(pw+gap)
		ui.RoundRect(img, image.Rect(x, y, x+pw, y+ph), 10, apCard2)
		iconPlayTriangle(img, x+pw/2, y+ph/2, 14, apSecondary)
	}
	y += ph
	foot("Applies at once to Discover and Library.")

	head("Player")
	rows = groupRows(img, mx, y, gw, 4, 110)
	apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+68, apLabel, "Brightness / volume swipe")
	sw := iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, !cfg.NoGestures, apDark)
	p.buttons = append(p.buttons, button{"pb:gestures", sw.Inset(-14)})
	apText(img, f.body, rows[1].Min.X+30, rows[1].Min.Y+68, apLabel, "Lighter picture (smoother)")
	sw = iosSwitch(img, rows[1].Max.X-24, (rows[1].Min.Y+rows[1].Max.Y)/2, cfg.Light, apDark)
	p.buttons = append(p.buttons, button{"pb:light", sw.Inset(-14)})
	apText(img, f.body, rows[2].Min.X+30, rows[2].Min.Y+68, apLabel, "Turn the picture upside down")
	sw = iosSwitch(img, rows[2].Max.X-24, (rows[2].Min.Y+rows[2].Max.Y)/2, cfg.Flip, apDark)
	p.buttons = append(p.buttons, button{"pb:flip", sw.Inset(-14)})
	apText(img, f.body, rows[3].Min.X+30, rows[3].Min.Y+68, apLabel, fmt.Sprintf("Volume: %d%%", cfg.Volume))
	for k, b := range []struct{ id, label string }{{"pb:vol-", "−"}, {"pb:vol+", "+"}} {
		br := image.Rect(rows[3].Max.X-250+k*120, rows[3].Min.Y+20, rows[3].Max.X-140+k*120, rows[3].Max.Y-20)
		ui.RoundRect(img, br, 35, apCard2)
		apTextCenter(img, f.title, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2-2, apBlue, b.label)
		p.buttons = append(p.buttons, button{b.id, br})
	}
	y = rows[3].Max.Y
	foot("Left side: swipe up or down to change the screen's brightness. Right side: the volume. The volume keys change the volume while a film plays. Turn the tablet on its side to watch; if the picture is upside down, switch Turn the picture.")

	head("Subtitles")
	langs := []string{"", "ara", "fre", "eng", "spa", "ger", "ita", "por", "tur"}
	var ids, labels []string
	for _, l := range langs {
		ids = append(ids, "pb:sublang:"+l)
		if l == "" {
			labels = append(labels, "Off")
		} else {
			labels = append(labels, langName(l))
		}
	}
	c.capsuleRow(p, "pb:sublangs", y, ids, labels, "pb:sublang:"+cfg.SubLang)
	y += 80
	foot("Chosen for each film when an add-on has subtitles in that language. CC in the player changes them.")

	head("Video player")
	rows = groupRows(img, mx, y, gw, 1, 110)
	state := "Fast (ffmpeg 6, for this processor)"
	switch {
	case pb.installing:
		state = "Installing…"
	case !ffmpegInstalled():
		state = "Not installed yet: tap to install"
	case !fastFFmpegReady():
		state = "Slow (Alpine's): tap to get the fast one"
	}
	apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+68, apLabel, "Player")
	apTextRight(img, f.callout, rows[0].Max.X-30, rows[0].Min.Y+66, apSecondary, clip(f.callout, state, 760))
	if !fastFFmpegReady() && !pb.installing {
		p.buttons = append(p.buttons, button{"pb:install-ffmpeg", rows[0]})
	}
	y = rows[0].Max.Y
	note := "Films are decoded on the processor: 720p plays best; 4K and HEVC are marked heavy."
	if fastFailed != "" {
		note += " The fast player couldn't be set up: " + fastFailed
	}
	foot(note)

	head("Sound output")
	cur := currentSoundDev()
	outs := []soundOut{{dev: "", name: "Speaker (the tablet's own chip)"}}
	if cloverviewCard() {
		outs = append(outs, soundOut{dev: soundHeadphones, name: "Headphones (the jack)"})
	}
	calls := 0
	for _, o := range soundOutputs() {
		if o.call {
			calls++
			if !pbShowCallOutputs {
				continue
			}
			o.name += " · for calls"
		}
		outs = append(outs, o)
	}
	nrows := len(outs)
	if calls > 0 {
		nrows++
	}
	rows = groupRows(img, mx, y, gw, nrows, 100)
	for i, o := range outs {
		r := rows[i]
		col := apLabel
		if o.call {
			col = apSecondary
		}
		apText(img, f.body, r.Min.X+30, r.Min.Y+62, col, clip(f.body, o.name, r.Dx()-200))
		if o.dev == cur {
			iconCheck(img, r.Max.X-70, (r.Min.Y+r.Max.Y)/2, apBlue)
		}
		p.buttons = append(p.buttons, button{"pb:snd:" + o.dev, r})
	}
	if calls > 0 {
		r := rows[len(rows)-1]
		label := fmt.Sprintf("Show %d outputs for calls", calls)
		if pbShowCallOutputs {
			label = "Hide the outputs for calls"
		}
		apText(img, f.body, r.Min.X+30, r.Min.Y+62, apBlue, label)
		p.buttons = append(p.buttons, button{"pb:sndcalls", r})
	}
	y = rows[len(rows)-1].Max.Y
	msg := "Tap an output: it's chosen and plays a chime. Pick the one you hear."
	if pbShowCallOutputs {
		msg = "Outputs for calls (voice, Bluetooth, the modem) can freeze the sound until the tablet restarts."
	}
	if pb.soundTest != "" {
		msg = pb.soundTest
	}
	foot(msg)
	if pbLastProblem != "" {
		head("Last playback problem")
		y = drawParagraphs(img, f.callout, pbLastProblem, mx+30, y, w-2*mx-60, 42, y+300, apRed) + 20
	}

	head("Storage")
	rows = groupRows(img, mx, y, gw, 2, 110)
	apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+68, apLabel, "Torrent cache")
	apTextRight(img, f.callout, rows[0].Max.X-30, rows[0].Min.Y+66, apSecondary, humanBytes(float64(cacheSize())))
	apText(img, f.body, rows[1].Min.X+30, rows[1].Min.Y+68, apRed, "Empty the cache")
	p.buttons = append(p.buttons, button{"pb:emptycache", rows[1]})
	y = rows[1].Max.Y
	foot("Films being watched are kept (up to 4 GB) so they resume without downloading again. Downloads are saved in Movies on the tablet's storage.")
	if pb.msg != "" {
		y = drawParagraphs(img, f.callout, pb.msg, mx, y+20, w-2*mx, 44, y+200, apSecondary)
	}
	end := y + 100
	if end < img.Rect.Dy() {
		p.img = img.SubImage(image.Rect(0, 0, w, max(end, c.viewH()))).(*image.RGBA)
	}
	return p
}

func cacheSize() int64 {
	var n int64
	filepath.Walk(torrentCacheDir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += diskUsage(fi)
		}
		return nil
	})
	return n
}

// --- taps ----------------------------------------------------------------------------------

// playboxTap handles the Playbox tab's buttons. Caller holds drawMu.
func (c *console) playboxTap(id string) bool {
	if !strings.HasPrefix(id, "pb:") && !strings.HasPrefix(id, "pbt:") {
		return false
	}
	pb := &c.pb
	if pb.typing && id != "pb:type:"+pb.field {
		c.pbDone(false) // a tap elsewhere: what was typed stays
	}
	d := pb.sel
	switch {
	case strings.HasPrefix(id, "pbt:"):
		v := strings.TrimPrefix(id, "pbt:")
		if v == pb.view && pb.sel == nil && v == "discover" && pb.query != "" {
			pb.query = "" // Discover again: back to the catalogs
		}
		pb.view, pb.sel = v, nil
		c.setScroll(modePlaybox, 0)
	case id == "pb:search":
		pb.view, pb.sel = "discover", nil
		c.setScroll(modePlaybox, 0)
		pb.typing, pb.field, pb.editing, c.fieldBack = true, "search", pb.query, 0
		c.skb.visible = true
	case strings.HasPrefix(id, "pb:type:"):
		f := strings.TrimPrefix(id, "pb:type:")
		if !pb.typing || pb.field != f {
			cur := map[string]string{"search": pb.query, "addonurl": pb.addonURL, "addonfind": pb.addonFind}[f]
			pb.typing, pb.field, pb.editing, c.fieldBack = true, f, cur, 0
		}
		c.skb.visible = true
	case id == "pb:clear":
		pb.query, pb.results = "", nil
		c.setScroll(modePlaybox, 0)
	case id == "pb:retry":
		pb.discErr, pb.discAt = "", time.Time{}
		c.loadDiscover()
	case strings.HasPrefix(id, "pb:item:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "pb:item:"))
		if i >= 0 && i < len(pb.items) {
			it := pb.items[i]
			c.transition("push", image.Rectangle{}, func() {
				c.pbOpen(it)
				c.showPage()
			})
			return true
		}
	case id == "pb:back" && d != nil:
		c.pbDropWarm(d)
		c.transition("pop", image.Rectangle{}, func() {
			pb.sel = nil
			c.showPage()
		})
		return true
	case id == "pb:epback" && d != nil:
		c.pbDropWarm(d)
		c.transition("pop", image.Rectangle{}, func() {
			d.episode, d.streams, d.subs = "", nil, nil
			c.setScroll(modePlaybox, d.seriesY)
			c.showPage()
		})
		return true
	case strings.HasPrefix(id, "pb:ep:") && d != nil:
		ep := strings.TrimPrefix(id, "pb:ep:")
		c.transition("push", image.Rectangle{}, func() {
			d.seriesY = c.scrollY()
			d.episode, d.limit, d.unsupp = ep, 12, false
			d.errs = map[string]string{}
			c.setScroll(modePlaybox, 0)
			c.pbLoadSources(d, ep)
			c.showPage()
		})
		return true
	case id == "pb:epmore" && d != nil:
		d.epLimit += 30
	case strings.HasPrefix(id, "pb:season:") && d != nil:
		d.season, _ = strconv.Atoi(strings.TrimPrefix(id, "pb:season:"))
		d.epLimit = 30
	case id == "pb:save" && d != nil:
		if pbLib.toggle(d.info()) {
			c.toastMsg("Added to your library")
		} else {
			c.toastMsg("Removed from your library")
		}
	case strings.HasPrefix(id, "pb:sub:") && d != nil:
		d.sub = strings.TrimPrefix(id, "pb:sub:")
	case strings.HasPrefix(id, "pb:sort:") && d != nil:
		d.sort = strings.TrimPrefix(id, "pb:sort:")
		cfg := pbSettingsNow()
		cfg.Sort, cfg.SortChosen = d.sort, true
		cfg.save()
	case strings.HasPrefix(id, "pb:q:") && d != nil:
		d.quality, d.limit = strings.TrimPrefix(id, "pb:q:"), 12
	case id == "pb:more" && d != nil:
		d.limit += 12
	case id == "pb:unsupp" && d != nil:
		d.unsupp = !d.unsupp
	case id == "pb:quick" && d != nil:
		if s := quickPick(d.streams); s != nil {
			c.pbPlay(d, s)
			return true
		}
	case strings.HasPrefix(id, "pb:play:") && d != nil:
		if s := c.pbStream(d, strings.TrimPrefix(id, "pb:play:")); s != nil {
			c.pbPlay(d, s)
			return true
		}
	case strings.HasPrefix(id, "pb:dl:") && d != nil:
		if s := c.pbStream(d, strings.TrimPrefix(id, "pb:dl:")); s != nil {
			c.pbDownload(d, s)
		}
	case strings.HasPrefix(id, "pb:stop:") && d != nil:
		if s := c.pbStream(d, strings.TrimPrefix(id, "pb:stop:")); s != nil {
			if j := engine.jobFor(s.key()); j != nil {
				engine.stop(j)
			}
			if dl := pb.httpDL[s.key()]; dl != nil {
				dl.cancel()
			}
		}
	case strings.HasPrefix(id, "pb:jstop:"):
		if j := engine.job(strings.TrimPrefix(id, "pb:jstop:")); j != nil {
			engine.stop(j)
		}
	case strings.HasPrefix(id, "pb:hstop:"):
		if dl := pb.httpDL[strings.TrimPrefix(id, "pb:hstop:")]; dl != nil {
			dl.cancel()
		}
	case id == "pb:libedit":
		pb.libEdit = !pb.libEdit
	case strings.HasPrefix(id, "pb:rm:"):
		what, eid, _ := strings.Cut(strings.TrimPrefix(id, "pb:rm:"), ":")
		if what == "saved" {
			pbLib.removeSaved(eid)
		} else {
			pbLib.removeRecent(eid)
		}
	case strings.HasPrefix(id, "pb:playdl:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "pb:playdl:"))
		if dls := pbLib.downloads(); i >= 0 && i < len(dls) {
			c.pbPlayDownload(dls[i])
			return true
		}
	case strings.HasPrefix(id, "pb:rmdl:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "pb:rmdl:"))
		if dls := pbLib.downloads(); i >= 0 && i < len(dls) {
			pbLib.removeDownload(dls[i].Path)
			c.toastMsg("Deleted from the tablet")
		}
	case strings.HasPrefix(id, "pb:install:"):
		for _, fa := range featuredAddons {
			if fa.id == strings.TrimPrefix(id, "pb:install:") {
				go addons.install(fa.manifest)
			}
		}
	case id == "pb:installurl":
		if u := strings.TrimSpace(pb.addonURL); u != "" {
			go addons.install(u)
		} else {
			pb.typing, pb.field, pb.editing, c.fieldBack = true, "addonurl", "", 0
			c.skb.visible = true
		}
	case strings.HasPrefix(id, "pb:uninstall:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "pb:uninstall:"))
		if list := addons.all(); i >= 0 && i < len(list) {
			addons.remove(list[i].Base)
			pb.discAt = time.Time{}
		}
	case id == "pb:cols-", id == "pb:cols+":
		cfg := pbSettingsNow()
		cfg.PosterCols = min(max(cfg.PosterCols+map[bool]int{true: 1, false: -1}[id == "pb:cols+"], 2), 8)
		cfg.save()
	case id == "pb:vol-", id == "pb:vol+":
		cfg := pbSettingsNow()
		cfg.Volume = min(max(cfg.Volume+map[bool]int{true: 10, false: -10}[id == "pb:vol+"], 0), 150)
		cfg.save()
	case id == "pb:gestures":
		cfg := pbSettingsNow()
		cfg.NoGestures = !cfg.NoGestures
		cfg.save()
	case id == "pb:light":
		cfg := pbSettingsNow()
		cfg.Light = !cfg.Light
		cfg.save()
	case id == "pb:flip":
		cfg := pbSettingsNow()
		cfg.Flip = !cfg.Flip
		cfg.save()
	case strings.HasPrefix(id, "pb:sublang:"):
		cfg := pbSettingsNow()
		cfg.SubLang = strings.TrimPrefix(id, "pb:sublang:")
		cfg.save()
	case id == "pb:install-ffmpeg":
		c.installFFmpeg(nil)
	case strings.HasPrefix(id, "pb:snd:"):
		dev := strings.TrimPrefix(id, "pb:snd:")
		if dev == "" {
			os.Remove(marginHome + "/sounddev")
		} else {
			os.WriteFile(marginHome+"/sounddev", []byte(dev), 0o644)
		}
		pb.soundTest = "Playing a chime…"
		go func() {
			msg := testSound()
			drawMu.Lock()
			defer drawMu.Unlock()
			pb.soundTest = msg
			c.pbRedraw()
		}()
	case id == "pb:sndcalls":
		pbShowCallOutputs = !pbShowCallOutputs
	case id == "pb:soundtest":
		pb.soundTest = "Playing…"
		go func() {
			msg := testSound()
			drawMu.Lock()
			defer drawMu.Unlock()
			pb.soundTest = msg
			c.pbRedraw()
		}()
	case id == "pb:emptycache":
		go func() {
			engine.mu.Lock()
			busy := map[string]bool{}
			for h := range engine.jobs {
				busy[h] = true
			}
			engine.mu.Unlock()
			entries, _ := os.ReadDir(torrentCacheDir)
			for _, e := range entries {
				if e.IsDir() && !busy[e.Name()] {
					os.RemoveAll(filepath.Join(torrentCacheDir, e.Name()))
				}
			}
			drawMu.Lock()
			defer drawMu.Unlock()
			c.toastMsg("Cache emptied")
			c.pbRedraw()
		}()
	}
	c.showPage()
	return true
}

func (c *console) pbStream(d *pbDetail, idx string) *addonStream {
	i, err := strconv.Atoi(idx)
	if err != nil || i < 0 || i >= len(d.streams) {
		return nil
	}
	return d.streams[i]
}

// pbKey takes the keyboard's output while typing in Playbox. Caller holds drawMu.
func (c *console) pbKey(b []byte) {
	pb := &c.pb
	if len(b) == 0 {
		return
	}
	var done bool
	if pb.editing, done = editLine(pb.editing, &c.fieldBack, b, 300); done {
		c.pbDone(true)
		c.showPage()
		return
	}
	c.fieldTyped()
	if pb.field == "search" { // search as you type, after a pause (Playbox waits 350 ms)
		q := pb.editing
		time.AfterFunc(600*time.Millisecond, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if pb.typing && pb.field == "search" && pb.editing == q && len([]rune(strings.TrimSpace(q))) >= 2 && q != pb.query {
				c.pbSearch(q)
				c.drawTypeBar()
			}
		})
	}
}

// pbDone finishes typing: a search runs, an add-on installs. Caller holds drawMu.
func (c *console) pbDone(enter bool) {
	pb := &c.pb
	text := strings.TrimSpace(pb.editing)
	pb.typing, pb.editing = false, ""
	c.skb.visible = false
	switch pb.field {
	case "search":
		if text != pb.query || enter {
			c.pbSearch(text)
		}
	case "addonurl":
		pb.addonURL = text
		if enter && text != "" {
			go addons.install(text)
		}
	case "addonfind":
		pb.addonFind = text
	}
}

func (l *pbLibrary) removeRecent(id string) {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, e := range l.Recent {
		if e.ID == id {
			l.Recent = append(l.Recent[:i], l.Recent[i+1:]...)
			break
		}
	}
	l.save()
}

// --- playing and downloading ----------------------------------------------------------------

// pbEpisodeInfo is the open episode's id and label ("S1 E3"), or "".
func (d *pbDetail) episodeInfo() (string, string) {
	if d.episode == "" {
		return "", ""
	}
	for _, v := range d.info().Videos {
		if v.ID == d.episode {
			return v.ID, fmt.Sprintf("S%d E%d", v.Season, v.episodeNo())
		}
	}
	return d.episode, ""
}

// pbPlay opens the player on a source: at once, even while a torrent is still finding its
// peers. Caller holds drawMu.
func (c *console) pbPlay(d *pbDetail, s *addonStream) {
	it := d.info()
	ep, epLabel := d.episodeInfo()
	title := it.Name
	if epLabel != "" {
		title += " · " + epLabel
	}
	req := videoReq{title: title, item: it, episode: ep, epLabel: epLabel, start: pbLib.resume(it.ID, ep)}
	req.subs, req.subLang = d.subs, d.sub
	if d.warm != nil && d.warm.key != s.key() {
		engine.stopPlay(d.warm)
	}
	d.warm = nil // the player has it now
	req.alts = alternatives(d.streams, s)
	if u := s.playableURL(); u != "" {
		req.direct = u
	} else {
		j, err := engine.start(s, false, "", it)
		if err != nil {
			d.errs[s.key()] = err.Error()
			c.showPage()
			return
		}
		req.job = j
	}
	c.openVideo(req)
}

// pbPlayDownload plays a film saved on the tablet. Caller holds drawMu.
func (c *console) pbPlayDownload(dl pbDownload) {
	it := metaItem{ID: dl.ID, Type: dl.Type, Name: dl.Name, Poster: dl.Poster}
	if it.ID == "" {
		it.ID = "file:" + dl.Path
	}
	c.openVideo(videoReq{title: dl.Name, item: it, local: dl.Path, start: pbLib.resume(it.ID, "")})
}

// pbDownload saves a source to the tablet: a torrent through the engine, a direct stream
// with a plain download. Caller holds drawMu.
func (c *console) pbDownload(d *pbDetail, s *addonStream) {
	it := d.info()
	_, epLabel := d.episodeInfo()
	name := it.Name
	if epLabel != "" {
		name += " " + strings.ReplaceAll(epLabel, " ", "")
	}
	name += " " + s.quality()
	if u := s.playableURL(); u != "" {
		if c.pb.httpDL == nil {
			c.pb.httpDL = map[string]*httpDownload{}
		}
		c.pb.httpDL[s.key()] = startHTTPDownload(s.key(), u, name, it, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			c.pbRedraw()
		})
		c.toastMsg("Downloading to Movies")
		return
	}
	if _, err := engine.start(s, true, name, it); err != nil {
		d.errs[s.key()] = err.Error()
		return
	}
	c.toastMsg("Downloading to Movies")
	go c.pbWatchProgress(s.key())
}

// pbWatchProgress redraws the page every couple of seconds while a source's job runs, so
// its line shows the progress.
func (c *console) pbWatchProgress(key string) {
	for {
		time.Sleep(2 * time.Second)
		j := engine.jobFor(key)
		drawMu.Lock()
		if c.mode == modePlaybox && !c.sc.drag && !c.pb.typing && c.screenOn && !c.locked {
			c.showPage()
		}
		drawMu.Unlock()
		if j == nil {
			return
		}
		if st := engine.status(j).State; st == "saved" || st == "failed" || st == "stopped" {
			return
		}
	}
}

// pbPrewarm starts the best torrent of a page in the background as soon as it's known: its
// details and first pieces arrive while the page is read, so Play starts at once. Caller
// holds drawMu.
func (c *console) pbPrewarm(d *pbDetail) {
	if d.warm != nil || d.warming || (d.item.Type == "series" && d.episode == "") {
		return
	}
	s := quickPick(d.streams)
	if s == nil || s.playableURL() != "" || s.seeders() == 0 {
		return
	}
	d.warming = true
	it := d.info()
	// In the background: the torrent client can take a while to start, and the screen must
	// not wait for it (holding drawMu here froze the page before its sources showed).
	go func() {
		j, err := engine.start(s, false, "", it)
		drawMu.Lock()
		defer drawMu.Unlock()
		d.warming = false
		if err != nil {
			return
		}
		if c.pb.sel != d || d.warm != nil {
			engine.stopPlay(j)
			return
		}
		d.warm = j
	}()
}

// pbDropWarm stops a page's background torrent when the page is left without playing.
func (c *console) pbDropWarm(d *pbDetail) {
	if d.warm != nil {
		engine.stopPlay(d.warm)
		d.warm = nil
	}
}
