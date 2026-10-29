package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// margin-init soma-check: what the tablet has of the last days' time entries and work log
// shifts, next to what Soma's relay has (read only: nothing is changed or sent). Run from the
// Clipboard (inside Alpine, through init's root) when hours on the PC don't show here.
func runSomaCheck() int {
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		somaDir = "/proc/1/root" + marginHome + "/soma"
	}
	soma.load()
	now := time.Now().In(displayZone)
	soma.mu.Lock()
	linked := soma.keys != nil
	fmt.Printf("tablet clock: %s (WAT)\n", now.Format("Mon 2006-01-02 15:04:05"))
	fmt.Printf("tablet: device %s, pulled up to seq %d, last good sync %s, %d records kept\n",
		soma.Device, soma.Seq, soma.LastOK.In(displayZone).Format("2006-01-02 15:04"), len(soma.Recs))
	count := map[string]int{}
	dirty := 0
	for k, r := range soma.Recs {
		if r.V != nil {
			count[somaField(k)]++
		}
		if r.Dirty {
			dirty++
		}
	}
	soma.mu.Unlock()
	fmt.Printf("tablet: %d time entries, %d shifts, %d projects; %d changes not sent yet\n",
		count["timeEntries"], count["shifts"], count["projects"], dirty)
	if !linked {
		fmt.Println("not linked to Soma: nothing more to check")
		return 1
	}

	from := dayStart(now).AddDate(0, 0, -2)
	type rec struct {
		key, value, device string
		t                  int64
	}
	day := func(v string) (string, bool) {
		var o obj
		if json.Unmarshal([]byte(v), &o) != nil {
			return "", false
		}
		st := msTime(o["start"])
		if st.Before(from) {
			return "", false
		}
		end := "running"
		if e := msTime(o["end"]); !e.IsZero() {
			end = e.In(displayZone).Format("15:04")
		}
		what := str(o["note"])
		if what == "" {
			what = str(o["client"])
		}
		if len(what) > 40 {
			what = what[:40]
		}
		return fmt.Sprintf("%s %s-%s project=%s %q", st.In(displayZone).Format("2006-01-02 Mon"), st.In(displayZone).Format("15:04"), end, str(o["projectId"]), what), true
	}
	mine := map[string]rec{}
	soma.mu.Lock()
	for k, r := range soma.Recs {
		if f := somaField(k); (f == "timeEntries" || f == "shifts") && r.V != nil {
			mine[k] = rec{k, *r.V, r.D, r.T}
		}
	}
	soma.mu.Unlock()

	// The relay, from the start, decrypted here.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	relay := map[string]rec{}
	var top int64
	whole := ""
	for after := int64(0); ; {
		b, err := soma.call(ctx, "GET", fmt.Sprintf("/v1/pull?after=%d", after), nil)
		if err != nil {
			fmt.Printf("relay: %v\n", err)
			return 1
		}
		var res struct {
			Items []struct {
				Seq  int64  `json:"seq"`
				Blob string `json:"blob"`
			} `json:"items"`
			More bool `json:"more"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			fmt.Printf("relay: %v\n", err)
			return 1
		}
		for _, it := range res.Items {
			after, top = max(after, it.Seq), max(top, it.Seq)
			e, err := soma.keys.open(it.Blob)
			if err != nil {
				continue
			}
			if e.Key == "timeEntries" || e.Key == "shifts" {
				whole += " " + e.Key
			}
			if f := somaField(e.Key); (f == "timeEntries" || f == "shifts") && e.Value != nil {
				relay[e.Key] = rec{e.Key, *e.Value, e.Device, e.T}
			}
		}
		if !res.More || len(res.Items) == 0 {
			break
		}
	}
	fmt.Printf("relay: up to seq %d (tablet has pulled up to %d)\n", top, soma.Seq)
	if whole != "" {
		fmt.Printf("relay: these lists are ONE record each (Soma couldn't split them):%s\n", whole)
	}
	var lines []string
	keys := map[string]bool{}
	for k := range mine {
		keys[k] = true
	}
	for k := range relay {
		keys[k] = true
	}
	for k := range keys {
		m, inMine := mine[k]
		r, inRelay := relay[k]
		v := r.value
		if !inRelay {
			v = m.value
		}
		d, ok := day(v)
		if !ok {
			continue
		}
		state := "same on both"
		switch {
		case !inMine:
			state = "MISSING ON THE TABLET"
		case !inRelay:
			state = "only on the tablet"
		case m.value != r.value:
			state = fmt.Sprintf("DIFFERS (tablet t=%d from %s, relay t=%d from %s)", m.t, m.device, r.t, r.device)
		}
		kind := "entry"
		if strings.HasPrefix(k, "shifts") {
			kind = "shift"
		}
		dev := r.device
		if dev == "" {
			dev = m.device
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s [from device %s]", d, kind, state, dev))
	}
	sort.Strings(lines)
	fmt.Printf("since %s, %d entries and shifts:\n", from.Format("Mon 02 Jan"), len(lines))
	for _, l := range lines {
		fmt.Println("  " + l)
	}
	return 0
}
