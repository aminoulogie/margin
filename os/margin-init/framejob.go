package main

import "time"

// frameJob draws only the latest of a run of finger moves, once per vertical blank: a move
// replaces the drawing still waiting, so a slow drawing never falls behind the finger (each
// move drawn in turn queued them up: the picture lagged further and further). For what
// follows a finger without a turned picture to copy (a selection, a swipe across a card).
type frameJob struct {
	next    func()
	looping bool
}

// onFrame has draw done at the next frame, in place of one not drawn yet. Caller holds drawMu.
func (c *console) onFrame(j *frameJob, draw func()) {
	j.next = draw
	if j.looping {
		return
	}
	j.looping = true
	go func() {
		for {
			drawMu.Lock()
			f := j.next
			j.next = nil
			if f == nil || !c.screenOn {
				j.looping, j.next = false, nil
				drawMu.Unlock()
				return
			}
			f()
			drawMu.Unlock()
			if !waitVsync(c.s) {
				time.Sleep(4 * time.Millisecond) // touches get in between frames
			}
		}
	}()
}

// flush draws what's waiting now (the finger lifted). Caller holds drawMu.
func (j *frameJob) flush() {
	if f := j.next; f != nil {
		j.next = nil
		f()
	}
}

// drop forgets what's waiting. Caller holds drawMu.
func (j *frameJob) drop() { j.next = nil }
