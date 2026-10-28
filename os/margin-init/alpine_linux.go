//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

var mountMu sync.Mutex

// alpineMounts makes the kernel's filesystems visible inside the Alpine root (once per boot):
// /proc, /sys, and the tablet's /dev (with /dev/pts, so the shell has its terminal).
func alpineMounts() error {
	mountMu.Lock() // the console and alpineBoot both get here at startup
	defer mountMu.Unlock()
	mounted, _ := os.ReadFile("/proc/mounts")
	isMounted := func(p string) bool { return strings.Contains(string(mounted), " "+p+" ") }
	for _, m := range []struct {
		src, target, fstype string
		flags               uintptr
	}{
		{"proc", alpineRoot + "/proc", "proc", 0},
		{"sysfs", alpineRoot + "/sys", "sysfs", 0},
		{"/dev", alpineRoot + "/dev", "", syscall.MS_BIND | syscall.MS_REC},
		{"tmpfs", alpineRoot + "/tmp", "tmpfs", 0},
		// The Wi-Fi driver opens its firmware by path from the process that raises wlan0, so
		// /system must resolve the same inside the chroot.
		{"/system", alpineRoot + "/system", "", syscall.MS_BIND},
	} {
		if isMounted(m.target) {
			continue
		}
		os.MkdirAll(m.target, 0o755)
		if err := syscall.Mount(m.src, m.target, m.fstype, m.flags, ""); err != nil {
			return fmt.Errorf("mount %s: %w", m.target, err)
		}
	}
	return nil
}

// setHostname names the machine "margin" (shown in the prompt).
func setHostname() { syscall.Sethostname([]byte("margin")) }

// runInAlpine runs a command inside the Alpine root and waits for it. Output goes to our own
// stdout/stderr (the init log at boot, the terminal for 'margin-init alpine-run'). Internet
// goes through margin-init's proxy, like in the console.
func runInAlpine(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = "/root"
	proxy := "http://" + proxyAddr
	cmd.Env = []string{"HOME=/root", "TERM=xterm", "USER=root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"http_proxy=" + proxy, "https_proxy=" + proxy, "HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy,
		"no_proxy=localhost,127.0.0.1"}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: alpineRoot}
	return cmd.Run()
}

// alpineArgs makes a bare command name ("apk") findable: exec.Command looks a name without a
// slash up in the host's (Android's) PATH before the chroot, where Alpine's programs aren't, and
// fails with no output at all. Through Alpine's shell, the lookup happens inside the chroot.
func alpineArgs(args []string) []string {
	if len(args) == 0 || strings.Contains(args[0], "/") {
		return args
	}
	return append([]string{"/bin/sh", "-c", `exec "$@"`, "sh"}, args...)
}

// outputInAlpine runs a command in Alpine and returns what it printed.
func outputInAlpine(args ...string) ([]byte, error) {
	args = alpineArgs(args)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = "/root"
	proxy := "http://" + proxyAddr
	cmd.Env = []string{"HOME=/root", "TERM=xterm", "USER=root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"http_proxy=" + proxy, "https_proxy=" + proxy, "HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy,
		"no_proxy=localhost,127.0.0.1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: alpineRoot}
	return cmd.CombinedOutput()
}

// outputInAlpineDirect is outputInAlpine without margin-init's proxy: straight out over
// Wi-Fi, for when the proxy can't reach a mirror (update does the same).
func outputInAlpineDirect(args ...string) ([]byte, error) {
	args = alpineArgs(args)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = "/root"
	cmd.Env = []string{"HOME=/root", "TERM=xterm", "USER=root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: alpineRoot}
	return cmd.CombinedOutput()
}
