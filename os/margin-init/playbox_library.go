package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Playbox's library (LibraryStore.swift): films and series saved from their pages, what was
// watched lately with where it stopped (Continue Watching), and the films downloaded to the
// tablet. Kept in /data/condor/playbox/library.json.

type pbEntry struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Name     string    `json:"name"`
	Poster   string    `json:"poster,omitempty"`
	Progress float64   `json:"progress"`
	Updated  time.Time `json:"updatedAt"`
	Position float64   `json:"position,omitempty"` // seconds
	Duration float64   `json:"duration,omitempty"`
	Episode  string    `json:"episode,omitempty"` // the episode's id, for a series
	EpLabel  string    `json:"episodeLabel,omitempty"`
}

func (e pbEntry) item() metaItem {
	return metaItem{ID: e.ID, Type: e.Type, Name: e.Name, Poster: e.Poster}
}

// pbDownload is a film saved on the tablet, playable without the internet.
type pbDownload struct {
	Path   string    `json:"path"`
	Name   string    `json:"name"`
	ID     string    `json:"id,omitempty"` // the film's id, when it came from a page
	Type   string    `json:"type,omitempty"`
	Poster string    `json:"poster,omitempty"`
	Added  time.Time `json:"added"`
}

type pbLibrary struct {
	mu        sync.Mutex
	loaded    bool
	Saved     []pbEntry    `json:"saved"`
	Recent    []pbEntry    `json:"recent"`
	Downloads []pbDownload `json:"downloads"`
}

var pbLib = &pbLibrary{}

// moviesDir is where downloads go: the tablet's own storage, next to Books.
var moviesDir = "/data/media/0/Movies"

func (l *pbLibrary) path() string { return filepath.Join(playboxDir, "library.json") }

func (l *pbLibrary) load() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loaded {
		return
	}
	l.loaded = true
	if b, err := os.ReadFile(l.path()); err == nil {
		json.Unmarshal(b, l)
	}
}

// save writes the library. Caller holds l.mu.
func (l *pbLibrary) save() {
	b, _ := json.MarshalIndent(l, "", "  ")
	os.MkdirAll(playboxDir, 0o755)
	if os.WriteFile(l.path()+".new", b, 0o644) == nil {
		os.Rename(l.path()+".new", l.path())
	}
}

func (l *pbLibrary) contains(id string) bool {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.Saved {
		if e.ID == id {
			return true
		}
	}
	return false
}

// toggle saves the item to the library, or takes it out.
func (l *pbLibrary) toggle(it metaItem) bool {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, e := range l.Saved {
		if e.ID == it.ID {
			l.Saved = append(l.Saved[:i], l.Saved[i+1:]...)
			l.save()
			return false
		}
	}
	l.Saved = append([]pbEntry{{ID: it.ID, Type: it.Type, Name: it.Name, Poster: it.Poster, Updated: time.Now()}}, l.Saved...)
	l.save()
	return true
}

func (l *pbLibrary) removeSaved(id string) {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, e := range l.Saved {
		if e.ID == id {
			l.Saved = append(l.Saved[:i], l.Saved[i+1:]...)
			break
		}
	}
	l.save()
}

// resume is where the item (or that episode) stopped, in seconds (0 = the start).
func (l *pbLibrary) resume(id, episode string) float64 {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.Recent {
		if e.ID == id && (episode == "" || e.Episode == episode) {
			return e.Position
		}
	}
	return 0
}

func (l *pbLibrary) recentEntry(id string) (pbEntry, bool) {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.Recent {
		if e.ID == id {
			return e, true
		}
	}
	return pbEntry{}, false
}

// played records where playback is: Continue Watching shows it until 95% is seen.
func (l *pbLibrary) played(it metaItem, episode, epLabel string, pos, dur float64) {
	if it.ID == "" || pos < 0 || dur <= 0 {
		return
	}
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.Recent[:0]
	for _, e := range l.Recent {
		if e.ID != it.ID {
			kept = append(kept, e)
		}
	}
	l.Recent = kept
	if frac := min(max(pos/dur, 0), 1); frac < 0.95 {
		e := pbEntry{ID: it.ID, Type: it.Type, Name: it.Name, Poster: it.Poster, Progress: frac,
			Updated: time.Now(), Position: pos, Duration: dur, Episode: episode, EpLabel: epLabel}
		l.Recent = append([]pbEntry{e}, l.Recent...)
	}
	if len(l.Recent) > 20 {
		l.Recent = l.Recent[:20]
	}
	l.save()
}

func (l *pbLibrary) lists() (saved, recent []pbEntry, downloads []pbDownload) {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]pbEntry(nil), l.Saved...), append([]pbEntry(nil), l.Recent...), append([]pbDownload(nil), l.Downloads...)
}

func (l *pbLibrary) addDownload(d pbDownload) {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range l.Downloads {
		if x.Path == d.Path {
			return
		}
	}
	l.Downloads = append([]pbDownload{d}, l.Downloads...)
	l.save()
}

// downloads lists the films on the tablet: the ones Playbox saved, and any other video in
// Movies (copied there by hand). Missing files drop out.
func (l *pbLibrary) downloads() []pbDownload {
	l.load()
	l.mu.Lock()
	known := map[string]bool{}
	var out []pbDownload
	for _, d := range l.Downloads {
		if _, err := os.Stat(d.Path); err == nil {
			out = append(out, d)
			known[d.Path] = true
		}
	}
	changed := len(out) != len(l.Downloads)
	l.Downloads = out
	if changed {
		l.save()
	}
	l.mu.Unlock()
	entries, _ := os.ReadDir(moviesDir)
	var extra []pbDownload
	for _, e := range entries {
		p := filepath.Join(moviesDir, e.Name())
		if e.IsDir() || known[p] || !isVideoName(e.Name()) || strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		info, _ := e.Info()
		var mt time.Time
		if info != nil {
			mt = info.ModTime()
		}
		extra = append(extra, pbDownload{Path: p, Name: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), Added: mt})
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Added.After(extra[j].Added) })
	return append(append([]pbDownload(nil), out...), extra...)
}

func (l *pbLibrary) removeDownload(path string) {
	l.load()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, d := range l.Downloads {
		if d.Path == path {
			l.Downloads = append(l.Downloads[:i], l.Downloads[i+1:]...)
			break
		}
	}
	l.save()
	os.Remove(path)
}

var videoExts = map[string]bool{".mp4": true, ".m4v": true, ".mov": true, ".mkv": true, ".avi": true,
	".webm": true, ".ts": true, ".m2ts": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true, ".3gp": true, ".ogv": true}

func isVideoName(name string) bool { return videoExts[strings.ToLower(filepath.Ext(name))] }

// safeFileName makes a title into a file name (Playbox's sanitizer).
func safeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:?%*|"<>`, r) || r < ' ' {
			return '-'
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	if s == "" {
		return "Video"
	}
	return s
}

// freeFilmName is dir/base.ext, or dir/base (2).ext and so on if that's taken.
func freeFilmName(dir, base, ext string) string {
	p := filepath.Join(dir, base+ext)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); err != nil {
			return p
		}
		p = filepath.Join(dir, base+" ("+strconv.Itoa(i)+")"+ext)
	}
}
