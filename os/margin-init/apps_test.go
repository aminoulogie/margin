package main

import (
	"errors"
	"testing"
)

// The Linux Apps store: Get installs with apk, search reads apk's list, Open runs it.
func TestLinuxApps(t *testing.T) {
	c := testConsole(t)
	var ran [][]string
	oldRun, oldOut := apkRun, apkOut
	apkRun = func(args ...string) error { ran = append(ran, args); return nil }
	apkOut = func(args ...string) ([]byte, error) {
		if args[1] == "add" || args[1] == "del" {
			ran = append(ran, args)
			return nil, nil
		}
		if args[3] == "nothing" {
			return nil, errors.New("exit 1")
		}
		return []byte("vim-9.1.0-r0 - Improved vi-style text editor\nvim-doc-9.1.0-r0 - docs\nneovim-0.10.0-r1 - Vim-fork\n"), nil
	}
	t.Cleanup(func() { apkRun, apkOut = oldRun, oldOut })
	drawMu.Lock()
	c.setPane = "apps"
	c.setMode(modeSettings)
	shot(t, c, "settings-apps")
	c.appsTap("app:get:btop")
	drawMu.Unlock()
	waitFor(t, "the install", func() bool { return c.apps.busy["btop"] == "" && len(ran) == 1 })
	if ran[0][0] != "apk" || ran[0][1] != "add" || ran[0][2] != "btop" {
		t.Fatalf("ran %v", ran)
	}
	drawMu.Lock()
	c.appsSearchTap("apps:search")
	c.appsKey([]byte("vim\r"))
	drawMu.Unlock()
	waitFor(t, "the search", func() bool { return len(c.apps.results) > 0 })
	if r := c.apps.results; len(r) != 2 || r[0].pkg != "vim" || r[1].pkg != "neovim" {
		t.Fatalf("results %+v", r)
	}
}
