package main

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// The outputs on the tablet (its /proc/asound/pcm, 2026-10-29): only the chip's first device
// and HDMI are for films; the rest are for calls and can block.
func TestSoundCallOutputs(t *testing.T) {
	for name, call := range map[string]bool{
		"cloverview_audi · 1":                                    false,
		"IntelHDMI · IntelHDMI":                                  false,
		"cloverview_audi · Voice cs42l73-vsp-1":                  true,
		"cloverview_audi · BTSCO snd-soc-dummy-dai-3":            true,
		"cloverview_audi · VOIP cs42l73-vsp-4":                   true,
		"cloverview_audi · IFX_MODEM_MIXING snd-soc-dummy-dai-5": true,
		"cloverview_audi · virtual-stream cs42l73-asp-6":         true,
		"cloverview_audi · CTP Probe snd-soc-dummy-dai-7":        true,
		"Loopback · Loopback PCM":                                true,
		"IntelALSASSP · BT_DEVICE":                               true,
		"IntelALSASSP · BUILTIN_DEVICE":                          true,
		"IntelALSAIFX · IFX_DEVICE":                              true,
	} {
		if callSoundRE.MatchString(name) != call {
			t.Errorf("%q: call output %v, want %v", name, !call, call)
		}
	}
}

// An output that stops taking sound (a call output) used to freeze the film: the sound is
// its clock. Now the sound is dropped and the film carries on, on the wall clock.
func TestSoundStuckOutputDoesntFreezeFilm(t *testing.T) {
	old := soundStuckAfter
	soundStuckAfter = 300 * time.Millisecond
	t.Cleanup(func() { soundStuckAfter = old })
	ar, aw, _ := os.Pipe() // the sound's ffmpeg: endless silence
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := aw.Write(buf); err != nil {
				return
			}
		}
	}()
	pr, pw, _ := os.Pipe() // to "aplay", which never reads
	setPipeSize(pw, 4096)
	stuck := exec.Command("sleep", "30")
	if err := stuck.Start(); err != nil {
		t.Skip("no sleep:", err)
	}
	r := &videoRun{aout: ar, aplayW: pw, aplay: stuck, rate: 24, speed: 1, done: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	r.volume.Store(100)
	r.audio.Store(true)
	go r.pumpSound()
	go r.soundWatch()
	deadline := time.Now().Add(5 * time.Second)
	for r.audio.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if r.audio.Load() {
		t.Fatal("the sound wasn't dropped")
	}
	r.mu.Lock()
	why := r.soundErr
	r.mu.Unlock()
	if !strings.Contains(why, "stuck") {
		t.Fatalf("soundErr %q", why)
	}
	c1 := r.clock()
	time.Sleep(200 * time.Millisecond)
	if c2 := r.clock(); c2-c1 < 0.15 {
		t.Fatalf("the clock doesn't move: %.2f then %.2f", c1, c2)
	}
	r.kill()
	stuck.Wait()
	aw.Close()
	pr.Close()
}

// The speaker route is Android's (asound.conf's pcm.speaker hooks), set with its alsa_amixer.
func TestSoundRouteCloverview(t *testing.T) {
	dir := t.TempDir()
	log := dir + "/calls"
	fake := dir + "/alsa_amixer"
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\n"), 0o755)
	old := androidAmixer
	androidAmixer = fake
	t.Cleanup(func() { androidAmixer = old })
	if err := routeCloverview("4", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	got := string(b)
	for _, want := range []string{"-q -c 4 cset name=LPE IHF mixer 3", "-q -c 4 cset name=IHFAMP_SD_N Switch on", "-q -c 4 cset name=HP Amp Switch off"} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	os.Remove(log)
	routeCloverview("4", true)
	b, _ = os.ReadFile(log)
	if !strings.Contains(string(b), "name=HP Amp Switch on\n") || !strings.Contains(string(b), "name=LPE headset mixer 3\n") {
		t.Fatalf("headphones:\n%s", b)
	}
}
