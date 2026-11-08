---
name: margin
description: How to work on Margin (the Condor TRA-901G tablet OS in this repo) the way the user wants it: safety rules, the tablet workflow (CLIPBOARD.md, margin-results), building for the 32-bit tablet, merging to main, and the speed and feel rules every screen must follow. Read it before any change to this repo, every session, every model.
---

# Margin: how to work here

This is the user's standing instructions, gathered in one place (2026-11-08, the user: "put all
what I taught you to do, the instructions, the speed, everything"). CLAUDE.md holds the facts
and the history (what's built, what was found on the tablet); this file holds **how** to work.
When the two disagree, the newer dated rule wins; update both.

## 1. Before anything
- Read `CLAUDE.md` (the "How the system works" diagram first), then the part of
  `docs/KNOWLEDGE.md` your task touches before suggesting anything about OS options, firmware
  or flashing. What's next: `docs/MARGIN-BACKLOG.md`, Soma's: `docs/SOMA-BACKLOG.md`.
- Don't re-litigate decisions listed in CLAUDE.md ("Decisions already made").
- The user is a beginner-to-intermediate: short steps, exact commands, plain words.

## 2. Safety (always)
1. Never write to the tablet's flash without explicit approval for that exact command
   (`fastboot flash|erase|format|oem|update`, `dd of=/dev/block/...`, wipes, resets).
   Reading is fine.
2. Never touch sector 0 or the OSIP boot/recovery/fastboot slots: signatures are enforced.
   The only /system change is the hook, via `margin takeover install-hook`.
3. `takeoverHook` in cli/takeover.go is byte-identical to the tablet's hook: never edit casually.
   `/data/condor/` and the process name `condor-init` stay (the hook depends on them).
4. Explain the risk in plain words before anything that could stop the tablet booting.

## 3. Commands for the user go in CLIPBOARD.md
The user can't type long commands or paste on the tablet.
- Whenever you ask the user to run something on the tablet, add it to `CLIPBOARD.md` (newest
  first, `## short plain title`, then a fenced block) and push it to `main` **before** answering.
  Remove commands that are no longer useful.
- Tell the user: Terminal > Clipboard, tap the command by its title, press return.
- Need the output? Open the fence with ```` ```send ````: the tablet uploads the result to the
  private repo `user445879785/margin-results` (`results/<date>/<time>-<title>.md`). When the
  user says they ran it, attach that repo (read) and read the newest files; don't ask for photos.
- Every fence needs a title line the tablet sees (TestClipboard checks it).

## 4. Building, testing, merging
- Go code: `cli/` (Windows tool), `os/margin-init` (the tablet). `gofmt`, `go vet` before commit.
- **The tablet is 32-bit**: `GOOS=linux GOARCH=386 go build ./...` and `GOARCH=386 go test ./...`
  in `os/margin-init` before pushing (a 1<<62 constant once broke the tablet's own `update`).
- No Go in a cloud session: see CLAUDE.md "Building in a cloud session without Go".
- **Merge your own work into `main` yourself** once the tests pass, then tell the user it's
  merged and that `update` in the Terminal tab installs it. Don't ask them to merge.
  Bigger batches: several items per merge. Small doc/Clipboard changes can go in at once.
- No model names in commits, comments or docs.
- The repos live under `user445879785/` now (margin, margin-results).
- **Keep CLAUDE.md current** in the same session: a status bullet per thing done, findings from
  margin-results with their date, replaced findings updated. Don't wait to be asked.

## 5. Speed and feel: the rules every screen follows
The tablet is an Intel Atom Z2580 (2 cores x 2 threads), 1200x1920 portrait on a **landscape**
1920x1200 framebuffer (Rot90): a logical column is a native row. Everything the user called
slow came from breaking one of these rules. Measure on the PC (`go test -bench`, the
`MARGIN_SPEED=1` tests); the Atom is roughly 10-15x slower.

1. **Never redraw what didn't change.** The terminal is the model the user holds up ("the
   terminal is instant"): it draws only the cells that changed (console.go render). A key typed
   in a field draws only the text's strip of the bar above the keyboard, from a kept picture of
   the bar without its text (typebar.go, `typeBarCache`); the composer the same
   (`composerTitle`). Never redraw a whole page for a key.
2. **Anything that follows the finger runs in a frame loop, never per touch event.** Moves only
   record where the finger is; one goroutine draws the latest position once per vertical blank
   (`waitVsync`), skipping the moves in between. Drawing each move in turn makes the picture
   fall further and further behind the finger. Examples: pages (fastscroll.go `frameLoop`),
   the terminal's scrollback (termscroll.go `termKick`), a dragged to-do (work_drag.go
   `dragDraw`). For anything else that follows a finger, use `c.onFrame(&job, draw)`
   (framejob.go): it keeps only the latest move; `job.flush()` on lift, `job.drop()` to cancel.
3. **Moving pictures are copies, not drawings.** Turn a picture into the framebuffer's layout
   once (turnScreen / turned lines) and make each frame one `copy` per native row. Text and
   glyphs are drawn once and kept (glyph cache, kept terminal lines, kept keyboard picture).
   Flush frames with `s.frame = true` (no shadow compare).
4. **Follow the finger pixel by pixel**, with iOS physics: fling (`flingMotion`), rubber band
   and bounce at the ends, rest on a line/cover (`easeTo`). No jump when the slop is passed
   (move from where the slop ended). A finger on moving content catches it; that lift isn't a
   tap.
5. **Background drawing gives way to the finger.** A redraw nobody is waiting for (a page after
   a pause in typing, a kept view brought up to date) runs inside `yielding(...)`; touches and
   held-key repeats mark themselves with `touchWaits()` so it stops at the next `drawCheck`.
   Restore `c.page`/`c.tabsOn` when it gave way, and try again when idle.
6. **Fixed parts stay fixed**: the tab band (`page.header`), the bottom band (`page.footer`) and
   the workstation's rail (`page.left`) never move when a page scrolls, on the CPU path *and*
   the GPU path (the GPU page quad starts at `page.left`). The user has "Draw With the GPU" on:
   test both paths.
7. **Feedback at once**: buttons show pressed 70 ms after touch-down (press.go); a long press
   **lifts** what's under the finger (grows ~6% with a shadow, lift.go `liftAt`) before its
   menu, sheet or drag; tabs and the rail answer on touch-down (`instantTap`).
8. **Memory**: 2 GB, 32-bit, `SetMemoryLimit(400 MB)`. Keep caches bounded (views 112 MB,
   kept terminal lines a few screens, covers 48 MB); reuse big buffers instead of allocating
   per frame.
9. **Never take drawMu in a callback that may run under drawMu** (it froze Margin for good once:
   addons remove). Hand it to a goroutine.
10. **The panel shows ~60 frames a second at most** (vsync-paced): asked for "160 fps", say so
    plainly and make every frame cost a few ms so all 60 are hit. Content that scrolls without
    end (the Planner's week, work_calweek.go) is kept as turned strips per item (day, line) and
    composed per frame; cache them bounded and prefetch neighbours when idle.
11. Motion (animations + smooth scroll) is on by default; every animation is time-boxed
    (`c.frames`) and tests run with `animScale = 0`.

## 6. Look
- Apple's iPadOS look: Inter as the system font, the terminal in DejaVu Sans Mono, dark by
  default, system blue #007aff (light) / #0a84ff (dark), no orange. Books looks like Apple Books
  on iPad (light mode there when asked); the workstation (Soma tab) uses its own wk colours and
  the accent from Soma's settings.
- Kindle-style Home and paged reading; e-reader calm: instant changes beat slow animations.

## 7. Where to look
- Screen and drawing: screen.go (Screen, blitRGBA, Flush), anim.go, fastscroll.go, scroll.go,
  gpuscroll.go, press.go, lift.go.
- Typing: keyboard.go (keys, repeat), typebar.go (bar above the keyboard), lineedit.go,
  work_compose.go (new to-do composer).
- Terminal: console.go (render), termui.go (touch, selection, shortcuts), termscroll.go
  (smooth scrollback), vt/ (the terminal emulator), clipboard.go.
- Soma/workstation: work*.go, soma*.go; planner drag: work_drag.go.
- Tests: `testConsole(t)` (power_test.go), `workAt(t, page)`, `shot(t, c, name)` (PNG with
  MARGIN_SHOTS=<dir>).
