package main

// The Terminal's Clipboard: commands to type, fetched from CLIPBOARD.md in the margin repo on
// GitHub. Long commands can't be typed by hand on the tablet (and there's nothing to paste
// from), so whoever asks the user to run something puts it in CLIPBOARD.md and pushes it
// (CLAUDE.md says so); the user opens Clipboard over the keyboard and taps it. The command is
// typed into the tab on show, without the last return: the user reads it, then presses return.
//
// CLIPBOARD.md: each command is a "## title" followed by a ``` fenced block; anything else
// (an intro) is ignored. Newest first.

import (
	"context"
	"image"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"
)

const (
	clipboardURL  = "https://raw.githubusercontent.com/aminoulogie/margin/main/CLIPBOARD.md"
	clipboardFile = marginHome + "/clipboard.md" // the last one fetched, for when there's no internet
	cbHeadH       = 84
	cbRowH        = 108
	cbRows        = 5 // rows on show; more go on further pages
)

type clipItem struct{ title, cmd string }

type clipState struct {
	open    bool
	items   []clipItem
	page    int
	loading bool
	status  string // "updated 13:40", "no internet: the saved copy", ...
	fetched time.Time
	rect    image.Rectangle // where the panel is on the screen
}

// parseClipboard reads CLIPBOARD.md's titles and fenced commands.
func parseClipboard(md string) []clipItem {
	var items []clipItem
	var cur *clipItem
	var body []string
	inFence := false
	for _, l := range strings.Split(strings.ReplaceAll(md, "\r", ""), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "```"):
			if inFence && cur != nil && cur.cmd == "" {
				cur.cmd = strings.Trim(strings.Join(body, "\n"), "\n")
			}
			inFence, body = !inFence, nil
		case inFence:
			body = append(body, l)
		case strings.HasPrefix(t, "## "):
			if cur != nil && cur.cmd != "" {
				items = append(items, *cur)
			}
			cur = &clipItem{title: strings.TrimSpace(t[3:])}
		}
	}
	if cur != nil && cur.cmd != "" {
		items = append(items, *cur)
	}
	return items
}

// toggleClipboard opens or closes the panel. Caller holds drawMu.
func (c *console) toggleClipboard() {
	cb := &c.cb
	cb.open = !cb.open
	if !cb.open {
		c.redrawAll()
		return
	}
	cb.page = 0
	if cb.items == nil {
		if b, err := os.ReadFile(clipboardFile); err == nil {
			cb.items = parseClipboard(string(b))
		}
	}
	if time.Since(cb.fetched) > 15*time.Second {
		c.fetchClipboard()
	}
	c.drawShortcuts()
	c.s.Flush()
}

// fetchClipboard gets CLIPBOARD.md from GitHub in the background. Caller holds drawMu.
func (c *console) fetchClipboard() {
	cb := &c.cb
	if cb.loading {
		return
	}
	cb.loading, cb.status = true, "getting the latest from GitHub…"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// A changing query: GitHub's raw files are cached for minutes otherwise.
		var b []byte
		resp, err := webGetCtx(ctx, clipboardURL+"?t="+strconv.FormatInt(time.Now().Unix(), 10))
		if err == nil {
			b, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		cb.loading = false
		if err != nil {
			log.Printf("clipboard: %v", err)
			cb.status = "no internet: the copy saved last time"
		} else {
			cb.items, cb.fetched, cb.page = parseClipboard(string(b)), time.Now(), 0
			cb.status = "updated " + time.Now().Format("15:04")
			os.WriteFile(clipboardFile, b, 0o644)
		}
		if c.mode == modeTerminal && cb.open && c.screenOn {
			c.drawShortcuts()
			c.s.Flush()
		}
	}()
}

// drawClipboard paints the panel over the bottom of the terminal's text, above the
// shortcuts bar. Caller holds drawMu.
func (c *console) drawClipboard() {
	cb := &c.cb
	if !cb.open {
		return
	}
	f := apple()
	from := cb.page * cbRows
	if from >= len(cb.items) {
		from, cb.page = 0, 0
	}
	n := min(len(cb.items)-from, cbRows)
	rows := max(n, 1)
	more := len(cb.items) > cbRows
	if more {
		rows++
	}
	h := cbHeadH + rows*cbRowH + 12
	img := image.NewRGBA(image.Rect(0, 0, c.s.W, h))
	ui.Fill(img, img.Rect, rgb(0x1c1c1e))
	ui.Fill(img, image.Rect(0, 0, c.s.W, 2), rgb(0x3a3a3c))
	apText(img, f.headline, kbPad+16, 54, rgb(0xffffff), "Clipboard")
	apText(img, f.caption, kbPad+16+ui.TextWidth(f.headline, "Clipboard")+20, 52, rgb(0x8e8e93), clip(f.caption, cb.status, 560))
	// Refresh and close, right.
	apTextRight(img, f.callout, c.s.W-kbPad-150, 54, rgb(0x0a84ff), "Refresh")
	apTextRight(img, f.callout, c.s.W-kbPad-24, 54, rgb(0x0a84ff), "Close")
	y := cbHeadH
	if n <= 0 {
		msg := "Nothing yet. Commands to type show up here, from CLIPBOARD.md on GitHub."
		if cb.loading {
			msg = "Getting the commands…"
		}
		apText(img, f.callout, kbPad+16, y+64, rgb(0x8e8e93), clip(f.callout, msg, c.s.W-2*kbPad-32))
	}
	for i := 0; i < n; i++ {
		it := cb.items[from+i]
		r := image.Rect(kbPad, y+6, c.s.W-kbPad, y+cbRowH-6)
		ui.RoundRect(img, r, 16, rgb(0x2c2c2e))
		apText(img, f.callout, r.Min.X+24, r.Min.Y+40, rgb(0xffffff), clip(f.callout, it.title, r.Dx()-48))
		cmd := strings.ReplaceAll(it.cmd, "\n", " ⏎ ")
		ui.DrawText(img, c.reg, r.Min.X+24, r.Min.Y+80, rgb(0x64d2ff), clip(c.reg, cmd, r.Dx()-48))
		y += cbRowH
	}
	if more {
		pages := (len(cb.items) + cbRows - 1) / cbRows
		label := "More (" + itoa(cb.page+1) + " of " + itoa(pages) + ")"
		ui.DrawTextCentered(img, f.callout, c.s.W/2, y+cbRowH/2, rgb(0x0a84ff), label)
	}
	top := c.accY() - h
	cb.rect = image.Rect(0, top, c.s.W, top+h)
	c.s.blitRGBA(img, 0, top)
}

// clipboardTouch handles a finger on the panel (acts on lift). Caller holds drawMu.
func (c *console) clipboardTouch(p TouchPoint) {
	cb := &c.cb
	if !p.Up {
		return
	}
	y := p.Y - cb.rect.Min.Y
	if y < cbHeadH {
		switch {
		case p.X > c.s.W-kbPad-130:
			c.toggleClipboard()
		case p.X > c.s.W-kbPad-340:
			c.fetchClipboard()
			c.drawShortcuts()
			c.s.Flush()
		}
		return
	}
	row := (y - cbHeadH) / cbRowH
	from := cb.page * cbRows
	n := min(len(cb.items)-from, cbRows)
	switch {
	case row < n:
		cmd := strings.ReplaceAll(cb.items[from+row].cmd, "\n", "\r")
		cb.open = false
		c.goLive()
		c.redrawAll()
		c.input([]byte(cmd)) // no return at the end: the user reads it, then presses return
	case row == n && len(cb.items) > cbRows:
		cb.page = (cb.page + 1) % ((len(cb.items) + cbRows - 1) / cbRows)
		c.drawShortcuts()
		c.s.Flush()
	}
}
