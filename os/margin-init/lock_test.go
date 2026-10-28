package main

import "testing"

// Waking shows the lock screen with the book being read; a tap opens Margin where it was.
// With the switch off, waking goes straight back.
func TestLockScreen(t *testing.T) {
	c := readerConsole(t)
	openFirstBook(t, c)
	c.saveProgress()
	c.setScreen(false)
	c.setScreen(true)
	if !c.locked {
		t.Fatal("waking should show the lock screen")
	}
	shot(t, c, "lock-screen")
	page := c.book.page
	pressKey(c, keyVolumeDown) // brightness, not a page turn behind the lock screen
	if c.book.page != page {
		t.Error("the volume keys turned a page behind the lock screen")
	}
	c.unlock()
	if c.locked || c.mode != modeReader {
		t.Fatalf("after the tap: locked %v, mode %v", c.locked, c.mode)
	}
	c.cfg.NoLock = true
	c.setScreen(false)
	c.setScreen(true)
	if c.locked {
		t.Fatal("with the lock screen off, waking should open Margin directly")
	}
}

// A passcode is set twice, asked for on wake (a wrong one keeps it locked), and turned off.
func TestPasscode(t *testing.T) {
	c := readerConsole(t)
	c.cfg.NoLock = true
	t.Cleanup(func() { c.cfg.PinHash = "" })
	tap := func(key string) {
		for _, k := range c.pinKeys() {
			if k.key == key {
				x, y := (k.r.Min.X+k.r.Max.X)/2, (k.r.Min.Y+k.r.Max.Y)/2+c.barH
				c.pinTouch(TouchPoint{Down: true, X: x, Y: y})
				return
			}
		}
		t.Fatalf("no key %q", key)
	}
	typePin := func(pin string) {
		for _, d := range pin {
			tap(string(d))
		}
	}
	c.setMode(modeBooksHome) // something behind the keypad to blur
	c.showPage()
	c.startPin("set")
	shot(t, c, "passcode")
	typePin("1234")
	typePin("1299") // doesn't match: asked again
	if c.pinMode != "set" || c.cfg.PinHash != "" {
		t.Fatalf("a mismatch was kept: %q %q", c.pinMode, c.cfg.PinHash)
	}
	typePin("2580")
	typePin("2580")
	if c.pinMode != "" || c.cfg.PinHash != pinHash("2580") {
		t.Fatal("the passcode wasn't set")
	}

	c.setScreen(false)
	c.setScreen(true)
	if !c.locked {
		t.Fatal("with a passcode, waking must lock even with the lock screen off")
	}
	shot(t, c, "lock-widgets")
	c.startPin("unlock")
	typePin("1111")
	if !c.locked || c.pinMsg == "" {
		t.Fatal("a wrong passcode unlocked")
	}
	tap("del") // nothing typed: cancel, back to the lock screen
	if c.pinMode != "" || !c.locked {
		t.Fatal("cancel should go back to the lock screen")
	}
	c.startPin("unlock")
	typePin("2580")
	if c.locked {
		t.Fatal("the right passcode didn't unlock")
	}

	c.startPin("off")
	typePin("2580")
	if c.cfg.PinHash != "" {
		t.Fatal("the passcode wasn't turned off")
	}
}
