package main

import (
	"bytes"
	"fmt"
	"testing"
)

// The scrollback drawn from the kept, turned lines is the same picture as drawn glyph by
// glyph, and frames between lines (the text following the finger) draw without trouble.
func TestTermFrameSameAsDrawn(t *testing.T) {
	c := testConsole(t)
	c.mode = modeTerminal
	for i := 0; i < 200; i++ {
		c.t.Write([]byte(fmt.Sprintf("\x1b[3%dmline %d \x1b[1mbold\x1b[0m %s\r\n", i%8, i, "xyz")))
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	c.redrawAll()
	for _, back := range []int{1, 7, 60, c.t.History()} {
		c.tu.back = back
		c.tu.selOn, c.tu.selA, c.tu.selB = true, termPos{-9, 0}, termPos{-9, 0} // nothing selected: the glyph path
		c.renderView()
		want := bytes.Clone(c.s.buf)
		c.tu.selOn = false
		c.s.Fill(c.offX, c.offY, c.offX+c.t.Cols*c.cw, c.offY+c.t.Rows*c.ch, 0x55, 0x22, 0x99) // the text, wiped
		c.renderView()                                                                         // from the kept lines
		for x := c.offX; x < c.offX+c.t.Cols*c.cw; x++ {                                       // the text (the Copy button differs: nothing to copy)
			o := (c.s.fbH-1-x)*c.s.stride + 4*c.offY
			if !bytes.Equal(c.s.buf[o:o+4*c.t.Rows*c.ch], want[o:o+4*c.t.Rows*c.ch]) {
				t.Fatalf("%d lines back: the kept lines' picture differs from the drawn one at column %d", back, x)
			}
		}
	}
	// Frames between whole lines, then output while scrolled back (screen lines drawn again).
	for off := 0; off < 3*c.ch; off += 5 {
		c.termFrame(10*c.ch+off, true)
	}
	c.t.Write([]byte("more\r\n"))
	c.termFrame(2*c.ch, true)
	// A finger: down, moves, up: rests on a whole line.
	x, y := c.s.W/2, c.offY+5*c.ch
	c.tu.back = 0
	c.goLive()
	c.termTouch(TouchPoint{Down: true, X: x, Y: y})
	for d := 30; d < 300; d += 17 {
		c.termTouch(TouchPoint{Moved: true, X: x, Y: y + d})
	}
	c.termTouch(TouchPoint{Up: true, X: x, Y: y + 300})
	if c.ts.on || c.tu.back != int(float64(300-24)/float64(c.ch)+0.5) {
		t.Fatalf("after the lift: moving %v, %d lines back", c.ts.on, c.tu.back)
	}
}

func BenchmarkTermScrollFrame(b *testing.B) {
	c := testConsole(&testing.T{})
	c.mode = modeTerminal
	for i := 0; i < 500; i++ {
		c.t.Write([]byte(fmt.Sprintf("line %d of the scrollback, with some text on it\r\n", i)))
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	c.redrawAll()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.termFrame(100*c.ch+i%(5*c.ch), true)
	}
}

// The old way, a scrolled-back view drawn glyph by glyph (renderView without kept lines).
func BenchmarkTermScrollDrawn(b *testing.B) {
	c := testConsole(&testing.T{})
	c.mode = modeTerminal
	for i := 0; i < 500; i++ {
		c.t.Write([]byte(fmt.Sprintf("line %d of the scrollback, with some text on it\r\n", i)))
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	c.redrawAll()
	c.tu.selOn, c.tu.selA, c.tu.selB = true, termPos{-9, 0}, termPos{-9, 0}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.tu.back = 100 + i%5
		c.renderView()
		c.s.Flush()
	}
}
