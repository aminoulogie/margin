package main

import (
	"bytes"
	"os"
	"testing"
)

// The update command is written without carriage returns, even from a Margin built on
// Windows (where Git may check update.sh out with CRLF line endings).
func TestUpdateScriptHasUnixLineEndings(t *testing.T) {
	oldRoot, oldScript := alpineRoot, updateScript
	t.Cleanup(func() { alpineRoot, updateScript = oldRoot, oldScript })
	alpineRoot = t.TempDir()
	os.MkdirAll(alpineRoot+"/etc/apk", 0o755)
	os.MkdirAll(alpineRoot+"/etc/profile.d", 0o755)
	updateScript = []byte("#!/bin/sh\r\necho hi\r\n")
	configureAlpine()
	b, err := os.ReadFile(alpineRoot + "/usr/local/bin/update")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("\r")) || !bytes.HasPrefix(b, []byte("#!/bin/sh\n")) {
		t.Fatalf("update written as %q", b)
	}
	if fi, _ := os.Stat(alpineRoot + "/usr/local/bin/update"); fi.Mode()&0o111 == 0 {
		t.Fatal("update isn't executable")
	}
	if bytes.Contains(oldScript, []byte("\r")) {
		t.Error("update.sh in this checkout has CRLF line endings")
	}
}
