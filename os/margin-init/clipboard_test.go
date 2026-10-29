package main

import (
	"io"
	"os"
	"testing"
	"time"
)

func TestClipboardParse(t *testing.T) {
	md := "# Clipboard\nintro\n\n## First\n```\necho one\necho two\n```\n\n## Empty\n\n## Second\n```sh\nls -la\n```\n"
	items := parseClipboard(md)
	if len(items) != 2 || items[0].title != "First" || items[0].cmd != "echo one\necho two" || items[1].cmd != "ls -la" {
		t.Fatalf("%+v", items)
	}
	b, err := os.ReadFile("../../CLIPBOARD.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(parseClipboard(string(b))) == 0 {
		t.Fatal("CLIPBOARD.md has no commands in the format the tablet reads")
	}
}

// Clipboard on the shortcuts bar opens the panel; a tap on a command types it (lines joined
// by returns, no return at the end) and closes the panel.
func TestClipboardTypesACommand(t *testing.T) {
	c := testConsole(t)
	c.mode = modeTerminal
	c.cb.items = []clipItem{{"Two lines", "cd /tmp\nls"}, {"Other", "echo hi"}}
	c.cb.fetched = time.Now() // no fetch from GitHub in the test
	r, w, _ := os.Pipe()
	c.mu.Lock()
	c.master = w
	c.mu.Unlock()
	drawMu.Lock()
	c.redrawAll()
	bar := c.accY() + accH/2
	x := -1
	for xx := kbPad; xx < c.s.W; xx += 10 {
		if i := c.shortcutAt(xx); i >= 0 && c.shortcuts()[i].label == "Clipboard" {
			x = xx
			break
		}
	}
	c.termTouch(TouchPoint{Down: true, X: x, Y: bar})
	c.termTouch(TouchPoint{Up: true, X: x, Y: bar})
	if !c.cb.open || c.cb.rect.Empty() {
		t.Fatal("the Clipboard didn't open")
	}
	y := c.cb.rect.Min.Y + cbHeadH + cbRowH/2
	c.termTouch(TouchPoint{Down: true, X: 300, Y: y})
	c.termTouch(TouchPoint{Up: true, X: 300, Y: y})
	drawMu.Unlock()
	w.Close()
	got, _ := io.ReadAll(r)
	if string(got) != "cd /tmp\rls" || c.cb.open {
		t.Fatalf("typed %q, open %v", got, c.cb.open)
	}
}
