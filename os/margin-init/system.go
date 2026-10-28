package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"strings"

	"margin-init/ui"
)

// Settings in iPadOS's look (a sidebar of panes with coloured icons, grouped lists on the
// right). It's a tab of the Books app, which is the whole system: there's no home screen.

// --- Settings ------------------------------------------------------------------------------

type settingsPane struct {
	id, name string
	icon     color.RGBA
}

var settingsPanes = []settingsPane{
	{"wifi", "Wi-Fi", rgb(0x0a84ff)},
	{"battery", "Battery", rgb(0x34c759)},
	{"display", "Display & Brightness", rgb(0x0a84ff)},
	{"wallpaper", "Wallpaper", rgb(0x5e5ce6)},
	{"sleep", "Sleep & Alarm", rgb(0xff9f0a)},
	{"tasks", "Task Manager", rgb(0x30d158)},
	{"general", "General", rgb(0x8e8e93)},
	{"power", "Restart & Shut Down", rgb(0xff3b30)},
}

// paneGlyph draws a pane's white symbol on its coloured square.
func paneGlyph(img *image.RGBA, id string, cx, cy int) {
	white := rgb(0xffffff)
	switch id {
	case "wifi":
		for _, rad := range []int{6, 12, 18} {
			for a := 225.0; a <= 315; a += 4 {
				t := a * math.Pi / 180
				ui.Circle(img, cx+int(float64(rad)*math.Cos(t)), cy+8+int(float64(rad)*math.Sin(t)), 2, white)
			}
		}
		ui.Circle(img, cx, cy+8, 3, white)
	case "battery":
		ui.RoundRect(img, image.Rect(cx-16, cy-8, cx+12, cy+8), 3, white)
		ui.Fill(img, image.Rect(cx+13, cy-3, cx+16, cy+3), white)
	case "display":
		ui.DrawText(img, apple().captionBold, cx-15, cy+9, white, "AA")
	case "tasks":
		for i, hgt := range []int{10, 22, 16} {
			ui.Fill(img, image.Rect(cx-14+i*10, cy+12-hgt, cx-8+i*10, cy+12), white)
		}
	case "sleep":
		ui.Circle(img, cx, cy, 14, white)
		ui.Circle(img, cx+7, cy-6, 12, rgb(0xff9f0a))
	case "wallpaper":
		ui.Circle(img, cx-6, cy-4, 8, white)
		ui.Circle(img, cx+7, cy+5, 10, white)
	case "general":
		ring(img, cx, cy, 11, 6, 1, white, white)
		for a := 0; a < 360; a += 45 {
			t := float64(a) * math.Pi / 180
			ui.Circle(img, cx+int(15*math.Cos(t)), cy+int(15*math.Sin(t)), 3, white)
		}
	case "power":
		ring(img, cx, cy+2, 12, 4, 0.85, white, white)
		ui.Fill(img, image.Rect(cx-2, cy-16, cx+2, cy), white)
	}
}

// groupRows draws an inset grouped list section and returns the rows' rectangles.
func groupRows(img *image.RGBA, x, y, w, n, rh int) []image.Rectangle {
	ui.RoundRect(img, image.Rect(x, y, x+w, y+n*rh), 24, apCard)
	var rows []image.Rectangle
	for i := 0; i < n; i++ {
		r := image.Rect(x, y+i*rh, x+w, y+(i+1)*rh)
		if i > 0 {
			ui.Fill(img, image.Rect(x+30, r.Min.Y, x+w, r.Min.Y+1), apSeparator)
		}
		rows = append(rows, r)
	}
	return rows
}

func (c *console) settingsPage() *page {
	f := apple()
	w, h := c.s.W, c.s.H-c.barH
	switch c.setPane {
	case "display":
		h = max(h, 2300) // taller than the screen: it scrolls
	case "wallpaper":
		h = max(h, 2200+len(c.walls.items)/3*560)
	case "tasks":
		h = max(h, 2400)
	}
	img := canvas(w, h)
	ui.Fill(img, img.Rect, apGrouped)
	p := &page{img: img}
	if c.setPane == "" {
		c.setPane = "wifi"
	}

	// The sidebar.
	const sw = 500
	ui.Fill(img, image.Rect(sw, 0, sw+1, h), apSeparator)
	iconBack(img, 32, 64, apBlue)
	apText(img, f.body, 60, 76, apBlue, "Books")
	p.buttons = append(p.buttons, button{"home", image.Rect(0, 10, 220, 120)})
	apText(img, f.largeTitle, 32, 200, apLabel, "Settings")
	// The device card, like the Apple Account row.
	dc := image.Rect(24, 240, sw-24, 360)
	ui.RoundRect(img, dc, 24, apCard)
	ui.Circle(img, dc.Min.X+60, (dc.Min.Y+dc.Max.Y)/2, 40, rgb(0x8e8e93))
	apTextCenter(img, f.headline, dc.Min.X+60, (dc.Min.Y+dc.Max.Y)/2, rgb(0xffffff), "M")
	apText(img, f.headline, dc.Min.X+120, dc.Min.Y+54, apLabel, "Margin")
	apText(img, f.caption, dc.Min.X+120, dc.Min.Y+92, apSecondary, "Condor TRA-901G")
	y := 400
	ui.RoundRect(img, image.Rect(24, y, sw-24, y+len(settingsPanes)*84), 24, apCard)
	for i, pn := range settingsPanes {
		r := image.Rect(24, y+i*84, sw-24, y+(i+1)*84)
		on := pn.id == c.setPane
		fg := apLabel
		if on {
			ui.RoundRect(img, r.Inset(4), 18, apBlue)
			fg = rgb(0xffffff)
		} else if i > 0 {
			ui.Fill(img, image.Rect(r.Min.X+92, r.Min.Y, r.Max.X, r.Min.Y+1), apSeparator)
		}
		ir := image.Rect(r.Min.X+20, r.Min.Y+20, r.Min.X+64, r.Min.Y+64)
		ui.RoundRect(img, ir, 11, pn.icon)
		paneGlyph(img, pn.id, (ir.Min.X+ir.Max.X)/2, (ir.Min.Y+ir.Max.Y)/2)
		apText(img, f.callout, r.Min.X+86, r.Min.Y+52, fg, clip(f.callout, pn.name, r.Dx()-110))
		p.buttons = append(p.buttons, button{"set:pane:" + pn.id, r})
	}

	// The pane.
	x, pw := sw+40, w-sw-80
	pane := settingsPanes[0]
	for _, pn := range settingsPanes {
		if pn.id == c.setPane {
			pane = pn
		}
	}
	apTextCenter(img, f.headline, sw+(w-sw)/2, 70, apLabel, pane.name)
	y = 150
	header := func(s string) {
		apText(img, f.caption, x+30, y+40, apSecondary, strings.ToUpper(s))
		y += 56
	}
	footer := func(s string) {
		y = drawParagraphs(img, f.caption, s, x+30, y+14, pw-60, 34, y+200, apSecondary) + 30
	}
	value := func(r image.Rectangle, name, v string) {
		apText(img, f.body, r.Min.X+30, r.Min.Y+56, apLabel, name)
		apTextRight(img, f.body, r.Max.X-30, r.Min.Y+56, apSecondary, clip(f.body, v, r.Dx()/2))
	}
	const rh = 88
	switch pane.id {
	case "wifi":
		ssid, ip := savedSSID(), wifiAddr()
		rows := groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Wi-Fi")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, ip != "" || c.wifiBusy, apDark)
		if ip == "" && ssid != "" {
			p.buttons = append(p.buttons, button{"wifi", rows[0]}) // on: join the saved network
		}
		y += rh + 20
		if ssid != "" {
			header("Network")
			rows = groupRows(img, x, y, pw, 2, rh)
			if ip != "" {
				iconCheck(img, rows[0].Min.X+26, (rows[0].Min.Y+rows[0].Max.Y)/2, apBlue)
			}
			apText(img, f.body, rows[0].Min.X+76, rows[0].Min.Y+56, apLabel, ssid)
			state := "Not Connected"
			if ip != "" {
				state = "Connected"
			}
			apTextRight(img, f.body, rows[0].Max.X-30, rows[0].Min.Y+56, apSecondary, state)
			label := "Reconnect"
			if c.wifiBusy {
				label = "Connecting…"
			}
			apText(img, f.body, rows[1].Min.X+30, rows[1].Min.Y+56, apBlue, label)
			p.buttons = append(p.buttons, button{"wifi", rows[1]})
			y += 2 * rh
		}
		if ip != "" {
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
		footer("To join a network, open Terminal and type: wifi connect \"name\" \"password\". The network is joined again at every start.")
	case "battery":
		pct, charging := ui.ReadBattery()
		rows := groupRows(img, x, y, pw, 1, 260)
		r := rows[0]
		if pct >= 0 {
			col := rgb(0x34c759)
			if pct <= 20 && !charging {
				col = rgb(0xff3b30)
			}
			ring(img, r.Min.X+130, (r.Min.Y+r.Max.Y)/2, 80, 22, float64(pct)/100, apCard2, col)
			apTextCenter(img, f.title, r.Min.X+130, (r.Min.Y+r.Max.Y)/2, apLabel, fmt.Sprintf("%d%%", pct))
			state := "On Battery"
			if charging {
				state = "Charging"
			}
			apText(img, f.title, r.Min.X+260, r.Min.Y+120, apLabel, state)
			apText(img, f.callout, r.Min.X+260, r.Min.Y+170, apSecondary, batteryInfo())
		} else {
			apText(img, f.body, r.Min.X+30, r.Min.Y+80, apSecondary, "No battery found.")
		}
		y += 280
		footer("The battery is the original one from 2014. Charge it with the 5 V 2 A adapter for the best results.")
	case "tasks":
		y = c.taskPane(p, x, y, pw)
	case "sleep":
		timeRow := func(r image.Rectangle, label, minus, plus string, mins int, on bool) {
			apText(img, f.body, r.Min.X+30, r.Min.Y+56, apLabel, label)
			col := apSecondary
			if on {
				col = apLabel
			}
			apTextCenter(img, f.title, (r.Min.X+r.Max.X)/2+60, (r.Min.Y+r.Max.Y)/2, col, hhmm(mins))
			for i, id := range []string{minus, plus} {
				b := image.Rect(r.Max.X-200+i*100, r.Min.Y+12, r.Max.X-120+i*100, r.Max.Y-12)
				ui.RoundRect(img, b, 30, apCard2)
				apTextCenter(img, f.title, (b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2, apBlue, []string{"−", "+"}[i])
				p.buttons = append(p.buttons, button{id, b})
			}
		}
		header("Alarm")
		rows := groupRows(img, x, y, pw, 3, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Alarm")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, c.cfg.AlarmOn, apDark)
		p.buttons = append(p.buttons, button{"alarm", rows[0]})
		timeRow(rows[1], "Time", "alarm-", "alarm+", c.cfg.AlarmMin, c.cfg.AlarmOn)
		apText(img, f.body, rows[2].Min.X+30, rows[2].Min.Y+56, apLabel, "Sunrise Before It")
		iosSwitch(img, rows[2].Max.X-24, (rows[2].Min.Y+rows[2].Max.Y)/2, !c.cfg.NoSunrise, apDark)
		p.buttons = append(p.buttons, button{"sunrise", rows[2]})
		y += 3*rh + 20
		footer("The screen brightens over 15 minutes before the alarm, then it rings. Tap I'm up: the night is logged in Soma, and the morning shows the weather and your day.")
		header("Wind Down")
		rows = groupRows(img, x, y, pw, 2, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Wind Down")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, c.cfg.WindOn, apDark)
		p.buttons = append(p.buttons, button{"wind", rows[0]})
		timeRow(rows[1], "From", "wind-", "wind+", c.cfg.WindMin, c.cfg.WindOn)
		y += 2*rh + 20
		footer("From this time until 5 am the screen is warm and dim, and Soma opens on today's list for the evening review.")
	case "wallpaper":
		header("Lock Screen")
		y = c.wallpaperPane(p, x, y, pw)
	case "display":
		header("Appearance")
		rows := groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Dark Mode")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, !c.cfg.Light, apDark)
		p.buttons = append(p.buttons, button{"dark", rows[0]})
		y += rh + 20
		header("Lock Screen")
		rows = groupRows(img, x, y, pw, 2, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Show Lock Screen on Wake")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, !c.cfg.NoLock, apDark)
		p.buttons = append(p.buttons, button{"lock", rows[0]})
		apText(img, f.body, rows[1].Min.X+30, rows[1].Min.Y+56, apLabel, "Passcode")
		state := "Off"
		if c.cfg.PinHash != "" {
			state = "On"
		}
		apText(img, f.body, rows[1].Max.X-40-ui.TextWidth(f.body, state+"  ›"), rows[1].Min.Y+56, apSecondary, state+"  ›")
		p.buttons = append(p.buttons, button{"passcode", rows[1]})
		y += rh
		y += rh + 20
		header("Brightness")
		rows = groupRows(img, x, y, pw, 1, 120)
		r := rows[0]
		tx0, tx1, ty := r.Min.X+110, r.Max.X-110, (r.Min.Y+r.Max.Y)/2
		ui.RoundRect(img, image.Rect(tx0, ty-4, tx1, ty+4), 4, apCard2)
		kx := tx0 + (tx1-tx0)*(c.cfg.Brightness-10)/90
		ui.RoundRect(img, image.Rect(tx0, ty-4, kx, ty+4), 4, apBlue)
		ui.Circle(img, kx, ty+2, 23, apSeparator)
		ui.Circle(img, kx, ty, 22, rgb(0xffffff))
		apTextCenter(img, f.headline, r.Min.X+56, ty, apSecondary, "−")
		apTextCenter(img, f.headline, r.Max.X-56, ty, apSecondary, "+")
		p.buttons = append(p.buttons, button{"bright-", image.Rect(r.Min.X, r.Min.Y, r.Min.X+r.Dx()/2, r.Max.Y)},
			button{"bright+", image.Rect(r.Min.X+r.Dx()/2, r.Min.Y, r.Max.X, r.Max.Y)})
		y += 140
		footer(fmt.Sprintf("%d%%. The volume buttons change it too, outside a book.", c.cfg.Brightness))
		header("Text Size")
		rows = groupRows(img, x, y, pw, 1, 120)
		r = rows[0]
		ty = (r.Min.Y + r.Max.Y) / 2
		apTextCenter(img, f.caption, r.Min.X+56, ty, apSecondary, "A")
		apTextCenter(img, f.title, r.Max.X-56, ty, apSecondary, "A")
		sizes := []float64{0.9, 1, 1.1, 1.15}
		for i, s := range sizes {
			dx := r.Min.X + 160 + i*(r.Dx()-320)/(len(sizes)-1)
			col := apCard2
			if s == textScale {
				col = apBlue
			}
			ui.Circle(img, dx, ty, 16, col)
		}
		p.buttons = append(p.buttons, button{"text-", image.Rect(r.Min.X, r.Min.Y, r.Min.X+r.Dx()/2, r.Max.Y)},
			button{"text+", image.Rect(r.Min.X+r.Dx()/2, r.Min.Y, r.Max.X, r.Max.Y)})
		y += 140
		footer(fmt.Sprintf("%.0f%%. Books have their own size, in a book's Aa menu.", textScale*100))
		header("Auto-Lock")
		opts := []struct {
			id, name string
			min      int
		}{{"off1", "1 Minute", 1}, {"off5", "5 Minutes", 5}, {"off10", "10 Minutes", 10}, {"off0", "Never", 0}}
		rows = groupRows(img, x, y, pw, len(opts), rh)
		for i, o := range opts {
			apText(img, f.body, rows[i].Min.X+30, rows[i].Min.Y+56, apLabel, o.name)
			if c.cfg.ScreenOff == o.min {
				iconCheck(img, rows[i].Max.X-60, (rows[i].Min.Y+rows[i].Max.Y)/2, apBlue)
			}
			p.buttons = append(p.buttons, button{o.id, rows[i]})
		}
		y += len(opts) * rh
		footer("The power button turns the screen off and on at any time.")
		header("Motion")
		rows = groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Animations")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, c.cfg.Animations, apDark)
		p.buttons = append(p.buttons, button{"anim", rows[0]})
		y += rh
		rows = groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Follow Finger When Scrolling")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, c.cfg.SmoothScroll, apDark)
		p.buttons = append(p.buttons, button{"smoothscroll", rows[0]})
		y += rh
		rows = groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Draw With the GPU")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, gpuEnabled(), apDark)
		p.buttons = append(p.buttons, button{"gpu", rows[0]})
		y += rh
		switch {
		case c.disp != nil:
			footer("The GPU shows the screen: pages follow your finger at 60 frames a second, and animations are smooth. Turning it off restarts the tablet.")
		case c.gpuStarting:
			footer("Starting the GPU...")
		default:
			footer("Off: every screen appears at once, like an e-reader. On: the GPU shows the screen (through Android's SurfaceFlinger), so scrolling follows your finger at 60 frames a second.")
		}
	case "general":
		header("About")
		ver := alpineVersion()
		if ver == "" {
			ver = "not installed"
		}
		host, _ := os.Hostname()
		items := [][2]string{{"Name", host}, {"Model", "Condor TRA-901G"}, {"Processor", "Intel Atom Z2580"},
			{"Software", "Margin on Alpine " + ver}, {"Kernel", "Linux " + kernelRelease()}, {"Up For", uptime()}}
		rows := groupRows(img, x, y, pw, len(items), rh)
		for i, it := range items {
			value(rows[i], it[0], it[1])
		}
		y += len(items) * rh
		footer("Fonts: Inter (the system font), Liberation and DejaVu. Books from Project Gutenberg, the Internet Archive, Open Library and Google Books.")
	case "power":
		header("Power")
		items := []struct{ id, name string }{{"restart", "Restart"}, {"poweroff", "Shut Down"}, {"android", "Restart into Android"}}
		rows := groupRows(img, x, y, pw, len(items), rh)
		for i, it := range items {
			name, col := it.name, apBlue
			if it.id == "poweroff" || it.id == "android" {
				col = apRed
			}
			if c.confirm == it.id {
				name, col = "Tap Again to "+it.name, apRed
			}
			apText(img, f.body, rows[i].Min.X+30, rows[i].Min.Y+56, col, name)
			p.buttons = append(p.buttons, button{it.id, rows[i]})
		}
		y += len(items) * rh
		footer("Restart into Android turns Margin's autostart off. To come back, run margin takeover auto on from the PC.")
	}
	return p
}
