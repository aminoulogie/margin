# Margin: what the user picked next (2026-11-01)

From 20 menu questions on 2026-11-01. Soma items stay in `docs/SOMA-BACKLOG.md` (its order is
still valid; "Lift plan to a date" confirmed as the next Soma item). **Deliver in bigger
batches**: several items per merge, not one merge per item (the user, 2026-11-01).

## How things are now (the user's answers)
- Moving between tabs: "nice" (fast enough).
- Wi-Fi: reconnects fine after power-on.
- Draw With the GPU: on, smoother, keep it.
- **Playbox sound: broken.** A film says the sound error ("no sound: the output stopped taking
  sound (stuck); pick another in Playbox > Settings", video.go soundErr), then **no video plays
  again until a reboot**. Likely the aplay stuck in the driver (unkillable, holds the PCM) makes
  every later play wait on it. Clipboard: "Playbox: why video stops after the sound error"
  (run it right after it happens, before rebooting).
- **Battery drains with the screen off.**
- Claude tab: "can't sign in without an API key". Account sign-in exists (claude_code.go +
  claude_login.go: Terminal > Claude, pick 1 or /login, the link shows on the phone's Send to
  Books page, paste the code back there). Told the user; check it works for them.
- microSD: not used. Drop "microSD bind into Alpine".

## Batch 1 (fixes)
1. Playbox: after a stuck sound, later films must still play (silent if the chip is still stuck,
   with a clear message); never wait on a stuck aplay. Then find why the speaker stalls even
   with the Android route set (routeCloverview).
2. **Deep sleep when the screen is turned off** (user picked it over light sleep): Wi-Fi off,
   processor slowed right down, background work stopped (Soma sync, view prep, cover retries,
   dictionary and Home fetches); Wi-Fi rejoins and Soma syncs on wake. Look at Android's
   earlysuspend / `echo mem > /sys/power/state` with the power button as the wake source
   (test carefully: the tablet must always wake).
3. Lock screen: **just the clock**, minimal and dark (instead of the current book).
4. "What's new" after an update: a short list of what changed, shown once after `update`.

## Batch 2 (books)
5. Book Store: keep the first page of each topic opened, with its covers, so the Store shows
   at once on opening (like Home's rows, homekeep.go). Not every page, not searches.
6. Books with no cover: find one online by title and author (Open Library), keep it forever.
7. **Reading progress and highlights synced to the phone** through Soma's relay (Soma has a
   reader: match books, then progress + highlights). Needs Soma's repo: ask before touching it.

## Batch 3 (Soma + new app)
8. Soma: Lift plan to a date (SOMA-BACKLOG §1).
9. Habits and the evening check-in are what the user uses most: polish them first among
   Soma's pages (the user picked "the evening check" when asked about planner items).
10. **Notes app**: Markdown notes, synced with Soma's notes on the phone.
