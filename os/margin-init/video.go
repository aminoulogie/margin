package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/font"

	"margin-init/ui"
)

// The video player (Playbox's TrackedPlayerView and VLC player). There's no video engine on
// this tablet's framebuffer, so the film is decoded by ffmpeg, from Alpine (installed the
// first time a film plays), and Margin shows it:
//
//	ffmpeg ─ frames, raw ─▶ margin-init ─▶ the screen (turned sideways: the panel's own rows)
//	       └ sound, PCM ──▶ margin-init ─▶ aplay ─▶ ALSA (the speaker, the jack)
//
// The sound sets the time: a frame is shown when the sound reaching the speaker is at its
// time, late frames are skipped. Pausing just stops reading (ffmpeg waits); seeking starts
// ffmpeg again at the new time. Without sound the clock is the wall clock. ffmpeg only ever
// reads plain http from 127.0.0.1 (the media server in playbox_torrent.go).
//
// The picture fills the panel in its native landscape: turn the tablet on its side. Taps
// show the controls (close, CC, speed, ±10 s, play/pause, the timeline); swiping up and down
// on the left changes the brightness, on the right the volume; the volume keys too.

type videoReq struct {
	title            string
	item             metaItem
	episode, epLabel string
	start            float64
	subs             []addonSubtitle // every subtitle offered
	subLang          string          // the one chosen ("" = off)
	job              *tjob
	direct, local    string
	from             mode
	alts             []*addonStream // next torrents to try if this one finds no peers
	src              *addonStream   // a torrent to start (by videoPrepare, off the screen's lock)
}

type videoProbe struct {
	duration   float64
	w, h       int
	sar        float64
	rate       string // ffmpeg's frame rate for the output, "24000/1001"
	rateVal    float64
	audio      int // the audio stream's index, -1 for none
	videoCodec string
}

type videoPlayer struct {
	req       videoReq
	state     string    // "install", "torrent", "probing", "starting", "playing", "paused", "seeking", "ended", "failed"
	loadAt    time.Time // when loading began (the loading screen's clock)
	textAt    time.Time // the loading screen's text was last redrawn
	spinStep  int       // the spinner's lit spoke
	spinAt    time.Time // ...since when
	msg       string
	url       string
	probe     *videoProbe
	run       *videoRun
	ctx       context.Context
	cancel    context.CancelFunc
	pos       float64 // the time of the frame on screen
	speed     float64
	light     bool // half resolution, pixel-doubled
	frame     []byte
	fw, fh    int
	fhalf     bool
	controls  bool
	ctlUntil  time.Time
	ctlGen    int
	buttons   []button // landscape coordinates
	subLangs  []string
	subLang   string
	cues      []subCue
	subMsg    string
	subImg    *ovPart
	subText   string
	hud       string
	hudUntil  time.Time
	noSound   string
	restarts  int
	lastSave  time.Time
	drops     int // frames skipped in the last stretch, to see whether it keeps up
	shown     int
	keepSince time.Time // when the current stretch of drops/shown began
	scrub     float64   // the timeline's finger: the time it points at (-1: none)
	touch     videoTouch
	ticker    bool
	pauseNext bool // show the next frame, then pause (a seek while paused)
	overlay   []*ovPart
	ovKey     string // what the overlay shows (videoOverlayKey): same key = the same parts
	ovBuilds  int    // overlays drawn, for the frame log
}

type videoTouch struct {
	down            bool
	x0, y0, x, y    int
	moved           bool
	kind            string // "", "scrub", "bright", "volume"
	startVal        int
	slot            int
	startBrightness int
	startVolume     int
	scrubbing       bool
	lastTapAt       time.Time
	lastTapX        int
}

// --- ffmpeg ------------------------------------------------------------------------------

// ffmpegInstalled: Alpine has ffmpeg and ffprobe.
func ffmpegInstalled() bool {
	if fastFFmpegReady() {
		return true
	}
	for _, p := range []string{"/usr/bin/ffmpeg", "/usr/bin/ffprobe"} {
		if _, err := os.Stat(alpineRoot + p); err != nil {
			return false
		}
	}
	return true
}

var ffmpegInstall sync.Mutex

// installFFmpeg installs the player's programs into Alpine (once): ffmpeg, and alsa-utils for
// the sound. done (if set) is called afterwards with the error. Caller holds drawMu.
func (c *console) installFFmpeg(done func(error)) {
	if c.pb.installing {
		return
	}
	c.pb.installing = true
	go func() {
		fastMu.Lock()
		fastTried = false // asked for: try the fast build again
		fastMu.Unlock()
		err := ensurePlayer(nil)
		drawMu.Lock()
		defer drawMu.Unlock()
		c.pb.installing = false
		if done != nil {
			done(err)
		} else {
			c.pbRedraw()
		}
	}()
}

func aplayInstalled() bool {
	_, err := os.Stat(alpineRoot + "/usr/bin/aplay")
	return err == nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

// probeStream is one stream as ffprobe describes it.
type probeStream struct {
	Index       int               `json:"index"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	SAR         string            `json:"sample_aspect_ratio"`
	AvgRate     string            `json:"avg_frame_rate"`
	RRate       string            `json:"r_frame_rate"`
	Disposition map[string]int    `json:"disposition"`
	Tags        map[string]string `json:"tags"`
}

// probeVideo asks ffprobe what the film is: its size, frame rate, length and sound.
func probeVideo(ctx context.Context, url string) (*videoProbe, error) {
	cmd := alpineCtxCmd(ctx, ffprobePath(), "-probesize", "3000000", "-analyzeduration", "3000000", "-v", "error", "-print_format", "json", "-show_streams", "-show_format", url)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var r struct {
		Streams []probeStream `json:"streams"`
		Format  struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if jerr := json.Unmarshal(out, &r); jerr != nil || len(r.Streams) == 0 {
		msg := "this file can't be read"
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg = lastLines(string(ee.Stderr), 1)
		} else if err != nil {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return probeFrom(r.Streams, r.Format.Duration)
}

// probeFrom picks the picture and the sound to play: the first picture (not a cover), and
// the sound marked default, else the first.
func probeFrom(streams []probeStream, duration string) (*videoProbe, error) {
	p := &videoProbe{audio: -1, sar: 1}
	p.duration, _ = strconv.ParseFloat(duration, 64)
	found, audioDef := false, false
	for _, s := range streams {
		switch s.CodecType {
		case "video":
			if found || s.Disposition["attached_pic"] == 1 || s.Width == 0 {
				continue
			}
			found = true
			p.w, p.h, p.videoCodec = s.Width, s.Height, s.CodecName
			if a, b, ok := strings.Cut(s.SAR, ":"); ok {
				x, _ := strconv.ParseFloat(a, 64)
				y, _ := strconv.ParseFloat(b, 64)
				if x > 0 && y > 0 {
					p.sar = x / y
				}
			}
			p.rate, p.rateVal = outputRate(s.AvgRate, s.RRate)
		case "audio":
			def := s.Disposition["default"] == 1
			if p.audio < 0 || def && !audioDef {
				p.audio, audioDef = s.Index, def
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("no picture in this file")
	}
	return p, nil
}

// outputRate is the frame rate shown: the film's own, at most 30 (this processor), as
// ffmpeg's fraction and as a number.
func outputRate(avg, r string) (string, float64) {
	parse := func(s string) float64 {
		a, b, ok := strings.Cut(s, "/")
		x, _ := strconv.ParseFloat(a, 64)
		if !ok {
			return x
		}
		y, _ := strconv.ParseFloat(b, 64)
		if y == 0 {
			return 0
		}
		return x / y
	}
	for _, s := range []string{avg, r} {
		switch v := parse(s); {
		case v >= 10 && v <= 31:
			return s, v
		case v > 31 && v <= 61: // 50 and 60 frames a second: every other one
			return strconv.FormatFloat(v/2, 'f', 3, 64), v / 2
		}
	}
	return "25", 25
}

// fitFrame is the size ffmpeg makes the frames: the film's shape, as large as fits w x h,
// in even numbers.
func fitFrame(p *videoProbe, w, h int) (int, int) {
	dw := float64(p.w) * p.sar
	dh := float64(p.h)
	if dw <= 0 || dh <= 0 {
		return w &^ 1, h &^ 1
	}
	k := math.Min(float64(w)/dw, float64(h)/dh)
	fw, fh := int(dw*k)&^1, int(dh*k)&^1
	return max(fw, 2), max(fh, 2)
}

// --- a run: one ffmpeg for the picture, one for the sound -----------------------------------
//
// Two processes, each reading the film: one ffmpeg feeding both would stop decoding sound
// while the picture waits for the sound's clock (its frames are 8 MB, its pipes 64 KB).

const (
	audioRate      = 48000
	audioBytesSec  = audioRate * 4 // 16-bit stereo
	audioLatency   = 0.3           // seconds between handing sound to aplay and hearing it
	aplayBufferUS  = "250000"
	aplayPeriodUS  = "50000"
	maxFrameDrops  = 4
	ctlShowFor     = 4 * time.Second
	videoSaveEvery = 10 * time.Second
)

type videoRun struct {
	vcmd    *exec.Cmd // the picture
	acmd    *exec.Cmd // the sound
	aplay   *exec.Cmd
	vout    io.ReadCloser
	aout    io.ReadCloser
	aplayW  *os.File
	fw, fh  int
	half    bool
	rate    float64
	base    float64 // the film's time where this run starts
	speed   float64
	written atomic.Int64 // sound bytes handed to aplay
	audio   atomic.Bool  // the sound is playing (and sets the clock)
	volume  atomic.Int32
	mu      sync.Mutex
	cond    *sync.Cond
	paused  bool
	killed  bool
	t0      time.Time
	started bool
	pausedT time.Duration
	pauseAt time.Time
	errs    lineRing
	frames  int // shown, under drawMu
	// The frame log (under drawMu): every 10 s, frames shown, what a redraw cost.
	statAt     time.Time
	statFrames int
	statBuilds int
	drawT      time.Duration
	done       chan struct{}
	soundErr   string
	soundEnd   atomic.Bool  // the sound's ffmpeg has finished (not an error)
	writing    atomic.Int64 // when the write to aplay now under way began (unix ns), 0 if none

	// Instant pause (2026-11-08; user: pause and play took ~1 s): the chip holds ~0.5 s of sound
	// queued, so pausing kills aplay (the queue is dropped at once) and playing starts a new one
	// from the moment last heard, resent from ring. Under mu.
	ctx        context.Context
	ring       []byte // the last soundRing bytes read from ffmpeg, by stream offset
	read       int64  // sound bytes read from ffmpeg (the ring's end)
	aplayGen   int    // bumped when an aplay is dropped or started
	aplayBase  int64  // written when the current aplay started
	statusPath string // /proc/asound/cardC/pcmDp/sub0/status of the sound device ("" = unknown)
	hwFrames   int64  // the chip's hw_ptr (frames played by the current aplay)
	hwAt       time.Time
	hwOK       bool    // hwFrames/hwAt are read from statusPath
	dropped    bool    // paused with the sound's aplay dropped: a new one starts on play
	pauseClock float64 // the clock while paused with the sound dropped
}

// soundRing is how much sound is kept to resend after a pause (well over the chip's queue).
const soundRing = audioBytesSec * 2

// soundStuckAfter: a write to aplay blocked this long means the output has stopped taking
// sound (a call output, a wedged driver). The first write waits for the device to open,
// which can take a moment the first time (the sound DSP's firmware loads).
var soundStuckAfter = 4 * time.Second // (tests make it shorter)

type lineRing struct {
	mu    sync.Mutex
	lines []string
}

func (l *lineRing) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, s)
	if len(l.lines) > 12 {
		l.lines = l.lines[1:]
	}
}

func (l *lineRing) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(l.lines[i]); s != "" {
			return s
		}
	}
	return ""
}

func seekArgs(url string, base float64) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if base > 0.5 {
		args = append(args, "-ss", strconv.FormatFloat(base, 'f', 3, 64))
	}
	return append(args, "-i", url)
}

// ffmpegVideoArgs: the picture from base seconds, as raw frames of fw x fh (the panel's
// pixel layout, bgr0), at the output frame rate.
func ffmpegVideoArgs(url string, p *videoProbe, base, speed float64, fw, fh int) []string {
	var vf []string
	if speed != 1 {
		vf = append(vf, "setpts=PTS/"+strconv.FormatFloat(speed, 'f', 2, 64))
	}
	vf = append(vf, "fps="+p.rate, fmt.Sprintf("scale=%d:%d:flags=fast_bilinear", fw, fh), "setsar=1", "format=bgr0")
	// Decoder options go before -i: skipping the loop filter and the fast flag cost a little
	// sharpness and save a lot of time on this processor.
	in := seekArgs(url, base)
	args := append(in[:4:4], "-flags2", "+fast", "-skip_loop_filter", "all")
	args = append(args, in[4:]...)
	return append(args, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", strings.Join(vf, ","), "-f", "rawvideo", "pipe:1")
}

// ffmpegAudioArgs: the sound from base seconds, as 16-bit stereo at 48 kHz.
func ffmpegAudioArgs(url string, p *videoProbe, base, speed float64) []string {
	args := append(seekArgs(url, base), "-map", "0:"+strconv.Itoa(p.audio), "-vn", "-sn", "-dn")
	if speed != 1 {
		args = append(args, "-af", "atempo="+strconv.FormatFloat(speed, 'f', 2, 64))
	}
	return append(args, "-ac", "2", "-ar", strconv.Itoa(audioRate), "-f", "s16le", "pipe:1")
}

// aplayArgs plays raw 16-bit stereo from its input through ALSA's default device.
var aplayArgs = []string{"-q", "-t", "raw", "-f", "S16_LE", "-c", "2", "-r", strconv.Itoa(audioRate),
	"-B", aplayBufferUS, "-F", aplayPeriodUS, "-"}

// aplayPath is the program the sound goes to (tests replace it).
var aplayPath = "/usr/bin/aplay"

func startRun(ctx context.Context, url string, p *videoProbe, base, speed float64, fw, fh int, half bool, volume int) (*videoRun, error) {
	run := &videoRun{fw: fw, fh: fh, half: half, rate: p.rateVal, base: base, speed: speed, done: make(chan struct{})}
	run.cond = sync.NewCond(&run.mu)
	run.volume.Store(int32(volume))
	vcmd := alpineCtxCmd(ctx, ffmpegPath(), ffmpegVideoArgs(url, p, base, speed, fw, fh)...)
	out, err := vcmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, _ := vcmd.StderrPipe()
	if err := vcmd.Start(); err != nil {
		return nil, fmt.Errorf("can't start ffmpeg: %v", err)
	}
	run.vcmd, run.vout = vcmd, out
	go logLines(stderr, "ffmpeg", &run.errs)
	if p.audio >= 0 {
		run.startSound(ctx, url, p)
	}
	return run, nil
}

func logLines(r io.Reader, who string, ring *lineRing) {
	if r == nil {
		return
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		t := sc.Text()
		if strings.Contains(t, "underrun") {
			continue
		}
		if ring != nil {
			ring.add(t)
		}
		log.Printf("%s: %s", who, t)
	}
}

// startSound starts the sound's ffmpeg and aplay; if they can't play, the film goes on
// without sound.
func (r *videoRun) startSound(ctx context.Context, url string, p *videoProbe) {
	if _, err := os.Stat(alpineRoot + aplayPath); err != nil {
		r.soundErr = "no sound: aplay isn't installed"
		return
	}
	r.ctx, r.statusPath = ctx, soundStatusPath(soundDev())
	r.mu.Lock()
	err := r.openAplayLocked()
	r.mu.Unlock()
	if err != nil {
		r.soundErr = "no sound: " + err.Error()
		return
	}
	acmd := alpineCtxCmd(ctx, ffmpegPath(), ffmpegAudioArgs(url, p, r.base, r.speed)...)
	aout, err := acmd.StdoutPipe()
	if err == nil {
		aerr, _ := acmd.StderrPipe()
		if err = acmd.Start(); err == nil {
			go logLines(aerr, "ffmpeg (sound)", nil)
		}
	}
	if err != nil {
		r.mu.Lock()
		if r.aplay != nil && r.aplay.Process != nil {
			r.aplay.Process.Kill()
		}
		r.aplayW.Close()
		r.mu.Unlock()
		r.soundErr = "no sound: " + err.Error()
		return
	}
	r.acmd, r.aout = acmd, aout
	r.audio.Store(true)
	go r.pumpSound()
	go r.soundWatch()
	go r.watchChip()
	go acmd.Wait()
}

// openAplayLocked starts an aplay on a fresh short pipe; the sound written from now on goes to
// it. Its end is reported (no sound) unless it was dropped on purpose (a pause, a newer one).
// Caller holds mu.
func (r *videoRun) openAplayLocked() error {
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	setPipeSize(pw, 16<<10) // a short pipe: what's heard is close to what was written
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a := alpineCtxCmd(ctx, aplayPath, filmAplayArgs(soundDev())...)
	a.Stdin = pr
	var errs lineRing
	ep, _ := a.StderrPipe()
	if err := a.Start(); err != nil {
		pr.Close()
		pw.Close()
		return err
	}
	pr.Close()
	r.aplayGen++
	gen := r.aplayGen
	r.aplay, r.aplayW, r.aplayBase = a, pw, r.written.Load()
	r.hwFrames, r.hwAt, r.hwOK = 0, time.Now(), r.statusPath != "" && chipStatusReadable(r.statusPath)
	go func() {
		logLines(ep, "aplay", &errs)
		a.Wait()
		r.mu.Lock()
		ours := gen == r.aplayGen
		if ours && !r.killed && r.audio.Load() && !r.soundEnd.Load() {
			r.soundErr = "no sound: " + strings.TrimPrefix(errs.last(), "aplay: ")
			if errs.last() == "" {
				r.soundErr = "no sound: the sound device didn't open"
			}
		}
		r.mu.Unlock()
		if ours {
			r.soundLost()
		}
	}()
	return nil
}

// heardLocked is how much of the sound has been heard, in bytes of the stream: from the chip's
// play position (hw_ptr, which moves a period at a time, filled in with the wall clock up to one
// period) when it can be read, else written minus the usual latency. Never past what was
// written. Caller holds mu.
func (r *videoRun) heardLocked() int64 {
	w := r.written.Load()
	var h int64
	if r.hwOK {
		extra := math.Min(time.Since(r.hwAt).Seconds(), chipPeriod)
		h = r.aplayBase + r.hwFrames*4 + int64(extra*audioBytesSec)&^3
	} else {
		h = w - int64(audioLatency*audioBytesSec)&^3
		h = max(h, r.aplayBase)
	}
	return max(min(h, w), 0)
}

// chipPeriod is how far the clock runs on between two reads of the chip's position (its period
// is 6000 frames, 0.125 s, on this tablet).
const chipPeriod = 0.15

// soundStatusPath is where the kernel shows a device's play position: plughw:4,0 →
// /proc/asound/card4/pcm0p/sub0/status.
func soundStatusPath(dev string) string {
	d := strings.TrimPrefix(strings.TrimPrefix(dev, "plughw:"), "hw:")
	card, devn, ok := strings.Cut(d, ",")
	if !ok || card == "" || devn == "" {
		return ""
	}
	return "/proc/asound/card" + card + "/pcm" + devn + "p/sub0/status"
}

func chipStatusReadable(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// chipHwPtr reads hw_ptr from a status file ("closed" or unreadable: false).
func chipHwPtr(path string) (int64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "hw_ptr" {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// watchChip reads the chip's play position every 20 ms, for the clock.
func (r *videoRun) watchChip() {
	if r.statusPath == "" {
		return
	}
	for !r.isKilled() && !r.soundEnd.Load() {
		time.Sleep(20 * time.Millisecond)
		r.mu.Lock()
		gen := r.aplayGen
		r.mu.Unlock()
		n, ok := chipHwPtr(r.statusPath)
		r.mu.Lock()
		if ok && gen == r.aplayGen && !r.dropped && n != r.hwFrames {
			r.hwFrames, r.hwAt, r.hwOK = n, time.Now(), true
		}
		r.mu.Unlock()
	}
}

// soundWatch drops the sound when the output stops taking it: the sound is the clock, so
// a stuck output froze the film (2026-10-29). The film carries on silent, on the wall clock.
func (r *videoRun) soundWatch() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		if r.isKilled() || !r.audio.Load() || r.soundEnd.Load() {
			return
		}
		w := r.writing.Load()
		if w == 0 || time.Since(time.Unix(0, w)) < soundStuckAfter {
			continue
		}
		log.Printf("playbox: %s stopped taking sound: carrying on without it", soundDev())
		r.mu.Lock()
		r.soundErr = "no sound: the output stopped taking sound (stuck); pick another in Playbox > Settings"
		r.mu.Unlock()
		r.soundLost()
		r.mu.Lock()
		ap, aw := r.aplay, r.aplayW
		r.mu.Unlock()
		if ap != nil && ap.Process != nil {
			ap.Process.Kill()
		}
		if aw != nil {
			aw.Close() // the blocked write returns
		}
		return
	}
}

// soundLost switches the clock from the sound to the wall, without a jump.
func (r *videoRun) soundLost() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.audio.Load() {
		return
	}
	now := r.clockLocked()
	r.audio.Store(false)
	r.t0, r.started, r.pausedT = time.Now().Add(-time.Duration(now*float64(time.Second))), true, 0
	if r.paused {
		r.pauseAt = time.Now()
	}
}

// clock is the run's time: what has reached the speaker, or the wall clock since the first
// frame (pauses taken out).
func (r *videoRun) clock() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clockLocked()
}

func (r *videoRun) clockLocked() float64 {
	if r.audio.Load() {
		if r.dropped {
			return r.pauseClock
		}
		return float64(r.heardLocked()) / audioBytesSec
	}
	if !r.started {
		return 0
	}
	d := time.Since(r.t0) - r.pausedT
	if r.paused {
		d -= time.Since(r.pauseAt)
	}
	return d.Seconds()
}

// wait blocks while the run is paused. False once it's killed.
func (r *videoRun) wait() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.paused && !r.killed {
		r.cond.Wait()
	}
	return !r.killed
}

func (r *videoRun) setPaused(p bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == r.paused {
		return
	}
	if p && r.audio.Load() && r.aplay != nil && !r.killed {
		// What was handed to the chip but not heard yet is dropped with its aplay, and resent to
		// the next one: the sound stops at once and comes back where it stopped.
		heard := r.heardLocked()
		r.pauseClock = float64(heard) / audioBytesSec
		r.written.Store(heard)
		r.aplayGen++
		r.dropped = true
		if r.aplay.Process != nil {
			r.aplay.Process.Kill()
		}
		r.aplayW.Close()
		r.aplay, r.aplayW = nil, nil
	}
	r.paused = p
	if p {
		r.pauseAt = time.Now()
	} else {
		if !r.pauseAt.IsZero() {
			r.pausedT += time.Since(r.pauseAt)
		}
		if r.dropped && r.audio.Load() && !r.killed {
			r.dropped = false
			if err := r.openAplayLocked(); err != nil {
				r.soundErr = "no sound: " + err.Error()
				r.mu.Unlock()
				r.soundLost()
				r.mu.Lock()
			}
		}
	}
	r.cond.Broadcast()
}

// kill stops the run's programs; it doesn't wait for them.
func (r *videoRun) kill() {
	r.mu.Lock()
	if r.killed {
		r.mu.Unlock()
		return
	}
	r.killed = true
	r.cond.Broadcast()
	ap, aw := r.aplay, r.aplayW
	r.mu.Unlock()
	r.audio.Store(false)
	for _, cmd := range []*exec.Cmd{r.vcmd, r.acmd, ap} {
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	if aw != nil {
		aw.Close()
	}
}

func (r *videoRun) isKilled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.killed
}

// pumpSound copies the sound to aplay at the volume chosen.
func (r *videoRun) pumpSound() {
	buf := make([]byte, 4096)
	r.mu.Lock()
	if r.ring == nil {
		r.ring = make([]byte, soundRing)
	}
	r.mu.Unlock()
	ended := false
	for r.wait() {
		// After a pause, first what was handed over but not heard (from the ring), then on.
		r.mu.Lock()
		w, rd := r.written.Load(), r.read
		var chunk []byte
		if w < rd && rd-w <= int64(len(r.ring)) {
			o := int(w % int64(len(r.ring)))
			n := int(min(rd-w, int64(len(buf)), int64(len(r.ring)-o)))
			chunk = buf[:copy(buf, r.ring[o:o+n])]
		}
		r.mu.Unlock()
		if chunk == nil {
			if ended {
				break
			}
			n, err := io.ReadFull(r.aout, buf)
			n &^= 3
			if n > 0 {
				vol := int(r.volume.Load())
				if vol != 100 {
					for i := 0; i+1 < n; i += 2 {
						v := int(int16(binary.LittleEndian.Uint16(buf[i:]))) * vol / 100
						binary.LittleEndian.PutUint16(buf[i:], uint16(int16(min(max(v, -32768), 32767))))
					}
				}
				r.mu.Lock()
				for k := 0; k < n; {
					o := int(r.read % int64(len(r.ring)))
					c := copy(r.ring[o:], buf[k:n])
					k += c
					r.read += int64(c)
				}
				r.mu.Unlock()
				chunk = buf[:n]
			}
			if err != nil {
				ended = true
			}
			if len(chunk) == 0 {
				continue
			}
		}
		r.mu.Lock()
		aw, gen, dropped := r.aplayW, r.aplayGen, r.dropped
		r.mu.Unlock()
		if dropped {
			continue // paused just now: this goes to the next aplay (wait blocks until play)
		}
		if r.audio.Load() && aw != nil {
			r.writing.Store(time.Now().UnixNano())
			_, werr := aw.Write(chunk)
			r.writing.Store(0)
			r.mu.Lock()
			ours := gen == r.aplayGen
			r.mu.Unlock()
			if !ours {
				continue // a pause dropped that aplay: this goes again to the next one
			}
			if werr != nil {
				r.soundLost()
			}
		}
		r.mu.Lock()
		if gen == r.aplayGen {
			r.written.Add(int64(len(chunk)))
		}
		r.mu.Unlock()
	}
	r.soundEnd.Store(true)
	r.aout.Close()
	r.mu.Lock()
	aw := r.aplayW
	r.mu.Unlock()
	if aw != nil {
		aw.Close() // aplay plays what it has, then ends
	}
	// The sound ran out before the picture (or failed): the wall clock carries on.
	if !r.isKilled() {
		time.Sleep(time.Duration(audioLatency * float64(time.Second)))
		r.soundLost()
	}
}

// --- opening and closing ----------------------------------------------------------------------

// openVideo opens the player at once and gets the film ready in the background. Caller
// holds drawMu.
func (c *console) openVideo(req videoReq) {
	from := c.mode
	if c.vid != nil {
		from = c.vid.req.from
		c.stopVideo()
	}
	if from == modeVideo {
		from = modePlaybox
	}
	req.from = from
	cfg := pbSettingsNow()
	ctx, cancel := context.WithCancel(context.Background())
	v := &videoPlayer{req: req, state: "starting", speed: 1, light: cfg.Light, ctx: ctx, cancel: cancel, scrub: -1, pos: req.start, loadAt: time.Now()}
	v.subLangs = subLangs(req.subs)
	c.vid = v
	c.mode = modeVideo
	c.endGPUScroll()
	c.skb.visible = false
	if req.subLang != "" {
		c.videoSetSubs(req.subLang)
	}
	c.videoRedraw()
	go c.videoPrepare(v)
	c.videoTicker(v)
}

// videoPrepare installs ffmpeg if needed, waits for the torrent, reads what the film is, and
// starts it.
func (c *console) videoPrepare(v *videoPlayer) {
	set := func(state, msg string) bool {
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.vid != v {
			return false
		}
		v.state, v.msg = state, msg
		c.videoRedraw()
		return true
	}
	fail := func(msg string) {
		set("failed", msg)
		drawMu.Lock()
		pbLastProblem = msg
		drawMu.Unlock()
	}
	// The torrent starts here, not when the source was tapped: starting the client and adding
	// the magnet can take seconds, and the player is already on screen meanwhile.
	if v.req.job == nil && v.req.src != nil {
		if !set("torrent", "") {
			return
		}
		j, err := engine.start(v.req.src, false, "", v.req.item)
		drawMu.Lock()
		if c.vid != v {
			drawMu.Unlock()
			if err == nil {
				engine.stopPlay(j)
			}
			return
		}
		if err == nil {
			v.req.job = j
		}
		drawMu.Unlock()
		if err != nil {
			fail(err.Error())
			return
		}
	}
	if !ffmpegInstalled() || !aplayInstalled() || !fastFFmpegReady() && !fastTried {
		if !set("install", "Getting the video player ready (once)…") {
			return
		}
		if err := ensurePlayer(func(msg string) { set("install", msg) }); err != nil {
			fail(err.Error())
			return
		}
	}
	setupSound()
	j := v.req.job
	if j != nil {
		if !set("torrent", "") {
			return
		}
		since := time.Now()
		for {
			st := engine.status(j)
			if st.State == "ready" || st.State == "downloading" || st.State == "saved" {
				break
			}
			// No peers after a while (or a failure): the next best source, by itself.
			stuck := st.State == "resolving" && (time.Since(since) > 25*time.Second && st.All == 0 || time.Since(since) > 50*time.Second && st.Peers == 0)
			if (stuck || st.State == "failed") && len(v.req.alts) > 0 {
				next := v.req.alts[0]
				nj, err := engine.start(next, false, "", v.req.item)
				drawMu.Lock()
				if c.vid != v {
					drawMu.Unlock()
					if err == nil {
						engine.stopPlay(nj)
					}
					return
				}
				v.req.alts = v.req.alts[1:]
				if err == nil {
					engine.stopPlay(j)
					j, v.req.job, since = nj, nj, time.Now()
					seeds := "seeders unknown"
					if n := next.seeders(); n >= 0 {
						seeds = groupDigits(n) + " seeders"
					}
					c.toastVideo("No peers: trying " + next.quality() + " · " + seeds)
					log.Printf("playbox: no peers for %s: trying the next source", st.Name)
				}
				drawMu.Unlock()
				continue
			}
			if st.State == "failed" {
				fail(st.Err)
				return
			}
			select {
			case <-v.ctx.Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	var url string
	var err error
	if j != nil && engine.status(j).State == "saved" { // it finished downloading meanwhile
		url, err = engine.streamURL(nil, "", engine.status(j).Saved)
	} else {
		url, err = engine.streamURL(j, v.req.direct, v.req.local)
	}
	if err != nil {
		fail(err.Error())
		return
	}
	if !set("probing", "") {
		return
	}
	p, err := probeVideo(v.ctx, url)
	if v.ctx.Err() != nil {
		return
	}
	if err != nil {
		fail("This film can't be played: " + err.Error())
		return
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	if c.vid != v {
		return
	}
	v.url, v.probe = url, p
	if p.h > 720 || strings.Contains(strings.ToLower(p.videoCodec), "hevc") {
		v.light = true
	}
	// A film no bigger than half the panel (480p, 576p) is made at half size and doubled on
	// the screen: the same picture for a quarter of the work (ffmpeg scaled 480p up to
	// 1920x1080: 8 MB a frame through the pipe, too much for the Atom).
	if dw := float64(p.w) * p.sar; dw > 0 && dw <= float64(c.s.fbW)/2*1.1 && p.h <= c.s.fbH/2 {
		v.light = true
	}
	if p.duration > 0 && v.pos > p.duration-10 {
		v.pos = 0 // it had ended: from the start
	}
	c.videoStart(v.pos, false)
}

// videoStart starts ffmpeg at t seconds; with keepPaused, the first frame shows and the film
// stays paused. Caller holds drawMu.
func (c *console) videoStart(t float64, keepPaused bool) {
	v := c.vid
	if v == nil || v.probe == nil {
		return
	}
	if v.run != nil {
		v.run.kill()
		v.run = nil
	}
	tw, th := c.s.fbW, c.s.fbH
	if v.light {
		tw, th = tw/2, th/2
	}
	fw, fh := fitFrame(v.probe, tw, th)
	run, err := startRun(v.ctx, v.url, v.probe, t, v.speed, fw, fh, v.light, pbSettingsNow().Volume)
	if err != nil {
		v.state, v.msg = "failed", err.Error()
		c.videoRedraw()
		return
	}
	v.run, v.pos, v.pauseNext = run, t, keepPaused
	if v.frame == nil {
		v.state = "starting"
	} else {
		v.state = "seeking"
	}
	v.drops, v.shown, v.keepSince = 0, 0, time.Time{}
	go c.videoFrames(v, run)
	go c.videoWait(v, run)
}

// videoFrames reads the run's frames and shows each at its time.
func (c *console) videoFrames(v *videoPlayer, run *videoRun) {
	defer close(run.done)
	size := run.fw * run.fh * 4
	bufs := [2][]byte{make([]byte, size), make([]byte, size)}
	dropped := 0
	for i := 0; ; i++ {
		if !run.wait() {
			return
		}
		buf := bufs[i%2]
		if _, err := io.ReadFull(run.vout, buf); err != nil {
			return
		}
		run.mu.Lock()
		if !run.started && !run.audio.Load() {
			run.t0, run.started = time.Now(), true
		}
		run.mu.Unlock()
		ft := float64(i) / run.rate
		for i > 0 {
			now := run.clock()
			if now >= ft-0.004 || run.isKilled() {
				break
			}
			if !run.wait() {
				return
			}
			time.Sleep(time.Duration(math.Min(ft-now, 0.04) * float64(time.Second)))
		}
		late := run.clock() - ft
		drop := late > 2/run.rate && dropped < maxFrameDrops && i > 0
		drawMu.Lock()
		if c.vid != v || v.run != run {
			drawMu.Unlock()
			return
		}
		if drop {
			dropped++
			v.drops++
			drawMu.Unlock()
			continue
		}
		dropped = 0
		v.shown++
		run.frames++
		v.frame, v.fw, v.fh, v.fhalf = buf, run.fw, run.fh, run.half
		v.pos = run.base + ft*run.speed
		if v.state == "starting" || v.state == "seeking" {
			v.state = "playing"
		}
		if v.pauseNext { // a seek while paused: this frame, then paused again
			v.pauseNext = false
			v.state = "paused"
			run.setPaused(true)
		}
		if run.soundErr != "" && v.noSound == "" {
			v.noSound = run.soundErr
			pbLastProblem = run.soundErr + " (on " + soundDev() + ")"
			log.Printf("playbox: %s", run.soundErr)
		}
		c.lastInput = time.Now() // a film playing keeps the screen on
		t0 := time.Now()
		c.videoCompose()
		run.drawT += time.Since(t0)
		if run.statAt.IsZero() {
			run.statAt = t0
		}
		if d := time.Since(run.statAt); d >= 10*time.Second {
			// What the tablet keeps up with: frames shown and dropped, and what a redraw costs.
			n := run.frames - run.statFrames
			log.Printf("playbox: picture %dx%d%s at %.1f of %.0f frames a second (%d dropped), redraw %.0f ms, overlay drawn %d times",
				run.fw, run.fh, map[bool]string{true: " doubled", false: ""}[run.half], float64(n)/d.Seconds(), run.rate,
				v.drops, float64(run.drawT.Milliseconds())/float64(max(n, 1)), v.ovBuilds-run.statBuilds)
			run.statAt, run.statFrames, run.statBuilds, run.drawT = time.Now(), run.frames, v.ovBuilds, 0
		}
		c.videoKeepUp(v)
		if time.Since(v.lastSave) > videoSaveEvery {
			c.videoSavePos()
		}
		drawMu.Unlock()
	}
}

// videoKeepUp watches how many frames are skipped: if more than a third over ten seconds,
// the picture goes lighter (half the resolution), at the same time. Caller holds drawMu.
func (c *console) videoKeepUp(v *videoPlayer) {
	if v.light || v.state != "playing" || v.run == nil {
		return
	}
	if v.keepSince.IsZero() {
		v.keepSince = time.Now()
	}
	if time.Since(v.keepSince) < 6*time.Second {
		return
	}
	n := v.drops + v.shown
	if n >= 3 && v.drops*3 > n {
		log.Printf("playbox: %d of %d frames late: lighter picture", v.drops, n)
		v.light = true
		c.toastVideo("Lighter picture, to keep up")
		c.videoStart(v.pos, false)
		return
	}
	v.drops, v.shown, v.keepSince = 0, 0, time.Time{}
}

// videoWait waits for the picture's ffmpeg to end and says how: the end of the film, or a
// failure (a broken stream starts again where it stopped).
func (c *console) videoWait(v *videoPlayer, run *videoRun) {
	err := run.vcmd.Wait()
	<-run.done
	drawMu.Lock()
	defer drawMu.Unlock()
	if c.vid != v || v.run != run {
		run.kill()
		return
	}
	if err == nil && run.audio.Load() { // the picture ended: let the sound finish its last moment
		drawMu.Unlock()
		time.Sleep(time.Duration(audioLatency * float64(time.Second)))
		drawMu.Lock()
		if c.vid != v || v.run != run {
			run.kill()
			return
		}
	}
	run.kill()
	v.run = nil
	near := v.probe.duration <= 0 || v.pos >= v.probe.duration-8
	switch {
	case err == nil && near:
		c.videoEnded()
	case run.frames > 0 && v.restarts < 3 && !near:
		// The stream broke off (a torrent stalling, a connection dropped): carry on.
		v.restarts++
		log.Printf("playbox: ffmpeg stopped at %s (%v): starting again", clockText(v.pos), err)
		c.videoStart(v.pos, false)
		return
	case err == nil:
		c.videoEnded()
	default:
		msg := run.errs.last()
		if msg == "" {
			msg = err.Error()
		}
		v.state, v.msg = "failed", "Playback failed: "+msg
		pbLastProblem = v.msg
		v.controls = true
	}
	c.videoRedraw()
}

func (c *console) videoEnded() {
	v := c.vid
	v.state = "ended"
	if v.probe.duration > 0 {
		v.pos = v.probe.duration
	}
	c.videoSavePos()
	v.controls = true
}

// videoTicker redraws the player while no picture is moving: about 30 times a second while it
// loads or seeks (the spinner turns, the clock and the numbers update live), slower if a redraw
// takes longer, so touches still get the screen; otherwise four times a second, to hide the
// controls and the HUD on time.
func (c *console) videoTicker(v *videoPlayer) {
	if v.ticker {
		return
	}
	v.ticker = true
	go func() {
		for {
			wait := 250 * time.Millisecond
			drawMu.Lock()
			if c.vid != v {
				drawMu.Unlock()
				return
			}
			switch {
			case v.frame == nil && v.state != "failed" || v.state == "seeking" || v.state == "starting":
				// The spinner alone at every step; the text (and everything live) 4 times a second.
				t0 := time.Now()
				text := time.Since(v.textAt) >= 250*time.Millisecond
				if text {
					v.textAt = t0
				}
				c.videoLoadingFrame(text)
				wait = max(spinStepEvery/3, 2*time.Since(t0))
			case v.controls && v.state == "playing" && time.Now().After(v.ctlUntil) && !v.touch.down:
				v.controls = false
				c.videoRedraw()
			case v.hud != "" && time.Now().After(v.hudUntil):
				v.hud = ""
				c.videoRedraw()
			}
			drawMu.Unlock()
			time.Sleep(wait)
		}
	}()
}

// closeVideo stops the film and goes back to where it was opened. Caller holds drawMu.
func (c *console) closeVideo() {
	v := c.vid
	if v == nil {
		return
	}
	from := v.req.from
	if from == modeVideo {
		from = modePlaybox
	}
	c.stopVideo()
	c.setMode(from)
}

// stopVideo stops the film (where it is is kept), without changing the screen. Caller holds
// drawMu.
func (c *console) stopVideo() {
	v := c.vid
	if v == nil {
		return
	}
	c.videoSavePos()
	if v.run != nil {
		v.run.kill()
	}
	v.cancel()
	c.vid = nil
	engine.stopPlay(v.req.job)
	c.s.shadowStale = true // the frames went out without it
}

// videoSavePos keeps where the film is, for Continue Watching. Caller holds drawMu.
func (c *console) videoSavePos() {
	v := c.vid
	if v == nil || v.probe == nil || v.probe.duration <= 0 || v.req.item.ID == "" {
		return
	}
	v.lastSave = time.Now()
	it, ep, label, pos, dur := v.req.item, v.req.episode, v.req.epLabel, v.pos, v.probe.duration
	go pbLib.played(it, ep, label, pos, dur)
}

// --- controls -------------------------------------------------------------------------------

func (c *console) videoShowControls(on bool) {
	v := c.vid
	v.controls = on
	v.ctlUntil = time.Now().Add(ctlShowFor)
}

func (c *console) videoTogglePause() {
	v := c.vid
	switch {
	case v.state == "ended":
		v.restarts = 0
		c.videoStart(0, false)
	case v.state == "failed":
		v.restarts, v.msg = 0, ""
		if v.probe == nil {
			v.state, v.loadAt = "starting", time.Now()
			go c.videoPrepare(v)
		} else {
			c.videoStart(v.pos, false)
		}
	case v.state == "paused":
		v.state = "playing"
		if v.run == nil {
			c.videoStart(v.pos, false)
		} else {
			v.run.setPaused(false)
		}
		v.ctlUntil = time.Now().Add(ctlShowFor)
	case v.run != nil:
		v.state = "paused"
		v.run.setPaused(true)
		c.videoSavePos()
	}
}

// videoSeek goes to t seconds (paused stays paused, on the new frame). Caller holds drawMu.
func (c *console) videoSeek(t float64) {
	v := c.vid
	if v.probe == nil || v.state == "failed" && v.frame == nil {
		return
	}
	if v.probe.duration > 0 {
		t = min(t, v.probe.duration-1)
	}
	t = max(t, 0)
	v.restarts = 0
	c.videoStart(t, v.state == "paused")
	v.ctlUntil = time.Now().Add(ctlShowFor)
}

// videoRestart starts the film again where it is (after a change of speed or picture),
// paused if it was. Caller holds drawMu.
func (c *console) videoRestart() {
	v := c.vid
	if v.probe == nil || v.state == "ended" || v.state == "failed" {
		return
	}
	c.videoStart(v.pos, v.state == "paused")
}

// videoSetSubs picks a subtitle language and loads its first file. Caller holds drawMu.
func (c *console) videoSetSubs(lang string) {
	v := c.vid
	v.subLang, v.cues, v.subMsg, v.subText = lang, nil, "", ""
	if lang == "" {
		return
	}
	files := subsFor(v.req.subs, lang)
	if len(files) == 0 {
		return
	}
	v.subMsg = "Loading subtitles…"
	go func() {
		var cues []subCue
		var err error
		for _, f := range files[:min(len(files), 3)] { // the first that reads
			if cues, err = loadSubtitles(f); err == nil && len(cues) > 0 {
				break
			}
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.vid != v || v.subLang != lang {
			return
		}
		v.cues, v.subMsg = cues, ""
		if len(cues) == 0 {
			v.subMsg = "Subtitle failed to load"
			if err == nil {
				v.subMsg = "No readable subtitle cues"
			}
		}
		c.videoRedraw()
	}()
}

func (c *console) toastVideo(s string) {
	v := c.vid
	if v == nil {
		return
	}
	v.hud, v.hudUntil = s, time.Now().Add(2500*time.Millisecond)
}

var videoSpeeds = []float64{1, 1.25, 1.5, 2, 0.5, 0.75}

// videoTap handles a control. Caller holds drawMu.
func (c *console) videoTap(id string) {
	v := c.vid
	v.ctlUntil = time.Now().Add(ctlShowFor)
	switch id {
	case "v:close":
		c.closeVideo()
		return
	case "v:play":
		c.videoTogglePause()
	case "v:back":
		c.videoSeek(v.pos - 10)
	case "v:fwd":
		c.videoSeek(v.pos + 10)
	case "v:cc":
		langs := append([]string{""}, v.subLangs...)
		next := 0
		for i, l := range langs {
			if l == v.subLang {
				next = (i + 1) % len(langs)
			}
		}
		c.videoSetSubs(langs[next])
		if langs[next] == "" {
			c.toastVideo("Subtitles off")
		} else {
			c.toastVideo("Subtitles: " + langName(langs[next]))
		}
	case "v:speed":
		next := 0
		for i, s := range videoSpeeds {
			if s == v.speed {
				next = (i + 1) % len(videoSpeeds)
			}
		}
		v.speed = videoSpeeds[next]
		c.toastVideo(strconv.FormatFloat(v.speed, 'g', -1, 64) + "×")
		c.videoRestart()
	case "v:flip":
		cfg := pbSettingsNow()
		cfg.Flip = !cfg.Flip
		cfg.save()
	case "v:light":
		v.light = !v.light
		c.videoRestart()
		c.toastVideo(map[bool]string{true: "Lighter picture", false: "Full picture"}[v.light])
	}
	c.videoRedraw()
}

// videoKey: the volume keys change the volume while a film plays. Caller holds drawMu.
func (c *console) videoKey(up bool) {
	cfg := pbSettingsNow()
	cfg.Volume = min(max(cfg.Volume+map[bool]int{true: 10, false: -10}[up], 0), 150)
	cfg.save()
	if c.vid.run != nil {
		c.vid.run.volume.Store(int32(cfg.Volume))
	}
	c.toastVideo(fmt.Sprintf("Volume %d%%", cfg.Volume))
	c.videoRedraw()
}

// landscape turns a touch (portrait) into the picture's own coordinates.
func (c *console) landscape(x, y int) (int, int) {
	fx, fy := c.s.rot.toFB(x, y, c.s.fbW, c.s.fbH)
	if pbSettingsNow().Flip {
		return c.s.fbW - 1 - fx, c.s.fbH - 1 - fy
	}
	return fx, fy
}

// videoTouch handles a finger on the player. Caller holds drawMu.
func (c *console) videoTouch(p TouchPoint) {
	v := c.vid
	if v == nil {
		return
	}
	t := &v.touch
	x, y := c.landscape(p.X, p.Y)
	W, H := c.s.fbW, c.s.fbH
	switch {
	case p.Down:
		*t = videoTouch{down: true, x0: x, y0: y, x: x, y: y, slot: p.Slot, lastTapAt: t.lastTapAt, lastTapX: t.lastTapX}
		t.startBrightness, t.startVolume = c.cfg.Brightness, pbSettingsNow().Volume
		if v.controls && v.probe != nil && v.probe.duration > 0 {
			if tr := c.videoTrack(); (image.Point{x, y}).In(tr.Inset(-40)) {
				t.kind, t.scrubbing = "scrub", true
				v.scrub = c.trackTime(x)
				c.videoRedraw()
			}
		}
	case p.Moved && t.down:
		t.x, t.y = x, y
		dx, dy := x-t.x0, y-t.y0
		if !t.moved && abs(dx) < 24 && abs(dy) < 24 {
			return
		}
		t.moved = true
		switch {
		case t.kind == "scrub":
			v.scrub = c.trackTime(x)
			v.ctlUntil = time.Now().Add(ctlShowFor)
			c.videoRedraw()
		case t.kind == "" && !pbSettingsNow().NoGestures && abs(dy) > abs(dx):
			if t.x0 < W/2 {
				t.kind = "bright"
			} else {
				t.kind = "volume"
			}
		}
		switch t.kind {
		case "bright":
			b := min(max(t.startBrightness-dy*100/(H*2/3), 5), 100)
			if b != c.cfg.Brightness {
				c.cfg.Brightness = b
				setBacklight(b)
			}
			v.hud, v.hudUntil = fmt.Sprintf("☀ %d%%", b), time.Now().Add(800*time.Millisecond)
			c.videoRedraw()
		case "volume":
			vol := min(max(t.startVolume-dy*150/(H*2/3), 0), 150)
			cfg := pbSettingsNow()
			if vol != cfg.Volume {
				cfg.Volume = vol
				if v.run != nil {
					v.run.volume.Store(int32(vol))
				}
			}
			v.hud, v.hudUntil = fmt.Sprintf("Volume %d%%", vol), time.Now().Add(800*time.Millisecond)
			c.videoRedraw()
		}
	case p.Up && t.down:
		t.down = false
		switch {
		case t.kind == "scrub":
			at := v.scrub
			v.scrub = -1
			if t.moved || at >= 0 {
				c.videoSeek(at)
			}
			c.videoRedraw()
			return
		case t.kind == "bright":
			c.cfg.save()
			return
		case t.kind == "volume":
			pbSettingsNow().save()
			return
		case t.moved:
			return
		}
		// A tap: a control, or the controls shown/hidden.
		for _, b := range v.buttons {
			if (image.Point{x, y}).In(b.r) {
				c.videoTap(b.id)
				return
			}
		}
		// Two quick taps on a side: back or forward 10 seconds.
		if time.Since(t.lastTapAt) < 350*time.Millisecond && v.probe != nil && (x < W/3) == (t.lastTapX < W/3) && (x < W/3 || x > W*2/3) {
			if x < W/3 {
				c.videoSeek(v.pos - 10)
				c.toastVideo("−10 s")
			} else {
				c.videoSeek(v.pos + 10)
				c.toastVideo("+10 s")
			}
			t.lastTapAt = time.Time{}
			c.videoRedraw()
			return
		}
		t.lastTapAt, t.lastTapX = time.Now(), x
		if v.state == "playing" || v.state == "seeking" || v.state == "starting" {
			c.videoShowControls(!v.controls)
		} else {
			c.videoShowControls(true)
		}
		c.videoRedraw()
	}
}

// --- drawing --------------------------------------------------------------------------------

// The overlay is drawn in small transparent pictures (the bars, the buttons, a subtitle)
// in landscape coordinates, laid over each frame where they have ink.

type ovSpan struct{ x0, x1 int }

type ovPart struct {
	img   *image.RGBA // premultiplied
	x, y  int
	spans [][]ovSpan
	live  bool // part of the loading screen, redrawn alone a few times a second
	spin  bool // the spinner, redrawn alone at every step
}

func newOvPart(w, h, x, y int) *ovPart {
	return &ovPart{img: image.NewRGBA(image.Rect(0, 0, w, h)), x: x, y: y}
}

// seal works out where the part has ink, row by row, so blending skips the rest.
func (o *ovPart) seal() *ovPart {
	b := o.img.Rect
	o.spans = make([][]ovSpan, b.Dy())
	for y := 0; y < b.Dy(); y++ {
		row := o.img.Pix[y*o.img.Stride:]
		in := -1
		for x := 0; x <= b.Dx(); x++ {
			ink := x < b.Dx() && row[4*x+3] != 0
			if ink && in < 0 {
				in = x
			} else if !ink && in >= 0 {
				o.spans[y] = append(o.spans[y], ovSpan{in, x})
				in = -1
			}
		}
	}
	return o
}

// ovRound fills a rounded rectangle with a translucent colour (premultiplied), anti-aliased.
func ovRound(img *image.RGBA, r image.Rectangle, rad int, c color.RGBA) {
	r = r.Intersect(img.Rect)
	rad = min(rad, r.Dx()/2, r.Dy()/2)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cov := 1.0
			cx, cy := float64(x)+0.5, float64(y)+0.5
			ccx := math.Max(float64(r.Min.X+rad), math.Min(cx, float64(r.Max.X-rad)))
			ccy := math.Max(float64(r.Min.Y+rad), math.Min(cy, float64(r.Max.Y-rad)))
			if d := math.Hypot(cx-ccx, cy-ccy); d > float64(rad)-0.5 {
				cov = math.Max(0, float64(rad)+0.5-d)
			}
			if cov <= 0 {
				continue
			}
			a := float64(c.A) / 255 * cov
			o := img.PixOffset(x, y)
			p := img.Pix[o : o+4 : o+4]
			k := 1 - a
			p[0] = uint8(float64(c.R)*a + float64(p[0])*k)
			p[1] = uint8(float64(c.G)*a + float64(p[1])*k)
			p[2] = uint8(float64(c.B)*a + float64(p[2])*k)
			p[3] = uint8(255*a + float64(p[3])*k)
		}
	}
}

func ovCircle(img *image.RGBA, cx, cy, rad int, c color.RGBA) {
	ovRound(img, image.Rect(cx-rad, cy-rad, cx+rad, cy+rad), rad, c)
}

var (
	ovGlass = color.RGBA{0, 0, 0, 150}
	ovWhite = rgb(0xffffff)
)

// ovText draws white text with a soft dark edge (readable on any picture).
func ovText(img *image.RGBA, f font.Face, x, y int, s string, edge bool) {
	s = visual(s)
	if edge {
		for _, d := range [][2]int{{-2, 0}, {2, 0}, {0, -2}, {0, 2}, {-2, -2}, {2, 2}, {-2, 2}, {2, -2}} {
			ui.DrawText(img, f, x+d[0], y+d[1], color.RGBA{0, 0, 0, 200}, s)
		}
	}
	ui.DrawText(img, f, x, y, ovWhite, s)
}

func ovTextCenter(img *image.RGBA, f font.Face, cx, cy int, s string) {
	m := f.Metrics()
	h := (m.Ascent + m.Descent).Ceil()
	ovText(img, f, cx-ui.TextWidth(f, visual(s))/2, cy-h/2+m.Ascent.Ceil(), s, false)
}

// videoTrack is the timeline's rectangle (landscape).
func (c *console) videoTrack() image.Rectangle {
	W, H := c.s.fbW, c.s.fbH
	return image.Rect(300, H-110, W-300, H-96)
}

func (c *console) trackTime(x int) float64 {
	v := c.vid
	tr := c.videoTrack()
	f := float64(x-tr.Min.X) / float64(tr.Dx())
	return min(max(f, 0), 1) * v.probe.duration
}

// videoOverlay draws the controls and the rest over the picture, as parts.
func (c *console) videoOverlay() []*ovPart {
	v := c.vid
	f := apple()
	W, H := c.s.fbW, c.s.fbH
	var parts []*ovPart
	v.buttons = v.buttons[:0]
	add := func(id string, r image.Rectangle) { v.buttons = append(v.buttons, button{id, r}) }
	loading := v.frame == nil
	if loading || v.controls || v.state == "paused" || v.state == "ended" || v.state == "failed" {
		// The top: close, the title; CC, speed, turn, on the right.
		top := newOvPart(W, 170, 0, 0)
		ovCircle(top.img, 100, 90, 50, ovGlass)
		line(top.img, 82, 72, 118, 108, 6, ovWhite)
		line(top.img, 118, 72, 82, 108, 6, ovWhite)
		add("v:close", image.Rect(30, 20, 170, 160))
		title := v.req.title
		tw := min(ui.TextWidth(f.headline, visual(title))+60, W-1000)
		ovRound(top.img, image.Rect(170, 50, 170+tw, 130), 40, ovGlass)
		ovText(top.img, f.headline, 200, 102, clip(f.headline, title, tw-60), false)
		rx := W - 40
		pill := func(id, label string) {
			w := ui.TextWidth(f.headline, label) + 60
			r := image.Rect(rx-w, 50, rx, 130)
			ovRound(top.img, r, 40, ovGlass)
			ovTextCenter(top.img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, label)
			add(id, r.Inset(-10))
			rx -= w + 20
		}
		pill("v:flip", "⟲")
		if !loading {
			pill("v:light", map[bool]string{true: "SD", false: "HD"}[v.light])
			pill("v:speed", strconv.FormatFloat(v.speed, 'g', -1, 64)+"×")
			if len(v.subLangs) > 0 {
				cc := "CC Off"
				if v.subLang != "" {
					cc = "CC " + langName(v.subLang)
				}
				pill("v:cc", cc)
			}
		}
		parts = append(parts, top.seal())
	}
	if loading {
		parts = append(parts, c.videoLoadingParts()...)
	} else if v.controls || v.state == "paused" || v.state == "ended" || v.state == "failed" {
		// The middle: back 10, play or pause, forward 10.
		mid := newOvPart(760, 240, W/2-380, H/2-120)
		cy := 120
		ovCircle(mid.img, 120, cy, 80, ovGlass)
		ovTextCenter(mid.img, f.headline, 120, cy, "−10")
		ovCircle(mid.img, 380, cy, 110, ovGlass)
		switch v.state {
		case "paused", "ended", "failed":
			iconPlayTriangle(mid.img, 386, cy, 48, ovWhite)
		default:
			ui.RoundRect(mid.img, image.Rect(350, cy-45, 370, cy+45), 4, ovWhite)
			ui.RoundRect(mid.img, image.Rect(390, cy-45, 410, cy+45), 4, ovWhite)
		}
		ovCircle(mid.img, 640, cy, 80, ovGlass)
		ovTextCenter(mid.img, f.headline, 640, cy, "+10")
		add("v:back", image.Rect(W/2-380, H/2-110, W/2-180, H/2+110))
		add("v:play", image.Rect(W/2-120, H/2-120, W/2+120, H/2+120))
		add("v:fwd", image.Rect(W/2+180, H/2-110, W/2+380, H/2+110))
		parts = append(parts, mid.seal())
		// The bottom: the time, the timeline, what's left.
		bot := newOvPart(W, 200, 0, H-200)
		ovRound(bot.img, image.Rect(40, 40, W-40, 180), 70, ovGlass)
		pos := v.pos
		if v.scrub >= 0 {
			pos = v.scrub
		}
		dur := 0.0
		if v.probe != nil {
			dur = v.probe.duration
		}
		ovText(bot.img, f.headline, 90, 124, clockText(pos), false)
		left := "Live"
		if dur > 0 {
			left = "−" + clockText(max(dur-pos, 0))
		}
		ovText(bot.img, f.headline, W-90-ui.TextWidth(f.headline, left), 124, left, false)
		tr := c.videoTrack().Sub(image.Pt(0, H-200))
		ovRound(bot.img, tr, 7, color.RGBA{255, 255, 255, 70})
		if j := v.req.job; j != nil && dur > 0 { // how much of the torrent is here
			if st := engine.status(j); st.Size > 0 {
				w := int(float64(tr.Dx()) * float64(st.Done) / float64(st.Size))
				ovRound(bot.img, image.Rect(tr.Min.X, tr.Min.Y, tr.Min.X+max(w, 14), tr.Max.Y), 7, color.RGBA{255, 255, 255, 110})
			}
		}
		if dur > 0 {
			kx := tr.Min.X + int(float64(tr.Dx())*min(pos/dur, 1))
			ovRound(bot.img, image.Rect(tr.Min.X, tr.Min.Y, max(kx, tr.Min.X+14), tr.Max.Y), 7, ovWhite)
			ovCircle(bot.img, kx, (tr.Min.Y+tr.Max.Y)/2, 20, ovWhite)
		}
		parts = append(parts, bot.seal())
		// What isn't right: no sound, a subtitle that didn't load, a failure.
		note := v.noSound
		if v.subMsg != "" {
			note = v.subMsg
		}
		switch v.state {
		case "failed":
			note = v.msg
		case "ended":
			note = "The end. Tap ▶ to play it again."
		}
		if note != "" {
			nw := min(ui.TextWidth(f.callout, visual(note))+60, W-200)
			n := newOvPart(nw, 70, (W-nw)/2, H-300)
			ovRound(n.img, n.img.Rect, 35, ovGlass)
			ovText(n.img, f.callout, 30, 47, clip(f.callout, note, nw-60), false)
			parts = append(parts, n.seal())
		}
	}
	if v.state == "seeking" || (v.state == "starting" && !loading) {
		parts = append(parts, c.videoSpinnerPart())
	}
	// The subtitle at this time, low on the picture (above the timeline when it shows).
	if text := cueAt(v.cues, v.pos); text != "" && !loading {
		if text != v.subText || v.subImg == nil {
			v.subText, v.subImg = text, c.subtitlePart(text)
		}
		sp := *v.subImg
		sp.y = H - 70 - sp.img.Rect.Dy()
		if v.controls {
			sp.y -= 230
		}
		parts = append(parts, &sp)
	}
	if v.hud != "" && time.Now().Before(v.hudUntil) {
		hw := ui.TextWidth(f.title, visual(v.hud)) + 100
		hp := newOvPart(hw, 110, (W-hw)/2, H/2+150)
		if loading || !v.controls {
			hp.y = H/2 - 55
		}
		ovRound(hp.img, hp.img.Rect, 40, ovGlass)
		ovTextCenter(hp.img, f.title, hw/2, 55, v.hud)
		parts = append(parts, hp.seal())
	}
	return parts
}

// subtitlePart draws a cue's lines, centred, with a dark edge.
func (c *console) subtitlePart(text string) *ovPart {
	W := c.s.fbW
	face := apple().title
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		lines = append(lines, wrapText(face, l, W-300)...)
	}
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	lh := face.Metrics().Height.Ceil() + 8
	p := newOvPart(W, lh*len(lines)+20, 0, 0)
	for i, l := range lines {
		w := ui.TextWidth(face, visual(l))
		ovText(p.img, face, (W-w)/2, 10+lh*i+face.Metrics().Ascent.Ceil(), l, true)
	}
	return p.seal()
}

// videoRedraw draws the player again (a new overlay over the last frame). Caller holds drawMu.
func (c *console) videoRedraw() {
	if c.vid == nil || c.mode != modeVideo || !c.screenOn || c.locked || c.pinMode != "" || c.alarm != "" {
		return
	}
	c.videoCompose()
}

// videoCompose lays the frame and the overlay into the screen, in the panel's own layout,
// and sends it. Caller holds drawMu.
func (c *console) videoCompose() {
	v := c.vid
	if !c.screenOn || c.locked || c.pinMode != "" || c.alarm != "" || c.mode != modeVideo {
		return
	}
	// The overlay is rebuilt only when what it shows changes (its drawing cost more than the
	// frame itself on the tablet: a film with the controls up played at ~5 frames a second).
	key := c.videoOverlayKey()
	parts := v.overlay
	if key == "" || key != v.ovKey || parts == nil {
		parts = c.videoOverlay()
		v.overlay, v.ovKey = parts, key
		v.ovBuilds++
	}
	c.videoRecompose(0, c.s.fbH, parts)
}

// videoOverlayKey sums up everything the overlay shows; "" = always draw it again (the
// spinner turning over a picture). Caller holds drawMu.
func (c *console) videoOverlayKey() string {
	v := c.vid
	if v.frame == nil || v.state == "seeking" || v.state == "starting" {
		return ""
	}
	pos := v.pos
	if v.scrub >= 0 {
		pos = v.scrub
	}
	hud := ""
	if v.hud != "" && time.Now().Before(v.hudUntil) {
		hud = v.hud
	}
	return fmt.Sprintf("%s|%v|%v|%v|%s|%d|%s|%d|%.0f|%v|%s|%s|%s|%s|%s|%v|%dx%d",
		v.state, v.controls, v.light, v.speed, v.subLang, len(v.subLangs), v.req.title, int(pos),
		v.scrub, v.scrub >= 0, v.noSound, v.subMsg, v.msg, hud, cueAt(v.cues, v.pos),
		pbSettingsNow().Flip, c.s.fbW, c.s.fbH)
}

// videoRecompose puts landscape rows y0..y1-1 together again (the frame, black around it, and
// the overlay parts that cross them) and sends just those rows. The loading screen's spinner and
// text are redrawn this way alone, so a step costs a strip, not the whole panel (on the Atom a
// whole redraw took long enough for the spinner to jump several spokes). Caller holds drawMu.
func (c *console) videoRecompose(y0, y1 int, parts []*ovPart) {
	v := c.vid
	s := c.s
	flip := pbSettingsNow().Flip
	W, H := s.fbW, s.fbH
	y0, y1 = max(y0, 0), min(y1, H)
	if y0 >= y1 {
		return
	}
	dst := words(s.buf)
	ds := s.stride / 4
	xrgb := s.bpp == 32 && s.red.offset == 16 && s.green.offset == 8 && s.blue.offset == 0
	// The frame, centred, black around it.
	fw, fh, scale := v.fw, v.fh, 1
	if v.fhalf {
		scale = 2
	}
	var src []uint32
	if v.frame != nil && xrgb {
		src = words(v.frame)
	}
	ox, oy := (W-fw*scale)/2, (H-fh*scale)/2
	rowBuf := make([]uint32, W)
	for y := y0; y < y1; y++ {
		clear(rowBuf)
		fy := y - oy
		if src != nil && fy >= 0 && fy < fh*scale {
			line := src[(fy/scale)*fw : (fy/scale+1)*fw]
			if scale == 1 {
				copy(rowBuf[ox:], line)
			} else {
				for i, px := range line {
					rowBuf[ox+2*i], rowBuf[ox+2*i+1] = px, px
				}
			}
		}
		ny := y
		if flip {
			ny = H - 1 - y
			row := dst[ny*ds : ny*ds+W]
			for i, px := range rowBuf {
				row[W-1-i] = px
			}
		} else {
			copy(dst[ny*ds:ny*ds+W], rowBuf)
		}
	}
	// The overlay.
	for _, p := range parts {
		blendPartRows(s, p, flip, y0, y1)
	}
	if !xrgb && v.frame != nil { // another pixel format: slow but right (tests use the tablet's)
		c.videoSlowFrame(flip)
	}
	if flip {
		s.markRows(H-y1, H-1-y0)
	} else {
		s.markRows(y0, y1-1)
	}
	if err := s.FlushFrame(); err != nil {
		log.Printf("flush: %v", err)
	}
}

// videoLoadingFrame redraws the loading screen's live parts alone: the spinner at every step,
// and (text) the whole loading screen's parts every quarter second. Caller holds drawMu.
func (c *console) videoLoadingFrame(text bool) {
	v := c.vid
	if c.vid == nil || c.mode != modeVideo || !c.screenOn || c.locked || c.pinMode != "" || c.alarm != "" {
		return
	}
	if v.overlay == nil {
		c.videoCompose()
		return
	}
	rows := func(ps []*ovPart, want func(*ovPart) bool, lo, hi int) (int, int) {
		for _, p := range ps {
			if want(p) {
				lo, hi = min(lo, p.y), max(hi, p.y+p.img.Rect.Dy())
			}
		}
		return lo, hi
	}
	if text {
		isLive := func(p *ovPart) bool { return p.live || p.spin }
		lo, hi := rows(v.overlay, isLive, c.s.fbH, 0)
		parts := c.videoOverlay()
		lo, hi = rows(parts, isLive, lo, hi)
		v.overlay, v.ovKey = parts, ""
		c.videoRecompose(lo, hi, parts)
		return
	}
	var old *ovPart
	for i, p := range v.overlay {
		if p.spin {
			old = p
			np := c.videoSpinnerPartAt(p.x+p.img.Rect.Dx()/2, p.y+p.img.Rect.Dy()/2, p.live)
			v.overlay[i] = np
			break
		}
	}
	if old == nil {
		return
	}
	c.videoRecompose(old.y, old.y+old.img.Rect.Dy(), v.overlay)
}

func (c *console) videoSlowFrame(flip bool) {}

// blendPart lays an overlay part over the screen's back buffer (XRGB, the panel's layout).
func blendPart(s *Screen, p *ovPart, flip bool) { blendPartRows(s, p, flip, 0, s.fbH) }

// blendPartRows is blendPart for landscape rows y0..y1-1 only.
func blendPartRows(s *Screen, p *ovPart, flip bool, y0, y1 int) {
	W, H := s.fbW, s.fbH
	dst := words(s.buf)
	ds := s.stride / 4
	pix := p.img.Pix
	for y, spans := range p.spans {
		ly := p.y + y
		if ly < 0 || ly >= H || ly < y0 || ly >= y1 {
			continue
		}
		ny := ly
		if flip {
			ny = H - 1 - ly
		}
		row := dst[ny*ds : ny*ds+W]
		for _, sp := range spans {
			for x := sp.x0; x < sp.x1; x++ {
				lx := p.x + x
				if lx < 0 || lx >= W {
					continue
				}
				nx := lx
				if flip {
					nx = W - 1 - lx
				}
				o := y*p.img.Stride + 4*x
				r, g, b, a := uint32(pix[o]), uint32(pix[o+1]), uint32(pix[o+2]), uint32(pix[o+3])
				if a == 255 {
					row[nx] = r<<16 | g<<8 | b
					continue
				}
				d := row[nx]
				k := 255 - a
				dr, dg, db := d>>16&0xff, d>>8&0xff, d&0xff
				row[nx] = (r+dr*k/255)<<16 | (g+dg*k/255)<<8 | (b + db*k/255)
			}
		}
	}
}

// --- sound setup -------------------------------------------------------------------------------

// soundCardNo is the number of the tablet's own audio chip in ALSA ("cloverview_audio"):
// card 0 here is the Loopback card, which ALSA's default device points at.
func soundCardNo() string {
	if b, err := os.ReadFile("/proc/asound/cards"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) > 1 && strings.Contains(l, "cloverview") {
				if _, err := strconv.Atoi(f[0]); err == nil {
					return f[0]
				}
			}
		}
	}
	return "0"
}

// cloverviewCard: the tablet's own audio chip is there (/proc/asound/cards).
func cloverviewCard() bool {
	b, _ := os.ReadFile("/proc/asound/cards")
	return strings.Contains(string(b), "cloverview")
}

// soundDev is the ALSA device films play through: the chip's first playback device (on this
// tablet "Headset", which the speaker is routed from), with ALSA converting formats.
// /data/condor/sounddev ("4,1": card,device) overrides it.
func soundDev() string {
	if d := currentSoundDev(); d != "" && d != soundHeadphones {
		return "plughw:" + d
	}
	return "plughw:" + soundCardNo() + ",0"
}

// soundHeadphones is the output "Headphones (the jack)": the chip's first device, routed to
// the jack instead of the speaker.
const soundHeadphones = "headphones"

// The chip's routes, as Android sets them before playing (its /system/etc/asound.conf,
// pcm.speaker and pcm.headphone hooks; found 2026-10-29). Without them the DSP's output
// mixers are "None" and the speaker amplifier is off: the chip takes one period of sound and
// stalls, and aplay hangs. Alpine's alsa-lib can't write these controls on kernel 3.4 (its
// newer ioctl structs get ENOTTY), so Android's own alsa_amixer sets them.
var (
	cloverviewSpeaker = [][2]string{
		{"SPK Amp Switch", "on"}, {"SPKLO Amp Switch", "on"}, {"HP Amp Switch", "off"}, {"EAR Amp Switch", "off"},
		{"LPE IHF mixer", "3"}, {"ESL-ASP Mux", "1"}, {"SPK-ASP Mux", "0"}, {"IHFAMP_SD_N Switch", "on"},
	}
	cloverviewHeadphones = [][2]string{
		{"HP Amp Switch", "on"}, {"SPK Amp Switch", "off"}, {"SPKLO Amp Switch", "off"}, {"EAR Amp Switch", "off"},
		{"LPE headset mixer", "3"}, {"IHFAMP_SD_N Switch", "off"},
	}
)

// androidAmixer is Android's alsa_amixer (bionic, made for this kernel); tests replace it.
var androidAmixer = "/system/bin/alsa_amixer"

// routeCloverview sets the chip's speaker or headphone route with Android's alsa_amixer, run
// on Android's side (not in Alpine). It's quick, so it's done before every sound. Best effort:
// it says what failed.
func routeCloverview(card string, headphones bool) error {
	if _, err := os.Stat(androidAmixer); err != nil {
		return err
	}
	route := cloverviewSpeaker
	if headphones {
		route = cloverviewHeadphones
	}
	var failed []string
	for _, c := range route {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, androidAmixer, "-q", "-c", card, "cset", "name="+c[0], c[1]).CombinedOutput()
		cancel()
		if err != nil {
			failed = append(failed, c[0]+": "+lastLines(string(out), 1))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s", strings.Join(failed, "; "))
	}
	return nil
}

// setupSound turns the speaker and headphone outputs on and up (ALSA's mixer), once: without
// Android's audio service nothing has. Best effort.
func setupSound() {
	card := soundCardNo()
	d := currentSoundDev()
	if d != "" && d != soundHeadphones {
		card = strings.Split(d, ",")[0]
	}
	if card == soundCardNo() && cloverviewCard() { // the tablet's own chip: Android's routes
		stopAndroidMedia() // its idle stream stalls every sound (sound_media.go)
		if err := routeCloverview(card, d == soundHeadphones); err != nil {
			log.Printf("playbox: sound route on card %s: %v", card, err)
		} else {
			log.Printf("playbox: sound routed to the %s", map[bool]string{true: "headphones", false: "speaker"}[d == soundHeadphones])
		}
	}
	soundSetMu.Lock()
	defer soundSetMu.Unlock()
	if soundSet[card] {
		return
	}
	soundSet[card] = true
	if _, err := os.Stat(alpineRoot + "/usr/bin/amixer"); err != nil {
		return
	}
	amixer := func(args ...string) ([]byte, error) {
		return alpineCtxCmd(context.Background(), "/usr/bin/amixer", append([]string{"-c", card}, args...)...).Output()
	}
	want := regexp.MustCompile(`(?i)speaker|headphone|headset|master|pcm|dac|line ?out|playback|spk|hp|media|ihf|earpiece`)
	skip := regexp.MustCompile(`(?i)capture|mic|boost|adc|record|input|loopback`)
	out, err := amixer("scontrols")
	if err != nil {
		log.Printf("playbox: amixer: %v", err)
		return
	}
	log.Printf("playbox: sound card %s controls: %s", card, strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " | "))
	re := regexp.MustCompile(`Simple mixer control '([^']+)',(\d+)`)
	for _, m := range re.FindAllStringSubmatch(string(out), -1) {
		if !want.MatchString(m[1]) || skip.MatchString(m[1]) {
			continue
		}
		ctl := m[1] + "," + m[2]
		if _, err := amixer("-q", "sset", ctl, "85%", "unmute"); err != nil {
			amixer("-q", "sset", ctl, "on")
		}
	}
	// The chip's switches that aren't simple controls (routes): on, for playback ones.
	if out, err := amixer("contents"); err == nil {
		reCtl := regexp.MustCompile(`numid=(\d+),iface=MIXER,name='([^']+)'\s*\n\s*; type=BOOLEAN`)
		for _, m := range reCtl.FindAllStringSubmatch(string(out), -1) {
			if want.MatchString(m[2]) && !skip.MatchString(m[2]) {
				amixer("-q", "cset", "numid="+m[1], "on")
			}
		}
	}
	log.Printf("playbox: mixer of card %s set up", card)
}

var (
	soundSetMu sync.Mutex
	soundSet   = map[string]bool{}
)

// testSound plays a short chime the way films play, and says how it went. The chime lasts
// about a second: an output still busy after 6 s is stuck (a call output), and is let go of
// without waiting for it (a program blocked in the driver can't be stopped).
func testSound() string {
	if !aplayInstalled() {
		out, err := apkOut("apk", "add", "alsa-utils")
		if err != nil {
			return "Couldn't install aplay: " + lastLines(string(out), 1)
		}
	}
	setupSound()
	wav := alpineRoot + "/tmp/playbox-test.wav"
	if err := os.WriteFile(wav, alarmWAV(), 0o644); err != nil {
		return err.Error()
	}
	dev := soundDev()
	cmd := alpineCtxCmd(context.Background(), "/usr/bin/aplay", "-q", "-D", dev, "/tmp/playbox-test.wav")
	var out lineRing
	ep, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return "No sound: " + err.Error()
	}
	go logLines(ep, "aplay (chime)", &out)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			why := out.last()
			if why == "" {
				why = err.Error()
			}
			return "No sound from " + dev + ": " + strings.TrimPrefix(why, "aplay: ")
		}
		return "Played on " + dev + ": did you hear a chime?"
	case <-time.After(6 * time.Second):
		cmd.Process.Kill()
		log.Printf("playbox: %s got stuck playing the chime", dev)
		if strings.TrimPrefix(dev, "plughw:") == currentSoundDev() {
			os.Remove(marginHome + "/sounddev")
		}
		return dev + " got stuck: it's an output for calls, not for films. Back to Automatic. " +
			"If nothing plays now, restart the tablet: a stuck output can hold the sound chip."
	}
}
