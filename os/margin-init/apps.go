package main

import (
	"fmt"
	"image"
	"os"
	"sort"
	"strings"

	"margin-init/ui"
)

// Settings > Linux Apps: a store of Alpine's packages that work here (in the terminal: the
// tablet has no desktop to open windows on), installed with apk, opened in the Terminal with
// a tap, removed with another. Search finds any of Alpine's packages.

type linuxApp struct {
	pkg, cmd, name, desc, kind string
}

var linuxApps = []linuxApp{
	{"btop", "btop", "btop", "A beautiful monitor of the processor, memory, disks and network.", "System"},
	{"htop", "htop", "htop", "Processes, the classic way.", "System"},
	{"ncdu", "ncdu", "ncdu", "What fills the storage, folder by folder.", "System"},
	{"fastfetch", "fastfetch", "fastfetch", "The system at a glance, with the Alpine logo.", "System"},
	{"mc", "mc", "Midnight Commander", "A two-pane file manager.", "Files"},
	{"nnn", "nnn", "nnn", "A tiny, fast file manager.", "Files"},
	{"ranger", "ranger", "ranger", "Files in columns, with previews.", "Files"},
	{"nano", "nano", "nano", "A simple text editor.", "Writing"},
	{"neovim", "nvim", "Neovim", "The editor for people who live in it.", "Writing"},
	{"micro", "micro", "micro", "An editor with mouse-free, familiar keys.", "Writing"},
	{"wordgrinder", "wordgrinder", "WordGrinder", "A word processor for writing, nothing else.", "Writing"},
	{"calcurse", "calcurse", "calcurse", "A calendar and to-do list.", "Organise"},
	{"task", "task", "Taskwarrior", "To-dos from the command line.", "Organise"},
	{"sc-im", "sc-im", "sc-im", "A spreadsheet.", "Organise"},
	{"w3m", "w3m", "w3m", "Browse the web as text.", "Internet"},
	{"lynx", "lynx", "Lynx", "The oldest web browser still going.", "Internet"},
	{"newsboat", "newsboat", "Newsboat", "Read RSS and Atom feeds.", "Internet"},
	{"weechat", "weechat", "WeeChat", "Chat on IRC.", "Internet"},
	{"aria2", "aria2c", "aria2", "Downloads, fast and resumable.", "Internet"},
	{"python3", "python3", "Python", "Programming, with its interactive prompt.", "Code"},
	{"nodejs", "node", "Node.js", "JavaScript outside the browser.", "Code"},
	{"git", "git", "Git", "Version control.", "Code"},
	{"tmux", "tmux", "tmux", "Several terminals in one, kept running.", "Code"},
	{"bc", "bc", "bc", "A calculator with any precision.", "Tools"},
	{"figlet", "figlet", "FIGlet", "Big letters made of letters.", "Fun"},
	{"cmatrix", "cmatrix", "cmatrix", "The Matrix's falling code.", "Fun"},
	{"sl", "sl", "sl", "A steam locomotive, for when you mistype ls.", "Fun"},
	{"nethack", "nethack", "NetHack", "The dungeon crawler, since 1987.", "Games"},
	{"bastet", "bastet", "Bastet", "Tetris, but it gives you the worst piece.", "Games"},
	{"moon-buggy", "moon-buggy", "Moon Buggy", "Drive across the Moon, jump the craters.", "Games"},
}

type appsState struct {
	typing  bool   // the search box has the keyboard
	editing string
	busy    map[string]string // package → "Installing..." / "Removing..."
	msg     string
	query   string
	results []linuxApp
}

var (
	apkRun = func(args ...string) error { return runInAlpine(args...) }
	apkOut = func(args ...string) ([]byte, error) { return outputInAlpine(args...) }
)

// installed: the app's command is in Alpine.
func (a linuxApp) installed() bool {
	for _, d := range []string{"/usr/bin/", "/bin/", "/usr/local/bin/", "/usr/games/", "/usr/sbin/"} {
		if _, err := os.Stat(alpineRoot + d + a.cmd); err == nil {
			return true
		}
	}
	return false
}

// appsSearch looks through all of Alpine's packages. Caller holds drawMu.
func (c *console) appsSearch(q string) {
	as := &c.apps
	as.query, as.msg = strings.TrimSpace(q), ""
	as.results = nil
	if as.query == "" {
		return
	}
	as.msg = "Searching..."
	go func(q string) {
		out, err := apkOut("apk", "search", "-d", q)
		drawMu.Lock()
		defer drawMu.Unlock()
		as.msg = ""
		if err != nil {
			as.msg = "Couldn't search: is Wi-Fi on? (" + strings.TrimSpace(string(out)) + ")"
		}
		as.results = parseApkSearch(string(out))
		if len(as.results) == 0 && as.msg == "" {
			as.msg = "Nothing found for " + q + "."
		}
		if c.mode == modeSettings && c.setPane == "apps" {
			c.showPage()
		}
	}(as.query)
}

// parseApkSearch reads "name-1.2-r0 - description" lines.
func parseApkSearch(out string) []linuxApp {
	var res []linuxApp
	for _, l := range strings.Split(out, "\n") {
		name, desc, ok := strings.Cut(strings.TrimSpace(l), " - ")
		if !ok {
			continue
		}
		// Drop the version: the last two dash-separated parts (1.2-r0).
		parts := strings.Split(name, "-")
		if len(parts) > 2 {
			name = strings.Join(parts[:len(parts)-2], "-")
		}
		if strings.HasSuffix(name, "-doc") || strings.HasSuffix(name, "-dev") || strings.HasSuffix(name, "-dbg") || strings.HasSuffix(name, "-lang") {
			continue
		}
		res = append(res, linuxApp{pkg: name, cmd: name, name: name, desc: desc, kind: "Search"})
		if len(res) == 30 {
			break
		}
	}
	return res
}

// appsTap installs, opens or removes. Caller holds drawMu.
func (c *console) appsTap(id string) bool {
	as := &c.apps
	if as.busy == nil {
		as.busy = map[string]string{}
	}
	act, pkg, ok := strings.Cut(strings.TrimPrefix(id, "app:"), ":")
	if !strings.HasPrefix(id, "app:") || !ok {
		return false
	}
	var app linuxApp
	for _, a := range append(append([]linuxApp{}, linuxApps...), as.results...) {
		if a.pkg == pkg {
			app = a
		}
	}
	if app.pkg == "" {
		return false
	}
	switch act {
	case "open":
		c.setMode(modeTerminal)
		c.input([]byte(app.cmd + "\r"))
		return true
	case "get", "del":
		if as.busy[pkg] != "" {
			return true
		}
		verb, label := "add", "Installing..."
		if act == "del" {
			verb, label = "del", "Removing..."
		}
		as.busy[pkg] = label
		go func() {
			err := apkRun("apk", verb, pkg)
			drawMu.Lock()
			defer drawMu.Unlock()
			delete(as.busy, pkg)
			switch {
			case err != nil:
				as.msg = fmt.Sprintf("%s: apk %s failed (is Wi-Fi on?)", app.name, verb)
			case act == "get":
				c.toastMsg(app.name + " installed")
			default:
				c.toastMsg(app.name + " removed")
			}
			if c.mode == modeSettings && c.setPane == "apps" {
				c.showPage()
			}
		}()
	}
	c.showPage()
	return true
}

// appsPane draws the store: search, then the apps by kind. Returns the y under it.
func (c *console) appsPane(p *page, x, y, pw int) int {
	f := apple()
	img := p.img
	as := &c.apps
	// Search Alpine.
	sr := image.Rect(x, y, x+pw, y+84)
	ui.RoundRect(img, sr, 42, apCard)
	iconMagnifier(img, sr.Min.X+44, (sr.Min.Y+sr.Max.Y)/2, apSecondary)
	text, col := "Search all of Alpine's apps", apSecondary
	if c.apps.typing {
		text, col = withCursor(c.apps.editing, c.fieldBack), apLabel
	} else if as.query != "" {
		text, col = as.query, apLabel
	}
	apText(img, f.body, sr.Min.X+84, sr.Min.Y+54, col, clip(f.body, text, pw-120))
	p.buttons = append(p.buttons, button{"apps:search", sr})
	y = sr.Max.Y + 20
	if as.msg != "" {
		y = drawParagraphs(img, f.callout, as.msg, x+20, y+10, pw-40, 40, y+200, apSecondary) + 10
	}
	list := linuxApps
	if len(as.results) > 0 {
		list = as.results
	}
	kinds := map[string][]linuxApp{}
	var order []string
	for _, a := range list {
		if _, ok := kinds[a.kind]; !ok {
			order = append(order, a.kind)
		}
		kinds[a.kind] = append(kinds[a.kind], a)
	}
	if len(as.results) == 0 {
		sort.SliceStable(order, func(i, j int) bool { return false })
	}
	for _, k := range order {
		apText(img, f.caption, x+30, y+40, apSecondary, strings.ToUpper(k))
		y += 56
		for _, a := range kinds[k] {
			r := image.Rect(x, y, x+pw, y+120)
			ui.RoundRect(img, r, radiusCard, apCard)
			// An icon: the name's first letter on a colour of its own.
			ic := image.Rect(r.Min.X+20, r.Min.Y+20, r.Min.X+100, r.Min.Y+100)
			ui.RoundRect(img, ic, 20, placeholderColour(a.pkg))
			apTextCenter(img, f.title, (ic.Min.X+ic.Max.X)/2, (ic.Min.Y+ic.Max.Y)/2, rgb(0xffffff), strings.ToUpper(a.name[:1]))
			apText(img, f.headline, r.Min.X+124, r.Min.Y+50, apLabel, clip(f.headline, a.name, pw-124-210))
			apText(img, f.caption, r.Min.X+124, r.Min.Y+88, apSecondary, clip(f.caption, a.desc, pw-124-210))
			bx := r.Max.X - 24
			pill := func(id, label string, c1, c2 uint32) {
				w := ui.TextWidth(f.headline, label) + 50
				br := image.Rect(bx-w, r.Min.Y+30, bx, r.Max.Y-30)
				ui.RoundRect(img, br, br.Dy()/2, rgb(c1))
				apTextCenter(img, f.headline, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2, rgb(c2), label)
				if id != "" {
					p.buttons = append(p.buttons, button{id, br})
				}
				bx -= w + 12
			}
			switch {
			case as.busy[a.pkg] != "":
				pill("", as.busy[a.pkg], 0x2c2c2e, 0x8d8d93)
			case a.installed():
				pill("app:open:"+a.pkg, "Open", 0x0a84ff, 0xffffff)
				pill("app:del:"+a.pkg, "Remove", 0x2c2c2e, 0xff453a)
			default:
				pill("app:get:"+a.pkg, "Get", 0x2c2c2e, 0x0a84ff)
			}
			y += 132
		}
	}
	return y + 20
}

// appsSearchTap opens the keyboard on the search box. Caller holds drawMu.
func (c *console) appsSearchTap(id string) bool {
	if id != "apps:search" {
		return false
	}
	c.apps.typing, c.apps.editing, c.fieldBack = true, c.apps.query, 0
	c.skb.visible = true
	c.showPage()
	return true
}

// appsKey takes the keyboard while searching. Caller holds drawMu.
func (c *console) appsKey(b []byte) {
	as := &c.apps
	if !as.typing {
		return
	}
	var done bool
	if as.editing, done = editLine(as.editing, &c.fieldBack, b, 60); done {
		as.typing, c.skb.visible = false, false
		c.appsSearch(as.editing)
	}
	c.fieldTyped()
}
