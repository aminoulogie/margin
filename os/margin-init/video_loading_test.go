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
		p := c.videoLoadingCard() // the text part: nothing behind the text
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

// The spinner turns once a second in 8 steps, never more than one spoke a redraw.
func TestSpinnerStep(t *testing.T) {
	base := time.UnixMilli(1_000_000_000_000)
	base = base.Add(-time.Duration(base.UnixMilli()%125) * time.Millisecond)
	a, b := spinnerStep(base), spinnerStep(base.Add(125*time.Millisecond))
	if (a+1)%8 != b {
		t.Fatalf("steps %d then %d", a, b)
	}
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	c.vid = &videoPlayer{spinStep: 3, spinAt: time.Now().Add(-5 * time.Second)} // a very slow redraw
	if got := c.spinNext(); got != 4 {
		t.Fatalf("after a slow redraw the spinner went to %d, want one spoke on (4)", got)
	}
	if got := c.spinNext(); got != 4 {
		t.Fatalf("a redraw right after moved it again: %d", got)
	}
	c.vid = nil
}

// A spinner step redraws only the spinner's rows, and the result equals a whole redraw.
func TestSpinnerFrameOnlyItsRows(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.mode, c.screenOn = modeVideo, true
	v := &videoPlayer{req: videoReq{title: "Buddy"}, state: "starting", speed: 1, ctx: ctx, cancel: cancel, scrub: -1, loadAt: time.Now()}
	c.vid = v
	c.videoCompose()
	var sp *ovPart
	for _, p := range v.overlay {
		if p.spin {
			sp = p
		}
	}
	if sp == nil {
		t.Fatal("no spinner part on the loading screen")
	}
	v.spinAt = time.Now().Add(-time.Second)
	before := append([]byte(nil), c.s.buf...)
	c.videoLoadingFrame(false)
	H, st := c.s.fbH, c.s.stride
	changed := false
	for y := 0; y < H; y++ {
		same := string(before[y*st:(y+1)*st]) == string(c.s.buf[y*st:(y+1)*st])
		if !same && (y < sp.y || y >= sp.y+sp.img.Rect.Dy()) {
			t.Fatalf("row %d outside the spinner (%d..%d) changed", y, sp.y, sp.y+sp.img.Rect.Dy())
		}
		changed = changed || !same
	}
	if !changed {
		t.Fatal("the spinner step changed nothing")
	}
	// The strip redraw leaves the screen as a whole redraw with the same parts would.
	strip := append([]byte(nil), c.s.buf...)
	c.videoRecompose(0, H, v.overlay)
	if string(strip) != string(c.s.buf) {
		t.Fatal("redrawing the spinner's rows alone left a different screen than a whole redraw")
	}
	c.vid = nil
}
