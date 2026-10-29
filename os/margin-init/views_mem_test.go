package main

import (
	"runtime"
	"testing"
)

// Going through every workstation page again and again (each drawn fresh, as after a change)
// keeps what the views hold within their budget, and the program's memory with it: keeping
// every page and screen had grown margin-init past a gigabyte on the tablet.
func TestViewsMemoryBounded(t *testing.T) {
	c := workAt(t, "overview")
	views.Lock()
	views.off = false
	views.Unlock()
	t.Cleanup(func() { views.off = true })
	var pages []string
	for _, p := range wkPages {
		pages = append(pages, p.id)
	}
	for _, p := range wkPersonal {
		pages = append(pages, p.id)
	}
	for round := 0; round < 3; round++ {
		for _, pg := range pages {
			c.somaUI.work = pg
			c.showPage()
			c.viewFresh = true // what the refresh a moment later does
			c.showPage()
			c.viewFresh = false
		}
	}
	views.writes.Wait()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	views.Lock()
	n, b := len(views.m), views.bytes
	views.Unlock()
	t.Logf("views %d, %d MB; live heap %d MB", n, b>>20, ms.HeapAlloc>>20)
	if b > viewMemBytes {
		t.Fatalf("views hold %d MB, over their %d MB", b>>20, viewMemBytes>>20)
	}
	if ms.HeapAlloc > 320<<20 {
		t.Fatalf("live heap %d MB after going through the pages", ms.HeapAlloc>>20)
	}
}
