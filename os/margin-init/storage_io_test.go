//go:build linux

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWithTorrentIOEnv(t *testing.T) {
	got := withTorrentIOEnv([]string{"PATH=/bin", torrentIOEnv + "=mmap", "HOME=/root"})
	if strings.Join(got, " ") != "PATH=/bin HOME=/root "+torrentIOEnv+"=classic" {
		t.Fatalf("env %v", got)
	}
}

// The torrent library picks plain file reads when the variable is set before it starts (what
// margin-init's restart does). Checked in a child test process, which has it from the start.
func TestTorrentIOClassicInChild(t *testing.T) {
	if os.Getenv("MARGIN_IO_CHILD") == "1" {
		testEngine(t)
		src, seeder, data := seedFilm(t)
		j, err := engine.start(src, false, "", metaItem{ID: "tt1254207", Type: "movie", Name: "Big Buck Bunny"})
		if err != nil {
			t.Fatal(err)
		}
		j.t.AddClientPeer(seeder)
		waitState(t, j, "ready", 20*1e9)
		if st := engine.status(j); st.Size != int64(len(data)) {
			t.Fatalf("size %d", st.Size)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestTorrentIOClassicInChild$", "-test.count", "1")
	cmd.Env = append(withTorrentIOEnv(os.Environ()), "MARGIN_IO_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a torrent with plain file reads failed: %v\n%s", err, out)
	}
}

// margin-init's restart: started without the variable, it starts itself again with it.
func TestReexecForTorrentIO(t *testing.T) {
	if os.Getenv("MARGIN_REEXEC_CHILD") == "1" {
		reexecForTorrentIO() // without the variable: execs itself; with it: returns
		os.Stdout.WriteString("IO=" + os.Getenv(torrentIOEnv) + "\n")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestReexecForTorrentIO$", "-test.count", "1", "-test.v")
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, torrentIOEnv+"=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "MARGIN_REEXEC_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "IO=classic") {
		t.Fatalf("after the restart: %v\n%s", err, out)
	}
}
