//go:build linux

package main

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// The Claude tab's terminal: raw keys (each key as it's pressed, no echo) and the window size.

const (
	tcgets     = 0x5401
	tcsets     = 0x5402
	tiocgwinsz = 0x5413
)

// ttyRaw puts the terminal on fd in raw mode and returns how to put it back.
func ttyRaw(fd uintptr) (restore func(), err error) {
	var old syscall.Termios
	if err := ioctl(fd, tcgets, unsafe.Pointer(&old)); err != nil {
		return func() {}, err
	}
	t := old
	t.Iflag &^= syscall.ICRNL | syscall.IXON | syscall.BRKINT | syscall.INPCK | syscall.ISTRIP
	t.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 1, 0
	if err := ioctl(fd, tcsets, unsafe.Pointer(&t)); err != nil {
		return func() {}, err
	}
	return func() { ioctl(fd, tcsets, unsafe.Pointer(&old)) }, nil
}

// ttyCols is the terminal's width in cells (80 if it can't tell).
func ttyCols(fd uintptr) int {
	var ws [4]uint16
	if ioctl(fd, tiocgwinsz, unsafe.Pointer(&ws)) != nil || ws[1] == 0 {
		return 80
	}
	return int(ws[1])
}

// ownGroup runs cmd in its own process group, so stopping it stops everything it started
// (go build's compilers, a shell's children).
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// claudeHome is where the Claude tab keeps its settings and the conversation: in Alpine's
// /root when it runs there (chrooted), else in Margin's folder.
func claudeHome() string {
	if h := os.Getenv("MARGIN_CLAUDE_HOME"); h != "" {
		return h
	}
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return "/root/.margin-claude"
	}
	return marginHome + "/claude"
}
