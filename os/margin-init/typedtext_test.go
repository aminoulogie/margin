package main

import (
	"bytes"
	"testing"
)

// A key typed into a field draws only the text's strip, and the screen ends up the same as
// with the whole bar (or the composer) drawn again; deleting too.
func TestTypedTextSameAsWholeBar(t *testing.T) {
	c := workAt(t, "planner")
	check := func(what string) {
		t.Helper()
		got := bytes.Clone(c.s.buf)
		c.drawTypeBar()
		if !bytes.Equal(got, c.s.buf) {
			t.Fatalf("%s: the strip drawn differs from the whole bar", what)
		}
	}
	tapBtn(t, c, "wk:newtask") // the composer
	if !c.composing() {
		t.Fatal("no composer")
	}
	for _, k := range []string{"B", "u", "y", " ", "r", "e", "b", "a", "r", "\x7f", "\x7f", "\x1b[D", "x"} {
		c.somaKey([]byte(k))
		check("composer, after " + k)
	}
	if c.tbar.key == "" {
		t.Fatal("the composer wasn't kept: every key draws it whole")
	}
	c.somaKey([]byte("\x15")) // cleared: what it's for shows again
	check("composer, cleared")
	// A plain field: the bar.
	c.somaUI.field = "w.search"
	c.somaUI.prompt = ""
	for _, k := range []string{"r", "e", "b", "\x7f", "a"} {
		c.somaKey([]byte(k))
		check("bar, after " + k)
	}
	c.somaUI.typing, c.skb.visible = false, false // no page drawn after the pause, in the next test
}
