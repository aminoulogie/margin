package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const fakeScan = "-48.00\t1\tHome Net\n-80.00\t1\tHome Net\n-67.00\t0\tCafé\n-85.00\t1\tNeighbour\n-90.00\t0\t\n"

func TestParseScan(t *testing.T) {
	nets := parseScan(fakeScan)
	if len(nets) != 3 || nets[0].ssid != "Home Net" || nets[0].dbm != -48 || !nets[0].lock ||
		nets[1].ssid != "Café" || nets[1].lock || nets[2].ssid != "Neighbour" {
		t.Fatalf("%+v", nets)
	}
}

// Settings > Wi-Fi lists the networks around; an open one joins at a tap; a locked one asks
// for its password, typed on the keyboard, then joins with it; a failure says why.
func TestWifiPaneJoins(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	var calls []string
	joinErr := error(nil)
	old := wifiCmd
	wifiCmd = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "scan":
			return []byte(fakeScan), nil
		case "connect":
			if joinErr != nil {
				return []byte("joining...\ncouldn't join: wrong password, or the network is out of range (wifi debug)\n"), joinErr
			}
			return []byte("online: 192.168.1.20/24  ssid: " + args[1]), nil
		}
		return nil, nil
	}
	defer func() { wifiCmd = old }()
	wait := func(what string, done func() bool) {
		t.Helper()
		for end := time.Now().Add(3 * time.Second); !done(); {
			if time.Now().After(end) {
				t.Fatalf("timed out: %s (calls %q)", what, calls)
			}
			drawMu.Unlock()
			time.Sleep(10 * time.Millisecond)
			drawMu.Lock()
		}
	}
	c.setMode(modeSettings)
	c.setPane = "wifi"
	c.wifiScan(true)
	wait("the scan", func() bool { return len(c.wifi.nets) == 3 })
	c.showPage()
	shot(t, c, "settings-wifi-list")

	tapButton(t, c, "wifi:net:1") // Café: open
	wait("joining Café", func() bool { return c.wifi.joining == "" && len(calls) >= 2 })
	if calls[1] != "connect Café" {
		t.Fatalf("calls %q", calls)
	}

	tapButton(t, c, "wifi:net:2") // Neighbour: asks for a password
	if c.wifi.join != "Neighbour" || !c.wifi.typing || !c.skb.visible {
		t.Fatalf("no password asked: %+v", c.wifi)
	}
	if text, ok := c.pageTyping(); !ok || text != "" {
		t.Fatalf("typing %q %v", text, ok)
	}
	c.wifiKey([]byte("secret12"))
	if text, _ := c.pageTyping(); text != "•••••••2" {
		t.Fatalf("the type bar shows %q, want the password hidden", text)
	}
	shot(t, c, "settings-wifi-password")
	joinErr = errors.New("exit 1")
	c.wifiKey([]byte("\r"))
	wait("joining Neighbour", func() bool { return c.wifi.joining == "" && len(calls) >= 3 })
	if calls[2] != "connect Neighbour secret12" {
		t.Fatalf("calls %q", calls)
	}
	if !strings.Contains(c.wifi.msg, "wrong password") {
		t.Fatalf("a failed join should say why: %q", c.wifi.msg)
	}
	// Too short: asked again, nothing run.
	tapButton(t, c, "wifi:net:2")
	c.wifiKey([]byte("short\r"))
	if len(calls) != 3 || !strings.Contains(c.wifi.msg, "8 characters") {
		t.Fatalf("a short password: %q, %q", calls, c.wifi.msg)
	}
	tapButton(t, c, "wifi:cancel")
	if c.wifi.join != "" || c.wifi.typing {
		t.Fatal("cancel should close the password card")
	}
}
