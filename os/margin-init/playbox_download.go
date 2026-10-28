package main

import (
	"context"
	"fmt"
	"image/color"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Downloads of direct (http/https) sources to Movies (DownloadManager.swift): a plain
// download with its progress, kept as a .part file until it's whole.

type httpDownload struct {
	key, name, url string
	meta           metaItem
	mu             sync.Mutex
	got, size      int64
	state          string // "downloading", "saved", "failed", "cancelled"
	err, path      string
	stop           context.CancelFunc
	started        time.Time
}

func startHTTPDownload(key, u, name string, meta metaItem, changed func()) *httpDownload {
	ctx, cancel := context.WithCancel(context.Background())
	d := &httpDownload{key: key, name: name, url: u, meta: meta, state: "downloading", stop: cancel, started: time.Now()}
	go func() {
		err := d.run(ctx, changed)
		d.mu.Lock()
		switch {
		case ctx.Err() != nil:
			d.state = "cancelled"
		case err != nil:
			d.state, d.err = "failed", shortErr(err)
			log.Printf("playbox: download %s: %v", u, err)
		default:
			d.state = "saved"
		}
		d.mu.Unlock()
		if changed != nil {
			changed()
		}
	}()
	return d
}

func (d *httpDownload) run(ctx context.Context, changed func()) error {
	req, err := http.NewRequestWithContext(ctx, "GET", d.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 4.2.2) Playbox")
	resp, err := streamClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the server answered %s", resp.Status)
	}
	ext := strings.ToLower(path.Ext(req.URL.Path))
	if !videoExts[ext] {
		ext = ".mp4"
		if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "matroska") {
			ext = ".mkv"
		} else if strings.Contains(ct, "webm") {
			ext = ".webm"
		}
	}
	os.MkdirAll(moviesDir, 0o755)
	dst := freeFilmName(moviesDir, safeFileName(d.name), ext)
	d.mu.Lock()
	d.size = resp.ContentLength
	d.mu.Unlock()
	f, err := os.Create(dst + ".part")
	if err != nil {
		return err
	}
	buf := make([]byte, 256<<10)
	last := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(dst + ".part")
				return werr
			}
			d.mu.Lock()
			d.got += int64(n)
			d.mu.Unlock()
			if changed != nil && time.Since(last) > 2*time.Second {
				last = time.Now()
				changed()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(dst + ".part")
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(dst+".part", dst); err != nil {
		return err
	}
	d.mu.Lock()
	d.path = dst
	d.mu.Unlock()
	pbLib.addDownload(pbDownload{Path: dst, Name: strings.TrimSuffix(filepath.Base(dst), ext), ID: d.meta.ID, Type: d.meta.Type, Poster: d.meta.Poster, Added: time.Now()})
	return nil
}

func (d *httpDownload) cancel() { d.stop() }

func (d *httpDownload) running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state == "downloading"
}

func (d *httpDownload) done() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state == "saved"
}

func (d *httpDownload) frac() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.size <= 0 {
		return 0
	}
	return float64(d.got) / float64(d.size)
}

// line is the download's status for its source's row, and its colour.
func (d *httpDownload) line() (string, color.RGBA) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch d.state {
	case "saved":
		return "Saved to Movies", rgb(0x34c759)
	case "failed":
		return "Download failed: " + d.err, apRed
	case "cancelled":
		return "Download stopped", apSecondary
	}
	speed := float64(d.got) / max(time.Since(d.started).Seconds(), 1)
	if d.size > 0 {
		return fmt.Sprintf("Downloading… %.0f%% · %s/s", float64(d.got)/float64(d.size)*100, humanBytes(speed)), apBlue
	}
	return fmt.Sprintf("Downloading… %s · %s/s", humanBytes(float64(d.got)), humanBytes(speed)), apBlue
}
