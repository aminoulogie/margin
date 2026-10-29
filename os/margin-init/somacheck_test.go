package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// Yesterday's entries made on the PC reach the tablet's timesheet, also on a project the tablet
// doesn't have and when Soma sent the list as one record; soma-check tells what's missing.
func TestPCEntriesReachTheTablet(t *testing.T) {
	_, relay, k := somaTestLink(t)
	y := dayStart(time.Now()).AddDate(0, 0, -1)
	ms := func(t time.Time) float64 { return float64(t.UnixMilli()) }
	now := time.Now().UnixMilli() - 1000
	relay.pcPut(t, k, "projects#p1", obj{"id": "p1", "name": "Rebar", "status": "active", "rate": 30}, now)
	relay.pcPut(t, k, "projects@order", []string{"p1"}, now)
	relay.pcPut(t, k, "timeEntries#te-a", obj{"id": "te-a", "projectId": "p1", "start": ms(y.Add(9 * time.Hour)), "end": ms(y.Add(12 * time.Hour))}, now)
	relay.pcPut(t, k, "timeEntries#te-b", obj{"id": "te-b", "projectId": "gone", "start": ms(y.Add(13 * time.Hour)), "end": ms(y.Add(15 * time.Hour))}, now)
	relay.pcPut(t, k, "timeEntries@order", []string{"te-a", "te-b"}, now)
	soma.sync()
	var got time.Duration
	for _, e := range wkEntries() {
		if dayStart(e.Start).Equal(y) {
			got += e.dur(time.Now())
		}
	}
	if got != 5*time.Hour {
		t.Fatalf("yesterday on the tablet: %v, want 5h", got)
	}
	// One record for the whole list (an item Soma couldn't split by id).
	relay.pcPut(t, k, "timeEntries", []any{obj{"id": "te-c", "projectId": "p1", "start": ms(y.Add(16 * time.Hour)), "end": ms(y.Add(17 * time.Hour))}}, now+5)
	soma.sync()
	found := false
	for _, e := range wkEntries() {
		found = found || e.ID == "te-c"
	}
	if !found {
		t.Fatal("entries in a whole-list record weren't read")
	}

	// soma-check: an entry on the relay the tablet hasn't got is called out.
	relay.pcPut(t, k, "timeEntries#te-d", obj{"id": "te-d", "projectId": "p1", "start": ms(y.Add(18 * time.Hour)), "end": ms(y.Add(19 * time.Hour))}, now+9)
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := runSomaCheck()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if code != 0 || !strings.Contains(string(out), "MISSING ON THE TABLET") || !strings.Contains(string(out), "same on both") {
		t.Fatalf("soma-check (%d):\n%s", code, out)
	}
}
