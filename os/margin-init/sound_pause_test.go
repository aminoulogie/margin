package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pausing drops the sound queued on the chip at once (its aplay goes) and freezes the clock
// where the sound was heard; playing starts a new aplay that gets the unheard part again, and
// the clock carries on from the same moment, without a jump.
func TestSoundPauseIsInstant(t *testing.T) {
	dir := t.TempDir()
	starts := filepath.Join(dir, "starts")
	fake := filepath.Join(dir, "aplay")
	os.WriteFile(fake, []byte("#!/bin/sh\necho start >> "+starts+"\nexec cat > /dev/null\n"), 0o755)
	status := filepath.Join(dir, "status")
	setHw := func(n int) {
		os.WriteFile(status, []byte(fmt.Sprintf("state: RUNNING\nhw_ptr      : %d\nappl_ptr    : 0\n", n)), 0o644)
	}
	setHw(0)
	oldRoot, oldAplay := alpineRoot, aplayPath
	alpineRoot, aplayPath = "", fake
	t.Cleanup(func() { alpineRoot, aplayPath = oldRoot, oldAplay })

	ar, aw, _ := os.Pipe() // the sound's ffmpeg: endless silence
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := aw.Write(buf); err != nil {
				return
			}
		}
	}()
	defer aw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &videoRun{aout: ar, rate: 24, speed: 1, done: make(chan struct{}), ctx: ctx, statusPath: status}
	r.cond = sync.NewCond(&r.mu)
	r.volume.Store(100)
	r.mu.Lock()
	if err := r.openAplayLocked(); err != nil {
		r.mu.Unlock()
		t.Fatal(err)
	}
	r.mu.Unlock()
	r.audio.Store(true)
	go r.pumpSound()
	go r.watchChip()
	defer r.kill()

	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(3 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	waitFor("sound handed over", func() bool { return r.written.Load() > audioBytesSec })
	setHw(4800) // the chip has played 0.1 s
	time.Sleep(60 * time.Millisecond)
	before := r.clock()
	if before < 0.09 || before > 0.1+chipPeriod+0.01 {
		t.Fatalf("clock %.3f, want about 0.1 s (the chip's position)", before)
	}

	r.setPaused(true)
	r.mu.Lock()
	dropped, heard := r.dropped, r.written.Load()
	r.mu.Unlock()
	if !dropped {
		t.Fatal("pausing didn't drop the queued sound")
	}
	if got := float64(heard) / audioBytesSec; math.Abs(got-before) > chipPeriod+0.02 {
		t.Fatalf("rewound to %.3f s, heard was %.3f s", got, before)
	}
	c1 := r.clock()
	time.Sleep(150 * time.Millisecond)
	if c2 := r.clock(); c2 != c1 {
		r.mu.Lock()
		why, dr := r.soundErr, r.dropped
		r.mu.Unlock()
		t.Fatalf("the clock moved while paused: %.6f then %.6f (sound on %v, dropped %v, %q)", c1, c2, r.audio.Load(), dr, why)
	}

	setHw(0) // the next aplay's stream starts at 0
	r.setPaused(false)
	if c3 := r.clock(); math.Abs(c3-c1) > 0.03 {
		t.Fatalf("the clock jumped on play: %.3f then %.3f", c1, c3)
	}
	waitFor("the unheard sound resent", func() bool { return r.written.Load() > heard+audioBytesSec/2 })
	b, _ := os.ReadFile(starts)
	if n := strings.Count(string(b), "start"); n != 2 {
		t.Fatalf("aplay started %d times, want 2 (once, then again on play)", n)
	}
	setHw(9600) // 0.2 s played by the new one
	time.Sleep(60 * time.Millisecond)
	if c4 := r.clock(); c4 < c1+0.19 || c4 > c1+0.2+chipPeriod+0.01 {
		t.Fatalf("after play the clock is %.3f, want %.3f + 0.2 s", c4, c1)
	}
	r.mu.Lock()
	why := r.soundErr
	r.mu.Unlock()
	if why != "" || !r.audio.Load() {
		t.Fatalf("dropping the old aplay was taken for a failure: %q", why)
	}
}

func TestSoundStatusPath(t *testing.T) {
	if got := soundStatusPath("plughw:4,0"); got != "/proc/asound/card4/pcm0p/sub0/status" {
		t.Fatal(got)
	}
	if got := soundStatusPath("default"); got != "" {
		t.Fatal(got)
	}
}
