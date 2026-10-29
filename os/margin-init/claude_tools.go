package main

// What Claude can do in the Claude tab: run shell commands, read, write and edit files, and
// publish fixes to GitHub. Each call is shown as Claude Code shows it: "● Bash(…)" and a
// short result under it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var claudeTools = []cTool{
	{Name: "bash", Description: "Run a shell command (busybox sh, inside Alpine on the tablet) in the working folder and get its " +
		"output (stdout and stderr together, long output cut in the middle). The folder carries over between calls " +
		"(cd works). No terminal: don't start editors, pagers or anything that waits for input. Default timeout 600 s.",
		Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"the command"},` +
			`"timeout_seconds":{"type":"integer","description":"stop it after this many seconds (max 3600)"}},"required":["command"]}`)},
	{Name: "read_file", Description: "Read a text file, with line numbers (cat -n style). Up to 2000 lines at a time: use offset " +
		"(first line, from 1) and limit for more.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer"},` +
			`"limit":{"type":"integer"}},"required":["path"]}`)},
	{Name: "write_file", Description: "Create a file or replace all of it (folders are made as needed). Prefer edit_file to change " +
		"part of an existing file.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},` +
			`"required":["path","content"]}`)},
	{Name: "edit_file", Description: "Replace exact text in a file. old_string must match the file exactly (spaces and tabs " +
		"included; read the file first) and only once, unless replace_all is true.",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},` +
			`"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_string","new_string"]}`)},
	{Name: "publish_to_github", Description: "Commit every file changed in " + marginSrc + " since `update` downloaded it to the " +
		"GitHub repo's branch, in one commit, so the next `update` keeps the fixes. Only when the user agrees.",
		Schema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","description":"the commit message"}},` +
			`"required":["message"]}`)},
}

// runTool runs one call and shows it; the text goes back to Claude.
func (a *agent) runTool(ctx context.Context, call cBlock) (string, bool) {
	var in struct {
		Command     string `json:"command"`
		Timeout     int    `json:"timeout_seconds"`
		Path        string `json:"path"`
		Offset      int    `json:"offset"`
		Limit       int    `json:"limit"`
		Content     string `json:"content"`
		OldString   string `json:"old_string"`
		NewString   string `json:"new_string"`
		ReplaceAll  bool   `json:"replace_all"`
		Message     string `json:"message"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return "invalid input: " + err.Error(), true
	}
	cols := a.cols()
	head := func(name, arg string, ok bool) string {
		dot := cGreenB + "● " + cReset
		if !ok {
			dot = cRed + "● " + cReset
		}
		return dot + cBold + name + cReset + "(" + oneLine(arg, cols-len(name)-6) + ")\r\n"
	}
	bad := func(msg string) string { // an error under a call, wrapped to the screen
		return wrapSegs([]mdSeg{{msg, cRed}}, cols-1, "  "+cDim+"└"+cReset+"  ", "     ")
	}
	under := func(lines ...string) string {
		var sb strings.Builder
		for i, l := range lines {
			p := "     "
			if i == 0 {
				p = "  " + cDim + "└" + cReset + "  "
			}
			sb.WriteString(p + l + "\r\n")
		}
		return sb.String()
	}
	switch call.Name {
	case "bash":
		label := in.Command
		if i := strings.IndexByte(label, '\n'); i >= 0 {
			label = label[:i] + " …"
		}
		a.out.startSpin("Running")
		out, code, err := a.bash(ctx, in.Command, in.Timeout)
		a.out.stopSpin()
		ok := err == nil && code == 0
		var shown []string
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if strings.TrimSpace(out) == "" {
			lines = nil
		}
		from := max(len(lines)-4, 0)
		if from > 0 {
			shown = append(shown, cDim+fmt.Sprintf("… %d lines before", from)+cReset)
		}
		for _, l := range lines[from:] {
			shown = append(shown, cDim+oneLineKeep(expandTabs(l), cols-7)+cReset)
		}
		switch {
		case err != nil:
			shown = append(shown, cRed+err.Error()+cReset)
		case code != 0:
			shown = append(shown, cRed+fmt.Sprintf("Exit code %d", code)+cReset)
		case len(shown) == 0:
			shown = append(shown, cDim+"(No output)"+cReset)
		}
		a.out.write(head("Bash", label, ok) + under(shown...) + "\r\n")
		res := out
		if err != nil {
			res += "\n[" + err.Error() + "]"
		} else if code != 0 {
			res += fmt.Sprintf("\n[exit code %d]", code)
		}
		if ok && strings.Contains(out, updateInstalling) {
			res += a.awaitRestart(ctx)
		}
		return res, !ok
	case "read_file":
		p := a.abs(in.Path)
		text, n, err := readNumbered(p, in.Offset, in.Limit)
		if err != nil {
			a.out.write(head("Read", a.rel(p), false) + bad(err.Error()) + "\r\n")
			return err.Error(), true
		}
		a.out.write(head("Read", a.rel(p), true) + under(cDim+fmt.Sprintf("Read %d lines", n)+cReset) + "\r\n")
		return text, false
	case "write_file":
		p := a.abs(in.Path)
		_, statErr := os.Stat(p)
		mode := os.FileMode(0o644)
		if st, err := os.Stat(p); err == nil {
			mode = st.Mode().Perm()
		}
		err := os.MkdirAll(filepath.Dir(p), 0o755)
		if err == nil {
			err = os.WriteFile(p, []byte(in.Content), mode)
		}
		if err != nil {
			a.out.write(head("Write", a.rel(p), false) + bad(err.Error()) + "\r\n")
			return err.Error(), true
		}
		lines := strings.Split(strings.TrimRight(in.Content, "\n"), "\n")
		verb := "Wrote"
		if statErr != nil {
			verb = "Created"
		}
		shown := []string{fmt.Sprintf("%s %d lines to %s", verb, len(lines), a.rel(p))}
		for i, l := range lines[:min(len(lines), 6)] {
			shown = append(shown, cDim+fmt.Sprintf("%4d ", i+1)+oneLineKeep(expandTabs(l), cols-13)+cReset)
		}
		if len(lines) > 6 {
			shown = append(shown, cDim+fmt.Sprintf("     … +%d lines", len(lines)-6)+cReset)
		}
		a.out.write(head("Write", a.rel(p), true) + under(shown...) + "\r\n")
		return fmt.Sprintf("%s %s (%d lines)", verb, p, len(lines)), false
	case "edit_file":
		p := a.abs(in.Path)
		msg, diff, err := editFile(p, in.OldString, in.NewString, in.ReplaceAll)
		if err != nil {
			a.out.write(head("Update", a.rel(p), false) + bad(err.Error()) + "\r\n")
			return err.Error(), true
		}
		shown := []string{msg}
		for _, d := range diff {
			shown = append(shown, oneLineKeep(d, cols+8)) // the colour codes take no room
		}
		a.out.write(head("Update", a.rel(p), true) + under(shown...) + "\r\n")
		return "Updated " + p + ": " + ansiRE.ReplaceAllString(msg, ""), false
	case "publish_to_github":
		a.out.startSpin("Publishing")
		res, err := publishFixes(ctx, a.cfg.GitHubToken, a.repo(), a.branch(), marginSrc, in.Message)
		a.out.stopSpin()
		if err != nil {
			a.out.write(head("Publish", in.Message, false) + bad(err.Error()) + "\r\n")
			return err.Error(), true
		}
		a.out.write(head("Publish", in.Message, true) + under(res) + "\r\n")
		return res, false
	}
	return "unknown tool " + call.Name, true
}

// updateInstalling is what update.sh says as it hands over to its installer, which restarts
// Margin (and so this tab) a few seconds later.
const updateInstalling = "installing: the screen restarts"

// awaitRestart waits for Margin's restart to end this program, with the tool call still
// open: the conversation, saved, carries on after it (resume). If no restart comes, the
// install log says why.
func (a *agent) awaitRestart(ctx context.Context) string {
	a.out.write(cAccent + "✻ " + cReset + "Margin is restarting with the new build: this tab is back in a few seconds\r\n")
	select {
	case <-ctx.Done():
	case <-time.After(90 * time.Second):
	}
	b, _ := os.ReadFile("/tmp/margin-update.log")
	return "\n[Margin didn't restart within 90 s. /tmp/margin-update.log:]\n" + string(b)
}

// abs makes p absolute from the working folder.
func (a *agent) abs(p string) string {
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(os.Getenv("HOME"), p[2:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(a.sess.Cwd, p)
	}
	return filepath.Clean(p)
}

// rel shows p from the working folder when it's under it.
func (a *agent) rel(p string) string {
	if r, err := filepath.Rel(a.sess.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

// bash runs a command with sh in the working folder (and keeps the folder it ends in). The
// output keeps its start and its end when it's long.
func (a *agent) bash(ctx context.Context, command string, timeout int) (string, int, error) {
	if timeout <= 0 {
		timeout = 600
	}
	timeout = min(timeout, 3600)
	sh := "/bin/sh"
	if _, err := os.Stat(sh); err != nil {
		sh = shellPath
	}
	pwdFile := filepath.Join(a.home, "pwd")
	os.Remove(pwdFile)
	script := "cd " + shQuote(a.sess.Cwd) + " 2>/dev/null\n" + command + "\n__s=$?\npwd > " + shQuote(pwdFile) + " 2>/dev/null\nexit $__s\n"
	cmd := exec.Command(sh, "-c", script)
	cmd.Env = append(os.Environ(), "MARGIN_FROM_CLAUDE=1", "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat", "NO_COLOR=1")
	var out capWriter
	cmd.Stdout, cmd.Stderr = &out, &out
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		return "", -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	code := 0
	select {
	case err = <-done:
	case <-ctx.Done():
		killGroup(cmd)
		<-done
		return out.String(), -1, fmt.Errorf("stopped: the user interrupted")
	case <-time.After(time.Duration(timeout) * time.Second):
		killGroup(cmd)
		<-done
		return out.String(), -1, fmt.Errorf("stopped after %d s (timeout_seconds)", timeout)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		code, err = ee.ExitCode(), nil
	}
	if b, e := os.ReadFile(pwdFile); e == nil {
		if d := strings.TrimSpace(string(b)); d != "" {
			a.sess.Cwd = d
		}
	}
	return out.String(), code, err
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// capWriter keeps the first 8 KB and the last 24 KB of what's written.
type capWriter struct {
	mu         sync.Mutex
	head, tail []byte
	total      int
}

const capHead, capTail = 8 << 10, 24 << 10

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n0 := len(p)
	w.total += n0
	if n := capHead - len(w.head); n > 0 {
		k := min(n, len(p))
		w.head = append(w.head, p[:k]...)
		p = p[k:]
	}
	w.tail = append(w.tail, p...)
	if len(w.tail) > 2*capTail {
		w.tail = append([]byte(nil), w.tail[len(w.tail)-capTail:]...)
	}
	return n0, nil
}

func (w *capWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	tail := w.tail
	cut := 0
	if len(tail) > capTail {
		cut = len(tail) - capTail
		tail = tail[cut:]
	}
	s := string(w.head)
	if skipped := w.total - len(w.head) - len(tail); skipped > 0 {
		s += fmt.Sprintf("\n[… %d bytes left out …]\n", skipped)
	}
	s += string(tail)
	s = strings.ToValidUTF8(ansiRE.ReplaceAllString(s, ""), "?")
	return lastOfCR(s)
}

// lastOfCR keeps what a terminal would show of lines redrawn with \r (progress bars).
func lastOfCR(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

// readNumbered reads lines offset.. (from 1) of a text file, numbered, at most limit (2000).
func readNumbered(p string, offset, limit int) (string, int, error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", 0, err
	}
	if st.IsDir() {
		return "", 0, fmt.Errorf("%s is a folder (use bash ls)", p)
	}
	if st.Size() > 8<<20 {
		return "", 0, fmt.Errorf("%s is %d MB: too big to read whole (use bash: head, sed -n, grep)", p, st.Size()>>20)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", 0, err
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return "", 0, fmt.Errorf("%s is a binary file", p)
	}
	if len(b) == 0 {
		return "(the file is empty)", 0, nil
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	if offset > len(lines) {
		return "", 0, fmt.Errorf("the file has only %d lines", len(lines))
	}
	end := min(offset-1+limit, len(lines))
	var sb strings.Builder
	for i := offset - 1; i < end; i++ {
		l := lines[i]
		if utf8.RuneCountInString(l) > 2000 {
			l = string([]rune(l)[:2000]) + "…"
		}
		fmt.Fprintf(&sb, "%6d\t%s\n", i+1, l)
	}
	if end < len(lines) {
		fmt.Fprintf(&sb, "[%d more lines: read on with offset %d]\n", len(lines)-end, end+1)
	}
	return sb.String(), end - offset + 1, nil
}

// editFile replaces old with new in p, and says what changed, with a few diff lines.
func editFile(p, old, new string, all bool) (string, []string, error) {
	if old == "" {
		return "", nil, fmt.Errorf("old_string is empty (use write_file for a new file)")
	}
	if old == new {
		return "", nil, fmt.Errorf("old_string and new_string are the same")
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", nil, err
	}
	s := string(b)
	n := strings.Count(s, old)
	switch {
	case n == 0:
		return "", nil, fmt.Errorf("old_string isn't in %s (read the file again: it must match exactly)", filepath.Base(p))
	case n > 1 && !all:
		return "", nil, fmt.Errorf("old_string is in %s %d times: give more of the text around it, or set replace_all", filepath.Base(p), n)
	}
	line := strings.Count(s[:strings.Index(s, old)], "\n") + 1
	if all {
		s = strings.ReplaceAll(s, old, new)
	} else {
		s = strings.Replace(s, old, new, 1)
	}
	if err := os.WriteFile(p, []byte(s), st.Mode().Perm()); err != nil {
		return "", nil, err
	}
	ol, nl := strings.Split(old, "\n"), strings.Split(new, "\n")
	// The lines both share at the start and end aren't changes.
	a0 := 0
	for a0 < len(ol) && a0 < len(nl) && ol[a0] == nl[a0] {
		a0++
	}
	e := 0
	for e < len(ol)-a0 && e < len(nl)-a0 && ol[len(ol)-1-e] == nl[len(nl)-1-e] {
		e++
	}
	rem, add := ol[a0:len(ol)-e], nl[a0:len(nl)-e]
	msg := fmt.Sprintf("Updated %s with %s and %s", filepath.Base(p), plural(len(add), "addition"), plural(len(rem), "removal"))
	if n > 1 {
		msg += fmt.Sprintf(" (%d places)", n)
	}
	var diff []string
	ln := line + a0
	for i, l := range rem {
		if len(diff) >= 10 {
			break
		}
		diff = append(diff, cRed+fmt.Sprintf("%4d - ", ln+i)+expandTabs(l)+cReset)
	}
	for i, l := range add {
		if len(diff) >= 16 {
			diff = append(diff, cDim+"     …"+cReset)
			break
		}
		diff = append(diff, cGreen+fmt.Sprintf("%4d + ", ln+i)+expandTabs(l)+cReset)
	}
	return msg, diff, nil
}

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	return fmt.Sprintf("%d %ss", n, w)
}
