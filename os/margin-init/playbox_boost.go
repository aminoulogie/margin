package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Getting a torrent going sooner. A magnet has only the torrent's hash: its details (files,
// pieces) normally come from the first peers, which is the long "Finding the torrent's
// details… 0 peers". So, at the same time:
//   - the .torrent file is asked from public caches, which have most of them: details at once;
//   - many more trackers are added: a fresh list (ngosang/trackerslist, kept a day) and the
//     .torrent's own, so peers are found sooner.

var torrentCaches = []string{
	"https://itorrents.org/torrent/%s.torrent",
	"https://torrage.info/torrent.php?h=%s",
	"https://btcache.me/torrent/%s",
}

// boostTorrent runs alongside a new torrent until it has its details.
func boostTorrent(t *torrent.Torrent, hash string) {
	t.AddTrackers(tiers(liveTrackers()))
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	go func() {
		select {
		case <-t.GotInfo():
			cancel()
		case <-ctx.Done():
		}
	}()
	for _, pattern := range torrentCaches {
		if ctx.Err() != nil {
			return
		}
		u := strings.Replace(pattern, "%s", strings.ToUpper(hash), 1)
		mi, err := fetchTorrentFile(ctx, u)
		if err != nil {
			continue
		}
		if mi.HashInfoBytes().HexString() != hash {
			continue // a cache's wrong answer
		}
		if err := t.SetInfoBytes(mi.InfoBytes); err != nil {
			log.Printf("playbox: %s from %s: %v", hash, u, err)
			continue
		}
		t.AddTrackers(mi.UpvertedAnnounceList())
		log.Printf("playbox: %s: details from %s", hash, u)
		return
	}
}

func fetchTorrentFile(ctx context.Context, u string) (*metainfo.MetaInfo, error) {
	resp, err := webGetCtx(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(resp.Body, 8<<20)); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if bytes.HasPrefix(b, []byte{0x1f, 0x8b}) {
		if u, err := gunzip(b); err == nil {
			b = u
		}
	}
	return metainfo.Load(bytes.NewReader(b))
}

func tiers(list []string) [][]string {
	out := make([][]string, len(list))
	for i, t := range list {
		out[i] = []string{t}
	}
	return out
}

var (
	trackersMu   sync.Mutex
	trackersList []string
	trackersAt   time.Time
)

const trackersURL = "https://raw.githubusercontent.com/ngosang/trackerslist/master/trackers_best.txt"

// liveTrackers is today's list of working public trackers (fetched at most once a day,
// kept on the tablet), and the built-in ones.
func liveTrackers() []string {
	trackersMu.Lock()
	defer trackersMu.Unlock()
	file := filepath.Join(playboxDir, "trackers.txt")
	if trackersList == nil {
		if b, err := os.ReadFile(file); err == nil {
			trackersList = parseTrackers(string(b))
			if fi, err := os.Stat(file); err == nil {
				trackersAt = fi.ModTime()
			}
		}
	}
	if time.Since(trackersAt) > 24*time.Hour {
		trackersAt = time.Now() // one try a day, in the background
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			resp, err := webGetCtx(ctx, trackersURL)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var buf bytes.Buffer
			buf.ReadFrom(io.LimitReader(resp.Body, 1<<20))
			if list := parseTrackers(buf.String()); len(list) > 5 {
				os.MkdirAll(playboxDir, 0o755)
				os.WriteFile(file, buf.Bytes(), 0o644)
				trackersMu.Lock()
				trackersList = list
				trackersMu.Unlock()
			}
		}()
	}
	return append(append([]string(nil), trackersList...), fallbackTrackers...)
}

func parseTrackers(s string) []string {
	var out []string
	for _, l := range strings.Fields(s) {
		if strings.HasPrefix(l, "udp://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
			out = append(out, l)
		}
	}
	return out
}

// --- many add-ons, one list ------------------------------------------------------------------

// mergeStream adds s to list, or folds it into the same torrent (same hash and file) from
// another add-on: the larger seeder count is kept and both add-ons are named.
func mergeStream(list []*addonStream, s *addonStream) []*addonStream {
	if h := strings.ToLower(s.InfoHash); h != "" {
		for _, o := range list {
			if strings.ToLower(o.InfoHash) == h && sameIdx(o.FileIdx, s.FileIdx) {
				if s.seeders() > o.seeders() {
					o.Title, o.Desc, o.Name = s.Title, s.Desc, s.Name
				}
				if s.Addon != "" && !strings.Contains(o.Addon, s.Addon) {
					o.Addon += " + " + s.Addon
				}
				o.Sources = append(o.Sources, s.Sources...)
				return list
			}
		}
	}
	for _, o := range list {
		if o.key() == s.key() {
			return list
		}
	}
	return append(list, s)
}

func sameIdx(a, b *int) bool {
	return a == nil || b == nil || *a == *b
}

// fastScore is how soon a source should start playing here: seeders (with diminishing
// returns), a size that's quick to fetch, a picture the processor can decode in time.
func fastScore(s *addonStream) float64 {
	n := s.seeders()
	if n < 0 {
		n = 3
	}
	score := 0.0
	for k := n; k > 0; k /= 2 {
		score += 10
	}
	if mb := s.sizeMB(); mb > 0 {
		score -= mb / 400 // 4 GB costs 10, like halving the seeders
	}
	switch s.quality() {
	case "720p":
		score += 12
	case "480p":
		score += 6
	case "1080p":
		score += 2
	}
	if s.heavy() {
		score -= 40
	}
	if n == 0 {
		score -= 100
	}
	return score
}

// alternatives are the next torrents to try if one finds no peers, best first.
func alternatives(streams []*addonStream, not *addonStream) []*addonStream {
	var out []*addonStream
	for _, s := range streams {
		if s.isTorrent() && s != not && s.seeders() != 0 && !s.heavy() {
			out = append(out, s)
		}
	}
	sortTorrents(out, "fast")
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// installNewAddons puts the add-ons added since an earlier Margin in, once, quietly.
func installNewAddons() {
	mark := filepath.Join(playboxDir, "addons-v2")
	if _, err := os.Stat(mark); err == nil {
		return
	}
	os.MkdirAll(playboxDir, 0o755)
	os.WriteFile(mark, nil, 0o644)
	for _, f := range featuredAddons {
		if f.auto && !addons.isInstalled(f.manifest) {
			if err := addons.install(f.manifest); err != nil {
				log.Printf("playbox: add-on %s: %v", f.name, err)
				addons.mu.Lock()
				addons.msg = ""
				addons.mu.Unlock()
			}
		}
	}
}
