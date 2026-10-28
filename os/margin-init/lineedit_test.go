package main

import "testing"

func TestEditLine(t *testing.T) {
	back := 0
	s, _ := editLine("", &back, []byte("helo"), 80)
	s, _ = editLine(s, &back, []byte("\x1b[D\x1b[D"), 80) // between "he" and "lo"
	s, _ = editLine(s, &back, []byte("l"), 80)
	if s != "hello" || withCursor(s, back) != "hel|lo" {
		t.Fatalf("%q %q", s, withCursor(s, back))
	}
	s, _ = editLine(s, &back, []byte("\x7f\x7f"), 80)
	if s != "hlo" {
		t.Fatalf("%q", s)
	}
	s, done := editLine(s, &back, []byte("\x1b[C\x1b[C\x1b[C!\r"), 80)
	if s != "hlo!" || !done {
		t.Fatalf("%q %v", s, done)
	}
}
