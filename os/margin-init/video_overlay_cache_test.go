package main

import (
	"context"
	"testing"
	"time"
)

// With the controls up, each film frame used to draw the whole overlay again (the bars, the
// round buttons: square roots over ~0.5 M pixels); now it's drawn only when what it shows
// changes, and a frame laid over the kept overlay is the same picture as over a fresh one.
func TestVideoOverlayKept(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.mode, c.screenOn = modeVideo, true
	fw, fh := 960, 540
	frame := make([]byte, fw*fh*4)
	for i := range frame {
		frame[i] = byte(i * 7)
	}
	v := &videoPlayer{req: videoReq{title: "Buddy"}, state: "playing", speed: 1, ctx: ctx, cancel: cancel, scrub: -1,
		frame: frame, fw: fw, fh: fh, fhalf: true, light: true, controls: true, pos: 19.2,
		probe: &videoProbe{duration: 5749, w: 720, h: 404, rateVal: 23.976}}
	c.vid = v
	defer func() { c.vid = nil }()

	t0 := time.Now()
	c.videoCompose()
	first := time.Since(t0)
	if v.ovBuilds != 1 {
		t.Fatalf("overlay drawn %d times, want 1", v.ovBuilds)
	}
	t1 := time.Now()
	for i := 0; i < 10; i++ {
		v.pos = 19.2 + float64(i)*0.04 // ten frames within the same second
		c.videoCompose()
	}
	kept := time.Since(t1) / 10
	if v.ovBuilds != 1 {
		t.Fatalf("overlay drawn %d times over ten frames in one second, want 1", v.ovBuilds)
	}
	t.Logf("frame with the controls up: %v drawing the overlay, %v with it kept", first, kept)
	keptScreen := append([]byte(nil), c.s.buf...)
	v.ovKey = "" // force a fresh one: same picture
	c.videoCompose()
	if string(keptScreen) != string(c.s.buf) {
		t.Fatal("a frame over the kept overlay differs from one over a fresh overlay")
	}
	v.pos = 20.5 // the clock text changes
	c.videoCompose()
	v.controls = false
	c.videoCompose()
	if v.ovBuilds != 4 {
		t.Fatalf("overlay drawn %d times, want 4 (start, forced, new second, controls hidden)", v.ovBuilds)
	}
}
