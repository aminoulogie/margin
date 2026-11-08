package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// androidSetprop is Android's setprop (margin-init has Android's environment from init, so it
// works when run directly); tests replace it.
var androidSetprop = "/system/bin/setprop"

// mediaServerPids lists Android's media server processes; tests replace it.
var mediaServerPids = func() []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil || len(b) == 0 {
			continue
		}
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		if filepath.Base(string(b)) == "mediaserver" {
			pids = append(pids, pid)
		}
	}
	return pids
}

var mediaStopOnce sync.Once

// stopAndroidMedia stops Android's media server, once. Margin's hook stops only zygote and the
// boot animation, so the server (init service "media", class main) keeps running, and at boot it
// opens a stream on the tablet's sound chip (pcmC4D6p) that it prepares but never starts. With
// the speaker mixer on Mixed ('LPE IHF mixer' 3, Android's own setting) the chip's DSP waits on
// that stream after the first block: every sound stalled after 0.125 s. Found 2026-11-08
// (Clipboard Sound 16): with the server stopped the tone plays to the end. Margin uses nothing
// from the server (no Android apps run).
func stopAndroidMedia() {
	mediaStopOnce.Do(func() {
		if len(mediaServerPids()) == 0 {
			return
		}
		if _, err := os.Stat(androidSetprop); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if out, err := exec.CommandContext(ctx, androidSetprop, "ctl.stop", "media").CombinedOutput(); err != nil {
				log.Printf("playbox: setprop ctl.stop media: %v %s", err, lastLines(string(out), 1))
			}
			cancel()
			for i := 0; i < 30 && len(mediaServerPids()) > 0; i++ {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if pids := mediaServerPids(); len(pids) > 0 {
			// setprop didn't do it: stop it by hand (init may start it again, but it's gone for now).
			for _, pid := range pids {
				if p, err := os.FindProcess(pid); err == nil {
					p.Kill()
				}
			}
			log.Printf("playbox: Android's media server killed (it held the sound chip): pids %v", pids)
			return
		}
		log.Printf("playbox: stopped Android's media server (it held the sound chip)")
	})
}
