package main

import (
	"image"
	"image/color"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"margin-init/ui"
)

// Settings > Wi-Fi, as on an iPad: the networks around, strongest first, with their signal
// and a lock on the ones that need a password; tap one to join (typing its password on the
// keyboard first if it has one). The work is the `wifi` command's (wifi.go), run in the
// background; the switch turns Wi-Fi off, or back on to the saved network.

type wifiNet struct {
	ssid string
	dbm  int
	lock bool
}

type wifiUI struct {
	scanning bool
	scanned  time.Time
	nets     []wifiNet
	scanErr  string
	join     string // the network whose password is being asked for
	typing   bool   // its password field has the keyboard
	pass     string
	joining  string // the network being joined now
	msg      string // how the last join went, if it didn't
}

// wifiAutoScan: the pane looks for networks when it opens (tests turn it off).
var wifiAutoScan = true

// wifiCmd runs the wifi command in Alpine (tests fake it).
var wifiCmd = func(args ...string) ([]byte, error) {
	return outputInAlpine(append([]string{"/usr/local/bin/wifi"}, args...)...)
}

// parseScan reads `wifi scan -m`: "signal<TAB>lock<TAB>ssid" per access point; a network
// seen from several keeps its strongest.
func parseScan(out string) []wifiNet {
	best := map[string]wifiNet{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.SplitN(l, "\t", 3)
		if len(f) != 3 || f[2] == "" {
			continue
		}
		dbm, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			continue
		}
		n := wifiNet{ssid: f[2], dbm: int(dbm), lock: f[1] == "1"}
		if old, ok := best[n.ssid]; !ok || n.dbm > old.dbm {
			best[n.ssid] = n
		}
	}
	var nets []wifiNet
	for _, n := range best {
		nets = append(nets, n)
	}
	sort.Slice(nets, func(i, j int) bool {
		if nets[i].dbm != nets[j].dbm {
			return nets[i].dbm > nets[j].dbm
		}
		return nets[i].ssid < nets[j].ssid
	})
	return nets
}

// lastLine is the last thing a command said: why it failed.
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// wifiScan looks for networks in the background, unless it did a moment ago. Caller holds
// drawMu.
func (c *console) wifiScan(force bool) {
	w := &c.wifi
	if w.scanning || w.joining != "" || (!force && time.Since(w.scanned) < 30*time.Second) {
		return
	}
	w.scanning = true
	go func() {
		out, err := wifiCmd("scan", "-m")
		drawMu.Lock()
		defer drawMu.Unlock()
		w.scanning, w.scanned = false, time.Now()
		if err != nil {
			w.scanErr = lastLine(out)
			if w.scanErr == "" {
				w.scanErr = "The scan didn't work."
			}
			log.Printf("wifi scan: %v: %s", err, w.scanErr)
		} else {
			w.nets, w.scanErr = parseScan(string(out)), ""
		}
		c.wifiRedraw()
	}()
}

// wifiJoin joins ssid with pass ("" for an open network) in the background. Caller holds
// drawMu.
func (c *console) wifiJoin(ssid, pass string) {
	w := &c.wifi
	if w.joining != "" {
		return
	}
	w.joining, w.join, w.typing, w.pass, w.msg = ssid, "", false, "", ""
	c.skb.visible = false
	c.wifiBusy = true
	go func() {
		args := []string{"connect", ssid}
		if pass != "" {
			args = append(args, pass)
		}
		out, err := wifiCmd(args...)
		drawMu.Lock()
		defer drawMu.Unlock()
		w.joining, c.wifiBusy = "", false
		if err != nil {
			w.msg = lastLine(out)
			if w.msg == "" {
				w.msg = "Couldn't join “" + ssid + "”."
			}
			log.Printf("wifi: join %q: %v: %s", ssid, err, w.msg)
		} else {
			c.toastMsg("Connected to " + ssid)
		}
		c.drawBar()
		c.wifiRedraw()
	}()
}

// wifiOff turns Wi-Fi off in the background. Caller holds drawMu.
func (c *console) wifiOff() {
	c.wifiBusy = true
	go func() {
		out, err := wifiCmd("off")
		if err != nil {
			log.Printf("wifi off: %v: %s", err, lastLine(out))
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		c.wifiBusy = false
		c.drawBar()
		c.wifiRedraw()
	}()
}

func (c *console) wifiRedraw() {
	if c.mode == modeSettings && c.setPane == "wifi" && c.screenOn && !c.locked {
		c.showPage()
	}
}

// wifiTap handles the Wi-Fi pane's buttons. Caller holds drawMu.
func (c *console) wifiTap(id string) bool {
	w := &c.wifi
	switch {
	case id == "wifi:switch":
		switch {
		case w.joining != "" || c.wifiBusy:
		case wifiAddr() != "":
			c.wifiOff()
		case savedSSID() != "":
			c.wifiBusy = true
			go c.reconnectWifi()
		default:
			c.wifiScan(true)
		}
	case id == "wifi:scan":
		c.wifiScan(true)
	case strings.HasPrefix(id, "wifi:net:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(id, "wifi:net:"))
		if i < 0 || i >= len(w.nets) || w.joining != "" {
			return true
		}
		n := w.nets[i]
		switch {
		case n.ssid == savedSSID() && wifiAddr() != "":
			c.toastMsg("Connected to " + n.ssid)
		case n.ssid == savedSSID():
			c.wifiBusy = true
			go c.reconnectWifi() // its password is saved
		case n.lock:
			w.join, w.typing, w.pass, w.msg, c.fieldBack = n.ssid, true, "", "", 0
			c.skb.visible = true
		default:
			c.wifiJoin(n.ssid, "")
		}
	case id == "wifi:pass":
		w.typing, c.fieldBack = true, 0
		c.skb.visible = true
	case id == "wifi:cancel":
		w.join, w.typing, w.pass = "", false, ""
		c.skb.visible = false
	case id == "wifi:join":
		if len(w.pass) >= 8 {
			c.wifiJoin(w.join, w.pass)
		} else {
			w.msg = "A Wi-Fi password has at least 8 characters."
		}
	default:
		return false
	}
	c.showPage()
	return true
}

// wifiKey takes the keyboard while the password is typed. Caller holds drawMu.
func (c *console) wifiKey(b []byte) {
	w := &c.wifi
	if !w.typing {
		return
	}
	var done bool
	if w.pass, done = editLine(w.pass, &c.fieldBack, b, 63); done {
		if len(w.pass) >= 8 {
			c.wifiJoin(w.join, w.pass)
		} else {
			w.msg = "A Wi-Fi password has at least 8 characters."
		}
		c.showPage()
		return
	}
	c.fieldTyped()
}

// masked shows a password as dots, its last character still readable, as iOS does.
func masked(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return strings.Repeat("•", len(r)-1) + string(r[len(r)-1])
}

// wifiPane draws Settings > Wi-Fi. Returns the y under it.
func (c *console) wifiPane(p *page, x, y, pw int) int {
	f := apple()
	img := p.img
	w := &c.wifi
	if wifiAutoScan {
		c.wifiScan(false) // the networks around, when the pane opens
	}
	const rh = 88
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	header := func(s string) {
		apText(img, f.caption, x+30, y+40, apSecondary, strings.ToUpper(s))
		y += 56
	}
	footer := func(s string) {
		y = drawParagraphs(img, f.caption, s, x+30, y+14, pw-60, 34, y+200, apSecondary) + 30
	}

	// Asking for a password: on top, so the keyboard leaves it in view.
	if w.join != "" {
		card := image.Rect(x, y, x+pw, y+330)
		ui.RoundRect(img, card, 24, apCard)
		apText(img, f.headline, x+30, y+60, apLabel, clip(f.headline, "Enter the password for “"+w.join+"”", pw-60))
		field := image.Rect(x+30, y+100, x+pw-30, y+184)
		ui.RoundRect(img, field, 18, apCard2)
		text, col := "Password", apSecondary
		switch {
		case w.typing:
			text, col = withCursor(masked(w.pass), c.fieldBack), apLabel
		case w.pass != "":
			text, col = strings.Repeat("•", len([]rune(w.pass))), apLabel
		}
		apText(img, f.body, field.Min.X+26, field.Min.Y+54, col, clip(f.body, text, field.Dx()-52))
		btn("wifi:pass", field)
		half := (pw - 90) / 2
		cancel := image.Rect(x+30, y+214, x+30+half, y+300)
		join := image.Rect(x+60+half, y+214, x+pw-30, y+300)
		ui.RoundRect(img, cancel, 43, apCard2)
		apTextCenter(img, f.headline, (cancel.Min.X+cancel.Max.X)/2, (cancel.Min.Y+cancel.Max.Y)/2, apLabel, "Cancel")
		jc := apBlue
		if len(w.pass) < 8 {
			jc = blend(apBlue, apCard, 0.5)
		}
		ui.RoundRect(img, join, 43, jc)
		apTextCenter(img, f.headline, (join.Min.X+join.Max.X)/2, (join.Min.Y+join.Max.Y)/2, rgb(0xffffff), "Join")
		btn("wifi:cancel", cancel)
		btn("wifi:join", join)
		y = card.Max.Y + 10
		if w.msg != "" {
			footer(w.msg)
		}
		y += 20
	}

	ssid, ip := savedSSID(), wifiAddr()
	on := ip != "" || c.wifiBusy || w.joining != ""
	rows := groupRows(img, x, y, pw, 1, rh)
	apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Wi-Fi")
	iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, on, apDark)
	btn("wifi:switch", rows[0])
	y += rh + 20

	// The network it's on (or joining, or saved).
	cur, state := ssid, "Not Connected"
	switch {
	case w.joining != "":
		cur, state = w.joining, "Joining…"
	case c.wifiBusy && ip == "":
		state = "Connecting…"
	case ip != "":
		state = "Connected"
	}
	if cur != "" {
		rows = groupRows(img, x, y, pw, 1, rh)
		if ip != "" && w.joining == "" {
			iconCheck(img, rows[0].Min.X+26, (rows[0].Min.Y+rows[0].Max.Y)/2, apBlue)
		}
		apText(img, f.body, rows[0].Min.X+76, rows[0].Min.Y+56, apLabel, clip(f.body, cur, pw-400))
		apTextRight(img, f.body, rows[0].Max.X-30, rows[0].Min.Y+56, apSecondary, state)
		if ip == "" && w.joining == "" && !c.wifiBusy {
			btn("wifi", rows[0]) // join it again
		}
		y += rh + 10
	}
	if w.msg != "" && w.join == "" {
		footer(w.msg)
	}

	// The networks around.
	apText(img, f.caption, x+30, y+40, apSecondary, "NETWORKS")
	label := "Scan Again"
	if w.scanning {
		label = "Searching…"
	}
	apTextRight(img, f.caption, x+pw-30, y+40, apBlue, label)
	btn("wifi:scan", image.Rect(x+pw-300, y, x+pw, y+60))
	y += 56
	var list []int
	for i, n := range w.nets {
		if n.ssid != cur || ip == "" {
			list = append(list, i)
		}
	}
	switch {
	case len(list) > 0:
		rows = groupRows(img, x, y, pw, len(list), rh)
		for k, i := range list {
			n, r := w.nets[i], rows[k]
			cy := (r.Min.Y + r.Max.Y) / 2
			apText(img, f.body, r.Min.X+76, r.Min.Y+56, apLabel, clip(f.body, n.ssid, pw-300))
			rx := r.Max.X - 40
			wifiBars(img, rx-18, cy+10, n.dbm)
			if n.lock {
				iconLock(img, rx-70, cy, apLabel)
			}
			btn("wifi:net:"+strconv.Itoa(i), r)
		}
		y += len(list) * rh
	case w.scanning:
		rows = groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apSecondary, "Looking for networks…")
		y += rh
	case w.scanErr != "":
		rows = groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apSecondary, "No networks found")
		y += rh
		footer(w.scanErr)
	default:
		rows = groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apSecondary, "No networks found")
		y += rh
	}
	footer("Tap a network to join it. Margin joins it again at every start.")

	if ip != "" {
		value := func(r image.Rectangle, name, v string) {
			apText(img, f.body, r.Min.X+30, r.Min.Y+56, apLabel, name)
			apTextRight(img, f.body, r.Max.X-30, r.Min.Y+56, apSecondary, clip(f.body, v, r.Dx()/2))
		}
		header("Address")
		rows = groupRows(img, x, y, pw, 2, rh)
		value(rows[0], "IP Address", ip)
		value(rows[1], "SSH", "root@"+ip)
		y += 2 * rh
		if a := sendAddress(); a != "" {
			header("Send to Books")
			rows = groupRows(img, x, y, pw, 1, rh)
			value(rows[0], "From a phone's browser", a)
			y += rh
			footer("On a phone or computer on the same Wi-Fi, open this address to send EPUB books to the Library.")
		}
	}
	return y
}

// wifiBars is the Wi-Fi fan, its arcs lit by the signal (bottom centre at cx, base).
func wifiBars(img *image.RGBA, cx, base int, dbm int) {
	lit := 1
	switch {
	case dbm >= -60:
		lit = 3
	case dbm >= -72:
		lit = 2
	}
	for k, rad := range []int{8, 16, 24} {
		col := apLabel
		if k >= lit {
			col = apSeparator
		}
		for a := 225.0; a <= 315; a += 3 {
			t := a * math.Pi / 180
			ui.Circle(img, cx+int(float64(rad)*math.Cos(t)), base+int(float64(rad)*math.Sin(t)), 2, col)
		}
	}
	ui.Circle(img, cx, base, 3, apLabel)
}

// iconLock is a small padlock centred on (cx, cy).
func iconLock(img *image.RGBA, cx, cy int, col color.RGBA) {
	ui.RoundRect(img, image.Rect(cx-11, cy-2, cx+11, cy+15), 3, col)
	line(img, cx-7, cy-2, cx-7, cy-9, 3, col)
	line(img, cx+7, cy-2, cx+7, cy-9, 3, col)
	line(img, cx-7, cy-9, cx, cy-14, 3, col)
	line(img, cx, cy-14, cx+7, cy-9, 3, col)
}
