//go:build linux

package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The Terminal's Shell and Claude tabs: pills under the tab bar, each with its own program
// and screen; typing goes to the tab on show.
func TestTerminalTabs(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	old := claudeStart
	t.Cleanup(func() { claudeStart = old })
	claudeStart = func(cols, rows int) (*os.File, func(), error) { // stands in for claude.go
		m, slave, err := openPty(cols, rows)
		if err != nil {
			return nil, nil, err
		}
		defer slave.Close()
		cmd := exec.Command("/bin/sh", "-c", "echo CLAUDE-TAB; stty -echo; while read l; do echo got:$l; done")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		return m, func() { cmd.Wait() }, nil
	}
	c := testConsole(t)
	c.terms[termShell].t.Write([]byte("shell text"))
	drawMu.Lock()
	c.setMode(modeTerminal)
	main, sub, mi, si := c.tabLayout(c.tabsOn)
	if c.tabsOn != "tt:shell" || main[mi].id != "tab:terminal" || len(sub) != 2 || sub[si].id != "tt:shell" || sub[1].label != "Claude" {
		t.Fatalf("tabs on %q: main %d sub %+v (%d)", c.tabsOn, mi, sub, si)
	}
	c.tapID("tt:claude")
	if c.cur != termClaude || c.t != c.terms[termClaude].t || c.tabsOn != "tt:claude" {
		t.Fatalf("Claude tab not on show: cur %d", c.cur)
	}
	drawMu.Unlock()
	screen := func(i int) string {
		var sb strings.Builder
		for y := 0; y < c.terms[i].t.Rows; y++ {
			sb.WriteString(c.terms[i].t.Text(y) + "\n")
		}
		return sb.String()
	}
	wait := func(i int, want string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if strings.Contains(screen(i), want) {
				return
			}
		}
		t.Fatalf("tab %d never showed %q:\n%s", i, want, screen(i))
	}
	wait(termClaude, "CLAUDE-TAB")
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		c.mu.Lock()
		ready := c.master != nil
		c.mu.Unlock()
		if ready {
			break
		}
	}
	c.input([]byte("fix it\r"))
	wait(termClaude, "got:fix it")
	if strings.Contains(screen(termShell), "fix it") || strings.Contains(screen(termClaude), "shell text") {
		t.Fatal("the tabs' screens got mixed")
	}
	drawMu.Lock()
	c.tapID("tt:shell")
	if c.cur != termShell || c.t != c.terms[termShell].t {
		t.Fatal("Shell tab not back")
	}
	drawMu.Unlock()
}
