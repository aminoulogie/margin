package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The X client against a real Xvfb, where there is one: it connects, finds XTEST and the
// keyboard, moves and clicks, and the screen file reads as the screen.
func TestXvfbBridge(t *testing.T) {
	bin, err := exec.LookPath("Xvfb")
	if err != nil {
		t.Skip("no Xvfb here")
	}
	dir := t.TempDir()
	cmd := exec.Command(bin, ":97", "-screen", "0", "640x480x24", "-fbdir", dir, "-ac", "-nolisten", "tcp")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	var x *xconn
	for i := 0; i < 50; i++ {
		if x, err = xdial("/tmp/.X11-unix/X97"); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer x.close()
	if x.w != 640 || x.h != 480 || x.xtest == 0 || len(x.keysyms) == 0 {
		t.Fatalf("screen %dx%d xtest %d keysyms %d", x.w, x.h, x.xtest, len(x.keysyms))
	}
	if _, _, ok := x.keycode('a'); !ok {
		t.Fatal("no key for a")
	}
	if _, shift, ok := x.keycode('A'); !ok || !shift {
		t.Fatal("A should be shift+a")
	}
	x.move(100, 100)
	x.button(1, true)
	x.button(1, false)
	x.typeBytes([]byte("Hi\r\x7f\x1b[A"))
	x.fillScreen()
	if _, err := x.call([]byte{43, 0, 1, 0}); err != nil { // GetInputFocus: still connected, no errors
		t.Fatal(err)
	}
	fb, unmap, err := mapFile(filepath.Join(dir, "Xvfb_screen0"))
	if err != nil {
		t.Fatal(err)
	}
	defer unmap()
	xa := &xapp{fb: fb}
	if err := xa.parseXWD(); err != nil || xa.w != 640 || xa.h != 480 {
		t.Fatalf("XWD %v %dx%d", err, xa.w, xa.h)
	}
	// Shown in Margin: the bar over the app, then its screen.
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	xa.app, xa.x = linuxApps[0], x
	c.xapp = xa
	c.drawXapp(true)
	shot(t, c, "desktop-app")
	c.xappTouch(TouchPoint{Down: true, X: 50, Y: c.xTop() + 50})
	c.xappTouch(TouchPoint{Up: true, X: 50, Y: c.xTop() + 50})
	c.xapp = nil
	_ = os.Remove
}
