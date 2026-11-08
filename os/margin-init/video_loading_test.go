package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The loading screen: the iOS spinner and white text straight on black (no card), in every
// loading state, and quick enough to redraw ~30 times a second. MARGIN_SHOTS=<dir> saves PNGs.
func TestVideoLoadingScreen(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.mode, c.screenOn = modeVideo, true
	cases := []struct {
		name  string
		setup func(v *videoPlayer)
	}{
		{"starting", func(v *videoPlayer) {}},
		{"install", func(v *videoPlayer) { v.state, v.msg = "install", "Downloading ffmpeg… 42%" }},
		{"torrent-nojob", func(v *videoPlayer) { v.state = "torrent" }},
		{"probing", func(v *videoPlayer) { v.state = "probing" }},
		{"starting-probe", func(v *videoPlayer) {
			v.probe = &videoProbe{duration: 6130, w: 1920, h: 1080, rateVal: 23.976, videoCodec: "h264"}
			v.pos = 754
		}},
		{"failed", func(v *videoPlayer) { v.state, v.msg = "failed", "This film can't be played: no video stream" }},
	}
	shots := os.Getenv("MARGIN_SHOTS")
	for _, tc := range cases {
		v := &videoPlayer{req: videoReq{title: "Halloween"}, state: "starting", speed: 1, ctx: ctx, cancel: cancel,
			scrub: -1, loadAt: time.Now().Add(-7420 * time.Millisecond)}
		tc.setup(v)
		c.vid = v
		t0 := time.Now()
		c.videoCompose()
		took := time.Since(t0)
		t.Logf("%s: one redraw %v", tc.name, took)
		status, _, lines, _ := c.loadingInfo(time.Now())
		if status == "" {
			t.Fatalf("%s: no status line", tc.name)
		}
		if tc.name != "failed" && fmt.Sprint(lines) == "[]" {
			t.Fatalf("%s: no info lines", tc.name)
		}
		// No card: the corners of the loading area stay black.
		p := c.videoLoadingCard()
		for _, pt := range [][2]int{{2, p.img.Rect.Dy() / 2}, {p.img.Rect.Dx() - 3, p.img.Rect.Dy() / 2}} {
			if a := p.img.RGBAAt(pt[0], pt[1]).A; a != 0 {
				t.Fatalf("%s: something drawn behind the text at %v (alpha %d)", tc.name, pt, a)
			}
		}
		if shots != "" {
			screenPNG(t, c.s, filepath.Join(shots, "loading-"+tc.name+".png"))
		}
	}
	c.vid = nil
}

// The spinner turns once a second in 12 steps, the lit spoke the brightest.
func TestSpinnerStep(t *testing.T) {
	base := time.UnixMilli(1_000_000_000_008) // a multiple of 83 ms, plus nothing left over
	base = base.Add(-time.Duration(base.UnixMilli()%83) * time.Millisecond)
	a, b := spinnerStep(base), spinnerStep(base.Add(83*time.Millisecond))
	if (a+1)%12 != b {
		t.Fatalf("steps %d then %d", a, b)
	}
}
