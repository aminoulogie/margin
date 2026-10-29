package main

// Signing the Claude tab in from the phone. Claude Code (claude_code.go) prints a sign-in link
// and waits for a code; the tablet has no browser, and typing a 400-character link on a phone
// is no fun. So Margin reads the link off the Claude tab's output and the Send to Books page
// shows it: the phone opens it, the user signs in, and the code the sign-in page gives is
// pasted on Send to Books, which types it into the Claude tab. The same page takes an API
// key and connectors (MCP servers) for the Claude tab, so nothing long is typed on the tablet.

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// claudeLoginWatch follows the Claude tab's output for Claude Code's sign-in link.
type claudeLoginWatch struct {
	mu    sync.Mutex
	raw   []byte // the latest output, raw, enough to hold a sign-in screen
	url   string // the link Claude Code waits on, "" if none
	state string // "waiting", "error" (Claude Code said the code failed), "done"
	at    time.Time
}

var claudeLogin = &claudeLoginWatch{}

const claudeLoginTail = 16 << 10

// feed takes output from the Claude tab, a terminal cols wide.
func (w *claudeLoginWatch) feed(p []byte, cols int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.raw = append(w.raw, p...)
	if len(w.raw) > claudeLoginTail {
		w.raw = append([]byte(nil), w.raw[len(w.raw)-claudeLoginTail:]...)
	}
	if !bytes.Contains(w.raw, []byte("oauth/authorize")) && w.url == "" {
		return
	}
	text := termPlain(w.raw)
	if u := findLoginURL(text, cols); u != "" && u != w.url {
		w.url, w.state, w.at = u, "waiting", time.Now()
		log.Printf("claude: sign-in link shown on Send to Books")
	}
	if w.url == "" {
		return
	}
	after := text
	if i := strings.LastIndex(text, "oauth/authorize"); i >= 0 {
		after = text[i:]
	}
	switch {
	case strings.Contains(after, "Login successful"), strings.Contains(after, "Logged in as"):
		w.url, w.state = "", "done"
		w.raw = w.raw[:0]
	case strings.Contains(after, "OAuth error"):
		w.state = "error"
	}
}

// pending is the sign-in link waiting for a code (and its state), "" if none or it's old.
func (w *claudeLoginWatch) pending() (string, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.url != "" && time.Since(w.at) > 30*time.Minute {
		w.url, w.state = "", ""
	}
	return w.url, w.state
}

// termPlain is terminal output as plain text: colours and other escapes dropped, cursor-right
// moves (how Claude Code writes spaces) as spaces, carriage returns dropped.
func termPlain(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == 0x1b && i+1 < len(b) && b[i+1] == '[':
			j := i + 2
			for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
				j++
			}
			if j >= len(b) {
				return sb.String() // cut off mid-sequence
			}
			if b[j] == 'C' {
				n := 0
				for _, d := range b[i+2 : j] {
					if d >= '0' && d <= '9' {
						n = n*10 + int(d-'0')
					}
				}
				sb.WriteString(strings.Repeat(" ", min(max(n, 1), 500)))
			}
			i = j
		case c == 0x1b && i+1 < len(b) && b[i+1] == ']':
			j := i + 2
			for j < len(b) && b[j] != 0x07 && !(b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\') {
				j++
			}
			if j < len(b) && b[j] == 0x1b {
				j++
			}
			i = j
		case c == 0x1b:
			i++ // ESC and the one byte after it
		case c == '\r':
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func isURLByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		strings.IndexByte("-._~:/?#[]@!$&'()*+,;=%", c) >= 0
}

func urlPrefix(s string) string {
	i := 0
	for i < len(s) && isURLByte(s[i]) {
		i++
	}
	return s[:i]
}

// findLoginURL finds the last sign-in link in text. Claude Code prints it cut into rows as
// wide as the terminal: a row filled to the edge with the link goes on in the next one.
func findLoginURL(text string, cols int) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		j := strings.LastIndex(lines[i], "https://")
		if j < 0 {
			continue
		}
		u := urlPrefix(lines[i][j:])
		if !strings.Contains(u, "/oauth/authorize?") {
			continue
		}
		full := len(u) == len(lines[i][j:]) && utf8.RuneCountInString(lines[i]) >= cols
		for k := i + 1; full && k < len(lines); k++ {
			l := strings.TrimLeft(lines[k], " ")
			seg := urlPrefix(l)
			if seg == "" {
				break
			}
			u += seg
			full = len(seg) == len(l) && utf8.RuneCountInString(lines[k]) >= cols
		}
		if strings.Contains(u, "state=") && strings.Contains(u, "code_challenge=") {
			return u
		}
	}
	return ""
}

// --- the Claude tab's settings, seen from margin-init (outside Alpine's chroot) -------------

func claudeHomeOutside() string {
	if alpineInstalled() {
		return alpineRoot + "/root/.margin-claude"
	}
	return marginHome + "/claude"
}

func loadClaudeConfig() claudeConfig {
	var cfg claudeConfig
	b, _ := os.ReadFile(filepath.Join(claudeHomeOutside(), "config.json"))
	json.Unmarshal(b, &cfg)
	return cfg
}

func saveClaudeConfig(cfg claudeConfig) error {
	home := claudeHomeOutside()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return writeFileAtomic(filepath.Join(home, "config.json"), b, 0o600)
}

// claudeAccountSignedIn: has Claude Code kept an account sign-in?
func claudeAccountSignedIn() bool {
	root := ""
	if alpineInstalled() {
		root = alpineRoot
	}
	_, err := os.Stat(root + "/root/.claude/.credentials.json")
	return err == nil
}

// --- the Send to Books page's side ------------------------------------------------------------

// claudePost answers the page's Claude card: GET /claude says what's set up and the link
// waiting for a code; POST /claude does one thing (what=code|key|connect|disconnect).
func (c *console) claudePost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	reply := func(v map[string]any) { json.NewEncoder(w).Encode(v) }
	if r.Method == http.MethodGet {
		reply(claudeStatus())
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		w.WriteHeader(http.StatusForbidden)
		reply(map[string]any{"error": "only this page can do that"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		reply(map[string]any{"error": "bad form"})
		return
	}
	f := func(k string) string { return strings.TrimSpace(r.PostForm.Get(k)) }
	switch f("what") {
	case "code":
		code := f("code")
		if code == "" || strings.ContainsAny(code, " \r\n\t") || len(code) > 2000 {
			reply(map[string]any{"error": "paste the code the sign-in page shows (one piece, no spaces)"})
			return
		}
		if u, _ := claudeLogin.pending(); u == "" {
			reply(map[string]any{"error": "the Claude tab isn't waiting for a code: open Terminal › Claude on the tablet, and in Claude Code type /login"})
			return
		}
		s := c.terms[termClaude]
		c.inputTo(s, []byte(code))
		time.Sleep(200 * time.Millisecond)
		c.inputTo(s, []byte("\r"))
		log.Printf("claude: sign-in code typed into the Claude tab")
		c.toastSoon("Sign-in code received")
		reply(map[string]any{"ok": true})
	case "key":
		k := f("key")
		if !strings.HasPrefix(k, "sk-ant-") || strings.ContainsAny(k, " \r\n\t") {
			reply(map[string]any{"error": "an Anthropic API key starts with sk-ant-"})
			return
		}
		cfg := loadClaudeConfig()
		cfg.Key = k
		if cfg.Mode == "" {
			cfg.Mode = "api"
		}
		if err := saveClaudeConfig(cfg); err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		c.toastSoon("API key saved for Claude")
		reply(map[string]any{"ok": true})
	case "connect":
		cfg := loadClaudeConfig()
		name, err := cfg.addConnector(f("name"), f("url"), f("token"))
		if err == nil {
			err = saveClaudeConfig(cfg)
		}
		if err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		log.Printf("claude: connector %s added from %s", name, r.RemoteAddr)
		c.toastSoon("Connector " + name + " added for Claude")
		reply(map[string]any{"ok": true, "name": name})
	case "disconnect":
		cfg := loadClaudeConfig()
		if !cfg.removeConnector(f("name")) {
			reply(map[string]any{"error": "no connector called " + f("name")})
			return
		}
		if err := saveClaudeConfig(cfg); err != nil {
			reply(map[string]any{"error": err.Error()})
			return
		}
		reply(map[string]any{"ok": true})
	default:
		reply(map[string]any{"error": "what?"})
	}
}

// claudeStatus is what the page's Claude card shows.
func claudeStatus() map[string]any {
	cfg := loadClaudeConfig()
	u, st := claudeLogin.pending()
	var names []string
	for _, cn := range cfg.Connectors {
		names = append(names, cn.Name)
	}
	return map[string]any{
		"mode":       cfg.Mode,
		"signed_in":  claudeAccountSignedIn(),
		"has_key":    cfg.Key != "",
		"connectors": names,
		"login_url":  u,
		"login":      st,
	}
}

// toastSoon shows a toast from a goroutine that doesn't hold drawMu.
func (c *console) toastSoon(msg string) {
	go func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		c.toastMsg(msg)
	}()
}
