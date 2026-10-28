package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// seedFilm makes a film-like file, a torrent of it, and a second client on 127.0.0.1 seeding
// it. It returns the source as an add-on would give it, the seeder and the file's bytes.
func seedFilm(t *testing.T) (*addonStream, *torrent.Client, []byte) {
	t.Helper()
	dir := t.TempDir()
	data := make([]byte, 3<<20+12345)
	rand.New(rand.NewSource(1)).Read(data)
	film := filepath.Join(dir, "seed", "Big Buck Bunny (2008)")
	os.MkdirAll(film, 0o755)
	os.WriteFile(filepath.Join(film, "Big.Buck.Bunny.720p.mp4"), data, 0o644)
	os.WriteFile(filepath.Join(film, "readme.txt"), []byte("hello"), 0o644)
	info := metainfo.Info{PieceLength: 256 << 10}
	if err := info.BuildFromFilePath(film); err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{}
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = filepath.Join(dir, "seed")
	cfg.Seed, cfg.NoDHT, cfg.DisableTrackers, cfg.NoDefaultPortForwarding = true, true, true, true
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort, cfg.DisableIPv6, cfg.DisableWebtorrent = 0, true, true
	seeder, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seeder.Close() })
	st, err := seeder.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	<-st.GotInfo()
	if err := st.VerifyData(); err != nil {
		t.Fatal(err)
	}
	idx := 0
	for i, f := range st.Files() {
		if filepath.Ext(f.DisplayPath()) == ".mp4" {
			idx = i
		}
	}
	h := mi.HashInfoBytes().HexString()
	return &addonStream{Name: "Torrentio\n720p", Title: "Big.Buck.Bunny.720p.mp4\n👤 42 💾 3.0 MB ⚙️ test",
		InfoHash: h, FileIdx: &idx}, seeder, data
}

// testEngine points the engine at temp folders, with no DHT or trackers.
func testEngine(t *testing.T) {
	oldDir, oldMovies, oldEng, oldCfg, oldPB := torrentCacheDir, moviesDir, engine, torrentTestConfig, playboxDir
	dir := t.TempDir()
	torrentCacheDir, moviesDir, playboxDir = filepath.Join(dir, "cache"), filepath.Join(dir, "Movies"), dir
	engine = &torrentEngine{jobs: map[string]*tjob{}}
	torrentTestConfig = func(cfg *torrent.ClientConfig) {
		cfg.NoDHT, cfg.DisableTrackers, cfg.NoDefaultPortForwarding = true, true, true
		cfg.ListenHost = func(string) string { return "127.0.0.1" }
		cfg.ListenPort = 0
	}
	oldLib := pbLib
	pbLib = &pbLibrary{}
	t.Cleanup(func() {
		if engine.cl != nil {
			engine.cl.Close()
		}
		if engine.srv != nil {
			engine.srv.Close()
		}
		torrentCacheDir, moviesDir, engine, torrentTestConfig, playboxDir, pbLib = oldDir, oldMovies, oldEng, oldCfg, oldPB, oldLib
	})
}

func waitState(t *testing.T, j *tjob, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		st := engine.status(j)
		if st.State == want {
			return
		}
		if st.State == "failed" {
			t.Fatalf("job failed: %s", st.Err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job still %q after %v, want %q", engine.status(j).State, d, want)
}

func TestTorrentStreamsWhileDownloading(t *testing.T) {
	testEngine(t)
	src, seeder, data := seedFilm(t)
	j, err := engine.start(src, false, "", metaItem{ID: "tt1254207", Type: "movie", Name: "Big Buck Bunny"})
	if err != nil {
		t.Fatal(err)
	}
	j.t.AddClientPeer(seeder)
	waitState(t, j, "ready", 20*time.Second)
	if st := engine.status(j); st.Size != int64(len(data)) || filepath.Ext(st.Name) != ".mp4" {
		t.Fatalf("picked %q (%d bytes), want the film (%d bytes)", st.Name, st.Size, len(data))
	}
	u, err := engine.streamURL(j, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// A seek into the middle, as a player does, then the end (an MP4's index).
	for _, rng := range [][2]int{{1 << 20, 1<<20 + 70000}, {len(data) - 5000, len(data) - 1}, {0, 1000}} {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rng[0], rng[1]))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, data[rng[0]:rng[1]+1]) {
			t.Fatalf("range %v: status %d, %d bytes, match %v", rng, resp.StatusCode, len(got), bytes.Equal(got, data[rng[0]:rng[1]+1]))
		}
	}
	engine.stopPlay(j)
	if st := engine.status(j); st.State != "stopped" {
		t.Fatalf("after stop: %q", st.State)
	}
}

func TestTorrentDownloadGoesToMovies(t *testing.T) {
	testEngine(t)
	src, seeder, data := seedFilm(t)
	j, err := engine.start(src, true, "Big Buck Bunny 720p", metaItem{ID: "tt1254207", Type: "movie", Name: "Big Buck Bunny"})
	if err != nil {
		t.Fatal(err)
	}
	j.t.AddClientPeer(seeder)
	waitState(t, j, "saved", 30*time.Second)
	st := engine.status(j)
	got, err := os.ReadFile(st.Saved)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("saved %s: %v (%d bytes)", st.Saved, err, len(got))
	}
	if filepath.Base(st.Saved) != "Big Buck Bunny 720p.mp4" {
		t.Fatalf("saved as %s", filepath.Base(st.Saved))
	}
	dl := pbLib.downloads()
	if len(dl) != 1 || dl[0].Path != st.Saved || dl[0].ID != "tt1254207" {
		t.Fatalf("library downloads: %+v", dl)
	}
	// The film plays from the tablet now: the media server's /f.
	oldRoots := localRoots
	localRoots = []string{filepath.Dir(st.Saved) + "/"}
	t.Cleanup(func() { localRoots = oldRoots })
	u, _ := engine.streamURL(nil, "", st.Saved)
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(b, data) {
		t.Fatalf("local file served %d bytes", len(b))
	}
}
