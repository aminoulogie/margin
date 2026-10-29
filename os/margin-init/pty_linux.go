//go:build linux

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	tiocgptn   = 0x80045430
	tiocsptlck = 0x40045431
	tiocswinsz = 0x5414
)

// shrcPath is the shell's startup file (mksh reads $ENV): the prompt and a few aliases.
const shrcPath = marginHome + "/shrc"

const shrc = `PS1='[root@margin ${PWD:-/}]# '
alias ll='ls -l'
alias la='ls -la'
`

// openPty makes a pseudo-terminal of cols x rows: its master side, and the slave side for
// the program (closed by the caller once the program has it).
func openPty(cols, rows int) (m, slave *os.File, err error) {
	m, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err := ioctl(m.Fd(), tiocsptlck, unsafe.Pointer(&unlock)); err != nil {
		m.Close()
		return nil, nil, fmt.Errorf("unlock pty: %w", err)
	}
	var n uint32
	if err := ioctl(m.Fd(), tiocgptn, unsafe.Pointer(&n)); err != nil {
		m.Close()
		return nil, nil, fmt.Errorf("pty number: %w", err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	ws := [4]uint16{uint16(rows), uint16(cols), 0, 0}
	ioctl(slave.Fd(), tiocswinsz, unsafe.Pointer(&ws))
	return m, slave, nil
}

// startShell runs the shell on a new pseudo-terminal of cols x rows. It returns the pty's
// master side and a function that waits for the shell to exit.
func startShell(cols, rows int) (*os.File, func(), error) {
	m, slave, err := openPty(cols, rows)
	if err != nil {
		return nil, nil, err
	}
	defer slave.Close()

	var cmd *exec.Cmd
	attr := &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if alpineInstalled() {
		configureAlpine()
		if err := alpineMounts(); err != nil {
			log.Printf("alpine mounts: %v", err)
		}
		// A login shell inside Alpine: /etc/profile sets PATH and the prompt.
		cmd = exec.Command("/bin/sh")
		cmd.Args = []string{"-sh"}
		cmd.Dir = "/root"
		cmd.Env = []string{"HOME=/root", "TERM=xterm", "USER=root", "LOGNAME=root", "SHELL=/bin/sh",
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
		attr.Chroot = alpineRoot
	} else {
		os.MkdirAll(marginHome, 0o755)
		os.WriteFile(shrcPath, []byte(shrc), 0o644)
		cmd = exec.Command(shellPath)
		cmd.Dir = marginHome
		cmd.Env = []string{"PATH=" + shellPATH, "HOME=" + marginHome, "TERM=xterm", "ENV=" + shrcPath,
			"USER=root", "LOGNAME=root"}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, func() { cmd.Wait() }, nil
}

// startClaude runs Claude (margin-init itself: "claude", claude.go) on a new pty, in Alpine
// like the shell, so its commands and Go are Alpine's. /proc/self/exe is this program even
// from inside the chroot (/proc is mounted there).
func startClaude(cols, rows int) (*os.File, func(), error) {
	m, slave, err := openPty(cols, rows)
	if err != nil {
		return nil, nil, err
	}
	defer slave.Close()
	cmd := exec.Command("/proc/self/exe", "claude")
	cmd.Env = []string{"HOME=/root", "TERM=xterm", "USER=root", "LOGNAME=root", "SHELL=/bin/sh", "TZ=WAT-1",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"http_proxy=http://" + proxyAddr, "https_proxy=http://" + proxyAddr,
		"HTTP_PROXY=http://" + proxyAddr, "HTTPS_PROXY=http://" + proxyAddr, "no_proxy=localhost,127.0.0.1"}
	attr := &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if alpineInstalled() {
		configureAlpine()
		if err := alpineMounts(); err != nil {
			log.Printf("alpine mounts: %v", err)
		}
		attr.Chroot = alpineRoot
		cmd.Dir = "/root"
	} else {
		os.MkdirAll(marginHome, 0o755)
		cmd.Dir = marginHome
		cmd.Env = append(cmd.Env[1:], "HOME="+marginHome, "PATH="+shellPATH)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, func() { cmd.Wait() }, nil
}

// setWinsize tells the pty (and so the programs on it, via SIGWINCH) its size in cells.
func setWinsize(f *os.File, cols, rows int) {
	ws := [4]uint16{uint16(rows), uint16(cols), 0, 0}
	ioctl(f.Fd(), tiocswinsz, unsafe.Pointer(&ws))
}
