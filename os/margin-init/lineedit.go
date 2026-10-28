package main

import "strings"

// editLine applies the keyboard's output to a one-line field with a cursor, as the terminal
// would: typing inserts at the cursor, ⌫ deletes before it, ← → move it (the space bar's
// trackpad sends those), ctrl+u clears. back is the cursor's place, counted in characters
// from the end, so 0 is the end. Returns the text and whether return was pressed.
func editLine(text string, back *int, b []byte, limit int) (string, bool) {
	r := []rune(text)
	*back = min(max(*back, 0), len(r))
	s := string(b)
	for s != "" {
		switch {
		case strings.HasPrefix(s, "\x1b[D"):
			*back = min(*back+1, len(r))
			s = s[3:]
			continue
		case strings.HasPrefix(s, "\x1b[C"):
			*back = max(*back-1, 0)
			s = s[3:]
			continue
		case s[0] == 0x1b: // other escapes (up, down): nothing in one line
			s = strings.TrimLeft(s[1:], "[0123456789;ABCDHF~")
			continue
		}
		ch := []rune(s)[0]
		s = s[len(string(ch)):]
		at := len(r) - *back
		switch {
		case ch == '\r' || ch == '\n':
			return string(r), true
		case ch == 0x7f || ch == 0x08:
			if at > 0 {
				r = append(r[:at-1], r[at:]...)
			}
		case ch == 0x15:
			r, *back = nil, 0
		case ch >= ' ' && len(r) < limit:
			r = append(r[:at], append([]rune{ch}, r[at:]...)...)
		}
	}
	return string(r), false
}

// withCursor is text with a bar where the cursor is.
func withCursor(text string, back int) string {
	r := []rune(text)
	at := len(r) - min(max(back, 0), len(r))
	return string(r[:at]) + "|" + string(r[at:])
}
