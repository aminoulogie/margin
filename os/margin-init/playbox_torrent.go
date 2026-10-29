package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	g "github.com/anacrolix/generics"
	alog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// Playbox's torrents (TorrentEngine.swift, which uses libtorrent; here anacrolix/torrent, in
// Go): a magnet becomes the torrent's details (from peers found through trackers and the
// DHT), then the film's file is read as it arrives. The player never waits for the whole
// film: it reads http://127.0.0.1:<port>/t/<hash>/<file> from the media server below, and
// each read (and seek) tells the torrent which pieces are wanted next. Download keeps going
// to the end and moves the film to Movies.
//
// The media server also relays direct http(s) sources (/r?u=) and serves films on the
// tablet (/f?p=), so the player (ffmpeg, in Alpine) only ever reads plain http from
// 127.0.0.1: TLS, certificates and proxies stay in Go.

var torrentCacheDir = playboxDir + "/cache"

const (
	torrentCacheLimit = 4 << 30 // films being watched are kept, up to this, for resuming
	metadataTimeout   = 2 * time.Minute
)

type torrentEngine struct {
	mu   sync.Mutex
	cl   *torrent.Client
	jobs map[string]*tjob // by info hash
	addr string           // the media server: 127.0.0.1:port
	srv  *http.Server
}

var engine = &torrentEngine{jobs: map[string]*tjob{}}

// torrentTestConfig, when set (tests), adjusts the client's settings: no DHT or trackers.
var torrentTestConfig func(*torrent.ClientConfig)

// tjob is one torrent being played or downloaded.
type tjob struct {
	key      string // the source's key (its row in the list)
	hash     string
	t        *torrent.Torrent
	file     *torrent.File
	fileIdx  int
	download bool
	name     string   // what a download is saved as
	meta     metaItem // the film, for the library
	started  time.Time
	state    string // "resolving", "ready" (playing), "downloading", "saved", "failed", "stopped"
	err      string
	saved    string // where a download went
	stats    *torrent.TorrentStats
	lastRead int64
	lastAt   time.Time
	speed    float64
}

// tstatus is what the screens show about a job.
type tstatus struct {
	State             string
	Peers, Seeds, All int
	Speed             float64 // bytes a second
	Done, Size        int64   // the film's file
	Name, Err, Saved  string
	Elapsed           time.Duration
}

var setupNetOnce sync.Once

// setupTabletNet makes Go's own name lookups and certificates work in Android's root, where
// there's no /etc/resolv.conf and only 2013's certificates: libraries that use the defaults
// (the torrent's trackers and DHT) then resolve names through Margin's servers and trust
// Alpine's certificates.
func setupTabletNet() {
	setupNetOnce.Do(func() {
		if !onTablet() {
			return
		}
		*net.DefaultResolver = net.Resolver{PreferGo: true, Dial: resolver.Dial}
		if ca := alpineRoot + "/etc/ssl/certs/ca-certificates.crt"; os.Getenv("SSL_CERT_FILE") == "" {
			if _, err := os.Stat(ca); err == nil {
				os.Setenv("SSL_CERT_FILE", ca)
			}
		}
	})
}

// onTablet: running on the Condor (Android's root), not a PC.
func onTablet() bool {
	_, err := os.Stat("/system/bin/linker")
	return err == nil
}

// client starts the BitTorrent client the first time it's needed.
func (e *torrentEngine) client() (*torrent.Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cl != nil {
		return e.cl, nil
	}
	setupTabletNet()
	if err := os.MkdirAll(torrentCacheDir, 0o755); err != nil {
		return nil, err
	}
	mk := func(port int) (*torrent.Client, error) {
		cfg := torrent.NewDefaultClientConfig()
		cfg.DataDir = torrentCacheDir
		cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
			ClientBaseDir: torrentCacheDir,
			// One folder per torrent (its hash), so a film's data can be deleted alone.
			TorrentDirMaker: func(base string, _ *metainfo.Info, ih metainfo.Hash) string {
				return filepath.Join(base, ih.HexString())
			},
			UsePartFiles: g.Some(false), // the film keeps its own name: a download is just moved
		})
		cfg.ListenPort = port
		cfg.DisableIPv6 = true // the tablet's Wi-Fi has IPv4 only
		cfg.DisableWebtorrent = true
		cfg.Seed = false
		cfg.Logger = alog.Default.WithFilterLevel(alog.Error)
		cfg.Slogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		cfg.HTTPUserAgent = "Playbox (Margin)"
		if torrentTestConfig != nil {
			torrentTestConfig(cfg)
		}
		return torrent.NewClient(cfg)
	}
	cl, err := mk(42069)
	if err != nil {
		log.Printf("playbox: torrent port 42069: %v; trying another", err)
		cl, err = mk(0)
	}
	if err != nil {
		return nil, fmt.Errorf("can't start the torrent client: %w", err)
	}
	log.Printf("playbox: torrent client listening on port %d", cl.LocalPort())
	e.cl = cl
	return cl, nil
}

// start begins playing (or downloading) a torrent source. A torrent already running is
// joined (a Play after a Download, or the same film again).
func (e *torrentEngine) start(src *addonStream, download bool, name string, meta metaItem) (*tjob, error) {
	magnet := src.magnet()
	if magnet == "" {
		return nil, errors.New("no magnet link or info hash for this source")
	}
	cl, err := e.client()
	if err != nil {
		return nil, err
	}
	m, err := metainfo.ParseMagnetUri(magnet)
	if err != nil {
		return nil, fmt.Errorf("bad magnet link: %v", err)
	}
	hash := m.InfoHash.HexString()
	e.mu.Lock()
	if j := e.jobs[hash]; j != nil && j.state != "failed" && j.state != "stopped" {
		if download && !j.download {
			j.download, j.name = true, name
			if j.file != nil {
				j.file.Download()
				j.state = "downloading"
			}
		}
		j.key = src.key()
		e.mu.Unlock()
		return j, nil
	}
	e.mu.Unlock()
	go e.cleanCache(hash)
	t, err := cl.AddMagnet(magnet)
	if err != nil {
		return nil, fmt.Errorf("can't add the torrent: %v", err)
	}
	idx := -1
	if src.FileIdx != nil {
		idx = *src.FileIdx
	}
	j := &tjob{key: src.key(), hash: hash, t: t, fileIdx: idx, download: download, name: name, meta: meta,
		started: time.Now(), state: "resolving", stats: new(torrent.TorrentStats)}
	e.mu.Lock()
	e.jobs[hash] = j
	e.mu.Unlock()
	go e.run(j)
	go boostTorrent(t, hash)
	return j, nil
}

// startMagnet plays a magnet link opened from somewhere else (the browser).
func (e *torrentEngine) startMagnet(magnet string) (*tjob, error) {
	return e.start(&addonStream{URL: magnet}, false, "", metaItem{})
}

func (e *torrentEngine) run(j *tjob) {
	select {
	case <-j.t.GotInfo():
	case <-time.After(metadataTimeout):
		e.fail(j, fmt.Sprintf("Couldn't get the torrent's details in 2 minutes: %d peers answered. Try a source with more seeders.", e.status(j).All))
		j.t.Drop()
		return
	case <-j.t.Closed():
		return
	}
	f := pickFile(j.t.Files(), j.fileIdx)
	if f == nil {
		e.fail(j, "This torrent has no video file.")
		j.t.Drop()
		return
	}
	e.mu.Lock()
	j.file = f
	if j.state == "stopped" {
		e.mu.Unlock()
		return
	}
	if j.download {
		j.state = "downloading"
	} else {
		j.state = "ready"
	}
	download := j.download
	e.mu.Unlock()
	touchDir(filepath.Join(torrentCacheDir, j.hash))
	log.Printf("playbox: %s: %s (%d MB), file %d of %d", j.hash, f.DisplayPath(), f.Length()>>20, j.fileIdx, len(j.t.Files()))
	// The start and the end first: players read a film's header, and MP4s keep their index
	// at the end.
	prioritizeEnds(j.t, f)
	if download {
		f.Download()
	}
	// A download goes on to the end, then moves to Movies.
	for {
		select {
		case <-j.t.Closed():
			return
		case <-time.After(time.Second):
		}
		e.mu.Lock()
		dl, stopped := j.download, j.state == "stopped"
		e.mu.Unlock()
		if stopped {
			return
		}
		if dl && f.BytesCompleted() >= f.Length() {
			e.finishDownload(j)
			return
		}
	}
}

// prioritizeEnds asks for the first and last few MB of the file before anything else.
func prioritizeEnds(t *torrent.Torrent, f *torrent.File) {
	info := t.Info()
	if info == nil || info.PieceLength <= 0 {
		return
	}
	pl := info.PieceLength
	head := max(int64(4<<20)/pl, 1)
	tail := max(int64(2<<20)/pl, 1)
	b, e := int64(f.BeginPieceIndex()), int64(f.EndPieceIndex())
	for i := b; i < min(b+head, e); i++ {
		t.Piece(int(i)).SetPriority(torrent.PiecePriorityHigh)
	}
	for i := max(e-tail, b); i < e; i++ {
		t.Piece(int(i)).SetPriority(torrent.PiecePriorityHigh)
	}
}

// pickFile is the file to play: the source's fileIdx when that's a video, otherwise the
// largest video (Playbox's rule), otherwise the largest file.
func pickFile(files []*torrent.File, idx int) *torrent.File {
	if idx >= 0 && idx < len(files) && isVideoName(files[idx].DisplayPath()) {
		return files[idx]
	}
	var best, bestAny *torrent.File
	for _, f := range files {
		if isVideoName(f.DisplayPath()) && !strings.Contains(strings.ToLower(f.DisplayPath()), "sample") {
			if best == nil || f.Length() > best.Length() {
				best = f
			}
		}
		if bestAny == nil || f.Length() > bestAny.Length() {
			bestAny = f
		}
	}
	if best == nil {
		for _, f := range files { // only samples: still better than nothing
			if isVideoName(f.DisplayPath()) && (best == nil || f.Length() > best.Length()) {
				best = f
			}
		}
	}
	if best != nil {
		return best
	}
	if bestAny != nil && isVideoName(bestAny.DisplayPath()) {
		return bestAny
	}
	return nil
}

func (e *torrentEngine) fail(j *tjob, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if j.state != "stopped" {
		j.state, j.err = "failed", msg
	}
	log.Printf("playbox: %s: %s", j.hash, msg)
}

// finishDownload moves a finished film to Movies and puts it in the library.
func (e *torrentEngine) finishDownload(j *tjob) {
	src := e.filePath(j)
	ext := strings.ToLower(filepath.Ext(j.file.DisplayPath()))
	if ext == "" {
		ext = ".mp4"
	}
	name := j.name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(j.file.DisplayPath()), filepath.Ext(j.file.DisplayPath()))
	}
	j.t.Drop()
	os.MkdirAll(moviesDir, 0o755)
	dst := freeFilmName(moviesDir, safeFileName(name), ext)
	err := os.Rename(src, dst)
	if err != nil { // another filesystem (a memory card): copy it
		err = copyFile(src, dst)
		if err == nil {
			os.Remove(src)
		}
	}
	if err != nil {
		e.fail(j, "Downloaded, but couldn't save it: "+shortErr(err))
		return
	}
	os.RemoveAll(filepath.Join(torrentCacheDir, j.hash))
	pbLib.addDownload(pbDownload{Path: dst, Name: name, ID: j.meta.ID, Type: j.meta.Type, Poster: j.meta.Poster, Added: time.Now()})
	e.mu.Lock()
	j.state, j.saved = "saved", dst
	e.mu.Unlock()
	log.Printf("playbox: saved %s", dst)
	if f := pbChanged; f != nil {
		f()
	}
}

// pbChanged is called when a download finishes (the Playbox screen redraws).
var pbChanged func()

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst + ".part")
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst + ".part")
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".part", dst)
}

// filePath is where the job's film is on the tablet, as the file storage lays it out.
func (e *torrentEngine) filePath(j *tjob) string {
	info := j.t.Info()
	fi := j.file.FileInfo()
	parts := []string{torrentCacheDir, j.hash}
	if info.BestName() != metainfo.NoName {
		parts = append(parts, info.BestName())
	}
	return filepath.Join(append(parts, fi.BestPath()...)...)
}

// stop ends a job: playing stops (the data stays a while, to resume); a download is
// cancelled.
func (e *torrentEngine) stop(j *tjob) {
	if j == nil {
		return
	}
	e.mu.Lock()
	if j.state == "saved" || j.state == "stopped" {
		e.mu.Unlock()
		return
	}
	j.state = "stopped"
	if e.jobs[j.hash] == j {
		delete(e.jobs, j.hash)
	}
	e.mu.Unlock()
	j.t.Drop()
}

// stopPlay ends a job that was only played (a download carries on).
func (e *torrentEngine) stopPlay(j *tjob) {
	if j == nil {
		return
	}
	e.mu.Lock()
	dl := j.download
	e.mu.Unlock()
	if !dl {
		e.stop(j)
	}
}

func (e *torrentEngine) job(hash string) *tjob {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.jobs[hash]
}

// jobFor is the running job of a source, if any.
func (e *torrentEngine) jobFor(key string) *tjob {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, j := range e.jobs {
		if j.key == key {
			return j
		}
	}
	return nil
}

// downloadsRunning lists the downloads not yet finished, for the library.
func (e *torrentEngine) downloadsRunning() []*tjob {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*tjob
	for _, j := range e.jobs {
		if j.download && (j.state == "resolving" || j.state == "downloading") {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].started.Before(out[b].started) })
	return out
}

func (e *torrentEngine) status(j *tjob) tstatus {
	if j == nil {
		return tstatus{}
	}
	e.mu.Lock()
	st := tstatus{State: j.state, Err: j.err, Saved: j.saved, Elapsed: time.Since(j.started)}
	f, t := j.file, j.t
	e.mu.Unlock()
	if st.State != "saved" && st.State != "stopped" {
		*j.stats = t.Stats() // j.stats is on the heap: its counters are aligned for 386's atomics
		st.Peers, st.Seeds, st.All = j.stats.ActivePeers, j.stats.ConnectedSeeders, j.stats.TotalPeers
		read := j.stats.BytesReadUsefulData.Int64()
		e.mu.Lock()
		if now := time.Now(); j.lastAt.IsZero() || now.Sub(j.lastAt) >= time.Second {
			if !j.lastAt.IsZero() {
				j.speed = 0.5*j.speed + 0.5*float64(read-j.lastRead)/now.Sub(j.lastAt).Seconds()
			}
			j.lastRead, j.lastAt = read, now
		}
		st.Speed = j.speed
		e.mu.Unlock()
	}
	if f != nil {
		st.Name, st.Size = f.DisplayPath(), f.Length()
		if st.State == "saved" {
			st.Done = st.Size
		} else {
			st.Done = f.BytesCompleted()
		}
	} else if t != nil {
		st.Name = t.Name()
	}
	return st
}

// cleanCache deletes the oldest films' data beyond torrentCacheLimit, keeping the running
// ones (and keep).
func (e *torrentEngine) cleanCache(keep string) {
	entries, err := os.ReadDir(torrentCacheDir)
	if err != nil {
		return
	}
	e.mu.Lock()
	busy := map[string]bool{keep: true}
	for h := range e.jobs {
		busy[h] = true
	}
	e.mu.Unlock()
	type dir struct {
		path string
		size int64
		mod  time.Time
	}
	var dirs []dir
	var total int64
	for _, en := range entries {
		if !en.IsDir() || len(en.Name()) != 40 {
			continue
		}
		p := filepath.Join(torrentCacheDir, en.Name())
		var size int64
		filepath.Walk(p, func(_ string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				size += diskUsage(fi)
			}
			return nil
		})
		info, _ := en.Info()
		d := dir{path: p, size: size}
		if info != nil {
			d.mod = info.ModTime()
		}
		total += size
		if !busy[en.Name()] {
			dirs = append(dirs, d)
		}
	}
	sort.Slice(dirs, func(a, b int) bool { return dirs[a].mod.Before(dirs[b].mod) })
	for _, d := range dirs {
		if total <= torrentCacheLimit && freeSpace(torrentCacheDir) > 2<<30 {
			break
		}
		log.Printf("playbox: cache: removing %s (%d MB)", filepath.Base(d.path), d.size>>20)
		os.RemoveAll(d.path)
		total -= d.size
	}
}

func touchDir(p string) {
	now := time.Now()
	os.Chtimes(p, now, now)
}

// --- the media server --------------------------------------------------------------------

// mediaServer starts (once) the server the player reads from, and returns its address.
func (e *torrentEngine) mediaServer() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.addr != "" {
		return e.addr, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/t/", e.serveTorrent)
	mux.HandleFunc("/r", serveRelay)
	mux.HandleFunc("/f", serveLocal)
	e.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 20 * time.Second}
	go e.srv.Serve(ln)
	e.addr = ln.Addr().String()
	log.Printf("playbox: media server on %s", e.addr)
	return e.addr, nil
}

// streamURL is where the player reads a source: the torrent's file, a relayed web address,
// or a film on the tablet.
func (e *torrentEngine) streamURL(j *tjob, direct, local string) (string, error) {
	addr, err := e.mediaServer()
	if err != nil {
		return "", err
	}
	switch {
	case j != nil:
		e.mu.Lock()
		f := j.file
		e.mu.Unlock()
		if f == nil {
			return "", errors.New("the torrent isn't ready")
		}
		return fmt.Sprintf("http://%s/t/%s/%d", addr, j.hash, fileIndex(j.t, f)), nil
	case local != "":
		return "http://" + addr + "/f?p=" + url.QueryEscape(local), nil
	}
	return "http://" + addr + "/r?u=" + url.QueryEscape(direct), nil
}

func fileIndex(t *torrent.Torrent, f *torrent.File) int {
	for i, x := range t.Files() {
		if x == f {
			return i
		}
	}
	return 0
}

// serveTorrent: GET /t/<hash>/<file index>, with ranges. Reads block until the pieces
// arrive; each read and seek moves the torrent's priorities to where the player is.
func (e *torrentEngine) serveTorrent(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/t/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	e.mu.Lock()
	var t *torrent.Torrent
	if j := e.jobs[parts[0]]; j != nil {
		t = j.t
	}
	cl := e.cl
	e.mu.Unlock()
	if t == nil && cl != nil {
		var h metainfo.Hash
		if h.FromHexString(parts[0]) == nil {
			t, _ = cl.Torrent(h)
		}
	}
	idx, err := strconv.Atoi(parts[1])
	if t == nil || err != nil || t.Info() == nil || idx < 0 || idx >= len(t.Files()) {
		http.NotFound(w, r)
		return
	}
	f := t.Files()[idx]
	rd := f.NewReader()
	defer rd.Close()
	rd.SetContext(r.Context())
	rd.SetResponsive()
	rd.SetReadahead(min(max(f.Length()/200, 5<<20), 32<<20))
	if ct := mime.TypeByExtension(filepath.Ext(f.DisplayPath())); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeContent(w, r, "", time.Time{}, rd)
}

// streamClient is webClient without its overall time limit: a film streams for hours.
var streamClient = sync.OnceValue(func() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
		TLSClientConfig:       &tls.Config{RootCAs: alpineRoots()},
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}}
})

// serveRelay: GET /r?u=<address>: a direct source, fetched by Go and passed on with its
// ranges. A connection that drops mid-film is picked up again where it stopped.
func serveRelay(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("u")
	if u, err := url.Parse(target); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "bad address", http.StatusBadRequest)
		return
	}
	get := func(rangeHdr string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
		if err != nil {
			return nil, err
		}
		if rangeHdr != "" {
			req.Header.Set("Range", rangeHdr)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 4.2.2) Playbox")
		return relayClient().Do(req)
	}
	resp, err := get(r.Header.Get("Range"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { resp.Body.Close() }()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead || resp.StatusCode >= 300 {
		return
	}
	// Where this answer starts in the file, to ask again from where it broke off.
	var pos int64
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		fmt.Sscanf(cr, "bytes %d-", &pos)
	}
	end := int64(-1)
	if resp.ContentLength >= 0 {
		end = pos + resp.ContentLength
	}
	resumable := resp.StatusCode == http.StatusPartialContent || resp.Header.Get("Accept-Ranges") == "bytes"
	for tries := 0; ; tries++ {
		n, err := io.Copy(w, resp.Body)
		pos += n
		if err == nil || r.Context().Err() != nil || !resumable || tries >= 5 || (end >= 0 && pos >= end) {
			return
		}
		if n > 0 {
			tries = 0
		}
		log.Printf("playbox: relay broke at %d MB (%v); picking it up again", pos>>20, err)
		time.Sleep(time.Second)
		resp.Body.Close()
		next, err := get(fmt.Sprintf("bytes=%d-", pos))
		if err != nil || next.StatusCode != http.StatusPartialContent {
			if next != nil {
				next.Body.Close()
			}
			return
		}
		resp = next
	}
}

// relayClient is the client the relay uses; tests replace it.
var relayClient = streamClient

// localRoots are the folders the media server may serve films from.
var localRoots = []string{"/data/media/", "/storage/", "/mnt/", playboxDir + "/"}

// serveLocal: GET /f?p=<path>: a film on the tablet (or the memory card).
func serveLocal(w http.ResponseWriter, r *http.Request) {
	p := filepath.Clean(r.URL.Query().Get("p"))
	ok := false
	for _, root := range localRoots {
		ok = ok || strings.HasPrefix(p, root)
	}
	if !ok || !isVideoName(p) {
		http.Error(w, "not a film on the tablet", http.StatusForbidden)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeContent(w, r, "", time.Time{}, f)
}

func humanBytes(n float64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", n/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", n/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", n/(1<<10))
	}
	return fmt.Sprintf("%.0f B", n)
}

// torrentLine is a job's status in a line: "Finding peers… 12 peers" and the like.
func torrentLine(st tstatus, download bool) string {
	switch st.State {
	case "resolving":
		return fmt.Sprintf("Finding the torrent's details… %d peers", st.All)
	case "ready", "downloading":
		pct := 0.0
		if st.Size > 0 {
			pct = float64(st.Done) / float64(st.Size) * 100
		}
		what := "Playing from the torrent"
		if download {
			what = "Downloading"
		}
		return fmt.Sprintf("%s · %.0f%% · %d peers · %s/s", what, pct, st.Peers, humanBytes(st.Speed))
	case "saved":
		return "Saved to Movies"
	case "failed":
		return st.Err
	}
	return ""
}
