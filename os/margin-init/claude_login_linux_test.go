//go:build linux

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Signing in from the phone: the link Claude Code prints in the Claude tab is offered on the
// Send to Books page, and the code pasted there is typed into the tab. Connectors and the key
// pasted there land in the Claude tab's settings.
func TestClaudeSignInFromPhone(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	oldRoot, oldWatch, oldStart := alpineRoot, claudeLogin, claudeStart
	t.Cleanup(func() { alpineRoot, claudeLogin, claudeStart = oldRoot, oldWatch, oldStart })
	alpineRoot = t.TempDir() // the settings go to a pretend Alpine, never the machine's
	os.MkdirAll(alpineRoot+"/bin", 0o755)
	os.WriteFile(alpineRoot+"/bin/busybox", nil, 0o755)
	claudeLogin = &claudeLoginWatch{}

	c := testConsole(t)
	screenFile := filepath.Join(t.TempDir(), "screen")
	os.WriteFile(screenFile, []byte(claudeCodeScreen(testLoginURL, c.terms[termClaude].t.Cols)), 0o644)
	claudeStart = func(cols, rows int) (*os.File, func(), error) { // stands in for Claude Code
		m, slave, err := openPty(cols, rows)
		if err != nil {
			return nil, nil, err
		}
		defer slave.Close()
		cmd := exec.Command("/bin/sh", "-c", "stty -echo; cat "+screenFile+"; while read l; do echo got:$l; done")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		return m, func() { cmd.Wait() }, nil
	}
	drawMu.Lock()
	c.setMode(modeTerminal)
	c.tapID("tt:claude")
	drawMu.Unlock()

	get := func() map[string]any {
		rec := httptest.NewRecorder()
		c.claudePost(rec, httptest.NewRequest("GET", "/claude", nil))
		var v map[string]any
		json.Unmarshal(rec.Body.Bytes(), &v)
		return v
	}
	post := func(form url.Values, origin string) map[string]any {
		r := httptest.NewRequest("POST", "/claude", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		c.claudePost(rec, r)
		var v map[string]any
		json.Unmarshal(rec.Body.Bytes(), &v)
		if rec.Code == http.StatusForbidden {
			v["forbidden"] = true
		}
		return v
	}
	for end := time.Now().Add(5 * time.Second); get()["login_url"] != testLoginURL; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the page never offered the link: %v", get())
		}
	}
	if v := post(url.Values{"what": {"code"}, "code": {"abc#def"}}, "http://evil.example"); v["forbidden"] != true {
		t.Fatalf("another site mustn't type into the tablet: %v", v)
	}
	if v := post(url.Values{"what": {"code"}, "code": {"abc#def"}}, ""); v["ok"] != true {
		t.Fatalf("code: %v", v)
	}
	screen := func() string {
		var sb strings.Builder
		for y := 0; y < c.terms[termClaude].t.Rows; y++ {
			sb.WriteString(c.terms[termClaude].t.Text(y) + "\n")
		}
		return sb.String()
	}
	for end := time.Now().Add(5 * time.Second); !strings.Contains(screen(), "got:abc#def"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the code never reached the Claude tab:\n%s", screen())
		}
	}

	if v := post(url.Values{"what": {"connect"}, "url": {"https://mcp.example.com/mcp"}, "token": {"tok"}}, ""); v["name"] != "mcp-example-com" {
		t.Fatalf("connect: %v", v)
	}
	if v := post(url.Values{"what": {"key"}, "key": {"not-a-key"}}, ""); v["error"] == nil {
		t.Fatal("a wrong key should be refused")
	}
	if v := post(url.Values{"what": {"key"}, "key": {"sk-ant-api03-x"}}, ""); v["ok"] != true {
		t.Fatalf("key: %v", v)
	}
	cfg := loadClaudeConfig()
	if cfg.Key != "sk-ant-api03-x" || cfg.Mode != "api" || len(cfg.Connectors) != 1 || cfg.Connectors[0].Token != "tok" {
		t.Fatalf("settings %+v", cfg)
	}
	if _, err := os.Stat(alpineRoot + "/root/.margin-claude/config.json"); err != nil {
		t.Fatal("the settings belong where the Claude tab (in Alpine) reads them:", err)
	}
	if v := get(); v["has_key"] != true || len(v["connectors"].([]any)) != 1 {
		t.Fatalf("status %v", v)
	}
	if v := post(url.Values{"what": {"disconnect"}, "name": {"mcp-example-com"}}, ""); v["ok"] != true || len(loadClaudeConfig().Connectors) != 0 {
		t.Fatalf("disconnect: %v", v)
	}
}
