package main

import (
	"image"
	"os"
	"time"

	"margin-init/ui"
	"margin-init/vt"
)

// The Terminal has two tabs, as pills under the tab bar: Shell (Alpine's shell, the one
// `margin term` shares with the PC) and Claude (claude.go: Claude fixing Margin from inside
// it). Each is a program on its own pty with its own screen and scrollback; the one on show
// is c.t / c.master / c.tu, and the other keeps running behind.

// termTabsH is the band above the terminal's text: the tab bar and the Shell/Claude pills.
const termTabsH = marginTabsH

const (
	termShell  = 0
	termClaude = 1
)

// termSession is one tab of the Terminal.
type termSession struct {
	id     string // "shell", "claude"
	t      *vt.Term
	master *os.File // its pty, nil while its program isn't running
	tu     termUI   // its scrollback position and selection, while another tab is on show
}

var termTabIDs = [2]string{"tt:shell", "tt:claude"}

// claudeStart starts the Claude tab's program (tests put a stand-in here).
var claudeStart = startClaude

// newTermSessions makes both tabs' screens; the Shell's is on show.
func (c *console) newTermSessions(cols, rows int) {
	for i, id := range []string{"shell", "claude"} {
		s := &termSession{id: id, t: vt.New(cols, rows)}
		s.t.Reply = func(p []byte) { c.inputTo(s, p) }
		c.terms[i] = s
	}
	c.cur = termShell
	c.t = c.terms[termShell].t
}

// drawTermTabs draws the tab bar above the shell, on the terminal's black, and makes it the
// page for taps there (the terminal has no other page). Caller holds drawMu.
func (c *console) drawTermTabs() {
	img := image.NewRGBA(image.Rect(0, 0, c.s.W, termTabsH))
	ui.Fill(img, img.Rect, consoleBG)
	p := &page{img: img, header: termTabsH}
	dark := apDark
	setPalette(true) // the terminal is dark whatever the system is
	c.booksTabs(p, termTabIDs[c.cur])
	setPalette(dark)
	c.termBar, c.page = p, p
	c.s.blitRGBA(img, 0, c.barH)
}

// switchTerm shows tab i (termShell, termClaude), starting Claude the first time it's
// opened. Caller holds drawMu.
func (c *console) switchTerm(i int) {
	if c.mode != modeTerminal {
		c.mode = modeTerminal
	}
	if i != c.cur {
		if c.tu.hold != nil {
			c.tu.hold.Stop()
		}
		c.terms[c.cur].tu = c.tu
		c.mu.Lock()
		c.cur = i
		c.master = c.terms[i].master
		c.mu.Unlock()
		c.t, c.tu = c.terms[i].t, c.terms[i].tu
		c.tu.down, c.tu.scrolling, c.tu.selecting = false, false, false
		c.termDrawn = nil
	}
	if i == termClaude {
		c.claudeOnce.Do(func() { go c.runSession(c.terms[termClaude], claudeStart, "") })
	}
	c.redrawAll()
}

// output shows a tab's program output (on the screen if that tab is on show) and copies the
// Shell's to connected PCs.
func (c *console) output(s *termSession, p []byte) {
	s.t.Write(p)
	drawMu.Lock()
	if c.terms[c.cur] == s {
		c.render()
	}
	drawMu.Unlock()
	if s.id != "shell" {
		return
	}
	c.mu.Lock()
	for conn := range c.clients {
		conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(p); err != nil {
			conn.Close()
			delete(c.clients, conn)
		}
	}
	c.mu.Unlock()
}

// inputTo types p into a tab's program.
func (c *console) inputTo(s *termSession, p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := s.master
	if c.terms[c.cur] == s && c.master != nil {
		m = c.master // (tests put a pipe here)
	}
	if m != nil {
		m.Write(p)
	}
}

// runSession keeps a tab's program running, starting it again whenever it exits.
// It doesn't return.
func (c *console) runSession(s *termSession, start func(cols, rows int) (*os.File, func(), error), hello string) {
	if hello != "" {
		c.output(s, []byte(hello))
	}
	for {
		m, wait, err := start(s.t.Cols, s.t.Rows)
		if err != nil {
			c.output(s, []byte("margin: can't start "+s.id+": "+err.Error()+"\r\n"))
			time.Sleep(5 * time.Second)
			continue
		}
		c.setMaster(s, m)
		started := time.Now()
		buf := make([]byte, 4096)
		for {
			n, err := m.Read(buf)
			if n > 0 {
				c.output(s, buf[:n])
			}
			if err != nil {
				break
			}
		}
		c.setMaster(s, nil)
		m.Close()
		wait()
		if s.id == "shell" {
			c.output(s, []byte("\r\n[shell exited; starting a new one]\r\n"))
		} else {
			c.output(s, []byte("\r\n\x1b[90m[Claude closed; starting it again]\x1b[0m\r\n"))
		}
		if time.Since(started) < 3*time.Second {
			time.Sleep(3 * time.Second) // failing at once: don't spin
		}
		time.Sleep(time.Second)
	}
}

func (c *console) setMaster(s *termSession, m *os.File) {
	c.mu.Lock()
	s.master = m
	if c.terms[c.cur] == s {
		c.master = m
	}
	c.mu.Unlock()
}

// claudeReopen is left by update.sh when Claude ran `update --local`: the restarted Margin
// opens the Claude tab again, where the conversation carries on.
const claudeReopen = marginHome + "/claude-reopen"

// reopenClaude opens the Claude tab at start if Claude restarted Margin. Caller holds drawMu.
func (c *console) reopenClaude() {
	if st, err := os.Stat(claudeReopen); err == nil {
		os.Remove(claudeReopen)
		if time.Since(st.ModTime()) < 10*time.Minute && !c.locked {
			c.switchTerm(termClaude)
		}
	}
}
