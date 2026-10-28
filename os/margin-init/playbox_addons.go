package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Playbox: the user's iPhone app (aminoulogie/playbox) on the tablet. A client for
// Stremio-style add-ons: an add-on is a web address answering manifest.json, catalog/,
// meta/, stream/ and subtitles/ requests in JSON. Cinemeta gives the catalogs and the pages
// of films and series, Torrentio the sources (most of them torrents), OpenSubtitles the
// subtitles. This file is AddonModels.swift, AddonClient.swift and AddonStore.swift.

var playboxDir = marginHome + "/playbox"

type addonManifest struct {
	ID          string          `json:"id"`
	Version     string          `json:"version,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Resources   []addonResource `json:"resources,omitempty"`
	Types       []string        `json:"types,omitempty"`
	IDPrefixes  []string        `json:"idPrefixes,omitempty"`
	Catalogs    []addonCatalog  `json:"catalogs,omitempty"`
}

// addonResource: the protocol allows a bare name ("stream") or {name, types, idPrefixes}.
type addonResource struct {
	Name       string   `json:"name"`
	Types      []string `json:"types,omitempty"`
	IDPrefixes []string `json:"idPrefixes,omitempty"`
}

func (r *addonResource) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		r.Name = s
		return nil
	}
	type plain addonResource
	return json.Unmarshal(b, (*plain)(r))
}

type addonCatalog struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Extra []struct {
		Name       string `json:"name"`
		IsRequired bool   `json:"isRequired,omitempty"`
	} `json:"extra,omitempty"`
	ExtraSupported []string `json:"extraSupported,omitempty"` // legacy manifests
	ExtraRequired  []string `json:"extraRequired,omitempty"`
}

func (c addonCatalog) supportsSearch() bool {
	for _, e := range c.Extra {
		if e.Name == "search" {
			return true
		}
	}
	for _, e := range c.ExtraSupported {
		if e == "search" {
			return true
		}
	}
	return false
}

// needsExtra: the catalog can't be listed without something (a search, a genre).
func (c addonCatalog) needsExtra() bool {
	for _, e := range c.Extra {
		if e.IsRequired {
			return true
		}
	}
	return len(c.ExtraRequired) > 0
}

// has: the add-on answers this resource (for this type and id, when it says).
func (m addonManifest) has(resource, typ, id string) bool {
	if len(m.Resources) == 0 {
		return resource == "catalog" || resource == "meta"
	}
	for _, r := range m.Resources {
		if r.Name != resource {
			continue
		}
		types, prefixes := r.Types, r.IDPrefixes
		if len(types) == 0 { // a bare name: the manifest's own types and prefixes
			types, prefixes = m.Types, m.IDPrefixes
		}
		if typ != "" && len(types) > 0 && !containsStr(types, typ) {
			continue
		}
		if id != "" && len(prefixes) > 0 {
			ok := false
			for _, p := range prefixes {
				ok = ok || strings.HasPrefix(id, p)
			}
			if !ok {
				continue
			}
		}
		return true
	}
	return false
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// metaItem is a film or series: MetaPreview in Playbox.
type metaItem struct {
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	Poster      string      `json:"poster,omitempty"`
	Description string      `json:"description,omitempty"`
	IMDbRating  string      `json:"imdbRating,omitempty"`
	ReleaseInfo string      `json:"releaseInfo,omitempty"`
	Genres      []string    `json:"genres,omitempty"`
	Runtime     string      `json:"runtime,omitempty"`
	Background  string      `json:"background,omitempty"`
	Videos      []metaVideo `json:"videos,omitempty"`
}

type metaVideo struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Name      string `json:"name,omitempty"`
	Season    int    `json:"season,omitempty"`
	Episode   int    `json:"episode,omitempty"`
	Number    int    `json:"number,omitempty"`
	Released  string `json:"released,omitempty"`
	Overview  string `json:"overview,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Runtime   string `json:"runtime,omitempty"`
}

func (v metaVideo) episodeNo() int {
	if v.Episode > 0 {
		return v.Episode
	}
	return v.Number
}

func (v metaVideo) label() string {
	t := v.Title
	if t == "" {
		t = v.Name
	}
	if t == "" {
		t = "Episode"
	}
	return fmt.Sprintf("E%d · %s", v.episodeNo(), t)
}

// UnmarshalJSON tolerates add-ons that send numbers where strings belong (imdbRating 7.5).
func (m *metaItem) UnmarshalJSON(b []byte) error {
	type plain metaItem
	var raw struct {
		plain
		IMDbRating  any `json:"imdbRating"`
		ReleaseInfo any `json:"releaseInfo"`
		Runtime     any `json:"runtime"`
		Year        any `json:"year"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*m = metaItem(raw.plain)
	m.IMDbRating, m.ReleaseInfo, m.Runtime = anyString(raw.IMDbRating), anyString(raw.ReleaseInfo), anyString(raw.Runtime)
	if m.ReleaseInfo == "" {
		m.ReleaseInfo = anyString(raw.Year)
	}
	return nil
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

type addonStream struct {
	Name     string   `json:"name,omitempty"`
	Title    string   `json:"title,omitempty"`
	Desc     string   `json:"description,omitempty"`
	URL      string   `json:"url,omitempty"`
	InfoHash string   `json:"infoHash,omitempty"`
	FileIdx  *int     `json:"fileIdx,omitempty"`
	Sources  []string `json:"sources,omitempty"`
	Addon    string   `json:"-"` // which add-on sent it
}

type addonSubtitle struct {
	ID   string `json:"id,omitempty"`
	URL  string `json:"url"`
	Lang string `json:"lang,omitempty"`
}

func (s addonSubtitle) key() string {
	if s.ID != "" {
		return s.ID
	}
	return s.URL
}

func (s *addonStream) key() string {
	idx := ""
	if s.FileIdx != nil {
		idx = strconv.Itoa(*s.FileIdx)
	}
	return strings.Join([]string{s.Name, s.title(), s.URL, s.InfoHash, idx, strings.Join(s.Sources, ",")}, "|")
}

// title: newer add-ons send the details in description, older ones in title.
func (s *addonStream) title() string {
	if s.Title != "" {
		return s.Title
	}
	return s.Desc
}

// playableURL is a direct http(s) source, or "".
func (s *addonStream) playableURL() string {
	u := strings.TrimSpace(s.URL)
	if l := strings.ToLower(u); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		return u
	}
	return ""
}

func (s *addonStream) isTorrent() bool {
	return s.InfoHash != "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.URL)), "magnet:")
}

func (s *addonStream) status() string {
	switch {
	case s.playableURL() != "":
		return "Direct stream · Tap to play"
	case s.isTorrent():
		return "Torrent source · Play or download"
	}
	return "Unsupported stream URL"
}

// Trackers added to every magnet: the source's own first, then these (Playbox's lists).
var fallbackTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.qu.ax:6969/announce",
	"udp://tracker2.dler.org:80/announce",
	"udp://tracker.dler.org:6969/announce",
	"https://tracker.pmman.tech:443/announce",
	"https://tracker.nekomi.cn:443/announce",
	"https://tracker.7471.top:443/announce",
	"https://tr.nyacat.pw:443/announce",
	"https://open.ftorrent.com:443/announce",
	"https://tracker.foreverpirates.co:443/announce",
	"https://tracker.bug38.com:443/announce",
	"https://t.213891.xyz:443/announce",
}

// trackers: the source's own trackers ("tracker:udp://..."), then the fallback ones.
func (s *addonStream) trackers() []string {
	var out []string
	seen := map[string]bool{}
	add := func(t string) {
		k := strings.ToLower(strings.TrimSpace(t))
		if k == "" || seen[k] {
			return
		}
		if !strings.HasPrefix(k, "http://") && !strings.HasPrefix(k, "https://") && !strings.HasPrefix(k, "udp://") {
			return
		}
		seen[k] = true
		out = append(out, strings.TrimSpace(t))
	}
	for _, src := range s.Sources {
		if t, ok := strings.CutPrefix(strings.TrimSpace(src), "tracker:"); ok {
			add(t)
		}
	}
	if raw := strings.TrimSpace(s.URL); strings.HasPrefix(strings.ToLower(raw), "magnet:") {
		if u, err := url.Parse(raw); err == nil {
			for _, t := range u.Query()["tr"] {
				add(t)
			}
		}
	}
	for _, t := range fallbackTrackers {
		add(t)
	}
	return out
}

// magnet is the source as a magnet link with every tracker, or "".
func (s *addonStream) magnet() string {
	hash := strings.ToLower(strings.TrimSpace(s.InfoHash))
	name := s.title()
	if name == "" {
		name = s.Name
	}
	if raw := strings.TrimSpace(s.URL); strings.HasPrefix(strings.ToLower(raw), "magnet:") {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		q := u.Query()
		if xt := q.Get("xt"); xt != "" {
			hash = strings.ToLower(strings.TrimPrefix(xt, "urn:btih:"))
		}
		if dn := q.Get("dn"); dn != "" {
			name = dn
		}
	}
	if hash == "" {
		return ""
	}
	if i := strings.IndexByte(name, '\n'); i >= 0 {
		name = name[:i]
	}
	v := url.Values{}
	v.Set("dn", name)
	q := "xt=urn:btih:" + hash + "&" + v.Encode()
	for _, t := range s.trackers() {
		q += "&tr=" + url.QueryEscape(t)
	}
	return "magnet:?" + q
}

func (s *addonStream) text() string { return s.Name + " " + s.title() }

func (s *addonStream) quality() string {
	t := strings.ToLower(s.text())
	switch {
	case strings.Contains(t, "2160p") || strings.Contains(t, "4k"):
		return "4K"
	case strings.Contains(t, "1080p"):
		return "1080p"
	case strings.Contains(t, "720p"):
		return "720p"
	case strings.Contains(t, "480p"):
		return "480p"
	}
	return "Auto"
}

func (s *addonStream) qualityRank() int {
	return map[string]int{"4K": 4, "1080p": 3, "720p": 2, "480p": 1}[s.quality()]
}

func (s *addonStream) codec() string {
	t := strings.ToLower(s.text())
	switch {
	case strings.Contains(t, "av1"):
		return "AV1"
	case strings.Contains(t, "hevc") || strings.Contains(t, "h265") || strings.Contains(t, "x265") || strings.Contains(t, "h.265"):
		return "HEVC"
	case strings.Contains(t, "h264") || strings.Contains(t, "x264") || strings.Contains(t, "h.264") || strings.Contains(t, "avc"):
		return "H.264"
	}
	return ""
}

func (s *addonStream) dynamicRange() string {
	t := " " + strings.ToLower(s.text()) + " "
	switch {
	case strings.Contains(t, "dolby vision") || strings.Contains(t, " dv ") || strings.Contains(t, ".dv."):
		return "Dolby Vision"
	case strings.Contains(t, "hdr10+"):
		return "HDR10+"
	case strings.Contains(t, "hdr"):
		return "HDR"
	}
	return ""
}

var reSize = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(GB|MB)`)

func (s *addonStream) fileSize() string {
	if m := reSize.FindStringSubmatch(s.text()); m != nil {
		return m[1] + " " + strings.ToUpper(m[2])
	}
	return ""
}

// sizeMB is the size the source says, in MB (0 = unknown).
func (s *addonStream) sizeMB() float64 {
	m := reSize.FindStringSubmatch(s.text())
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	if strings.EqualFold(m[2], "GB") {
		v *= 1024
	}
	return v
}

// Add-ons such as Torrentio put the swarm in the title ("👤 123"). An unknown count stays
// unknown (-1); it is never taken as peers actually connected.
var reSeeders = []*regexp.Regexp{
	regexp.MustCompile(`(?:👤|👥|🌱)\s*([0-9][0-9,.]*\s*[kKmM]?)`),
	regexp.MustCompile(`(?i)\b(?:seeders?|seeds?)\s*[:=]?\s*([0-9][0-9,.]*\s*[kKmM]?)\b`),
}

func (s *addonStream) seeders() int {
	for _, t := range []string{s.title(), s.Name} {
		for _, re := range reSeeders {
			m := re.FindStringSubmatch(t)
			if m == nil {
				continue
			}
			v := strings.ToLower(strings.ReplaceAll(m[1], " ", ""))
			mul := 1.0
			switch {
			case strings.HasSuffix(v, "k"):
				mul, v = 1000, strings.TrimSuffix(v, "k")
				v = strings.ReplaceAll(v, ",", "")
			case strings.HasSuffix(v, "m"):
				mul, v = 1e6, strings.TrimSuffix(v, "m")
				v = strings.ReplaceAll(v, ",", "")
			default:
				v = strings.NewReplacer(",", "", ".", "").Replace(v)
			}
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				return int(n*mul + 0.5)
			}
		}
	}
	return -1
}

// summary: "1080p · HEVC · HDR · 2.1 GB".
func (s *addonStream) summary() string {
	var parts []string
	for _, p := range []string{s.quality(), s.codec(), s.dynamicRange(), s.fileSize()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// heavy: too much for the tablet's processor to decode in time (4K, HEVC, AV1).
func (s *addonStream) heavy() bool {
	return s.quality() == "4K" || s.codec() == "HEVC" || s.codec() == "AV1"
}

// sortStreams: direct streams first, then torrents, best quality first (Playbox's order).
func sortStreams(list []*addonStream) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if (a.playableURL() != "") != (b.playableURL() != "") {
			return a.playableURL() != ""
		}
		if a.isTorrent() != b.isTorrent() {
			return !a.isTorrent()
		}
		if a.qualityRank() != b.qualityRank() {
			return a.qualityRank() > b.qualityRank()
		}
		return a.key() < b.key()
	})
}

// Source orders and filters (Playbox's backlog: seeders high to low by default).
var sourceSorts = []struct{ id, label string }{
	{"seeders", "Seeders"}, {"quality", "Quality"}, {"size", "Smallest"}, {"tablet", "Best Here"},
}

// sortTorrents orders torrent sources by how.
func sortTorrents(list []*addonStream, how string) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch how {
		case "quality":
			if a.qualityRank() != b.qualityRank() {
				return a.qualityRank() > b.qualityRank()
			}
		case "size":
			sa, sb := a.sizeMB(), b.sizeMB()
			if (sa == 0) != (sb == 0) {
				return sb == 0
			}
			if sa != sb {
				return sa < sb
			}
		case "tablet": // what plays smoothly here: not 4K or HEVC, 720p before 1080p, then seeders
			if a.heavy() != b.heavy() {
				return !a.heavy()
			}
			ra, rb := tabletRank(a), tabletRank(b)
			if ra != rb {
				return ra > rb
			}
		}
		if a.seeders() != b.seeders() {
			return a.seeders() > b.seeders()
		}
		if a.qualityRank() != b.qualityRank() {
			return a.qualityRank() > b.qualityRank()
		}
		return a.key() < b.key()
	})
}

func tabletRank(s *addonStream) int {
	return map[string]int{"720p": 4, "1080p": 3, "480p": 2, "Auto": 1}[s.quality()]
}

// quickPick is Quick Play's choice: a direct stream if there is one, otherwise the torrent
// with the most seeders that the tablet can decode in time (any torrent if none can).
func quickPick(streams []*addonStream) *addonStream {
	var torrents []*addonStream
	for _, s := range streams {
		if s.playableURL() != "" {
			return s
		}
		if s.isTorrent() {
			torrents = append(torrents, s)
		}
	}
	if len(torrents) == 0 {
		return nil
	}
	sortTorrents(torrents, "seeders")
	for _, s := range torrents {
		if !s.heavy() && s.seeders() != 0 {
			return s
		}
	}
	return torrents[0]
}

// --- the add-on client ---------------------------------------------------------------------

// addonGet fetches base/path and decodes it into v.
func addonGet(ctx context.Context, base, path string, v any) error {
	return getJSON(ctx, strings.TrimRight(base, "/")+"/"+path, v)
}

func fetchManifest(ctx context.Context, base string) (addonManifest, error) {
	var m addonManifest
	err := addonGet(ctx, base, "manifest.json", &m)
	if err == nil && m.ID == "" {
		err = fmt.Errorf("not an add-on (no id in its manifest)")
	}
	return m, err
}

func fetchCatalog(ctx context.Context, base, typ, id string) ([]metaItem, error) {
	var r struct {
		Metas []metaItem `json:"metas"`
	}
	err := addonGet(ctx, base, "catalog/"+url.PathEscape(typ)+"/"+url.PathEscape(id)+".json", &r)
	return r.Metas, err
}

func fetchSearch(ctx context.Context, base, typ, id, query string) ([]metaItem, error) {
	var r struct {
		Metas []metaItem `json:"metas"`
	}
	err := addonGet(ctx, base, "catalog/"+url.PathEscape(typ)+"/"+url.PathEscape(id)+"/search="+url.PathEscape(query)+".json", &r)
	return r.Metas, err
}

func fetchMeta(ctx context.Context, base, typ, id string) (metaItem, error) {
	var r struct {
		Meta metaItem `json:"meta"`
	}
	err := addonGet(ctx, base, "meta/"+url.PathEscape(typ)+"/"+url.PathEscape(id)+".json", &r)
	return r.Meta, err
}

func fetchStreams(ctx context.Context, base, typ, id string) ([]*addonStream, error) {
	var r struct {
		Streams []*addonStream `json:"streams"`
	}
	err := addonGet(ctx, base, "stream/"+url.PathEscape(typ)+"/"+url.PathEscape(id)+".json", &r)
	return r.Streams, err
}

func fetchSubtitles(ctx context.Context, base, typ, id string) ([]addonSubtitle, error) {
	var r struct {
		Subtitles []addonSubtitle `json:"subtitles"`
	}
	err := addonGet(ctx, base, "subtitles/"+url.PathEscape(typ)+"/"+url.PathEscape(id)+".json", &r)
	return r.Subtitles, err
}

// --- installed add-ons ----------------------------------------------------------------------

type installedAddon struct {
	Base     string        `json:"base"`
	Manifest addonManifest `json:"manifest"`
}

type featuredAddon struct{ id, name, summary, category, manifest string }

const cinemetaBase = "https://v3-cinemeta.strem.io"

var featuredAddons = []featuredAddon{
	{"cinemeta", "Cinemeta", "Movie and series catalogs, metadata and ratings", "Catalogs", cinemetaBase + "/manifest.json"},
	{"torrentio", "Torrentio", "Community source index with direct and torrent sources.", "Community", "https://torrentio.strem.fun/manifest.json"},
	{"opensubtitles", "OpenSubtitles v3", "Subtitles in multiple languages", "Subtitles", "https://opensubtitles-v3.strem.io/manifest.json"},
	{"watchhub", "WatchHub", "Find official streaming availability", "Discovery", "https://watchhub.strem.io/manifest.json"},
}

// addonStore keeps the installed add-ons in /data/condor/playbox/addons.json. On the first
// run Cinemeta, Torrentio and OpenSubtitles are put in, so films have sources and subtitles
// at once (Playbox on the iPhone needs Torrentio installed by hand).
type addonStore struct {
	mu       sync.Mutex
	loaded   bool
	list     []installedAddon
	busy     string // the address being installed
	msg      string // the last error
	changed  func()
	loadedAt time.Time
}

var addons = &addonStore{}

func addonBase(input string) string {
	v := strings.TrimSpace(input)
	if strings.HasPrefix(v, "stremio://") {
		v = "https://" + strings.TrimPrefix(v, "stremio://")
	}
	if !strings.Contains(v, "://") && v != "" {
		v = "https://" + v
	}
	v = strings.TrimSuffix(v, "/manifest.json")
	return strings.TrimRight(v, "/")
}

func (as *addonStore) path() string { return filepath.Join(playboxDir, "addons.json") }

// load reads the saved add-ons once; the first time ever, it installs the defaults (in the
// background). Safe to call often.
func (as *addonStore) load() {
	as.mu.Lock()
	if as.loaded {
		as.mu.Unlock()
		return
	}
	as.loaded = true
	b, err := os.ReadFile(as.path())
	if err == nil {
		json.Unmarshal(b, &as.list)
	}
	first := err != nil
	stale := len(as.list) > 0 && as.list[0].Manifest.Name == "" // saved without manifests
	as.mu.Unlock()
	if first || stale {
		go func() {
			for _, f := range featuredAddons[:3] {
				as.install(f.manifest)
			}
		}()
	}
}

func (as *addonStore) save() {
	as.mu.Lock()
	b, _ := json.MarshalIndent(as.list, "", "  ")
	as.mu.Unlock()
	os.MkdirAll(playboxDir, 0o755)
	os.WriteFile(as.path()+".new", b, 0o644)
	os.Rename(as.path()+".new", as.path())
}

func (as *addonStore) all() []installedAddon {
	as.mu.Lock()
	defer as.mu.Unlock()
	return append([]installedAddon(nil), as.list...)
}

func (as *addonStore) isInstalled(manifestURL string) bool {
	b := addonBase(manifestURL)
	for _, a := range as.all() {
		if a.Base == b {
			return true
		}
	}
	return false
}

func (as *addonStore) notify() {
	as.mu.Lock()
	f := as.changed
	as.mu.Unlock()
	if f != nil {
		f()
	}
}

// install fetches an add-on's manifest and keeps it. Returns the error shown to the user.
func (as *addonStore) install(input string) error {
	base := addonBase(input)
	u, err := url.Parse(base)
	if base == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		as.mu.Lock()
		as.msg = "Invalid add-on URL."
		as.mu.Unlock()
		as.notify()
		return fmt.Errorf("invalid add-on URL")
	}
	as.mu.Lock()
	as.busy = base
	as.mu.Unlock()
	as.notify()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	m, err := fetchManifest(ctx, base)
	cancel()
	as.mu.Lock()
	as.busy = ""
	if err != nil {
		as.msg = u.Host + ": " + shortErr(err)
		as.mu.Unlock()
		as.notify()
		return err
	}
	as.msg = ""
	kept := as.list[:0]
	for _, a := range as.list {
		if a.Base != base && a.Manifest.ID != m.ID {
			kept = append(kept, a)
		}
	}
	as.list = append(kept, installedAddon{base, m})
	as.mu.Unlock()
	as.save()
	as.notify()
	return nil
}

func (as *addonStore) remove(base string) {
	as.mu.Lock()
	kept := as.list[:0]
	for _, a := range as.list {
		if a.Base != base {
			kept = append(kept, a)
		}
	}
	as.list = kept
	as.mu.Unlock()
	as.save()
	as.notify()
}

// metaBase is where films' pages come from: Cinemeta, whether or not it's installed (as in
// Playbox), unless another installed add-on serves the id's kind (kitsu:, tt...).
func metaBase(typ, id string) string {
	if strings.HasPrefix(id, "tt") {
		return cinemetaBase
	}
	for _, a := range addons.all() {
		if a.Manifest.has("meta", typ, id) {
			return a.Base
		}
	}
	return cinemetaBase
}

// relevance ranks a title against a search: exact > prefix > whole word > substring > all
// words present.
func relevance(title, query string) int {
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(foldAccents(s)) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		}
		return strings.Join(strings.Fields(b.String()), " ")
	}
	name, needle := norm(title), norm(query)
	switch {
	case needle == "":
		return 0
	case name == needle:
		return 100
	case strings.HasPrefix(name, needle):
		return 80
	case strings.Contains(" "+name+" ", " "+needle+" "):
		return 60
	case strings.Contains(name, needle):
		return 40
	}
	words := strings.Fields(needle)
	n := 0
	for _, w := range words {
		if strings.Contains(name, w) {
			n++
		}
	}
	if n == len(words) {
		return 30
	}
	return n * 10 / max(len(words), 1)
}

var accentFold = strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a", "ä", "a", "á", "a",
	"î", "i", "ï", "i", "í", "i", "ô", "o", "ö", "o", "ó", "o", "ù", "u", "û", "u", "ü", "u", "ú", "u", "ç", "c", "ñ", "n",
	"É", "E", "È", "E", "À", "A", "Ç", "C")

func foldAccents(s string) string { return accentFold.Replace(s) }
