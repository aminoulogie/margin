package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClipboardParse(t *testing.T) {
	md := "# Clipboard\nintro\n\n## First\n```\necho one\necho two\n```\n\n## Empty\n\n## Second\n```send\nls -la\n```\n"
	items := parseClipboard(md)
	if len(items) != 2 || items[0].title != "First" || items[0].cmd != "echo one\necho two" || items[1].cmd != "ls -la" ||
		items[0].send || !items[1].send {
		t.Fatalf("%+v", items)
	}
	b, err := os.ReadFile("../../CLIPBOARD.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(parseClipboard(string(b))) == 0 {
		t.Fatal("CLIPBOARD.md has no commands in the format the tablet reads")
	}
}

// Clipboard on the shortcuts bar opens the panel; a tap on a command types it (lines joined
// by returns, no return at the end) and closes the panel.
func TestClipboardTypesACommand(t *testing.T) {
	c := testConsole(t)
	c.mode = modeTerminal
	c.cb.items = []clipItem{{title: "Two lines", cmd: "cd /tmp\nls"}, {title: "Other", cmd: "echo hi"}}
	c.cb.fetched = time.Now() // no fetch from GitHub in the test
	r, w, _ := os.Pipe()
	c.mu.Lock()
	c.master = w
	c.mu.Unlock()
	drawMu.Lock()
	c.redrawAll()
	bar := c.accY() + accH/2
	x := -1
	for xx := kbPad; xx < c.s.W; xx += 10 {
		if i := c.shortcutAt(xx); i >= 0 && c.shortcuts()[i].label == "Clipboard" {
			x = xx
			break
		}
	}
	c.termTouch(TouchPoint{Down: true, X: x, Y: bar})
	c.termTouch(TouchPoint{Up: true, X: x, Y: bar})
	if !c.cb.open || c.cb.rect.Empty() {
		t.Fatal("the Clipboard didn't open")
	}
	y := c.cb.rect.Min.Y + cbHeadH + cbRowH/2
	c.termTouch(TouchPoint{Down: true, X: 300, Y: y})
	c.termTouch(TouchPoint{Up: true, X: 300, Y: y})
	drawMu.Unlock()
	w.Close()
	got, _ := io.ReadAll(r)
	if string(got) != "cd /tmp\rls" || c.cb.open {
		t.Fatalf("typed %q, open %v", got, c.cb.open)
	}
}

// A command's output goes to the private results repo; a public one is refused at linking.
func TestClipboardSendsOutput(t *testing.T) {
	var putPath, putBody string
	private := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/me/results":
			fmt.Fprintf(w, `{"private":%v}`, private)
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/repos/me/results/contents/"):
			var b struct{ Content string }
			json.NewDecoder(r.Body).Decode(&b)
			raw, _ := base64.StdEncoding.DecodeString(b.Content)
			putPath, putBody = r.URL.Path, string(raw)
			w.WriteHeader(201)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("MARGIN_GITHUB_API", srv.URL)
	t.Setenv("NO_PROXY", "*")
	oldClient := webClient
	webClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { webClient = oldClient })
	ctx := context.Background()
	if err := checkResultsRepo(ctx, "tok", "me/results"); err != nil {
		t.Fatal(err)
	}
	private = false
	if err := checkResultsRepo(ctx, "tok", "me/results"); err == nil || !strings.Contains(err.Error(), "public") {
		t.Fatalf("a public results repo should be refused: %v", err)
	}
	if _, err := sendCmdResult(ctx, githubLink{}, "x", "ls", "out", 0, time.Now()); err == nil {
		t.Fatal("no token should be an error")
	}
	when := time.Date(2026, 10, 29, 13, 50, 7, 0, time.UTC)
	where, err := sendCmdResult(ctx, githubLink{Token: "tok", Results: "me/results"}, "Sound: list switches", "amixer -c 4 scontrols", "Simple mixer control 'Speaker',0\n", 0, when)
	if err != nil {
		t.Fatal(err)
	}
	if putPath != "/repos/me/results/contents/results/2026-10-29/135007-sound-list-switches.md" || where != "me/results/results/2026-10-29/135007-sound-list-switches.md" ||
		!strings.Contains(putBody, "amixer -c 4 scontrols") || !strings.Contains(putBody, "'Speaker',0") {
		t.Fatalf("put %s (%s):\n%s", putPath, where, putBody)
	}
}

func TestCbrunScriptParses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cbrun")
	os.WriteFile(p, []byte(cbrunScript), 0o755)
	if out, err := exec.Command("sh", "-n", p).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}
