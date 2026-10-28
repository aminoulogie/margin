package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	_ "image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"margin-init/ui"
)

// Desktop apps: real Linux programs with windows, run on a virtual X display (Xvfb) inside
// Alpine and shown full screen by Margin. Xvfb keeps its screen in a file (-fbdir), which
// Margin maps and copies to the panel ten times a second, only the rows that changed;
// touches go back as the pointer (XTEST), the keyboard as keys. There's no window manager:
// an app's windows are made to fill the screen. One app at a time.

const (
	xDisplay   = ":7"
	xFBDir     = "/tmp/margin-x"
	xChromeH   = 96 // the bar over the app: its name, the keyboard, Done
	xFrameTick = 100 * time.Millisecond
)

// desktopDeps are what an X app needs besides itself: the display, fonts, and D-Bus (most
// GTK apps want a session bus).
var desktopDeps = []string{"xvfb", "font-dejavu", "dbus", "dbus-x11", "adwaita-icon-theme", "hicolor-icon-theme"}

type xapp struct {
	app      linuxApp
	xvfb     *exec.Cmd
	proc     *exec.Cmd
	x        *xconn
	fb       []byte // the mapped XWD file
	unmap    func()
	off      int // where its pixels start
	stride   int
	w, h     int
	prev     []byte
	status   string // while starting: what it's doing
	gen      int
	down     bool
	lastFill time.Time
}

// xTop is the screen y where the app's screen starts.
func (c *console) xTop() int { return c.barH + xChromeH }

// startDesktop opens an app on the virtual display, installing what it needs first.
// Caller holds drawMu.
func (c *console) startDesktop(app linuxApp) {
	if c.xapp != nil {
		c.stopDesktop()
	}
	xa := &xapp{app: app, status: "Starting " + app.name + "..."}
	c.xapp = xa
	c.skb.visible = false
	c.drawXapp(true)
	go func() {
		err := c.launchX(xa)
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.xapp != xa {
			return
		}
		if err != nil {
			log.Printf("desktop %s: %v", app.pkg, err)
			xa.status = app.name + " didn't start: " + err.Error()
			c.drawXapp(true)
			return
		}
		xa.status = ""
		c.drawXapp(true)
		go c.xFrames(xa)
	}()
}

func (c *console) launchX(xa *xapp) error {
	if _, err := os.Stat(alpineRoot + "/usr/bin/Xvfb"); err != nil {
		drawMu.Lock()
		xa.status = "Getting the display (once)..."
		c.drawXapp(true)
		drawMu.Unlock()
		if err := apkRun(append([]string{"apk", "add"}, desktopDeps...)...); err != nil {
			return err
		}
	}
	w, h := c.s.W, c.s.H-c.xTop()
	os.MkdirAll(alpineRoot+xFBDir, 0o755)
	os.MkdirAll(alpineRoot+"/tmp/xdg", 0o700)
	sock := alpineRoot + "/tmp/.X11-unix/X" + strings.TrimPrefix(xDisplay, ":")
	os.Remove(sock)
	os.Remove(alpineRoot + "/tmp/.X" + strings.TrimPrefix(xDisplay, ":") + "-lock")
	xa.xvfb = alpineCmd("/usr/bin/Xvfb", xDisplay, "-screen", "0", itoa(w)+"x"+itoa(h)+"x24",
		"-fbdir", xFBDir, "-ac", "-nolisten", "tcp", "-dpi", "192")
	if err := xa.xvfb.Start(); err != nil {
		return err
	}
	var err error
	for i := 0; i < 100; i++ {
		if xa.x, err = xdial(sock); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	fbPath := alpineRoot + xFBDir + "/Xvfb_screen0"
	if xa.fb, xa.unmap, err = mapFile(fbPath); err != nil {
		return err
	}
	if err := xa.parseXWD(); err != nil {
		return err
	}
	// The app, scaled for the tablet's density, with a session bus if there is one.
	args := []string{"/bin/sh", "-c", "exec " + xa.app.cmd}
	if _, err := os.Stat(alpineRoot + "/usr/bin/dbus-run-session"); err == nil {
		args = []string{"/usr/bin/dbus-run-session", "--", "/bin/sh", "-c", "exec " + xa.app.cmd}
	}
	xa.proc = alpineCmd(args...)
	xa.proc.Env = append(xa.proc.Env, "DISPLAY="+xDisplay, "XDG_RUNTIME_DIR=/tmp/xdg", "GDK_SCALE=2",
		"QT_SCALE_FACTOR=2", "ELM_SCALE=2", "NO_AT_BRIDGE=1", "GTK_THEME=Adwaita:dark")
	return xa.proc.Start()
}

// alpineCmd is a command in Alpine, not waited for, in its own process group.
func alpineCmd(args ...string) *exec.Cmd {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = "/root"
	proxy := "http://" + proxyAddr
	cmd.Env = []string{"HOME=/root", "USER=root", "TERM=xterm",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"http_proxy=" + proxy, "https_proxy=" + proxy, "no_proxy=localhost,127.0.0.1"}
	cmd.SysProcAttr = alpineAttr()
	return cmd
}

// parseXWD reads the screen file's header (big-endian, as XWD's always is).
func (xa *xapp) parseXWD() error {
	if len(xa.fb) < 100 {
		return os.ErrInvalid
	}
	be := binary.BigEndian
	hdr := int(be.Uint32(xa.fb[0:]))
	xa.w, xa.h = int(be.Uint32(xa.fb[16:])), int(be.Uint32(xa.fb[20:]))
	bpp := int(be.Uint32(xa.fb[44:]))
	xa.stride = int(be.Uint32(xa.fb[48:]))
	ncolors := int(be.Uint32(xa.fb[76:]))
	xa.off = hdr + 12*ncolors
	if bpp != 32 || xa.off+xa.stride*xa.h > len(xa.fb) {
		return os.ErrInvalid
	}
	return nil
}

// xFrames copies the app's screen to the panel while it's open.
func (c *console) xFrames(xa *xapp) {
	t := time.NewTicker(xFrameTick)
	defer t.Stop()
	for range t.C {
		drawMu.Lock()
		if c.xapp != xa {
			drawMu.Unlock()
			return
		}
		if c.screenOn && !c.locked && c.pinMode == "" {
			if time.Since(xa.lastFill) > time.Second {
				xa.x.fillScreen()
				xa.lastFill = time.Now()
			}
			c.drawXFrame(false)
			c.s.Flush()
		}
		exited := xa.proc != nil && xa.proc.ProcessState != nil
		drawMu.Unlock()
		if exited {
			return
		}
	}
}

// drawXFrame copies the rows of the app's screen that changed (all of them when full).
// Caller holds drawMu and flushes.
func (c *console) drawXFrame(full bool) {
	xa := c.xapp
	if xa == nil || xa.fb == nil {
		return
	}
	h := min(xa.h, c.s.H-c.xTop())
	if c.skb.visible {
		h = min(h, c.skb.y0-c.xTop())
	}
	rowB := xa.w * 4
	if len(xa.prev) != rowB*xa.h {
		xa.prev, full = make([]byte, rowB*xa.h), true
	}
	y0, y1 := -1, -1
	for y := 0; y < h; y++ {
		src := xa.fb[xa.off+y*xa.stride : xa.off+y*xa.stride+rowB]
		if full || !bytes.Equal(src, xa.prev[y*rowB:(y+1)*rowB]) {
			if y0 < 0 {
				y0 = y
			}
			y1 = y + 1
			copy(xa.prev[y*rowB:], src)
		}
	}
	if y0 < 0 {
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, xa.w, y1-y0))
	for y := y0; y < y1; y++ {
		src := xa.prev[y*rowB : (y+1)*rowB]
		dst := img.Pix[(y-y0)*img.Stride:]
		for x := 0; x < xa.w; x++ { // BGRX → RGBA
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = src[4*x+2], src[4*x+1], src[4*x], 255
		}
	}
	c.s.blitRGBA(img, 0, c.xTop()+y0)
}

// drawXapp paints the bar over the app (and the app's screen, or what it's waiting for).
// Caller holds drawMu.
func (c *console) drawXapp(flush bool) {
	xa := c.xapp
	if xa == nil {
		return
	}
	f := apple()
	w := c.s.W
	bar := image.NewRGBA(image.Rect(0, 0, w, xChromeH))
	ui.Fill(bar, bar.Rect, rgb(0x16171d))
	ui.Fill(bar, image.Rect(0, xChromeH-2, w, xChromeH), rgb(0x2a2b33))
	c.appIcon(bar, xa.app, image.Rect(24, 18, 84, 78))
	apText(bar, f.headline, 104, 58, rgb(0xffffff), clip(f.headline, xa.app.name, w-480))
	kb := image.Rect(w-330, 18, w-190, 78)
	ui.RoundRect(bar, kb, 30, rgb(0x2a2b33))
	apTextCenter(bar, f.callout, (kb.Min.X+kb.Max.X)/2, (kb.Min.Y+kb.Max.Y)/2, rgb(0xffffff), "Keys")
	done := image.Rect(w-170, 18, w-24, 78)
	ui.RoundRect(bar, done, 30, rgb(0x0a84ff))
	apTextCenter(bar, f.headline, (done.Min.X+done.Max.X)/2, (done.Min.Y+done.Max.Y)/2, rgb(0xffffff), "Done")
	c.s.blitRGBA(bar, 0, c.barH)
	if xa.fb == nil {
		body := image.NewRGBA(image.Rect(0, 0, w, c.s.H-c.xTop()))
		ui.Fill(body, body.Rect, rgb(0x0b0c10))
		c.appIcon(body, xa.app, image.Rect(w/2-80, 500, w/2+80, 660))
		apTextCenter(body, f.callout, w/2, 740, rgb(0x9a9da6), xa.status)
		c.s.blitRGBA(body, 0, c.xTop())
	} else {
		c.drawXFrame(true)
	}
	if c.skb.visible {
		c.skb.draw()
	}
	if flush {
		c.s.Flush()
	}
}

// xappTouch: the bar's buttons, the keyboard, or the pointer on the app. Caller holds drawMu.
func (c *console) xappTouch(p TouchPoint) {
	xa := c.xapp
	switch {
	case p.Y < c.xTop():
		if !p.Up {
			return
		}
		switch {
		case p.Y < c.barH || p.X >= c.s.W-180:
			c.stopDesktop()
		case p.X >= c.s.W-340:
			c.skb.visible = !c.skb.visible
			c.drawXapp(true)
		}
	case c.skb.visible && p.Y >= c.skb.y0:
		c.skb.touch(p)
	case xa.x != nil:
		x, y := p.X, p.Y-c.xTop()
		xa.x.move(x, y)
		switch {
		case p.Down:
			xa.x.button(1, true)
			xa.down = true
		case p.Up && xa.down:
			xa.x.button(1, false)
			xa.down = false
		}
	}
}

// xappKey types into the app. Caller holds drawMu.
func (c *console) xappKey(b []byte) {
	if c.xapp != nil && c.xapp.x != nil {
		c.xapp.x.typeBytes(b)
	}
}

// stopDesktop closes the app and the display, and goes back to where it was opened.
// Caller holds drawMu.
func (c *console) stopDesktop() {
	xa := c.xapp
	if xa == nil {
		return
	}
	c.xapp = nil
	c.skb.visible = false
	for _, p := range []*exec.Cmd{xa.proc, xa.xvfb} {
		if p != nil && p.Process != nil {
			syscall.Kill(-p.Process.Pid, syscall.SIGTERM)
			p.Process.Kill()
			go p.Wait()
		}
	}
	if xa.x != nil {
		xa.x.close()
	}
	if xa.unmap != nil {
		xa.unmap()
	}
	c.redrawAll()
}

// --- icons --------------------------------------------------------------------------------------

// appIconPath is the app's own icon once it's installed: its .desktop file's Icon, looked up in
// the hicolor theme (largest PNG first) and in pixmaps.
func appIconPath(app linuxApp) string {
	icon := app.cmd
	desks, _ := filepath.Glob(alpineRoot + "/usr/share/applications/*.desktop")
	for _, d := range desks {
		b, err := os.ReadFile(d)
		if err != nil {
			continue
		}
		s := string(b)
		base := strings.TrimSuffix(filepath.Base(d), ".desktop")
		if !strings.Contains(s, "\nExec="+app.cmd) && !strings.EqualFold(base, app.pkg) && !strings.HasSuffix(strings.ToLower(base), "."+app.pkg) {
			continue
		}
		for _, l := range strings.Split(s, "\n") {
			if v, ok := strings.CutPrefix(l, "Icon="); ok {
				icon = strings.TrimSpace(v)
				break
			}
		}
		break
	}
	if strings.HasPrefix(icon, "/") {
		return alpineRoot + icon
	}
	for _, size := range []string{"256x256", "128x128", "96x96", "64x64", "48x48"} {
		for _, theme := range []string{"hicolor", "Adwaita"} {
			p := alpineRoot + "/usr/share/icons/" + theme + "/" + size + "/apps/" + icon + ".png"
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if p := alpineRoot + "/usr/share/pixmaps/" + icon + ".png"; fileExists(p) {
		return p
	}
	return ""
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

var appIcons = map[string]*image.RGBA{} // decoded icon files, by path

// appIcon draws an app's icon in r: its own once installed (a PNG from its theme), else
// lucide's symbol for its kind on a colour of its own, as a rounded square.
func (c *console) appIcon(img *image.RGBA, app linuxApp, r image.Rectangle) {
	rad := r.Dx() * 22 / 100
	if path := appIconPath(app); path != "" && app.installed() {
		src, ok := appIcons[path]
		if !ok {
			if f, err := os.Open(path); err == nil {
				if im, _, err := image.Decode(f); err == nil {
					src = image.NewRGBA(im.Bounds())
					draw.Draw(src, src.Rect, im, im.Bounds().Min, draw.Src)
				}
				f.Close()
			}
			appIcons[path] = src
		}
		if src != nil {
			ui.RoundRect(img, r, rad, rgb(0x2a2b33))
			in := r.Inset(r.Dx() / 10)
			for y := in.Min.Y; y < in.Max.Y; y++ {
				for x := in.Min.X; x < in.Max.X; x++ {
					sx := src.Rect.Min.X + (x-in.Min.X)*src.Rect.Dx()/in.Dx()
					sy := src.Rect.Min.Y + (y-in.Min.Y)*src.Rect.Dy()/in.Dy()
					s := src.RGBAAt(sx, sy)
					if s.A == 0 || !(image.Pt(x, y).In(img.Rect)) {
						continue
					}
					a := float64(s.A) / 255
					o := img.PixOffset(x, y)
					img.Pix[o] = uint8(float64(img.Pix[o])*(1-a) + float64(s.R))
					img.Pix[o+1] = uint8(float64(img.Pix[o+1])*(1-a) + float64(s.G))
					img.Pix[o+2] = uint8(float64(img.Pix[o+2])*(1-a) + float64(s.B))
				}
			}
			return
		}
	}
	col := placeholderColour(app.pkg)
	ui.RoundRect(img, r, rad, col)
	glass(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+r.Dy()/2), rad, rgb(0xffffff), 0.1)
	if app.icon != "" {
		lucideIcon(img, app.icon, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, r.Dx()*55/100, rgb(0xffffff))
	} else {
		apTextCenter(img, apple().title, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, rgb(0xffffff), strings.ToUpper(app.name[:1]))
	}
}
