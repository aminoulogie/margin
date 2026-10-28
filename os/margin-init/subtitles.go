package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Subtitles for the player (SubtitleOverlayController.swift): SRT or WebVTT files from the
// add-ons, read into cues; the player shows the cue at the current time.

type subCue struct {
	start, end float64
	text       string
}

var (
	reCueTime = regexp.MustCompile(`(\d+):(\d{2}):(\d{2})[,.](\d{1,3})|(\d{2}):(\d{2})[,.](\d{3})`)
	reSubTag  = regexp.MustCompile(`<[^>]*>|\{\\[^}]*\}`)
)

// parseCueTime reads 00:01:02,345 (or 01:02.345) as seconds.
func parseCueTime(s string) (float64, bool) {
	m := reCueTime.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n := func(i int) float64 { v, _ := strconv.Atoi(m[i]); return float64(v) }
	frac := func(i int) float64 {
		v, _ := strconv.Atoi(m[i])
		for k := len(m[i]); k < 3; k++ {
			v *= 10
		}
		return float64(v) / 1000
	}
	if m[1] != "" {
		return n(1)*3600 + n(2)*60 + n(3) + frac(4), true
	}
	return n(5)*60 + n(6) + frac(7), true
}

// parseSubtitles reads SRT and WebVTT alike: blocks separated by blank lines, each with a
// "start --> end" line and the text under it.
func parseSubtitles(raw string) []subCue {
	raw = strings.TrimPrefix(raw, "\ufeff")
	raw = strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	var cues []subCue
	for _, block := range strings.Split(raw, "\n\n") {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		for i, l := range lines {
			a, b, ok := strings.Cut(l, "-->")
			if !ok {
				continue
			}
			start, ok1 := parseCueTime(a)
			end, ok2 := parseCueTime(strings.Fields(b + " x")[0])
			if !ok1 || !ok2 || end <= start {
				break
			}
			var text []string
			for _, t := range lines[i+1:] {
				t = strings.TrimSpace(reSubTag.ReplaceAllString(t, ""))
				if t != "" {
					text = append(text, t)
				}
			}
			if len(text) > 0 {
				cues = append(cues, subCue{start, end, strings.Join(text, "\n")})
			}
			break
		}
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].start < cues[j].start })
	return cues
}

// cueAt is the text to show at t seconds ("" for none).
func cueAt(cues []subCue, t float64) string {
	i := sort.Search(len(cues), func(i int) bool { return cues[i].start > t })
	var shown []string
	for k := i - 1; k >= 0 && k >= i-4; k-- { // overlapping cues show together
		if cues[k].end > t {
			shown = append([]string{cues[k].text}, shown...)
		}
	}
	return strings.Join(shown, "\n")
}

// decodeSubtitles turns a subtitle file's bytes into text: UTF-8 (or UTF-16) as it is,
// otherwise the old Windows code page of its language (Arabic files are often cp1256).
func decodeSubtitles(b []byte, lang string) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xff, 0xfe}) || bytes.HasPrefix(b, []byte{0xfe, 0xff}):
		return decodeUTF16(b)
	case utf8.Valid(b):
		return string(b)
	}
	cm := charmap.Windows1252
	switch strings.ToLower(lang) {
	case "ara", "ar", "per", "fas", "fa":
		cm = charmap.Windows1256
	case "rus", "ru", "bul", "bg", "ukr", "uk", "srp", "sr":
		cm = charmap.Windows1251
	case "gre", "ell", "el":
		cm = charmap.Windows1253
	case "tur", "tr":
		cm = charmap.Windows1254
	case "heb", "he":
		cm = charmap.Windows1255
	case "pol", "pl", "cze", "ces", "cs", "hun", "hu", "rum", "ron", "ro", "hrv", "hr", "slv", "sl", "slo", "slk":
		cm = charmap.Windows1250
	}
	out, err := cm.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(out)
}

func decodeUTF16(b []byte) string {
	le := b[0] == 0xff
	b = b[2:]
	var r []rune
	for i := 0; i+1 < len(b); i += 2 {
		v := uint16(b[i]) | uint16(b[i+1])<<8
		if !le {
			v = uint16(b[i])<<8 | uint16(b[i+1])
		}
		r = append(r, rune(v))
	}
	return string(r)
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, 16<<20))
}

// loadSubtitles fetches and reads a subtitle file.
func loadSubtitles(s addonSubtitle) ([]subCue, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	resp, err := webGetCtx(ctx, s.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(b, []byte{0x1f, 0x8b}) { // gzipped (some add-ons serve .srt.gz)
		if u, err := gunzip(b); err == nil {
			b = u
		}
	}
	return parseSubtitles(decodeSubtitles(b, s.Lang)), nil
}
