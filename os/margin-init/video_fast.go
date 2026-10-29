package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The faster ffmpeg. Alpine builds ffmpeg for 32-bit x86 with --disable-asm (its APKBUILD,
// case x86): no SSE/SSSE3 code, so on the Atom a film decoded at about one frame a second.
// These are static i686 builds of ffmpeg 6.0 with the assembly in (John Van Sickle's, as
// shipped by the ffmpeg-static project), checked against their SHA-256 and kept in Alpine's
// /opt/ffmpeg. They need Linux 3.2 or later (the tablet has 3.4). Alpine's own ffmpeg stays
// as the fallback, and aplay (alsa-utils) still comes from Alpine.

const fastFFmpegDir = "/opt/ffmpeg" // inside Alpine

var fastFFmpegFiles = []struct{ name, url, sha256 string }{
	{"ffmpeg", "https://github.com/eugeneware/ffmpeg-static/releases/download/b6.0/ffmpeg-linux-ia32.gz",
		"f2872e3bbc849a38adb17679254cb7ad4dd79ae592833e460b69f8d375f2d892"},
	{"ffprobe", "https://github.com/eugeneware/ffmpeg-static/releases/download/b6.0/ffprobe-linux-ia32.gz",
		"1368342c7022fc47f7e295c167f0285e82545d7d75bc0ddac0ef4eb88b2bec8a"},
}

var (
	fastMu     sync.Mutex
	fastTried  bool   // this session (a failure isn't retried until a restart)
	fastFailed string // why it couldn't be used
)

func fastFFmpegReady() bool {
	_, err := os.Stat(alpineRoot + fastFFmpegDir + "/ok")
	return err == nil
}

// ffmpegPath and ffprobePath are the programs the player runs (paths inside Alpine).
func ffmpegPath() string {
	if fastFFmpegReady() {
		return fastFFmpegDir + "/ffmpeg"
	}
	return "/usr/bin/ffmpeg"
}

func ffprobePath() string {
	if fastFFmpegReady() {
		return fastFFmpegDir + "/ffprobe"
	}
	return "/usr/bin/ffprobe"
}

// getFastFFmpeg downloads, checks and tries the fast build (once per session). say gets
// progress lines.
func getFastFFmpeg(say func(string)) error {
	fastMu.Lock()
	defer fastMu.Unlock()
	if fastFFmpegReady() {
		return nil
	}
	if fastTried {
		return fmt.Errorf("%s", fastFailed)
	}
	fastTried = true
	err := fetchFastFFmpeg(say)
	if err != nil {
		fastFailed = err.Error()
		os.RemoveAll(alpineRoot + fastFFmpegDir)
		log.Printf("playbox: fast ffmpeg: %v (Alpine's is used)", err)
	}
	return err
}

func fetchFastFFmpeg(say func(string)) error {
	dir := alpineRoot + fastFFmpegDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, f := range fastFFmpegFiles {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		resp, err := webGetCtx(ctx, f.url)
		if err != nil {
			cancel()
			return fmt.Errorf("download: %s", shortErr(err))
		}
		var buf bytes.Buffer
		h := sha256.New()
		chunk := make([]byte, 256<<10)
		last := time.Time{}
		for {
			n, rerr := resp.Body.Read(chunk)
			buf.Write(chunk[:n])
			h.Write(chunk[:n])
			if time.Since(last) > time.Second && say != nil {
				last = time.Now()
				say(fmt.Sprintf("Getting the faster video player (%d of 2): %.0f of 21 MB", i+1, float64(buf.Len())/(1<<20)))
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				resp.Body.Close()
				cancel()
				return fmt.Errorf("download: %s", shortErr(rerr))
			}
		}
		resp.Body.Close()
		cancel()
		if got := hex.EncodeToString(h.Sum(nil)); got != f.sha256 {
			return fmt.Errorf("%s: wrong checksum", f.name)
		}
		zr, err := gzip.NewReader(&buf)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dir+"/"+f.name+".new", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, zr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		if err := os.Rename(dir+"/"+f.name+".new", dir+"/"+f.name); err != nil {
			return err
		}
	}
	// It must run here before it's used.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, f := range fastFFmpegFiles {
		out, err := alpineCtxCmd(ctx, fastFFmpegDir+"/"+f.name, "-version").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "version") {
			return fmt.Errorf("%s doesn't run here: %v %s", f.name, err, lastLines(string(out), 1))
		}
	}
	log.Printf("playbox: fast ffmpeg ready")
	return os.WriteFile(dir+"/ok", nil, 0o644)
}

// ensurePlayer gets everything a film needs: the fast ffmpeg (or Alpine's), and aplay.
func ensurePlayer(say func(string)) error {
	if !fastFFmpegReady() {
		if say != nil {
			say("Getting the faster video player (once, about 42 MB)…")
		}
		getFastFFmpeg(say) // without it, Alpine's ffmpeg (slower)
	}
	var pkgs []string
	if !ffmpegInstalled() {
		pkgs = append(pkgs, "ffmpeg")
	}
	if !aplayInstalled() {
		pkgs = append(pkgs, "alsa-utils")
	}
	if len(pkgs) == 0 {
		return nil
	}
	if say != nil {
		say("Installing " + strings.Join(pkgs, " and ") + " (once)…")
	}
	ffmpegInstall.Lock()
	defer ffmpegInstall.Unlock()
	out, err := apkOut(append([]string{"apk", "add"}, pkgs...)...)
	log.Printf("playbox: apk add %v: %v %s", pkgs, err, lastLines(string(out), 3))
	if !ffmpegInstalled() {
		why := lastLines(string(out), 1)
		if why == "" && err != nil {
			why = err.Error()
		}
		return fmt.Errorf("Couldn't install the video player (ffmpeg). Is Wi-Fi on? %s", why)
	}
	return nil
}

// pbLastProblem is what last went wrong playing (shown in Playbox > Settings).
var pbLastProblem string

// soundOut is an ALSA playback device. call marks the ones that aren't for films: voice
// calls, Bluetooth headsets, the modem, loopback and test devices. Their clock comes from the
// modem or a headset that isn't there, so on this tablet a program writing to one blocks,
// sometimes in the driver where it can't even be stopped, and the sound chip stays stuck
// until a restart (a chime on 4,1 "Voice" froze it, 2026-10-29).
type soundOut struct {
	dev, name string
	call      bool
}

var callSoundRE = regexp.MustCompile(`(?i)voice|voip|btsco|bt_device|builtin_device|ifx|modem|probe|dummy|loopback|vsp|virtual`)

// soundOutputs lists the sound devices that can play ("card,device"), from /proc/asound.
func soundOutputs() []soundOut {
	cards := map[string]string{}
	if b, err := os.ReadFile("/proc/asound/cards"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) > 1 && strings.Contains(l, "]:") {
				if _, name, ok := strings.Cut(l, "]: "); ok {
					cards[f[0]] = strings.TrimSpace(strings.Split(name, " - ")[0])
				}
			}
		}
	}
	var out []soundOut
	b, _ := os.ReadFile("/proc/asound/pcm")
	for _, l := range strings.Split(string(b), "\n") {
		parts := strings.Split(l, ":")
		if len(parts) < 3 || !strings.Contains(l, "playback") {
			continue
		}
		cd := strings.SplitN(strings.TrimSpace(parts[0]), "-", 2)
		if len(cd) != 2 {
			continue
		}
		card, dev := strings.TrimLeft(cd[0], "0"), strings.TrimLeft(cd[1], "0")
		if card == "" {
			card = "0"
		}
		if dev == "" {
			dev = "0"
		}
		name := strings.TrimSpace(parts[1])
		if cn := cards[card]; cn != "" {
			name = cn + " · " + name
		}
		out = append(out, soundOut{card + "," + dev, name + " (" + card + "," + dev + ")", callSoundRE.MatchString(name)})
	}
	return out
}

// currentSoundDev is the output chosen by hand ("" = automatic). A call output chosen
// before they were told apart is dropped: films would freeze on it.
func currentSoundDev() string {
	b, err := os.ReadFile(marginHome + "/sounddev")
	if err != nil {
		return ""
	}
	dev := strings.TrimSpace(string(b))
	for _, o := range soundOutputs() {
		if o.dev == dev && o.call && !callOutputsOK() {
			log.Printf("playbox: sound output %s is for calls: back to automatic", dev)
			os.Remove(marginHome + "/sounddev")
			return ""
		}
	}
	return dev
}

// callOutputsOK: the call outputs have been shown on purpose (Settings, "Show call outputs")
// and one may be chosen.
func callOutputsOK() bool { return pbShowCallOutputs }

var pbShowCallOutputs bool
