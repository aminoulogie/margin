package main

// claude: Claude in the Terminal's Claude tab, in the style of Claude Code. Tell it what's
// wrong with Margin ("the battery shows 0%") and it reads the code in /root/margin-src, fixes
// it, builds and installs the new Margin (update --local), and can publish the fix to GitHub.
//
// It's margin-init itself, run as "margin-init claude": the console starts it in Alpine's
// chroot on its own pty (termtabs.go), and Alpine's shell has /usr/local/bin/claude for the
// same thing over ssh. An Anthropic API key is asked for the first time and kept (0600) in
// claudeHome(), with the conversation, so it carries on after Margin restarts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The look: Apple's dark colours through the terminal's ANSI palette (console.go). Claude's
// coral is the bright red.
const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cDim    = "\x1b[90m"
	cAccent = "\x1b[91m"
	cRed    = "\x1b[31m"
	cGreen  = "\x1b[32m"
	cGreenB = "\x1b[92m"
	cCode   = "\x1b[96m"
	cWhite  = "\x1b[97m"
)

var claudeModels = map[string]string{
	"opus":   "claude-opus-5-5",
	"sonnet": "claude-sonnet-5-5",
	"haiku":  "claude-haiku-5-5",
}

const claudeDefaultModel = "claude-sonnet-5-5"

// marginSrc is where `update` unpacks Margin's code on the tablet.
const marginSrc = "/root/margin-src"

type claudeConfig struct {
	Mode        string            `json:"mode,omitempty"` // "account" (Claude Code, claude_code.go), "api", "" = not chosen
	Key         string            `json:"key,omitempty"`
	Model       string            `json:"model,omitempty"`
	GitHubToken string            `json:"github_token,omitempty"`
	Repo        string            `json:"repo,omitempty"`   // owner/name, default aminoulogie/margin
	Branch      string            `json:"branch,omitempty"` // default main
	Connectors  []claudeConnector `json:"connectors,omitempty"`
}

type claudeSession struct {
	Cwd  string `json:"cwd"`
	Msgs []cMsg `json:"messages"`
}

type agent struct {
	home    string
	cfg     claudeConfig
	sess    claudeSession
	net     *claudeNet
	out     *claudeOut
	keys    chan []byte // what's typed, a read at a time (a paste arrives as one)
	ahead   []byte      // typed while Claude was working, for the next prompt
	aheadMu sync.Mutex
	hist    []string
	exitArm bool // Ctrl-C once on an empty prompt: once more leaves
	lastIn  int  // tokens the last request used, for /status

	rawOn, rawOff func() // the terminal raw (keys one at a time) and back (Claude Code sets its own)
}

// runClaude is "margin-init claude".
func runClaude() int {
	a := &agent{home: claudeHome(), net: newClaudeNet(), keys: make(chan []byte, 64)}
	a.out = &claudeOut{w: os.Stdout}
	restore, err := ttyRaw(os.Stdin.Fd())
	if err != nil {
		fmt.Println("claude needs a terminal:", err)
		return 1
	}
	defer func() { a.rawOff() }()
	a.rawOff = restore
	a.rawOn = func() {
		if r, err := ttyRaw(os.Stdin.Fd()); err == nil {
			a.rawOff = r
		}
	}
	os.MkdirAll(a.home, 0o700)
	a.loadConfig()
	a.loadSession()
	if a.cfg.Mode == "" && a.cfg.Key == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		a.banner()
		if !a.chooseMode() {
			return 0
		}
	}
	if a.cfg.Mode == "account" && !a.runAccount() {
		return 0
	}
	// From here on keys are read in the background (Esc stops Claude mid-answer). Nothing
	// else may read the terminal after this: Claude Code starts in a new run of this tab.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				a.keys <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(a.keys)
				return
			}
		}
	}()
	a.banner()
	if a.cfg.Key == "" {
		if k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); k != "" {
			a.cfg.Key = k
		} else if !a.askKey() {
			return 0
		}
	}
	if a.resume() {
		a.runTurn()
	}
	for {
		line, ok := a.readLine("", false)
		if !ok {
			a.out.write("\r\n")
			return 0
		}
		a.loadConfig()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		a.hist = append(a.hist, line)
		if strings.HasPrefix(line, "/") {
			if a.command(line) {
				return 0
			}
			continue
		}
		a.addUser(cBlock{Type: "text", Text: line})
		a.runTurn()
	}
}

// --- settings and the conversation, on disk ------------------------------------------------

// loadConfig reads the settings (again: the phone can add a key or a connector meanwhile,
// claude_login.go).
func (a *agent) loadConfig() {
	b, _ := os.ReadFile(filepath.Join(a.home, "config.json"))
	a.cfg = claudeConfig{}
	json.Unmarshal(b, &a.cfg)
	if a.cfg.Model == "" {
		a.cfg.Model = claudeDefaultModel
	}
	if a.cfg.Key == "" {
		a.cfg.Key = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	}
}

func (a *agent) saveConfig() {
	b, _ := json.MarshalIndent(a.cfg, "", "  ")
	writeFileAtomic(filepath.Join(a.home, "config.json"), b, 0o600)
}

// chooseMode asks, the first time, how Claude signs in. false: the terminal closed.
func (a *agent) chooseMode() bool {
	a.out.write(cBold + "  How should Claude sign in?" + cReset + "\r\n\r\n" +
		"  " + cAccent + "1" + cReset + "  Your Claude account (Pro, Max, Team)\r\n" +
		a.para("     Runs Claude Code here. Sign in from your phone: the Send to Books page shows the link.", cDim) +
		"  " + cAccent + "2" + cReset + "  An Anthropic API key\r\n" +
		a.para("     Margin's own Claude, billed per use from console.anthropic.com.", cDim) + "\r\n")
	for {
		switch a.keySync() {
		case '1':
			a.cfg.Mode = "account"
		case '2':
			a.cfg.Mode = "api"
		case 3, 4:
			return false
		default:
			continue
		}
		a.saveConfig()
		return true
	}
}

func (a *agent) loadSession() {
	b, _ := os.ReadFile(filepath.Join(a.home, "session.json"))
	json.Unmarshal(b, &a.sess)
	if st, err := os.Stat(a.sess.Cwd); a.sess.Cwd == "" || err != nil || !st.IsDir() {
		a.sess.Cwd = defaultCwd()
	}
}

func (a *agent) saveSession() {
	b, _ := json.Marshal(a.sess)
	writeFileAtomic(filepath.Join(a.home, "session.json"), b, 0o600)
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func defaultCwd() string {
	for _, d := range []string{marginSrc, os.Getenv("HOME"), "/root"} {
		if st, err := os.Stat(d); d != "" && err == nil && st.IsDir() {
			return d
		}
	}
	return "/"
}

// addUser adds a user turn (joined to the last one if that was the user's too: the API wants
// turns to alternate).
func (a *agent) addUser(blocks ...cBlock) {
	if n := len(a.sess.Msgs); n > 0 && a.sess.Msgs[n-1].Role == "user" {
		a.sess.Msgs[n-1].Content = append(a.sess.Msgs[n-1].Content, blocks...)
	} else {
		a.sess.Msgs = append(a.sess.Msgs, cMsg{Role: "user", Content: blocks})
	}
	a.saveSession()
}

// resume shows where the last conversation was. A tool still running when it was saved
// means Margin restarted under it (update --local): that's answered, and resume says to carry
// on, so Claude can check the new build.
func (a *agent) resume() bool {
	msgs := a.sess.Msgs
	if len(msgs) == 0 {
		return false
	}
	a.out.write(wrapSegs([]mdSeg{{fmt.Sprintf("Carrying on from the last conversation (%d messages) · /clear starts a new one", len(msgs)), cDim}},
		a.cols()-1, "  ", "  ") + "\r\n")
	// The last thing typed and the answer to it, so it's clear where things are.
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && userText(msgs[i]) != "" {
			a.out.write(cDim + "❯ " + oneLine(userText(msgs[i]), a.cols()-4) + cReset + "\r\n\r\n")
			for _, m := range msgs[i+1:] {
				if m.Role == "assistant" {
					for _, b := range m.Content {
						if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
							a.out.write(renderMarkdown(b.Text, a.cols(), "● ") + "\r\n")
						}
					}
				}
			}
			break
		}
	}
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" {
		return false
	}
	var results []cBlock
	for _, b := range last.Content {
		if b.Type == "tool_use" {
			results = append(results, cBlock{Type: "tool_result", ToolUseID: b.ID,
				Content: "Margin restarted while this was running (update --local installs the new build and restarts Margin, " +
					"which restarts this tab too). This conversation has just resumed on the restarted Margin."})
		}
	}
	if len(results) == 0 {
		return false
	}
	a.out.write(cGreenB + "● " + cReset + "Margin restarted · checking the new build" + "\r\n\r\n")
	a.addUser(results...)
	return true
}

func userText(m cMsg) string {
	var parts []string
	for _, b := range m.Content {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// --- one turn: ask, run the tools it calls, ask again, until it's done ----------------------

func (a *agent) runTurn() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var watch sync.WaitGroup
	watch.Add(1)
	go func() { // Esc (or Ctrl-C) stops Claude; anything else typed waits for the next prompt
		defer watch.Done()
		for {
			select {
			case <-done:
				return
			case k, ok := <-a.keys:
				if !ok {
					cancel()
					return
				}
				if string(k) == "\x1b" || bytes.IndexByte(k, 3) >= 0 {
					cancel()
					continue
				}
				a.aheadMu.Lock()
				a.ahead = append(a.ahead, k...)
				a.aheadMu.Unlock()
			}
		}
	}()
	a.turn(ctx)
	close(done)
	watch.Wait()
	a.out.stopSpin()
	if ctx.Err() != nil {
		a.out.write("  " + cDim + "└ " + cReset + cRed + "Interrupted" + cReset + cDim + " · what should Claude do instead?" + cReset + "\r\n\r\n")
	}
}

func (a *agent) turn(ctx context.Context) {
	for step := 0; step < 300; step++ {
		rep, err := a.ask(ctx)
		if rep != nil {
			var keep []cBlock
			for _, b := range rep.Blocks {
				switch {
				case b.Type == "text" && strings.TrimSpace(b.Text) != "":
					keep = append(keep, cBlock{Type: "text", Text: b.Text})
				case (b.Type == "tool_use" || b.Type == "mcp_tool_use" || b.Type == "mcp_tool_result") && err == nil:
					keep = append(keep, b) // a connector's call and result stay in the conversation
				}
			}
			if len(keep) > 0 {
				a.sess.Msgs = append(a.sess.Msgs, cMsg{Role: "assistant", Content: keep})
				a.saveSession()
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				a.out.write("  " + cRed + "● " + err.Error() + cReset + "\r\n\r\n")
				if strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "certificate") {
					a.out.write(cDim + "    (the tablet's clock may be wrong: check `date`)" + cReset + "\r\n")
				}
			}
			return
		}
		var calls []cBlock
		for _, b := range rep.Blocks {
			if b.Type == "tool_use" {
				calls = append(calls, b)
			}
		}
		if len(calls) == 0 {
			if rep.StopReason == "pause_turn" { // a long connector call: ask again to let it go on
				continue
			}
			if rep.StopReason == "max_tokens" {
				a.out.write(cDim + "  (the answer hit its length limit: say \"go on\" to continue)" + cReset + "\r\n")
			}
			return
		}
		var results []cBlock
		for _, call := range calls {
			if ctx.Err() != nil {
				results = append(results, cBlock{Type: "tool_result", ToolUseID: call.ID, Content: "Not run: the user interrupted.", IsError: true})
				continue
			}
			res, isErr := a.runTool(ctx, call)
			if res == "" {
				res = "(no output)"
			}
			results = append(results, cBlock{Type: "tool_result", ToolUseID: call.ID, Content: res, IsError: isErr})
		}
		a.addUser(results...)
		if ctx.Err() != nil {
			return
		}
	}
}

// ask sends the conversation and streams the answer onto the screen, retrying a busy or
// unreachable API a few times (as long as nothing has been shown yet).
func (a *agent) ask(ctx context.Context) (*cReply, error) {
	req := cRequest{Model: a.cfg.Model, MaxTokens: 16000, Tools: append([]cTool(nil), claudeTools...),
		System: []cBlock{{Type: "text", Text: a.systemPrompt(), CacheControl: &cacheCtl{"ephemeral"}}}}
	req.withConnectors(a.cfg.Connectors)
	req.Messages = make([]cMsg, len(a.sess.Msgs))
	copy(req.Messages, a.sess.Msgs)
	if n := len(req.Messages); n > 0 { // cache the conversation so far: the next request starts from it
		last := req.Messages[n-1]
		content := append([]cBlock(nil), last.Content...)
		content[len(content)-1].CacheControl = &cacheCtl{"ephemeral"}
		req.Messages[n-1] = cMsg{Role: last.Role, Content: content}
	}
	md := &mdStream{a: a}
	for try := 0; ; try++ {
		a.out.startSpin("Thinking")
		rep, err := a.net.stream(ctx, a.cfg.Key, req, md.text, func(name string) {
			md.flush()
			a.out.startSpin(toolVerb(name))
		}, func(b cBlock) {
			md.flush()
			a.out.stopSpin()
			a.showConnectorBlock(b)
			md.shown = true
			if b.Type == "mcp_tool_use" {
				a.out.startSpin("Waiting for " + b.ServerName)
			} else {
				a.out.startSpin("Thinking")
			}
		})
		md.flush()
		a.out.stopSpin()
		if rep != nil {
			a.lastIn = rep.InTokens
		}
		if err == nil || ctx.Err() != nil || md.shown || try >= 4 {
			return rep, err
		}
		var ae *apiError
		if errors.As(err, &ae) && !ae.retryable() {
			return rep, err
		}
		wait := []time.Duration{2, 5, 10, 20}[try] * time.Second
		a.out.write(cDim + "  " + oneLine(err.Error(), a.cols()-20) + " · retrying in " + wait.String() + cReset + "\r\n")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// showConnectorBlock shows a call Anthropic's servers made to a connector, Claude Code's way:
// "● server · tool(args)", then its result under it.
func (a *agent) showConnectorBlock(b cBlock) {
	cols := a.cols()
	switch b.Type {
	case "mcp_tool_use":
		label := b.ServerName + " · " + b.Name
		args := oneLine(string(b.Input), cols-visibleLen(label)-6)
		if args == "{}" {
			args = ""
		}
		a.out.write(cGreenB + "● " + cReset + cBold + label + cReset + "(" + args + ")\r\n")
	case "mcp_tool_result":
		text, isErr := mcpResult(b.Raw)
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		style := cDim
		if isErr {
			style = cRed
		}
		var sb strings.Builder
		for i, l := range lines {
			if i == 3 {
				sb.WriteString("     " + cDim + fmt.Sprintf("… %d more lines", len(lines)-3) + cReset + "\r\n")
				break
			}
			p := "     "
			if i == 0 {
				p = "  " + cDim + "└" + cReset + "  "
			}
			sb.WriteString(p + style + oneLineKeep(expandTabs(l), cols-7) + cReset + "\r\n")
		}
		if strings.TrimSpace(text) == "" {
			sb.Reset()
			sb.WriteString("  " + cDim + "└  (No output)" + cReset + "\r\n")
		}
		a.out.write(sb.String() + "\r\n")
	}
}

func toolVerb(name string) string {
	switch name {
	case "bash":
		return "Running"
	case "read_file":
		return "Reading"
	case "write_file", "edit_file":
		return "Editing"
	case "publish_to_github":
		return "Publishing"
	}
	return "Working"
}

// mdStream shows streamed text a whole line at a time, so Markdown can be drawn and the
// spinner keeps its own line under it.
type mdStream struct {
	a       *agent
	pending string
	started bool // the "●" is out for this block
	inCode  bool
	shown   bool
}

func (m *mdStream) text(s string) {
	m.pending += s
	for {
		i := strings.IndexByte(m.pending, '\n')
		if i < 0 {
			return
		}
		m.line(m.pending[:i])
		m.pending = m.pending[i+1:]
	}
}

func (m *mdStream) flush() {
	if m.pending != "" {
		m.line(m.pending)
		m.pending = ""
	}
	if m.started {
		m.a.out.write("\r\n") // a blank line before what comes next, as Claude Code spaces things
	}
	m.started, m.inCode = false, false
}

func (m *mdStream) line(l string) {
	if !m.started && strings.TrimSpace(l) == "" {
		return // no blank line under the dot
	}
	prefix := "  "
	if !m.started {
		prefix = cWhite + "● " + cReset
		m.started = true
	}
	var out string
	out, m.inCode = mdLine(l, m.inCode, m.a.cols(), prefix)
	if out != "" {
		m.a.out.write(out)
	}
	m.shown = true
}

// --- the screen: output with a spinner line under it ----------------------------------------

type claudeOut struct {
	mu        sync.Mutex
	w         io.Writer
	spinOn    bool
	spinShown bool
	label     string
	since     time.Time
	frame     int
	stop      chan struct{}
}

func (o *claudeOut) write(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.spinShown {
		io.WriteString(o.w, "\r\x1b[K")
		o.spinShown = false
	}
	io.WriteString(o.w, s)
}

var spinFrames = []string{"·", "✢", "✳", "✶", "✻", "✶", "✳", "✢"}

func (o *claudeOut) startSpin(label string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.label = label
	if o.spinOn {
		return
	}
	o.spinOn, o.since = true, time.Now()
	o.stop = make(chan struct{})
	go func(stop chan struct{}) {
		t := time.NewTicker(160 * time.Millisecond)
		defer t.Stop()
		for {
			o.mu.Lock()
			if o.spinOn {
				o.frame++
				secs := int(time.Since(o.since).Seconds())
				io.WriteString(o.w, "\r\x1b[K"+cAccent+spinFrames[o.frame%len(spinFrames)]+" "+o.label+"…"+cReset+
					cDim+fmt.Sprintf(" (%ds · esc to interrupt)", secs)+cReset)
				o.spinShown = true
			}
			o.mu.Unlock()
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}(o.stop)
}

func (o *claudeOut) stopSpin() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.spinOn {
		return
	}
	o.spinOn = false
	close(o.stop)
	if o.spinShown {
		io.WriteString(o.w, "\r\x1b[K")
		o.spinShown = false
	}
}

func (a *agent) cols() int { return max(ttyCols(os.Stdout.Fd()), 30) }

func (a *agent) banner() {
	w := min(a.cols()-1, 62)
	line := func(s string) string {
		return cAccent + "│" + cReset + " " + s + strings.Repeat(" ", max(w-3-visibleLen(s), 0)) + cAccent + "│" + cReset + "\r\n"
	}
	a.out.write("\r\n" + cAccent + "╭" + strings.Repeat("─", w-2) + "╮" + cReset + "\r\n" +
		line(cAccent+"✻"+cReset+" Welcome to "+cBold+"Claude"+cReset+" on Margin!") +
		line("") +
		line(cDim+"  /help for help · esc stops Claude"+cReset) +
		line("") +
		line(cDim+"  "+oneLine("cwd: "+a.sess.Cwd, w-6)+cReset) +
		cAccent + "╰" + strings.Repeat("─", w-2) + "╯" + cReset + "\r\n\r\n" +
		wrapSegs([]mdSeg{{"Tell it what's wrong with Margin, like \"the Wi-Fi icon stays grey\": it reads the code, " +
			"fixes it, then rebuilds and restarts Margin with the fix.", cDim}}, a.cols()-1, " ", " ") + "\r\n")
}

// para is text wrapped to the terminal, indented two cells.
func (a *agent) para(text, style string) string {
	return wrapSegs([]mdSeg{{text, style}}, a.cols()-1, "  ", "  ")
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n < 2 {
		n = 2
	}
	if utf8.RuneCountInString(s) > n {
		r := []rune(s)
		return string(r[:n-1]) + "…"
	}
	return s
}

// --- slash commands -------------------------------------------------------------------------

// command handles a /command; true means leave.
func (a *agent) command(line string) bool {
	f := strings.Fields(line)
	arg := strings.TrimSpace(strings.TrimPrefix(line, f[0]))
	say := func(s string) { a.out.write("  " + cDim + "└ " + cReset + s + "\r\n\r\n") }
	switch f[0] {
	case "/help", "/?":
		a.out.write(cBold + "  Claude on Margin" + cReset + "\r\n" +
			a.para("Type what you want done and press return. Claude reads and edits the code in "+marginSrc+
				" (get it with `update` in the Shell tab), runs commands, and installs the result with `update --local`: "+
				"Margin restarts, then Claude carries on.", cDim) + "\r\n" +
			"  /clear          start a new conversation\r\n" +
			"  /model [name]   opus, sonnet or haiku (now " + a.cfg.Model + ")\r\n" +
			"  /key            change the Anthropic API key\r\n" +
			"  /login          use your Claude account instead (Claude Code)\r\n" +
			"  /connect <url> [token]  add a connector (MCP server)\r\n" +
			"  /connectors     list them · /disconnect <name>\r\n" +
			"  /github         set a GitHub token, so fixes can be published\r\n" +
			"  /cd <folder>    work somewhere else\r\n" +
			"  /status         what's set up\r\n" +
			"  /exit           leave (the Claude tab starts again)\r\n\r\n" +
			a.para("esc stops Claude · a line ending in \\ goes on to the next · ↑ ↓ earlier prompts · "+
				"Ctrl-C twice leaves · Ctrl-L clears the screen", cDim) + "\r\n")
	case "/clear", "/new", "/reset":
		a.sess.Msgs = nil
		a.saveSession()
		a.out.write("\x1b[2J\x1b[3J\x1b[H")
		a.banner()
	case "/model":
		if arg == "" {
			say("model: " + a.cfg.Model + cDim + " (/model opus, sonnet or haiku)" + cReset)
			break
		}
		m := strings.ToLower(arg)
		if full, ok := claudeModels[m]; ok {
			m = full
		}
		a.cfg.Model = m
		a.saveConfig()
		say("model set to " + cBold + m + cReset)
	case "/key":
		a.askKey()
	case "/login", "/account":
		a.cfg.Mode = "account"
		a.saveConfig()
		say("switching to your Claude account: Claude Code starts in a moment")
		return true // this tab starts again, in account mode
	case "/connect":
		f := strings.Fields(arg)
		if len(f) == 0 {
			say("/connect <https address> [token] · or paste it on the Send to Books page")
			break
		}
		tok := ""
		if len(f) > 1 {
			tok = f[1]
		}
		name, err := a.cfg.addConnector("", f[0], tok)
		if err != nil {
			say(cRed + err.Error() + cReset)
			break
		}
		a.saveConfig()
		say("connector " + cBold + name + cReset + " added: Claude can use its tools from the next message")
	case "/connectors", "/mcp":
		if len(a.cfg.Connectors) == 0 {
			say("no connectors · /connect <url> [token], or paste one on the Send to Books page")
			break
		}
		for _, c := range a.cfg.Connectors {
			tok := ""
			if c.Token != "" {
				tok = cDim + " · token set" + cReset
			}
			a.out.write("  " + cBold + c.Name + cReset + "  " + cDim + oneLine(c.URL, a.cols()-len(c.Name)-20) + cReset + tok + "\r\n")
		}
		a.out.write("\r\n")
	case "/disconnect":
		if a.cfg.removeConnector(arg) {
			a.saveConfig()
			say("connector " + arg + " removed")
		} else {
			say(cRed + "no connector named " + arg + cReset + cDim + " (/connectors lists them)" + cReset)
		}
	case "/github":
		a.askGitHub()
	case "/cd":
		d := a.abs(arg)
		if arg == "" {
			d = defaultCwd()
		}
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			say(cRed + "no folder " + d + cReset)
			break
		}
		a.sess.Cwd = d
		a.saveSession()
		say("working in " + d)
	case "/status":
		gh := "not set (/github)"
		if a.cfg.GitHubToken != "" {
			gh = "set · " + a.repo() + "@" + a.branch()
		}
		key := "not set (/key)"
		if a.cfg.Key != "" {
			key = "…" + a.cfg.Key[max(len(a.cfg.Key)-4, 0):]
		}
		conns := "none (/connect)"
		if n := len(a.cfg.Connectors); n > 0 {
			var names []string
			for _, c := range a.cfg.Connectors {
				names = append(names, c.Name)
			}
			conns = strings.Join(names, ", ")
		}
		a.out.write(fmt.Sprintf("  model    %s\r\n  folder   %s\r\n  API key  %s\r\n  GitHub   %s\r\n  connect  %s\r\n  context  %d messages, %d tokens last time\r\n\r\n",
			a.cfg.Model, a.sess.Cwd, key, gh, oneLine(conns, a.cols()-12), len(a.sess.Msgs), a.lastIn))
	case "/exit", "/quit":
		return true
	default:
		say(cRed + "unknown command " + f[0] + cReset + cDim + " (/help lists them)" + cReset)
	}
	return false
}

func (a *agent) askKey() bool {
	a.out.write(a.para("Paste your Anthropic API key (console.anthropic.com > API Keys), or paste it on the Send to Books page "+
		"from your phone and press return here. It stays on this tablet. (/login uses your Claude account instead.)", ""))
	k, ok := a.readLine("key", true)
	k = strings.TrimSpace(k)
	if k == "/login" || k == "/account" {
		a.cfg.Mode = "account"
		a.saveConfig()
		return false // this tab starts again, in account mode
	}
	if !ok || k == "" {
		if ok {
			a.loadConfig() // pasted on the phone meanwhile?
		}
		if a.cfg.Key != "" {
			a.out.write("  " + cGreenB + "✓" + cReset + " key set\r\n\r\n")
			return true
		}
		a.out.write(cDim + "  no key set" + cReset + "\r\n\r\n")
		return false
	}
	a.cfg.Key = k
	a.saveConfig()
	a.out.write("  " + cGreenB + "✓" + cReset + " key saved\r\n\r\n")
	return true
}

func (a *agent) askGitHub() {
	a.out.write(a.para("Paste a GitHub token with access to "+a.repo()+
		" (github.com > Settings > Developer settings > Fine-grained tokens, Contents: read and write).", ""))
	k, ok := a.readLine("token", true)
	if k = strings.TrimSpace(k); ok && k != "" {
		a.cfg.GitHubToken = k
		a.saveConfig()
		a.out.write("  " + cGreenB + "✓" + cReset + " token saved\r\n\r\n")
	}
}

func (a *agent) repo() string {
	if a.cfg.Repo != "" {
		return a.cfg.Repo
	}
	return "aminoulogie/margin"
}

func (a *agent) branch() string {
	if a.cfg.Branch != "" {
		return a.cfg.Branch
	}
	return "main"
}

// --- the prompt: a line editor between two rules, like Claude Code's input box ------------

// readLine edits a prompt until return. hidden shows dots (keys, tokens) and keeps it out
// of the history. ok=false: leave (Ctrl-C twice, Ctrl-D, the terminal closed).
func (a *agent) readLine(label string, hidden bool) (string, bool) {
	var buf []rune
	pos, hi := 0, len(a.hist)
	rowsUp := 0 // rows from the box's top to the cursor, as last drawn
	var utf []byte
	status := ""
	draw := func(final bool) {
		cols := a.cols()
		var sb strings.Builder
		sb.WriteString("\r")
		if rowsUp > 0 {
			fmt.Fprintf(&sb, "\x1b[%dA", rowsUp)
		}
		sb.WriteString("\x1b[J")
		shown := buf
		if hidden {
			shown = []rune(strings.Repeat("•", len(buf)))
		}
		if final { // what was typed stays above, dimmed, as Claude Code leaves it
			text := string(shown)
			if hidden {
				text = oneLine(text, 12)
			}
			for i, l := range strings.Split(text, "\n") {
				p := "❯ "
				if i > 0 {
					p = "  "
				}
				sb.WriteString(cDim + p + l + cReset + "\r\n")
			}
			sb.WriteString("\r\n")
			rowsUp = 0
			a.out.write(sb.String())
			return
		}
		lines, crow, ccol := editLayout(shown, pos, cols-1)
		rule := cDim + strings.Repeat("─", cols-1) + cReset
		sb.WriteString(rule + "\r\n")
		for i, l := range lines {
			p := "  "
			if i == 0 {
				p = cBold + "❯ " + cReset
				if label != "" {
					p = cBold + label + "❯ " + cReset
				}
			}
			sb.WriteString(p + l + "\r\n")
		}
		sb.WriteString(rule + "\r\n")
		st := status
		if st == "" {
			st = "▸▸ edits on · " + strings.TrimPrefix(a.cfg.Model, "claude-") + " · /help"
		}
		sb.WriteString(cDim + "  " + oneLine(st, cols-4) + cReset)
		total := 1 + len(lines) + 2 // rule, lines, rule, status
		up := total - 1 - (1 + crow)
		if up > 0 {
			fmt.Fprintf(&sb, "\x1b[%dA", up)
		}
		sb.WriteString("\r")
		indent := 2
		if crow == 0 && label != "" {
			indent += utf8.RuneCountInString(label)
		}
		if c := indent + ccol; c > 0 {
			fmt.Fprintf(&sb, "\x1b[%dC", c)
		}
		rowsUp = 1 + crow
		a.out.write(sb.String())
	}
	setText := func(s string) { buf, pos = []rune(s), utf8.RuneCountInString(s) }
	draw(false)
	a.aheadMu.Lock()
	pendingAhead := a.ahead
	a.ahead = nil
	a.aheadMu.Unlock()
	for {
		var chunk []byte
		if len(pendingAhead) > 0 {
			chunk, pendingAhead = pendingAhead, nil
		} else {
			k, ok := <-a.keys
			if !ok {
				return "", false
			}
			chunk = k
		}
		status = ""
		for i := 0; i < len(chunk); i++ {
			b := chunk[i]
			if len(utf) > 0 || b >= 0x80 {
				utf = append(utf, b)
				if utf8.FullRune(utf) {
					r, _ := utf8.DecodeRune(utf)
					utf = nil
					buf = append(buf[:pos], append([]rune{r}, buf[pos:]...)...)
					pos++
				}
				continue
			}
			if b != 3 {
				a.exitArm = false
			}
			switch {
			case b == 0x1b: // escape sequences: arrows, home, end, delete; alone: clear
				seq, n := escSeq(chunk[i:])
				i += n - 1
				switch seq {
				case "\x1b":
					buf, pos = nil, 0
				case "\x1b[D", "\x1bOD":
					pos = max(pos-1, 0)
				case "\x1b[C", "\x1bOC":
					pos = min(pos+1, len(buf))
				case "\x1b[H", "\x1b[1~", "\x1bOH":
					pos = 0
				case "\x1b[F", "\x1b[4~", "\x1bOF":
					pos = len(buf)
				case "\x1b[3~":
					if pos < len(buf) {
						buf = append(buf[:pos], buf[pos+1:]...)
					}
				case "\x1b[A", "\x1bOA":
					if !hidden && hi > 0 {
						hi--
						setText(a.hist[hi])
					}
				case "\x1b[B", "\x1bOB":
					if !hidden && hi < len(a.hist) {
						hi++
						if hi == len(a.hist) {
							setText("")
						} else {
							setText(a.hist[hi])
						}
					}
				}
			case b == '\r' || b == '\n':
				if b == '\n' && i > 0 && chunk[i-1] == '\r' {
					continue // CR LF: one line end
				}
				if i < len(chunk)-1 { // inside a paste: a new line, not "send"
					buf = append(buf[:pos], append([]rune{'\n'}, buf[pos:]...)...)
					pos++
					continue
				}
				if pos > 0 && pos == len(buf) && buf[pos-1] == '\\' {
					buf[pos-1] = '\n'
					continue
				}
				draw(true)
				return string(buf), true
			case b == 0x7f || b == 8:
				if pos > 0 {
					buf = append(buf[:pos-1], buf[pos:]...)
					pos--
				}
			case b == 1:
				pos = 0
			case b == 5:
				pos = len(buf)
			case b == 0x15:
				buf, pos = buf[pos:], 0
			case b == 0x0b:
				buf = buf[:pos]
			case b == 0x17: // the word before the cursor
				j := pos
				for j > 0 && buf[j-1] == ' ' {
					j--
				}
				for j > 0 && buf[j-1] != ' ' && buf[j-1] != '\n' {
					j--
				}
				buf = append(buf[:j], buf[pos:]...)
				pos = j
			case b == 0x0c:
				a.out.write("\x1b[2J\x1b[3J\x1b[H")
				rowsUp = 0
			case b == 3:
				if len(buf) > 0 {
					buf, pos = nil, 0
					continue
				}
				if a.exitArm || hidden {
					draw(true)
					return "", false
				}
				a.exitArm = true
				status = "Press Ctrl-C again to leave"
			case b == 4:
				if len(buf) == 0 {
					draw(true)
					return "", false
				}
			case b == '\t':
				buf = append(buf[:pos], append([]rune("  "), buf[pos:]...)...)
				pos += 2
			case b >= 0x20:
				buf = append(buf[:pos], append([]rune{rune(b)}, buf[pos:]...)...)
				pos++
			}
		}
		draw(false)
	}
}

// escSeq is the escape sequence at the start of b and its length (a lone ESC is "\x1b").
func escSeq(b []byte) (string, int) {
	if len(b) < 2 {
		return "\x1b", 1
	}
	switch b[1] {
	case '[':
		for j := 2; j < len(b); j++ {
			if b[j] >= 0x40 && b[j] <= 0x7e {
				return string(b[:j+1]), j + 1
			}
		}
		return string(b), len(b)
	case 'O':
		if len(b) >= 3 {
			return string(b[:3]), 3
		}
	}
	return "\x1b", 1
}

// editLayout wraps the typed text into rows width-2 wide (after the "❯ "), and says on which
// row and column the cursor is.
func editLayout(buf []rune, pos, width int) (lines []string, crow, ccol int) {
	w := max(width-2, 8)
	var cur []rune
	row, col := 0, 0
	for i := 0; i <= len(buf); i++ {
		if i == pos {
			crow, ccol = row, col
		}
		if i == len(buf) {
			break
		}
		r := buf[i]
		if r == '\n' {
			lines = append(lines, string(cur))
			cur, row, col = nil, row+1, 0
			continue
		}
		if col == w {
			lines = append(lines, string(cur))
			cur, row, col = nil, row+1, 0
			if i == pos {
				crow, ccol = row, col
			}
		}
		cur = append(cur, r)
		col++
	}
	lines = append(lines, string(cur))
	return lines, crow, ccol
}

// --- Markdown, enough of it: headings, lists, **bold**, `code`, fenced blocks -------------

func renderMarkdown(text string, cols int, first string) string {
	var sb strings.Builder
	inCode := false
	started := false
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if !started && strings.TrimSpace(l) == "" {
			continue
		}
		p := "  "
		if !started {
			p, started = cWhite+first+cReset, true
		}
		var out string
		out, inCode = mdLine(l, inCode, cols, p)
		sb.WriteString(out)
	}
	return sb.String()
}

// mdLine draws one line of Markdown, wrapped to cols, the first row after prefix (2 cells
// wide) and the rest indented the same. It returns "" for a code fence.
func mdLine(l string, inCode bool, cols int, prefix string) (string, bool) {
	t := strings.TrimSpace(l)
	if strings.HasPrefix(t, "```") {
		return "", !inCode
	}
	if inCode {
		return prefix + cCode + oneLineKeep(expandTabs(l), cols-3) + cReset + "\r\n", true
	}
	var segs []mdSeg
	cont := "  "
	switch {
	case strings.HasPrefix(t, "#"):
		h := strings.TrimSpace(strings.TrimLeft(t, "#"))
		segs = []mdSeg{{strings.ReplaceAll(h, "**", ""), cBold}}
	case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
		lead := l[:len(l)-len(strings.TrimLeft(l, " "))]
		segs = append([]mdSeg{{lead + "• ", ""}}, mdInline(t[2:])...)
		cont += lead + "  "
	default:
		segs = mdInline(l)
	}
	return wrapSegs(segs, cols-1, prefix, cont), false
}

type mdSeg struct{ text, style string }

var mdInlineRE = regexp.MustCompile("\\*\\*[^*]+\\*\\*|`[^`]+`")

func mdInline(s string) []mdSeg {
	var segs []mdSeg
	for {
		loc := mdInlineRE.FindStringIndex(s)
		if loc == nil {
			break
		}
		if loc[0] > 0 {
			segs = append(segs, mdSeg{s[:loc[0]], ""})
		}
		m := s[loc[0]:loc[1]]
		if m[0] == '`' {
			segs = append(segs, mdSeg{m[1 : len(m)-1], cCode})
		} else {
			segs = append(segs, mdSeg{m[2 : len(m)-2], cBold})
		}
		s = s[loc[1]:]
	}
	if s != "" {
		segs = append(segs, mdSeg{s, ""})
	}
	return segs
}

// wrapSegs lays styled text out in rows of width cells, breaking at spaces, the first row
// after prefix and the others after cont (both 2+ cells, counted as their visible width).
func wrapSegs(segs []mdSeg, width int, prefix, cont string) string {
	type word struct {
		text, style string
		space       bool
	}
	var words []word
	for _, s := range segs {
		for _, part := range splitKeepSpaces(expandTabs(s.text)) {
			words = append(words, word{part, s.style, strings.TrimSpace(part) == ""})
		}
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	col := visibleLen(prefix)
	start := col
	contW := visibleLen(cont)
	for _, w := range words {
		n := utf8.RuneCountInString(w.text)
		if col+n > width && col > start {
			if w.space {
				continue
			}
			sb.WriteString(cReset + "\r\n" + cont)
			col, start = contW, contW
		}
		for n > width-col && width-col > 0 { // a word longer than the row: cut it
			r := []rune(w.text)
			k := width - col
			sb.WriteString(w.style + string(r[:k]) + cReset + "\r\n" + cont)
			w.text = string(r[k:])
			n -= k
			col, start = contW, contW
		}
		if w.space && col == start && col == contW && sb.Len() > 0 && start != visibleLen(prefix) {
			continue // no space at the start of a wrapped row
		}
		sb.WriteString(w.style + w.text)
		if w.style != "" {
			sb.WriteString(cReset)
		}
		col += n
	}
	sb.WriteString(cReset + "\r\n")
	return sb.String()
}

func splitKeepSpaces(s string) []string {
	var out []string
	start := 0
	for i := 1; i <= len(s); i++ {
		if i == len(s) || (s[i] == ' ') != (s[i-1] == ' ') {
			out = append(out, s[start:i])
			start = i
		}
	}
	return out
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b[()][0-9A-Za-z]|\x1b[=>]")

func visibleLen(s string) int { return utf8.RuneCountInString(ansiRE.ReplaceAllString(s, "")) }

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

// oneLineKeep cuts s to n cells without folding its spaces (code keeps its indentation).
func oneLineKeep(s string, n int) string {
	if utf8.RuneCountInString(s) > n && n > 1 {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}

// --- the instructions -----------------------------------------------------------------------

func (a *agent) systemPrompt() string {
	src := "Margin's code is in " + marginSrc + " (unpacked there by `update`; the repo root, with CLAUDE.md)."
	if _, err := os.Stat(marginSrc); err != nil {
		src = marginSrc + " doesn't exist yet: Margin's code gets there when the user runs `update` once in the Shell tab " +
			"(it downloads github.com/aminoulogie/margin and builds it). Say so if a task needs the code."
	}
	conn := ""
	if len(a.cfg.Connectors) > 0 {
		var names []string
		for _, c := range a.cfg.Connectors {
			names = append(names, c.Name)
		}
		conn = "\nThe user connected these connectors (MCP servers; Anthropic's servers call their tools for you): " +
			strings.Join(names, ", ") + ". Use their tools when a task needs what they reach.\n"
	}
	gh := "No GitHub token is set, so publish_to_github can't work until the user types /github."
	if a.cfg.GitHubToken != "" {
		gh = "A GitHub token is set for " + a.repo() + " (branch " + a.branch() + ")."
	}
	return `You are Claude, running inside Margin: the user's own operating system on their Condor TRA-901G tablet
(32-bit Intel Atom, Linux kernel 3.4, Alpine Linux in a chroot, all of Margin's screen is one Go program, margin-init).
You are in the "Claude" tab of Margin's Terminal, working like Claude Code: you can run shell commands and read and
edit files on the tablet, as root, inside Alpine. The user is right here, usually describing something in Margin
they want fixed or changed. Fix it on the spot.

` + src + `
Read CLAUDE.md first when a task touches Margin: it explains how the system works, what has been tried, and the
project's safety rules. The tablet program is in os/margin-init (Go, package main).

How to fix Margin:
1. Find the code (grep -rn, read_file), change it with edit_file. Keep the project's style.
2. Build to check it: cd ` + marginSrc + `/os/margin-init && GOTMPDIR=/root/.cache/gotmp GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -o /tmp/mi-check .
   (the tablet is slow: a build takes a few minutes; use timeout_seconds 1800). gofmt -l . should print nothing.
3. Install it with: update --local
   That builds again, installs the new margin-init and restarts Margin. The restart closes this tab for about ten
   seconds; this conversation is saved and continues by itself afterwards, so you can check the result. If the new
   build doesn't start, the old one comes back by itself.
4. Plain ` + "`update`" + ` (without --local) downloads GitHub's code over ` + marginSrc + `; it refuses while there are
   fixes not on GitHub. When a fix works, ask the user whether to publish it, then use publish_to_github.
` + gh + conn + `

Rules: never write to block devices (/dev/block, mmcblk), never change /system, never flash or wipe anything, never
delete the user's books or data. Don't run programs that wait for keyboard input (editors, pagers, interactive
prompts): commands run with no terminal. Long builds are fine. Answer in short, plain words: the user is a beginner
to intermediate programmer reading on a tablet. Don't put model names in commit messages.

Working folder: ` + a.sess.Cwd + `. Today is ` + time.Now().Format("Monday 2 January 2006, 15:04") + `.`
}
