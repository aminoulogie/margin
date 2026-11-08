package main

import (
	"testing"
	"time"
)

// Remove is tapped with drawMu held, and the change callback takes drawMu: it must not run
// inline (that froze Margin for good, 2026-11-08).
func TestAddonRemoveUnderDrawMu(t *testing.T) {
	oldDir := playboxDir
	playboxDir = t.TempDir()
	t.Cleanup(func() { playboxDir = oldDir })
	as := &addonStore{list: []installedAddon{{Base: "https://a.example"}, {Base: "https://b.example"}}}
	called := make(chan struct{}, 1)
	as.changed = func() {
		drawMu.Lock()
		drawMu.Unlock()
		called <- struct{}{}
	}
	done := make(chan struct{})
	go func() {
		drawMu.Lock()
		as.remove("https://a.example")
		drawMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("remove deadlocked under drawMu")
	}
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("the change callback never ran")
	}
	if len(as.list) != 1 || as.list[0].Base != "https://b.example" {
		t.Fatalf("list after remove: %+v", as.list)
	}
}
