package main

// The Claude tab with the user's Claude account (Pro, Max, Team): signing in with an account
// is only for Anthropic's own apps, so in this mode the tab runs the real Claude Code instead
// of Margin's own client (claude.go, which needs an API key). Claude Code signs itself in:
// it prints a sign-in link and waits for a code. The tablet can't open links, so Margin shows
// that link on the Send to Books page (claude_login.go): the phone opens it, and the code
// pasted there is typed into this tab.
//
// The newest Claude Code ships only 64-bit programs. Version 2.1.112 is the last one written
// in plain JavaScript, so it runs on the tablet's 32-bit Atom with Alpine's Node.js. It's
// installed once into /opt/claude-code (not /usr/local, where "claude" is this tab), with its
// self-updater off (an update would be a 64-bit program).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const claudeCodeVersion = "2.1.112"

// claudeCodePrefix is where Claude Code is installed in Alpine (MARGIN_CLAUDE_CODE_PREFIX
// elsewhere, for tests).
func claudeCodePrefix() string {
	if p := os.Getenv("MARGIN_CLAUDE_CODE_PREFIX"); p != "" {
		return p
	}
	return "/opt/claude-code"
}

func claudeCodeCLI() string {
	return claudeCodePrefix() + "/lib/node_modules/@anthropic-ai/claude-code/cli.js"
}

// claudeConnector is an MCP server (a "custom connector") Claude may use: its address and,
// if it needs one, a token. Both modes use them: the API client sends them with each request
// (Anthropic's servers connect to it), Claude Code gets them as --mcp-config.
type claudeConnector struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Token string `json:"token,omitempty"`
}

// connectorName makes a name for a connector from its address: "mcp.example.com" →
// "mcp-example-com" (letters, digits and dashes, as the API wants), unique among have.
func connectorName(raw string, have []claudeConnector) string {
	base := "connector"
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
		base = u.Hostname()
	}
	var sb strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	name := strings.Trim(sb.String(), "-")
	if name == "" {
		name = "connector"
	}
	if len(name) > 40 {
		name = name[:40]
	}
	taken := func(n string) bool {
		for _, c := range have {
			if c.Name == n {
				return true
			}
		}
		return false
	}
	out := name
	for i := 2; taken(out); i++ {
		out = fmt.Sprintf("%s-%d", name, i)
	}
	return out
}

// checkConnectorURL says what's wrong with a connector's address, if anything.
func checkConnectorURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("that isn't a web address")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("a connector's address starts with https://")
	}
	return nil
}

// addConnector adds (or, same address, updates) a connector in cfg and returns its name.
func (cfg *claudeConfig) addConnector(name, rawURL, token string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := checkConnectorURL(rawURL); err != nil {
		return "", err
	}
	for i, c := range cfg.Connectors {
		if c.URL == rawURL {
			if strings.TrimSpace(token) != "" {
				cfg.Connectors[i].Token = strings.TrimSpace(token)
			}
			return c.Name, nil
		}
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " /\"'") {
		name = connectorName(rawURL, cfg.Connectors)
	}
	cfg.Connectors = append(cfg.Connectors, claudeConnector{Name: name, URL: rawURL, Token: strings.TrimSpace(token)})
	return name, nil
}

func (cfg *claudeConfig) removeConnector(name string) bool {
	for i, c := range cfg.Connectors {
		if c.Name == name {
			cfg.Connectors = append(cfg.Connectors[:i], cfg.Connectors[i+1:]...)
			return true
		}
	}
	return false
}

// mcpConfigJSON is Claude Code's --mcp-config for the connectors ("" without any). An address
// ending in /sse is the older SSE kind of server; the rest speak streamable HTTP.
func mcpConfigJSON(cs []claudeConnector) string {
	if len(cs) == 0 {
		return ""
	}
	servers := map[string]any{}
	for _, c := range cs {
		kind := "http"
		if u, err := url.Parse(c.URL); err == nil && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/sse") {
			kind = "sse"
		}
		s := map[string]any{"type": kind, "url": c.URL}
		if c.Token != "" {
			s["headers"] = map[string]string{"Authorization": "Bearer " + c.Token}
		}
		servers[c.Name] = s
	}
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	return string(b)
}

func claudeCodeInstalled() bool {
	_, err := os.Stat(claudeCodeCLI())
	return err == nil
}

// claudeCodeProject is the folder where Claude Code keeps the conversations held in dir.
func claudeCodeProject(home, dir string) string {
	r := strings.NewReplacer("/", "-", ".", "-")
	return filepath.Join(home, ".claude", "projects", r.Replace(dir))
}

// claudeCodeHasConversation: is there a conversation to carry on (--continue) in dir?
func claudeCodeHasConversation(home, dir string) bool {
	m, _ := filepath.Glob(filepath.Join(claudeCodeProject(home, dir), "*.jsonl"))
	return len(m) > 0
}

// seedClaudeCode picks Claude Code's 16-colour theme before its first start (the terminal
// draws those best); the theme screen then only needs return.
func seedClaudeCode(home string) {
	p := filepath.Join(home, ".claude.json")
	if _, err := os.Stat(p); err == nil {
		return
	}
	os.WriteFile(p, []byte(`{"theme":"dark-ansi"}`+"\n"), 0o600)
}

// claudeCodeNotes is added to Claude Code's own instructions: what's special about this machine.
func claudeCodeNotes() string {
	return `You are running in the Claude tab of Margin's Terminal: Margin is the user's own operating system on their
Condor TRA-901G tablet (32-bit Intel Atom, Linux kernel 3.4, Alpine Linux in a chroot; Margin's whole screen is one Go
program, margin-init). Margin's code is in ` + marginSrc + ` (unpacked by ` + "`update`" + `; read its CLAUDE.md). The tablet program is
os/margin-init (Go, package main).

To fix Margin: change the code, check it builds with
  cd ` + marginSrc + `/os/margin-init && GOTMPDIR=/root/.cache/gotmp GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -o /tmp/mi-check .
(a build takes a few minutes here; give Bash a long timeout), then install it with ` + "`update --local`" + `: that builds, installs
and restarts Margin, which closes this tab for a few seconds; it reopens by itself and this conversation carries on.
If the new build doesn't start, the old one comes back by itself. Plain ` + "`update`" + ` downloads GitHub's code and refuses
while there are unpublished fixes. To publish fixes to GitHub (only when the user agrees): margin-publish "message"
(one commit with every file changed since the download). git doesn't work on this kernel: don't use it.

Never write to block devices, never change /system, never flash or wipe anything, never delete the user's books or data.
The user reads on a 73-column screen: answer in short, plain words. Don't put model names in commit messages.`
}

// runAccount runs Claude Code in the tab until the user chooses the API-key Claude instead
// (true) or leaves (false). The terminal is raw on entry and on return.
func (a *agent) runAccount() bool {
	prompt := ""
	if os.Getenv("MARGIN_CLAUDE_REOPENED") != "" {
		prompt = "Margin restarted after `update --local`: the new build is running now. Carry on: check that the fix works."
	}
	for {
		if !claudeCodeInstalled() && !a.installClaudeCode() {
			a.out.write("\r\n" + a.para("Claude Code couldn't be installed (see above). Check Wi-Fi and the clock (`date`), then try again.", cRed) +
				"\r\n  " + cBold + "return" + cReset + " try again   " + cBold + "k" + cReset + " use an API key instead\r\n")
			if k := a.keySync(); k == 'k' || k == 'K' {
				return a.switchToAPI()
			} else if k == 3 || k == 4 {
				return false
			}
			continue
		}
		started := time.Now()
		err := a.startClaudeCode(prompt)
		prompt = ""
		quick := time.Since(started) < 8*time.Second
		msg := "Claude Code closed."
		if err != nil && quick {
			msg = "Claude Code stopped at once: " + err.Error() + ". If it keeps doing that, it may not run on this tablet: use an API key instead (k)."
		}
		a.out.write("\r\n" + a.para(msg, cDim) +
			"\r\n  " + cBold + "return" + cReset + " start Claude Code again   " + cBold + "k" + cReset + " use an API key instead\r\n")
		switch k := a.keySync(); k {
		case 'k', 'K':
			return a.switchToAPI()
		case 3, 4:
			return false
		}
	}
}

func (a *agent) switchToAPI() bool {
	a.loadConfig()
	a.cfg.Mode = "api"
	a.saveConfig()
	a.out.write("\x1b[2J\x1b[3J\x1b[H")
	return true
}

// keySync waits for one key (the terminal is raw); 4 (Ctrl-D) when the terminal is gone.
func (a *agent) keySync() byte {
	var b [64]byte
	n, err := os.Stdin.Read(b[:])
	if err != nil || n == 0 {
		return 4
	}
	return b[0]
}

// installClaudeCode installs Node.js and Claude Code, showing what happens.
func (a *agent) installClaudeCode() bool {
	a.out.write("\r\n" + a.para("Installing Claude Code "+claudeCodeVersion+" (the last one that runs on this 32-bit tablet). "+
		"The first time takes a few minutes.", "") + "\r\n")
	steps := [][]string{
		{"apk", "add", "nodejs", "npm", "bash", "ripgrep"},
		{"npm", "install", "-g", "--prefix", claudeCodePrefix(), "--no-audit", "--no-fund", "--loglevel", "error",
			"@anthropic-ai/claude-code@" + claudeCodeVersion},
	}
	for _, s := range steps {
		a.out.write(cDim + "  $ " + strings.Join(s, " ") + cReset + "\r\n")
		cmd := exec.Command(s[0], s[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stdout
		cmd.Env = append(os.Environ(), "npm_config_update_notifier=false")
		if err := cmd.Run(); err != nil {
			a.out.write(cRed + "  ● " + err.Error() + cReset + "\r\n")
			return false
		}
	}
	if !claudeCodeInstalled() {
		a.out.write(cRed + "  ● " + claudeCodeCLI() + " is missing after the install" + cReset + "\r\n")
		return false
	}
	a.out.write("  " + cGreenB + "✓" + cReset + " Claude Code installed\r\n\r\n")
	return true
}

// startClaudeCode runs Claude Code on this terminal until it exits. prompt, if any, is sent
// as the first message.
func (a *agent) startClaudeCode(prompt string) error {
	a.loadConfig()
	home := os.Getenv("HOME")
	if home == "" {
		home = "/root"
	}
	seedClaudeCode(home)
	cwd := a.sess.Cwd
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		cwd = defaultCwd()
	}
	args := []string{"--max-old-space-size=768", claudeCodeCLI(), "--append-system-prompt", claudeCodeNotes()}
	if j := mcpConfigJSON(a.cfg.Connectors); j != "" {
		p := filepath.Join(a.home, "mcp.json")
		if writeFileAtomic(p, []byte(j), 0o600) == nil {
			args = append(args, "--mcp-config", p)
		}
	}
	if claudeCodeHasConversation(home, cwd) {
		args = append(args, "--continue")
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	a.out.write("\x1b[2J\x1b[3J\x1b[H" + cDim + "Starting Claude Code…" + cReset + "\r\n")
	cmd := exec.Command("node", args...)
	cmd.Dir = cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(),
		"DISABLE_AUTOUPDATER=1",                // an update would be a 64-bit program
		"USE_BUILTIN_RIPGREP=0",                // its own ripgrep is 64-bit only: Alpine's
		"SHELL=/bin/bash",                      // the Bash tool wants bash
		"MARGIN_FROM_CLAUDE=1",                 // update --local reopens this tab afterwards
		"CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1", // the tab has no title to set
		"PATH="+claudeCodePrefix()+"/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	a.rawOff()
	defer a.rawOn()
	return cmd.Run()
}

// runPublish is "margin-init publish <message>" (margin-publish in Alpine): every file changed
// in Margin's code since `update` downloaded it goes to GitHub in one commit, with the token
// set in the Claude tab (/github) or linked from the phone.
func runPublish(args []string) int {
	msg := strings.TrimSpace(strings.Join(args, " "))
	if msg == "" {
		fmt.Println("usage: margin-publish \"what the fix does\"")
		return 2
	}
	a := &agent{home: claudeHome()}
	a.loadConfig()
	token := a.cfg.GitHubToken
	if token == "" {
		token = loadGitHubLink().Token
	}
	if token == "" {
		fmt.Println("No GitHub token: link GitHub on the Send to Books page first.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := publishFixes(ctx, token, a.repo(), a.branch(), marginSrc, msg)
	if err != nil {
		fmt.Println("publishing failed:", err)
		return 1
	}
	fmt.Println(res)
	return 0
}
