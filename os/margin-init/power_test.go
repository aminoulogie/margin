package main

import (
	"bytes"
	"image"
	"path/filepath"
	"testing"
)

func testConsole(t *testing.T) *console {
	oldSettings := settingsPath // settings saved to a temp file, never the machine's own
	settingsPath = filepath.Join(t.TempDir(), "settings.json")
	t.Cleanup(func() { settingsPath = oldSettings })
	s := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
	c, err := newConsole(s)
	if err != nil {
		t.Fatal(err)
	}
	// Defaults, whatever a settings file on this machine says, and saved to a temp file.
	c.cfg = savedSettings{Brightness: 80, ScreenOff: 5, Motion: 1}
	// A fresh dictionary of saved words in a temp folder, no background fetching.
	oldDir, oldDict, oldBg := dictDir, dict, dictBackground
	dictDir, dict, dictBackground = t.TempDir(), newDictStore(), false
	t.Cleanup(func() { dictDir, dict, dictBackground = oldDir, oldDict, oldBg })
	// Covers from a temp folder, in a fresh cache.
	oldCovDir, oldCovers := coverDir, covers
	coverDir, covers = t.TempDir(), newCoverCache()
	t.Cleanup(func() { // under drawMu (a redraw still queued reads covers), unless the test holds it
		if drawMu.TryLock() {
			defer drawMu.Unlock()
		}
		coverDir, covers = oldCovDir, oldCovers
		c.screenOn = false // a toast's timer left running draws nothing after the test
	})
	// Nothing from Home in the background (not put back: stray arrivals read them later).
	backgroundRedraws, homeLoads, turnAhead, wifiAutoScan = false, false, false, false
	// Views drawn every time (tests check what a drawing shows), kept in a temp folder.
	oldViewDir := viewDirFn
	vd := t.TempDir()
	viewDirFn = func() string { return vd }
	views.Lock()
	views.m, views.bytes, views.sums, views.saving, views.off, views.async, views.noPrep, views.c = nil, 0, nil, false, true, false, true, nil
	views.Unlock()
	t.Cleanup(func() { viewDirFn = oldViewDir })
	setPalette(true)
	return c
}

func TestPowerButtonTogglesScreen(t *testing.T) {
	c := testConsole(t)
	c.key(keyPower, 1)
	if c.screenOn {
		t.Fatal("power press should turn the screen off")
	}
	c.key(keyPower, 0) // release: nothing
	c.key(keyPower, 2) // auto-repeat: nothing
	if c.screenOn {
		t.Fatal("release/repeat must not toggle")
	}
	c.key(keyVolumeUp, 1) // ignored while off
	if c.cfg.Brightness != 80 {
		t.Fatalf("brightness changed while off: %d", c.cfg.Brightness)
	}
	c.key(keyPower, 1)
	if !c.screenOn {
		t.Fatal("second press should turn it back on")
	}
	c.key(keyVolumeDown, 1)
	c.key(keyVolumeDown, 2)
	if c.cfg.Brightness != 60 {
		t.Fatalf("brightness %d, want 60", c.cfg.Brightness)
	}
}

func TestGlyphCacheMatchesFreshDraw(t *testing.T) {
	c := testConsole(t)
	a := image.NewRGBA(image.Rect(0, 0, c.cw*2, c.ch))
	b := image.NewRGBA(image.Rect(0, 0, c.cw*2, c.ch))
	c.putGlyph(a, c.cw, 'g', consoleFG, consoleBG, false) // first: rasterized
	c.putGlyph(b, c.cw, 'g', consoleFG, consoleBG, false) // second: from the cache
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("cached glyph differs from the first draw")
	}
	if len(c.glyphs) != 1 {
		t.Fatalf("cache has %d entries, want 1", len(c.glyphs))
	}
}
