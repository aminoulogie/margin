package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	xterm "golang.org/x/term"
)

const termPort = "2323"

// term joins the console shown on the tablet's screen (margin-init serves it on
// 127.0.0.1:2323), reached over USB with 'adb forward': PC and tablet share one session. Only works in takeover mode: margin-init must
// be running (margin takeover arm <margin-init>, then margin reboot).
func term(args []string) error {
	if err := needDevice(); err != nil {
		return err
	}
	if !strings.Contains(sh("ps"), initProc) {
		return fmt.Errorf("margin-init isn't running; arm takeover and reboot:\n" +
			"  margin takeover arm <margin-init>\n  margin reboot")
	}
	if out, err := adb("forward", "tcp:"+termPort, "tcp:"+termPort); err != nil {
		return fmt.Errorf("adb forward: %s", strings.TrimSpace(out))
	}
	defer adb("forward", "--remove", "tcp:"+termPort)

	c, err := net.Dial("tcp", "127.0.0.1:"+termPort)
	if err != nil {
		return fmt.Errorf("connect to margin-init shell: %w", err)
	}
	defer c.Close()
	// In a real console, go raw: every key goes straight to the tablet, which does the echo
	// and line editing (so nothing is typed twice), and Ctrl+C reaches the tablet's shell.
	// Ctrl+] leaves, like telnet. Piped input keeps the old line mode.
	done := make(chan struct{}, 2)
	go func() { io.Copy(os.Stdout, c); done <- struct{}{} }()
	fd := int(os.Stdin.Fd())
	if xterm.IsTerminal(fd) {
		if old, err := xterm.MakeRaw(fd); err == nil {
			defer xterm.Restore(fd, old)
		}
		enableVTOutput()
		fmt.Print("connected to the tablet console. Ctrl+] to leave.\r\n")
		go func() {
			buf := make([]byte, 256)
			for {
				n, err := os.Stdin.Read(buf)
				if i := bytes.IndexByte(buf[:n], 0x1d); i >= 0 { // Ctrl+]
					c.Write(buf[:i])
					break
				}
				if n > 0 {
					c.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
			done <- struct{}{}
		}()
	} else {
		// Windows pipes send CRLF; the tablet's shell wants bare LF.
		go func() { io.Copy(c, crStripper{os.Stdin}); done <- struct{}{} }()
	}
	<-done
	fmt.Print("\r\n")
	return nil
}

// crStripper is a reader that removes carriage returns from the wrapped stream.
type crStripper struct{ r io.Reader }

func (s crStripper) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	w := 0
	for i := 0; i < n; i++ {
		if p[i] != '\r' {
			p[w] = p[i]
			w++
		}
	}
	return w, err
}
