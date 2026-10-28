package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"margin-init/ui"
)

// Settings > Linux Apps: a store of Alpine's packages, installed with apk, removed with a
// tap. Desktop apps open full screen on Margin's own X display (desktop.go); the rest open in
// the Terminal. Search finds any of Alpine's packages.

type linuxApp struct {
	pkg, cmd, name, desc, kind string
	gui                        bool
	icon                       string // lucide's, on a coloured square, until its own is installed
}

var linuxApps = []linuxApp{
	{pkg: "dillo", cmd: "dillo", name: "Dillo", desc: "A small, fast web browser.", kind: "Desktop apps", gui: true, icon: "globe"},
	{pkg: "netsurf", cmd: "netsurf-gtk3", name: "NetSurf", desc: "A web browser that runs on little.", kind: "Desktop apps", gui: true, icon: "globe"},
	{pkg: "mousepad", cmd: "mousepad", name: "Mousepad", desc: "A simple text editor.", kind: "Desktop apps", gui: true, icon: "file-text"},
	{pkg: "geany", cmd: "geany", name: "Geany", desc: "A light programming editor.", kind: "Desktop apps", gui: true, icon: "file-code"},
	{pkg: "abiword", cmd: "abiword", name: "AbiWord", desc: "A word processor.", kind: "Desktop apps", gui: true, icon: "file-text"},
	{pkg: "gnumeric", cmd: "gnumeric", name: "Gnumeric", desc: "A spreadsheet.", kind: "Desktop apps", gui: true, icon: "table"},
	{pkg: "galculator", cmd: "galculator", name: "Galculator", desc: "A scientific calculator.", kind: "Desktop apps", gui: true, icon: "calculator"},
	{pkg: "mtpaint", cmd: "mtpaint", name: "mtPaint", desc: "Painting and pixel art.", kind: "Desktop apps", gui: true, icon: "pen-tool"},
	{pkg: "gpicview", cmd: "gpicview", name: "GPicView", desc: "A quick picture viewer.", kind: "Desktop apps", gui: true, icon: "image"},
	{pkg: "pcmanfm", cmd: "pcmanfm", name: "PCManFM", desc: "A file manager with windows.", kind: "Desktop apps", gui: true, icon: "folder-open"},
	{pkg: "audacious", cmd: "audacious", name: "Audacious", desc: "A music player.", kind: "Desktop apps", gui: true, icon: "music"},
	{pkg: "xterm", cmd: "xterm", name: "XTerm", desc: "The X terminal.", kind: "Desktop apps", gui: true, icon: "terminal"},
	{pkg: "gnome-mines", cmd: "gnome-mines", name: "Mines", desc: "Clear the minefield.", kind: "Desktop games", gui: true, icon: "gamepad-2"},
	{pkg: "gnome-sudoku", cmd: "gnome-sudoku", name: "Sudoku", desc: "The number puzzle.", kind: "Desktop games", gui: true, icon: "gamepad-2"},
	{pkg: "quadrapassel", cmd: "quadrapassel", name: "Quadrapassel", desc: "Falling blocks.", kind: "Desktop games", gui: true, icon: "gamepad-2"},
	{"btop", "btop", "btop", "A beautiful monitor of the processor, memory, disks and network.", "System", false, ""},
	{"htop", "htop", "htop", "Processes, the classic way.", "System", false, ""},
	{"ncdu", "ncdu", "ncdu", "What fills the storage, folder by folder.", "System", false, ""},
	{"fastfetch", "fastfetch", "fastfetch", "The system at a glance, with the Alpine logo.", "System", false, ""},
	{"mc", "mc", "Midnight Commander", "A two-pane file manager.", "Files", false, ""},
	{"nnn", "nnn", "nnn", "A tiny, fast file manager.", "Files", false, ""},
	{"ranger", "ranger", "ranger", "Files in columns, with previews.", "Files", false, ""},
	{"nano", "nano", "nano", "A simple text editor.", "Writing", false, ""},
	{"neovim", "nvim", "Neovim", "The editor for people who live in it.", "Writing", false, ""},
	{"micro", "micro", "micro", "An editor with mouse-free, familiar keys.", "Writing", false, ""},
	{"wordgrinder", "wordgrinder", "WordGrinder", "A word processor for writing, nothing else.", "Writing", false, ""},
	{"calcurse", "calcurse", "calcurse", "A calendar and to-do list.", "Organise", false, ""},
	{"task", "task", "Taskwarrior", "To-dos from the command line.", "Organise", false, ""},
	{"sc-im", "sc-im", "sc-im", "A spreadsheet.", "Organise", false, ""},
	{"w3m", "w3m", "w3m", "Browse the web as text.", "Internet", false, ""},
	{"lynx", "lynx", "Lynx", "The oldest web browser still going.", "Internet", false, ""},
	{"newsboat", "newsboat", "Newsboat", "Read RSS and Atom feeds.", "Internet", false, ""},
	{"weechat", "weechat", "WeeChat", "Chat on IRC.", "Internet", false, ""},
	{"aria2", "aria2c", "aria2", "Downloads, fast and resumable.", "Internet", false, ""},
	{"python3", "python3", "Python", "Programming, with its interactive prompt.", "Code", false, ""},
	{"nodejs", "node", "Node.js", "JavaScript outside the browser.", "Code", false, ""},
	{"git", "git", "Git", "Version control.", "Code", false, ""},
	{"tmux", "tmux", "tmux", "Several terminals in one, kept running.", "Code", false, ""},
	{"bc", "bc", "bc", "A calculator with any precision.", "Tools", false, ""},
	{"figlet", "figlet", "FIGlet", "Big letters made of letters.", "Fun", false, ""},
	{"cmatrix", "cmatrix", "cmatrix", "The Matrix's falling code.", "Fun", false, ""},
	{"sl", "sl", "sl", "A steam locomotive, for when you mistype ls.", "Fun", false, ""},
	{"nethack", "nethack", "NetHack", "The dungeon crawler, since 1987.", "Games", false, ""},
	{"bastet", "bastet", "Bastet", "Tetris, but it gives you the worst piece.", "Games", false, ""},
	{"moon-buggy", "moon-buggy", "Moon Buggy", "Drive across the Moon, jump the craters.", "Games", false, ""},
}

// The terminal apps' icons, by kind.
func init() {
	icons := map[string]string{"System": "cpu", "Files": "folder-open", "Writing": "file-text", "Organise": "calendar",
		"Internet": "globe", "Code": "file-code", "Tools": "calculator", "Fun": "sparkles", "Games": "gamepad-2"}
	for i := range linuxApps {
		if linuxApps[i].icon == "" {
			linuxApps[i].icon = icons[linuxApps[i].kind]
		}
	}
}

type appsState struct {
	typing  bool // the search box has the keyboard
	editing string
	busy    map[string]string // package → "Installing..." / "Removing..."
	msg     string
	query   string
	results []linuxApp
}

var (
	apkRun = func(args ...string) error { _, err := apkTry(args...); return err }
	apkOut = func(args ...string) ([]byte, error) { return apkTry(args...) }
)

// apkTry runs an apk command the ways that can work, until one does: through margin-init's
// proxy, then straight out; and when a package can't be found, again after fetching the
// package lists (they may never have been, or be old). Community is turned on first: most
// desktop apps are there. What it printed last comes back, to say what went wrong.
func apkTry(args ...string) ([]byte, error) {
	ensureCommunity()
	var out []byte
	var err error
	for _, refresh := range []bool{false, true} {
		for _, run := range []func(...string) ([]byte, error){outputInAlpine, outputInAlpineDirect} {
			if refresh {
				run("apk", "update")
			}
			if out, err = run(args...); err == nil {
				return out, nil
			}
		}
	}
	return out, err
}

// ensureCommunity adds Alpine's community repository beside main, if it isn't there.
func ensureCommunity() {
	path := alpineRoot + "/etc/apk/repositories"
	b, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(b), "/community") {
		return
	}
	var add []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); strings.HasSuffix(l, "/main") && !strings.HasPrefix(l, "#") {
			add = append(add, strings.TrimSuffix(l, "/main")+"/community")
		}
	}
	if len(add) > 0 {
		os.WriteFile(path, []byte(strings.TrimRight(string(b), "\n")+"\n"+strings.Join(add, "\n")+"\n"), 0o644)
	}
}

// installed: apk has the package (its database), or the command is there.
func (a linuxApp) installed() bool {
	if apkInstalled()[a.pkg] {
		return true
	}
	for _, d := range []string{"/usr/bin/", "/bin/", "/usr/local/bin/", "/usr/games/", "/usr/sbin/"} {
		if _, err := os.Stat(alpineRoot + d + a.cmd); err == nil {
			return true
		}
	}
	return false
}

var apkDB struct {
	mtime time.Time
	pkgs  map[string]bool
}

// apkInstalled is every installed package's name, from apk's database (read again when it
// changes).
func apkInstalled() map[string]bool {
	path := alpineRoot + "/lib/apk/db/installed"
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !st.ModTime().Equal(apkDB.mtime) || apkDB.pkgs == nil {
		b, _ := os.ReadFile(path)
		apkDB.pkgs = map[string]bool{}
		for _, l := range strings.Split(string(b), "\n") {
			if n, ok := strings.CutPrefix(l, "P:"); ok {
				apkDB.pkgs[n] = true
			}
		}
		apkDB.mtime = st.ModTime()
	}
	return apkDB.pkgs
}

// command is what to run for the app: its command if it's there, else the first program the
// package installed.
func (a linuxApp) command() string {
	for _, d := range []string{"/usr/bin/", "/bin/", "/usr/games/"} {
		if fileExists(alpineRoot + d + a.cmd) {
			return a.cmd
		}
	}
	out, _ := apkOut("apk", "info", "-L", a.pkg)
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "usr/bin/") || strings.HasPrefix(l, "usr/games/") {
			return "/" + l
		}
	}
	return a.cmd
}

// isGUI: a desktop app (in the list as one, or it installed a .desktop file of its name).
func (a linuxApp) isGUI() bool {
	if a.gui {
		return true
	}
	m, _ := filepath.Glob(alpineRoot + "/usr/share/applications/*" + a.pkg + "*.desktop")
	return len(m) > 0
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
		if app.isGUI() {
			app.cmd = app.command()
			c.startDesktop(app)
			return true
		}
		c.setMode(modeTerminal)
		c.input([]byte(app.command() + "\r"))
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
			out, err := apkOut("apk", verb, pkg)
			drawMu.Lock()
			defer drawMu.Unlock()
			delete(as.busy, pkg)
			switch {
			case err != nil:
				as.msg = fmt.Sprintf("%s: apk %s failed: %s", app.name, verb, lastLine(string(out)))
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
			c.appIcon(img, a, image.Rect(r.Min.X+20, r.Min.Y+20, r.Min.X+100, r.Min.Y+100))
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

// lastLine is a command's last line of output, what usually says why it failed.
func lastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if l := strings.TrimSpace(ls[len(ls)-1]); l != "" {
		return l
	}
	return "no output"
}
