# Margin: project memory

Turning an old **Condor TRA-901G** tablet into its own little Linux machine: it boots straight
into our userspace (Alpine Linux + our Go `margin-init`) on top of Condor's signed kernel, with
Wi-Fi, SSH and a console on its screen. Plan and what's next: `PLAN.md`. Everything learned,
with sources: `docs/KNOWLEDGE.md` (read it before suggesting anything about OS options,
firmware or flashing). **Start with "How the system works" below.**

## Names (2026-10-27: the OS is Margin)
The OS was called condor until 2026-10-27; it is **Margin** now (repo aminoulogie/margin;
the PC tool `margin.exe`, the tablet program `margin-init`, the GPU helpers `os/margin-gl`
and `marginsf`). "Condor" (capital C) is the tablet's maker and model: Condor TRA-901G,
Condor's kernel and boot logo. Keep those.

A few old names stay **on purpose**, because the tablet's /system startup hook depends on them
and rewriting the hook needs the PC over USB (`margin takeover install-hook`, safety rule 3):
- `/data/condor/` is still Margin's folder on the tablet (`marginHome`); the hook checks
  `/data/condor/autostart|takeover|bootfail` and execs **`/data/condor/condor-init`**, so
  margin-init is installed under that file name and runs as process `condor-init`
  (`initProc` in cli/takeover.go; `pidof condor-init` in update.sh / wifi-update.ps1).
- `takeoverHook` in cli/takeover.go is byte-identical to the hook on the tablet (md5
  9ed5cd11...): `margin takeover status` compares it, so never edit its text casually.
- Kept for a smooth move: the PC tool reads `~/.condor` when `~/.margin` doesn't have something
  yet (`homePath`), the scripts read the old `~/.condor\tablet-ip`, the wifi script copies
  `/etc/condor/wpa.conf` to `/etc/margin/` once, and margin-init deletes the old
  `/etc/profile.d/condor.sh`. Real folders keep their names too: the PC backup
  `C:\Users\pro\condor-backup` and the microSD's `condor/`.

## How the system works (2026-10-03)
```
power on → signed kernel + ramdisk (stock, can't change) → Android init
  → init runs /system/etc/install-recovery.sh as root (service flash_recovery, class main)
      = our hook (cli/takeover.go: takeoverHook). If /data/condor/autostart (every boot) or
        /data/condor/takeover (one-shot) exists: stop bootanim + zygote, exec margin-init.
        Crash-loop guard: /data/condor/bootfail counter; at 5 the hook boots Android and
        deletes autostart. margin-init clears it after 25 s (bootguard.go).
  → /data/condor/condor-init (os/margin-init, Go, linux/386, runs as root, logs to init.log)
      - fixes the clock (no RTC battery: floor = its own install mtime), hostname "margin"
      - screen: Linux console on fb0 (console.go + vt/), Go Mono, 73x57 cells, portrait
      - shell: Alpine login shell in a chroot of /data/alpine on a pty (pty_linux.go),
        with /proc /sys /dev /tmp(tmpfs) /system(bind) mounted (alpine_linux.go)
      - alpineBoot: writes Alpine config (prompt, proxy env, /usr/local/bin/wifi), runs
        `wifi boot` (rejoin saved network), starts sshd if openssh is installed
      - ports (all 127.0.0.1 except sshd):
          2323 console: `margin term` joins the screen's session (PC + tablet share it)
          2324 plain root shell (no pty), back door for tools
          2325 tunnel: `margin net` control/data connections (internet over USB)
          3128 HTTP proxy: via the PC when margin net runs, else direct over Wi-Fi
          22   sshd inside Alpine (keys only), reachable over Wi-Fi
  adbd keeps running the whole time, so the PC can always fix things over USB.
```
- On the tablet: `/data/condor/` (margin-init, autostart, bootfail, hook.log, init.log, shrc),
  `/data/alpine/` (Alpine 3.24 x86 root; `/etc/margin/wpa.conf` = saved Wi-Fi network, carried
  over from `/etc/condor/` by the wifi script).
- Without Alpine installed, the console falls back to Android's `/system/bin/sh`; if the
  fonts fail, to the `ui` launcher; if the console fails entirely, the touch test screen.

## Daily workflow (PowerShell on the PC)
```
.\dev.cmd           build margin-init for linux/386, push + restart (or arm + reboot)
wifi-update.cmd <ip>  same over Wi-Fi, no USB (IP remembered after the first time)
update  (in the tablet's Terminal tab)  the tablet fetches, builds and installs it by itself
update --local                   same, from /root/margin-src as it is (fixes made on the tablet)
Terminal > Claude tab            Claude fixes Margin on the tablet (API key asked once)
margin term                      the tablet's console from the PC (Ctrl+] leaves)
ssh root@<tablet ip> / margin ssh  same over Wi-Fi (keys set up by margin ssh setup)
margin alpine run <cmd>          run one command inside Alpine (exit code checked)
margin net                       internet over USB for the tablet (leave running)
margin takeover status           hook, autostart, margin-init md5, last log lines
margin takeover auto on|off      boot into Margin every time / back to Android
margin takeover install-hook     update the /system hook (one file, md5-checked, ro after)
margin reboot
```
Rebuild the CLI after pulling CLI changes: `cd cli; go build -o margin.exe .; .\margin.exe setup`
(close `margin term` windows first, the exe is locked while they run).
GitHub is often unreachable from this PC: pull with a retry loop, or use a git bundle.

## The machine you're running on
- Windows PC, PowerShell. The tablet is attached over **micro-USB**. A WSL Linux environment
  is available but USB devices aren't visible in WSL without `usbipd`, so use Windows tools.
- `margin` CLI (Go, `cli/main.go`) is on PATH after `margin setup`; it wraps adb/fastboot
  from `%USERPROFILE%\.margin\platform-tools` (auto-downloaded). Use `margin` commands
  first; call `adb`/`fastboot` directly from that folder when you need something it lacks,
  then consider adding it as a `margin` subcommand.
- Rebuild the CLI: `cd cli; go build -o margin.exe .; .\margin.exe setup`.

## Device facts (verified)
- Condor TRA-901G, made in Algeria, ~2014. Intel Atom **Z2580** (Clover Trail+, 32-bit x86
  Android build), PowerVR SGX544MP2, 2 GB RAM, 16 GB eMMC + microSD, 8.9" 1920×1200 IPS,
  micro-USB, DC 5V 2A. Ships Android **4.2.2**. Likely an OEM twin of the **Ramos i9**.
- Root: setuid `/system/bin/su` (mgyun, "16 com.mgyun.shua.su") + manager app
  `/data/app/Superuser.apk` (com.mgyun.superuser). Defender blocks the APK on the PC.
- Battery is old; it showed the "battery too low" screen and needed a long charge.
  It boots (Condor logo → Android). Screen is protected by a **pattern lock**.

## Recon findings (2026-10-03)
- Serial CLV13E2A6BD, build `Condor_TRA-901G_20151127`, platform `clovertrail`, ABI x86, API 17.
- Kernel **3.4.34** SMP PREEMPT (built 2015-11-27, gcc 4.6) → Go (needs ≥3.2) and Alpine OK.
- Root: `su` works over adb. `/proc/cmdline` and dmesg need su.
- Display: framebuffer `psbfb` (fb0, Intel MID PowerVR/psb driver), GPU module `sgx`.
  **Native scanout is 1920x1200 landscape** (mode `U:1920x1200p-0`, 32 bpp XRGB: r16 g8 b0,
  stride 7680, one page, fb rotate 0, panel mm unknown), but the tablet is used **portrait**:
  framebuffer x=0 is the top edge, its bottom rows are the left edge. margin-init draws in
  logical portrait 1200x1920 via one setting, `rotation` = Rot90 (`os/margin-init/screen.go`,
  override in /data/condor/rotation). Goodix touch reports native X 0–1920, Y 0–1200 (protocol
  B, slots 0–254), so touch maps through the same setting.
  Backlight: /sys/class/backlight/psb-bl, max 100.
- Touch: **Goodix Capacitive TouchScreen** (event2). Keys: `gpio-keys` (event1, volume),
  `mid_powerbtn` (event3). Accelerometer `bma250` (event0). Audio jack event4.
- Wi-Fi/BT: Broadcom **bcmdhd** (SDIO, mmc2, wlan0 + p2p0), `cfg80211`, `bcm_bt_lpm`.
  Modules live in `/lib/modules` (ramdisk); firmware in `/system/etc/firmware`, `/system/etc/wifi`.
- Cameras: ov5645 (rear), ov2675 (front) via atomisp.
- Storage: ~11 GB /data (internal storage shares /data). No `/dev/block/by-name`.
- Windows adb returns `\r\r\n` from this tablet; the CLI strips every `\r`.
- **Wi-Fi works in takeover mode (2026-10-03)**: `iw` scans fine, but Alpine's
  wpa_supplicant 2.11 fails on this kernel's nl80211 ("Could not allocate genl cache") and
  wext scans fail. Condor's `/system/bin/wpa_supplicant -Dnl80211` works from inside Alpine
  (/system is bind-mounted); it runs as uid 1010 (wifi), so its config + socket dir must be
  owned by 1010 and `wpa_cli` needs `umask 0`. busybox `udhcpc` gets the lease. All of this is
  the `wifi` command (`os/margin-init/wifi.go`); the saved network reconnects at boot.

## Boot chain findings (2026-10-03, verified)
- GPT on mmcblk0: reserved(p1, sector 40) panic factory misc config cache logs system data.
  Identical sector layout to the Ramos i9 `partition.tbl` (p4 is "spare" there).
- **No boot/recovery/fastboot partitions.** Sector 0 holds Intel's OSIP header; its 4 entries
  point into `reserved`: fastboot 4050 (20164 blocks, attr 0x0E), recovery 26050 (18530, 0x0C),
  splash 48050 (1642, 0x04), boot 70050 (15014, 0x00). Load 0x01100000, entry +0x1000.
- Image layout: 480-byte signature block (Intel manifest + RSA, records payload size) +
  4 KB header (cmdline, kernel size, ramdisk size) + 4 KB bootstub + bzImage + gzip ramdisk.
  Flash-tool files add a 512-byte single-entry OSIP sector in front. `margin bootimg` handles
  both; all tablet and Ramos images repack byte-identical.
- droidboot 0.5 (`product: clovertrail`): **`fastboot boot` is disabled** ("boot command
  stubbed on this platform"), `getvar all` returns nothing. Windows needs the Google driver's
  "Android Bootloader Interface" bound manually to USB\VID_8087&PID_09EF.
- Read raw eMMC without writing anything: `su -c 'hd -b <byte offset> -c <bytes> /dev/block/mmcblk0'`
  through `cmd /c ... > file` (PowerShell adds a BOM), decoded and checksum-verified on the PC.
- Dumps of boot/recovery/fastboot from this tablet: `C:\Users\pro\condor-backup\dumps\`.
- `/sdcard/update.zip` = full Ramos i9 Intel flash-tool package (PROD IFWI, DnX, flash.xml);
  its flash.xml **erases factory**. `/sdcard/safeupdate.zip` = DigiflipPro XT911 ROM that also
  writes IFWI: don't use.

## Decisions already made (don't re-litigate)
- Windows 8/10, Kindle OS, Android 5+: **impossible** on this chip (see KNOWLEDGE.md §2).
- Plan: keep Condor's kernel + drivers, replace everything above it. Current direction is
  **"terminal first, then write our own code from scratch"**: our own `/init`, our own
  framebuffer UI and EPUB reader in **Go** (`GOOS=linux GOARCH=386`), developed first on the
  PC with a fake-screen window, then on `/dev/fb0` + `/dev/input/event*` on the tablet.
- ~~Our Linux image goes in the recovery slot~~: impossible, the firmware rejects any
  modified boot/recovery image. Route in use: keep the signed kernel + ramdisk, hook early
  boot from **/system**, stop Android's zygote, run margin-init + Alpine (chroot).
- The user first wanted a **Linux-style system** (console, terminal-first); now (2026-10-03)
  the tablet is a Kindle-like **Books app that is the whole system**, dark by default, with
  Terminal and Settings as its tabs. Exception they asked for later (2026-10-03):
  the **Books app looks like Apple Books on iPad (iPadOS 18), light mode** — the user sent
  screenshots and said "exactly like this"; no orange (they disliked it): system blue
  #007aff, white pages, serif titles (Liberation Serif Bold for New York), Liberation Sans
  for SF. apple.go holds the palette, icons, tab bar, ring, sheets.
- Fallback ladder in PLAN.md if a phase is blocked.

## Current status
- [x] Phase 0: `margin` CLI built (doctor, info, books, install, apps, shell, screenshot, recon, reboot, setup, term)
- [x] Tablet charges and boots to Android
- [x] USB data works (needed reseating/another cable; first tries gave "Device Descriptor Request Failed")
- [x] adb already enabled + PC authorized, works through the pattern lock; **su root available**
- [x] First `margin recon` (2026-10-03): kernel, modules, firmware pulled. Partition dumps
      were skipped because /dev/block/by-name doesn't exist. Fixed in CLI; re-run recon.
- [x] `margin backup`: internal storage (1.55 GB) in `C:\Users\pro\condor-backup\sdcard`
      (Defender quarantined `rootauthor_8.1.1.3.apk`). microSD backup skipped by the user.
- [x] Boot/recovery/fastboot dumped (read-only) and partition table + OSIP mapped (see above).
      `margin recon` fixed to dump OSIP images, reserved, factory, eMMC boot areas, and to read
      root-only init*.rc/fstab via su; not re-run yet.
- [x] `margin bootimg info|unpack|pack`: byte-identical repacks verified
- [x] `fastboot boot`: not available on this droidboot
- [x] Signature test (2026-10-03): **signatures are enforced.** Recovery slot (sector 26050)
      overwritten via `dd` with a 1-byte-changed cmdline (same size): `reboot recovery` fell
      back to droidboot ("RESULT: OKAY", no error text). Control with the original image:
      stock recovery booted ("Aucune commande"). Original restored and verified; sector 0,
      boot and fastboot verified unchanged before and after. → No custom kernel/ramdisk.
- [x] No factory reset (root is too valuable). Pattern lock removed instead: gesture.key deleted,
      locksettings password_type/autolock set to 0; originals on microSD `condor/` + PC `condor-backup\lock`.
      Safety copy of Superuser.apk on microSD `condor/`.
- [x] /system backed up: `condor-backup\dumps\system.img` + microSD `condor/system.img`
      (md5 dd3e7835e36b8120435a62b099ed404a, equals the partition)
- [x] Takeover hook installed: `/system/etc/install-recovery.sh` (stock flash_recovery service
      runs it as root at class main). Verified harmless when not armed.
- [x] **Milestone 1** (2026-10-03): `margin takeover arm` + reboot → zygote/system_server
      stopped, `/data/condor/condor-init` (Go, linux/386) as root draws a test pattern on
      psbfb (colours correct, but drawn in native coordinates it appeared rotated 90°) and
      serves a root shell on 127.0.0.1:2323 (`adb forward`).
      adbd keeps running. Backlight: /sys/class/backlight/psb-bl (was 23).
- [x] Display fix: the panel scans out the read()/write() view of fb0; the mmap view is shifted
      299 rows and ends early. margin-init draws into a RAM back buffer and flushes dirty rows
      with write(); verified by read-back. Backlight set to 80/100 at start.
- [x] `margin takeover push <bin>` / `restart` (restart = touch trigger + `start flash_recovery`,
      so init launches it detached; `su ... &` doesn't survive).
- [x] **Milestone 2** (2026-10-03): touch works. Goodix event2, protocol B, raw X 0..1920 /
      Y 0..1200 in native fb orientation, mapped through the same Rot90 → strokes follow the
      finger. Follow-up: some touch-downs arrive with raw Y=0 in their first frame (logged as
      logical x=1199) → stray dot at the right edge; wait for both axes before drawing.
- [x] `margin term` (2026-10-03): bridges to margin-init's root shell (127.0.0.1:2323) via
      `adb forward`; requires takeover mode (margin-init running). Windows sends CRLF; term.go
      strips `\r` from stdin or every command arrives as "id\r" (": not found"). `cli/term.go`.
- [x] Clock: tablet boots at **2013** (no RTC battery), which breaks TLS. margin-init sets the
      clock forward to its own install time at start (timefix_linux.go); `margin takeover push`
      refreshes that. Manual alternative: `date -s` takes **local** time (fixed CET = UTC+1);
      `date -u +%s` is a toolbox bug (1 h off), verify with `date +%s`.
- [x] Text + launcher (`os/margin-init/ui`, Go fonts): status bar, app list. Kept as a fallback;
      the user preferred a Linux console. `go run ./cmd/ui-preview` renders it to PNG.
- [x] **Linux console on the screen** (console.go, vt/): pty shell, VT100/xterm subset, colours,
      Go Mono; `margin term` shares the session (raw mode on Windows, VT output, Ctrl+] leaves).
      Known: htop etc. render for 73x57, so a smaller PowerShell window scrolls; margin-init
      redraws glyphs from the font each time (CPU-heavy under htop; a glyph cache would fix it).
- [x] **Alpine 3.24 (x86)** in /data/alpine: `margin alpine install` downloads the minirootfs
      (sha256-checked), `margin-init untar` unpacks it (Android has no tar). Console = Alpine
      login shell. apk uses http mirrors through margin-init's proxy.
- [x] **Internet over USB**: `margin net` (no adb reverse on 4.2: reverse tunnel, see tunnel.go).
- [x] **Autostart**: `margin takeover auto on` → every boot goes into Margin, with the
      crash-loop fallback. Needed `install-hook` (the old one-shot hook ignored autostart).
- [x] **Wi-Fi**: `wifi scan|connect|status|off|forget|debug` (see Recon findings for why it uses
      Condor's wpa_supplicant). Rejoins at boot; give it ~30-40 s after boot. Proxy retries once.
- [x] **SSH over Wi-Fi**: `margin ssh setup` (openssh, PC's ed25519 key, sshd), sshd at boot.
      Don't run `setup-alpine` (it's for real installs).
- [x] **On-screen keyboard** (keyboard.go): bottom 616 px, console above it (73x38), terminal
      layout (esc tab ctrl alt arrows / numbers / letters / sym layer), one-shot shift/ctrl/alt,
      commits on finger lift, bksp/arrows repeat; `hide` gives the console the full screen
      (73x56), any tap brings it back; the pty is resized (SIGWINCH) both ways.
- [x] Daily-use basics (2026-10-03): glyph cache (glyphs.go), status bar with time, Wi-Fi
      address and battery (bar.go; battery = cw2015_battery, charger = smb347-mains/usb),
      power button toggles the screen (backlight 0 + FBIOBLANK powerdown; touches ignored
      while off), volume keys = brightness (power.go), NTP at boot after Wi-Fi (busybox ntpd),
      shell TZ=WAT-1.
- [x] **Launcher + settings** (pages.go, screens.go): boots to the launcher (terminal,
      settings, books "coming soon"); "≡ Margin" corner of the status bar = home from anywhere;
      the terminal keeps running in the background. Settings: brightness, auto screen-off
      (never/1/5/10 min, idleLoop), Wi-Fi status + reconnect, battery, system, power (restart,
      power off, android = autostart off + reboot; two taps; via `setprop sys.powerctl`).
      Saved in /data/condor/settings.json. Ready-made launchers don't fit: they need X/Wayland
      or a GPU, and fbdev mmap is shifted 299 rows on this panel.
- [x] **Books** (reader.go, epub/): EPUB parsing ported from Soma's reader
      (aminoulogie/kite-bay-otter-topaz src/lib/epub.ts: container.xml → OPF, percent-decoded
      paths, linear="no" skipped, dc: metadata, 3 cover conventions; Soma's resolvePath tests
      ported). Shelf scans /data/media/0/Books, microSD Books, /data/alpine/root/books,
      /data/condor/books. Paged reader: Soma's themes (night default, paper, sepia), A-/A+,
      line height 1.7, tap left third = back, else forward, volume keys turn pages; progress
      per book as (chapter, first word) in /data/condor/books.json. Soma itself can't run here
      (React web app; no browser engine on the framebuffer).
- [x] **Speed** (2026-10-03): pages took 45-55 ms on a PC (~0.5-1 s on the Atom); now ~9-10 ms.
      Rot90 blit in 8x8 tiles, word-sized, across 4 threads; ui.Fill copies rows; RoundRect
      anti-aliases corners only; ui.CachedFace glyph cache; one reused page canvas; shelf
      metadata cached. `go test -bench Page` (bench_test.go) to measure.
- [x] **Store** (store.go UI, storesrc.go sources, covers.go, opds.go): Kindle-style cover
      grid (3x2 per screen) and a book page (big cover, where to get it, summary). Browsing =
      Project Gutenberg's popular books by topic; a search asks 4 libraries at once and merges
      the same book (normKey: title up to ;:(, + surname; one library's separate editions stay
      apart; the preferred library's title/cover win): Project Gutenberg (gutendex.com, falling
      back after 25 s to gutenberg.org's OPDS feed, which then stays first), Google Books
      (public-domain EPUB downloads; for-sale books = price + description), Internet Archive
      (advancedsearch, EPUB found through /metadata/<id>), Open Library ("public" = IA item;
      lending-only = info; description fetched on demand). Full books first. Covers cached in
      /data/condor/covers, 3 loads at a time, 40 scaled in memory. Downloads try the no-images
      Gutenberg EPUB first; "read preview" = /data/condor/previews (newest 5), "download" =
      /data/media/0/Books. HTTPS via margin-init's proxy with Alpine's CA bundle. Gutenberg OPFs
      have `<meta property>text</meta>`: no xml.HTMLAutoClose in the epub package. Paid-book
      page previews (Google's viewer) need a browser: we show description + price instead.
- [x] **Reader = Soma's reader** (reader.go core/layout/cache, readerui.go gestures+overlays,
      marks.go, lookup.go, wordbook.go, rtext.go, shelf.go): themes night/paper/sepia with Soma's
      colours and mark inks; 5 fonts (Liberation Serif, Latin Modern, DejaVu Serif/Sans, Go,
      all with DejaVu Sans fallback for Arabic), size, line spacing 1.2-2.2, margins, justified
      text. Line by line (Soma's lineFocus): page dimmed at 0.8 except the lit line in the
      theme's mark colour; tap = next line, left sixth = previous, past the ends turns the
      page; long press = jump to that line + select the word; volume keys step lines; a step
      only redraws two strips (refreshLines, pixel-identical to a full redraw, test). Long
      press selects (hold 350 ms), drag extends; menu: highlight (5 Soma inks, overlapping marks
      merge, remove), look up (Wiktionary keyed by the book's dc:language, Datamuse fallback
      for English), translate (MyMemory, AR FR EN ES DE IT PT TR RU; Arabic shaped with
      presentation forms + right to left, no tashkeel), keep (word book with sentence; meaning
      filled in by a background lookup). Contents (chapter titles read in the background),
      highlights list (jump/delete), reading clock (gaps < 2 min count) + daily goal on the shelf,
      books finished per year. Words app: list, word page (type a meaning), review at 2/7/30
      days (Soma's review-queue). Arabic books lay out right to left. No copy (no clipboard here).
      Tests hold drawMu like the touch loop (key() takes it itself: pressKey helper).
- [x] **Apple Books look, from the user's screenshots** (apple.go, bookshome.go, shelf.go,
      store.go, reader.go, readerui.go, pageturn.go, wordbook.go): floating tab bar Home ·
      Library · Book Store · Words · search, reading-goal ring top right (launcher "books" opens
      Home). Home: Continue cards tinted with the cover's average colour, Top Picks
      (illustrated cards into store topics), Want to Read band, Popular Free Books (Gutenberg).
      Reader: Apple's 6 themes Original/Quiet/Paper/Bold(bold body face)/Calm/Focus + Night via
      the half-moon (prefs migrated: theme_set 2), book title on top, centred serif chapter
      titles with an ornament, "n of m" + "k pages left in chapter", rounded highlights, blue
      selection with handles, iPadOS vertical edit menu (Highlight · Look Up · Translate ·
      Keep Word · Remove Highlight; Highlight → 5 inks), controls bar like Books on Mac (Library;
      contents/highlights group; title; line-by-line/Aa/bookmark group) + slider, Themes &
      Settings popover (A/A, half moon, 6 tiles, Customize: font list, page turn Slide/Curl/
      None, spacing, margins, brightness, line by line, translate to, goal), bookmarks (red
      ribbon, Contents | Bookmarks | Highlights). Page turns time-boxed (curl 360 ms, slide 280
      ms; curl shows the page's back with mirrored print). Tests set PageTurn "none".
- [x] **Whole OS in iPadOS's look** (system.go, bar.go, keyboard.go, anim.go, fonts/): Inter
      (OFL, fonts/LICENSE-Inter.txt) is the system font — SF itself may not be used off Apple
      platforms; terminal in DejaVu Sans Mono (Menlo's family). Status bar: time + date left,
      Wi-Fi, battery % and battery icon right, coloured by the screen under it. Home screen:
      wallpaper, Calendar + Books/reading-goal widgets, icons (Books, Book Store, Words,
      Terminal, Settings), frosted dock. Settings: sidebar (device card, Wi-Fi, Battery,
      Display & Brightness, General/About, Restart & Shut Down) + inset grouped panes.
      Keyboard: iPad light (store, words) / dark (terminal) with ⇧ ⌫ return.
- [x] **Animations** (anim.go): composed in the framebuffer's native layout (sideways on the
      portrait screen = whole native rows, a memmove), never redrawn per frame; paced by
      FBIO_WAITFORVSYNC when the driver has it; time-boxed (spring ease-out). push/pop
      (apps, books, store pages, words), rise/fall (Look Up sheet, Contents), page slide/curl,
      the lit line gliding. Frame cost on the PC: push/slide ~1 ms, rise ~2 ms, curl ~3 ms.
      The panel's refresh is logged at start ("panel refresh: N Hz"); 120 Hz is impossible
      (panel ~60 Hz). Tests: animScale (0 = no frames); anim_test.go checks every transition
      ends on the exact new screen.
- [x] **Update over Wi-Fi** (wifi-update.cmd/.ps1): git pull, build, scp to Alpine /tmp, md5
      check, then a detached (setsid) busybox script on the tablet installs into
      /proc/1/root/data/condor (init's root: the real /data/condor seen from the chroot), keeps
      margin-init.bak, restarts via /system/bin/start flash_recovery (bind-mounted Android
      toolbox) and rolls back if no margin-init runs 15 s later. PC polls /tmp/margin-update.log.
      Tested in simulation (good, broken, damaged); not yet on the tablet.
- [x] Animations off by default (cfg.Animations, Settings > Display & Brightness > Motion):
      without a GPU every frame is CPU-pushed; instant changes feel fastest (e-reader style).
- [x] **GPU works without Android's UI** (2026-10-03, os/margin-gl/gltest): PowerVR SGX 544MP,
      OpenGL ES 2.0, swap 62 fps, full-screen texture upload 9.6 ms, full-screen slide 61.6 fps.
      Needs: Android's root + environment (ANDROID_PROPERTY_WORKSPACE from init; run via the
      2324 shell or from margin-init), SurfaceFlinger stopped, and `-mstackrealign` (bionic 4.2
      enters main with a 4-byte aligned stack; clang's movaps faulted). Crash reports:
      /data/tombstones + logcat (gl-run.sh prints both). A crashed GL program can leave the
      display grey/green: reboot (don't FBIOPAN by hand; that made it green).
- [x] **GPU animations: dead end on this panel** (2026-10-03). glanim (gpu.go, gpu_linux.go,
      os/margin-gl/glanim.c → glanim.bin) works and gpu_test.go matches its quads to the CPU
      frames, but on the tablet the screen goes black. gl-show proved it: the GPU renders
      correctly (glReadPixels matches Margin's screen) yet nothing it posts through
      FramebufferNativeWindow is shown (fb0 virtual 1920x1200, pan 0,0; power off/on doesn't
      restore Margin either; reboot does). Android displays via Intel's hardware composer,
      which only SurfaceFlinger drives. Readback + write() would cost what the CPU path costs.
      (Superseded 2026-10-06: the SurfaceFlinger route below works; glanim is no longer used.)
- [x] **Books is the whole system, dark by default** (2026-10-03, user: "boot to kindle directly
      with dark mode, no launcher; terminal and settings as tabs"). No home screen (launcher,
      wallpaper and app icons deleted). Margin starts on Books Home; tab bar: Home · Library ·
      Store · Words · Terminal · Settings · search. Settings' "‹ Books" and the left of the
      status bar ("‹ Books" in Terminal) go back. Dark Mode (Settings > Display & Brightness,
      or the reader's half moon; cfg "light", default dark) switches every screen
      (setPalette: iOS dark colours, apBand/apCard/apGrouped) and books (Night theme;
      ThemeSet 3 migration puts books in Night once).
- [x] **Update from the tablet itself** (2026-10-04): `update` in the Terminal tab (Alpine,
      /usr/local/bin/update = os/margin-init/update.sh, embedded and written at boot). It
      installs go + curl with apk (once), downloads the public margin repo's main branch as
      a codeload tarball into /root/margin-src (not git: Alpine's git needs getrandom(),
      ENOSYS on kernel 3.4), builds margin-init on the tablet (GOTMPDIR on flash,
      GOTOOLCHAIN=auto), then the same detached installer as wifi-update (backup, restart,
      rollback); log in /tmp/margin-update.log. **Verified on the tablet 2026-10-04 (DONE).** First time (before margin-init carries it):
      curl the raw update.sh (busybox wget https went through the proxy's own fetch, which had no CA certs before c930cc5). Dry-run tested on the PC (fetch, build, installer syntax).
- [x] **Easier terminal** (2026-10-04, termui.go, vt scrollback): 3000 lines of scrollback
      (vt keeps lines that scroll off a full-screen region; `clear`'s ESC[3J forgets them),
      swipe down/up to read back (pill "N lines back · tap to return"; output waits while
      scrolled back), hold 350 ms to select + drag, shortcuts bar over the keyboard (Copy ·
      Paste · update · clear · ls · cd .. · wifi), tap the typed line to put the cursor there,
      space bar = trackpad (slide), held keys repeat after 280 ms and speed up 45→15 ms.
      256-colour/truecolour map to the nearest of 16 (bat: BAT_THEME=ansi). Apple dark system colours for ANSI,
      coloured prompt and `ls --color`. Test: termui_test.go.
- [x] **Send to Books** (2026-10-04, sendbooks.go): http on the Wi-Fi address (:80, else :8080),
      local-network clients only; multipart upload of EPUBs into /data/media/0/Books under safe
      names (letters/marks/digits, " (2)" for duplicates), kept only if epub.Open works, max
      300 MB; Library + Settings > Wi-Fi show the address; toast "N books received".
- [x] **Lock screen + power save** (2026-10-04, lock.go): waking shows time, date and the book
      being read (cover, title, progress) on the cover's colour; tap opens Margin where it was
      (Settings > Display > Lock Screen switch, cfg no_lock_screen). Screen off: cpufreq
      governors → powersave and `iw dev wlan0 set power_save on`, restored on wake (only when
      /system/bin/linker exists, i.e. on the tablet). Not verified on the tablet yet.
- [x] **Offline dictionary** (2026-10-04, dict.go): every Look Up result is saved in
      /data/condor/dict/<lang>.jsonl (append-only; "none" entries for words with no page), and
      Look Up asks the saved words first (lookupSaved). Opening a book fetches its distinct words
      (lowercase, elisions stripped, from the current chapter on) in the background, one every
      300 ms; no internet = retry each minute. Customize: Offline Dictionary switch
      (prefs no_offline_dict) + status line. Tests: dict_test.go (testConsole isolates dictDir,
      no background fetch).
- [x] **Scrolling, Kindle-style Home, more Store categories** (2026-10-04, scroll.go,
      bookshome.go, shelf.go, store.go; user sent Kindle/Fable/Bookmory screenshots). Pages can
      be taller than the screen (page.header = fixed tab band tabsH; c.sc.y per mode): vertical
      drags only re-blit a window of the tall canvas (33 ms throttle, thin scroll bar); rows
      (page.addRow with a paint closure, buttons in page.rowBtns) move sideways by repainting
      just that row (70 ms throttle). Taps map through the scroll (pageY). Home: Continue, Top
      Picks, Want to Read (whole library, sideways), Popular (sideways), then 13 store rows by
      topic/language (homeShelves; Gutenberg, 2 loads at a time, debounced homeRedrawSoon).
      Paged by default (Kindle): a swipe jumps a screen / a row by its visible covers in one
      redraw (following the finger costs a 9 MB framebuffer write per step: laggy on the Atom);
      Settings > Display > "Follow Finger When Scrolling" (cfg smooth_scroll) for drag mode.
      Library: 48 books per tall page; Store: 24 per tall page, 26 topics in a sideways capsule
      row. Tests: scroll_test.go; tests switch off Home's background loads/redraws
      (homeLoads/backgroundRedraws) and get longer waits under -race (raceOn).
- [x] **GPU through SurfaceFlinger works** (2026-10-06, os/margin-gl/glsf.cpp): on this panel only
      SurfaceFlinger reaches the screen. glsf asks it for a full-screen layer (libgui's C++ API
      called by mangled name through dlsym: sp<T> returns use a callee-popped hidden pointer on
      i386; EGL needs the ANativeWindow found by its '_wnd' magic 8 bytes into the Surface).
      SurfaceFlinger is commented out of init.rc (Android runs it in system_server), so
      /system/bin/surfaceflinger is run directly. Verified on the tablet: 58-59 fps, Margin's
      screen, red, gradient all shown (gl-sf.cmd).
- [x] **Margin draws through the GPU** (2026-10-06, gpu.go, gpu_linux.go, gpuscroll.go,
      os/margin-gl/marginsf.cpp → marginsf.bin): Settings > Display & Brightness > "Draw With the
      GPU" (flag /data/condor/gpu, off by default). margin-init starts SurfaceFlinger + the
      marginsf helper, which keeps a layer; the screen buffer (s.buf) moves into shared memory
      (/dev/margin-gl.shm) and Screen.dev becomes gpuWriter: Flush = upload the changed rows +
      show. Tall pages (≤ 10000 rows) go to the GPU in 1024-row slices, only those on screen, at
      most 4 at a time (the whole Home page at once, ~37 MB, made touch unresponsive), and scroll as an
      overlay quad at 60 fps, always following the finger, with a fling (tau 0.45 s); sideways
      rows repaint + upload their strip; lifting settles (blitPage + overlayOff). Animations
      use the same helper (quads, as glanim). Safety: /data/condor/gpu.trying until 10 s after
      the layer is up (found at boot = GPU turned off). **Once SurfaceFlinger has had the panel,
      the framebuffer never shows again until a reboot** (verified 2026-10-06: killing it +
      FBIOBLANK powerdown/unblank left the screen black). So: a SurfaceFlinger already running
      (left by the Margin before an update) means the screen goes through the GPU for that
      session, whatever the setting; a helper failure or failed start while SurfaceFlinger runs
      → flag removed + reboot (gpuReboot). Turning it off restarts the tablet. Tests:
      gpuscroll_test.go (software model of the helper; overlay pixels == blitPage). **Not yet
      run on the tablet.**
- [x] **Responsiveness** (2026-10-06): (1) touch: the Goodix sometimes reports a touch's first
      frame with raw Y=0 (logical x 1199) — taps became swipes from the right edge and did
      nothing; touch.go now holds such a finger until it's placed (dropped if lifted first).
      (2) covers: memory held only 40, Home needs 100+, so covers were evicted and reloaded
      forever, each load redrawing all 7808 rows of Home (touches waited, GPU slices went
      stale). Now covers.go keeps scaled covers on disk too (covers/s/<hash>-<w>x<h>, raw
      RGBA, read synchronously: shown at once after the first time, even after a restart) and
      48 MB in memory, least recently drawn dropped. (3) a cover arriving repaints only the
      rows holding it (hrow.covers, paintArrived), uploading their strips to the GPU. (4) the
      visible page slices are sent to the GPU 300 ms after a page is drawn (prefetchPage), so
      the first swipe moves at once. "slow page" in init.log = a page draw over 150 ms.
      (5) sideways rows slide on the GPU too: on the first move a row is painted whole (every
      cover, offset 0) into a strip (≤ 12288 x 512, sent in 1024-wide columns: ES 2 can't
      upload part of a wider picture), then each move is only a quad (gpuStrip, kind 5); fling
      sideways; at settle the row is painted into the page at its offset. Row paint closures
      fill and draw the whole picture they're given (img.Rect), with a 40 px margin for
      shadows. (6) covers.loaded is set by showPage/drawLock for whatever is on screen
      (coverLoaded): Library, Words and the lock screen now redraw when covers arrive (before,
      only Home and Store did). (7) EPUB covers: also an image named *cover*, else the first
      non-SVG picture; WebP decodes (x/image/webp).
- [x] **Soma tab (beta)** (2026-10-06, somasync.go, somadata.go, soma.go): Soma (the user's PC/iOS
      app, aminoulogie/kite-bay-otter-topaz) on the tablet, synced through Soma's own E2E relay
      (src/lib/sync: crypto.ts, records.ts, engine.ts). Go port: recovery code (Crockford
      base32 + checksum) → secret; HKDF-SHA256 salt "soma-sync-v1": AES-GCM key "records",
      HMAC key "ids", vault "vault"(16), token "auth"(32); record id = b64url(HMAC(name)[:16]);
      blob = b64url(iv12 + AES-GCM(JSON {key,value,t,device})). Verified against vectors from
      Soma's own crypto.ts (somasync_test.go) both ways. Records kept in /data/condor/soma
      (state.json; link.json 0600); pull /v1/pull?after=seq, push dirty records /v1/push;
      newest t wins (tie: higher device); a local unsent edit isn't overwritten. Synced every
      2 min, on opening the tab, and after each edit. Edits written like Soma's store: habit
      tick (history[date], steps → stepLog; ramp habits: phone only), to-do toggle/add (newId,
      todos@order), reading +min. Views: Today (habits, reading, today/week to-dos), Calendar
      (month; day's plan blocks with times, to-dos, training, food), Reports (training 2
      weeks, nutrition 7 days, habits this week, reading). Link: type address + code in the
      tab, or soma-link.cmd on the PC (asks both, writes link.txt over ssh; picked up live),
      or (no PC: the user's usual case) from the phone: the Send to Books page has a "Link
      Soma" card (POST /soma-link, same-origin only, code never echoed) to paste both from
      Soma > Settings > Sync on the iPhone. Updating without a PC: `update` in the Terminal tab.
      Only habits/todos/dayPlans/reading/history/nutrition records are kept (somaFields;
      changing the list bumps somaFieldsVersion → re-pull from seq 0); state.json is written
      700 ms after a change, off the touch path (a full vault rewritten per tap could freeze).
      Calendar day = "Plan the day" timeline like Soma's (timeline.ts): plan blocks faint
      behind, the workout from history.timestamp − duration, slotted to-dos (overlaps in
      columns); tapping an empty half hour types a to-do there (somaAddTodoAt = addTodoAt +
      setTodoSlot: date = slot.date, scope day, cleanSlot to 15 min).
- [x] **Feel: scrolling, touch, motion** (2026-10-28, fastscroll.go, press.go, edgeback.go,
      wifiui.go): tall pages are turned once (lazily, by rows; blitPage's window is kept) into
      the framebuffer's native layout, so a scroll frame is one copy per native row (PC: 0.42 ms
      vs blitPage 2.8 ms; BenchmarkScrollFrame); sideways rows slide from a turned strip of the
      whole row. One frame loop draws the finger's latest position per vsync (moves skipped,
      never queued); fling (flingTau), rubber band past the ends + bounce, rows settle on a
      cover (hrow.snap), paged swipes glide. Frames flush with Screen.frame (no shadow compare).
      "scrolling: N a frame" in init.log every 120 frames. Motion (Animations + SmoothScroll)
      on by default, migrated once (cfg.Motion). Buttons show pressed 70 ms after touch-down
      (or at lift), dim if shaped, highlight if a row. Swipe in from the left edge = back
      (backID: s:back, w:back, wk:back, so:notes, Settings' home). Books open out of their
      cover (transition "open", CPU frame == GPU quads). Settings > Wi-Fi: scan list (wifi
      scan -m), tap to join, password on the keyboard (masked). Fields get a 5-row text
      keyboard (newTextKeyboard, follows dark/light); the Terminal keeps its own. Tests write
      settings to a temp file (settingsPath var). **Not yet verified on the tablet.**
- [x] **Playbox tab** (2026-10-28, playbox*.go, video*.go, subtitles.go): the user's iPhone app
      aminoulogie/playbox in Go. Stremio add-ons (Cinemeta, Torrentio, OpenSubtitles installed on
      first run; /data/condor/playbox/), Discover/search/film+series pages, sources sorted by
      seeders (Quick Play avoids 4K/HEVC), Library, downloads to /data/media/0/Movies. Torrents:
      anacrolix/torrent (pure Go, builds 386 without cgo; first `update` compiles ~70 extra
      modules, silent for a long while), streamed over a 127.0.0.1 media server (/t /r /f).
      Player: ffmpeg + ffprobe from Alpine (apk add on first play), picture and sound from two
      ffmpeg processes (one would deadlock), sound to aplay sets the clock, frames written in the
      panel's native landscape (Screen.frame flush).
- [x] **Browser tab** (2026-10-29, browser.go, browser_layout.go): Go HTML layout in Margin's
      look (no JS/CSS), DuckDuckGo html search, links, images, forms (GET/POST), history,
      favourites (/data/condor/browser.json), text size, long pages in 6500-row parts; videos and
      magnets open in Playbox's player, EPUBs saved to Books. **Not yet verified on the tablet.**
- [x] **Workstation fully usable** (2026-10-29, work_ui.go, work_edit.go, work_projects.go,
      work_planner.go, work_habits.go, work_training.go, work_tabs.go, work_reports.go): every button
      is "wk:<action>:<arg>" in wkActions; sheets (menus, steppers, segments, colours, a month to pick
      a date) over the page (wkSheets, a stack: one opened from another goes back to it); typed
      fields with a prompt (wkTypers, back to their sheet after); swipe zones (page.swipes); undo
      for deletes. Projects: list/board/timeline/archive, sort, client filter, search, every field
      editable, steps, time entries, notes, findings. Planner: week/day/month swipe, tap a time to
      add, task sheet, board, day routines (dayRoutines synced now), Plan my day. Habits page
      (habit-score.ts: weighted, 70% keeps the streak; only today changes, as on the phone).
      Training: Soma's imported lift sheet (training-seed.json, fetched from the Soma repo into
      /data/condor/soma, not kept in this repo) + logOverrides + history merged as training-log.ts
      does; movements with progression; sessions and sets editable (recomputeSession). Overview/
      Time/Money/Reports/Work log/Findings/Nutrition/Mind tabs all real; Settings > accent colour
      (settings.accent, shared with the phone). TestNoDeadButtons fails on any button with no
      handler. somaFieldsVersion 8 (dayRoutines, logOverrides, restDays).
- [x] **Claude tab in the Terminal** (2026-10-29, claude.go, claude_api.go, claude_tools.go,
      claude_github.go, termtabs.go): the Terminal has two tabs as pills under the tab bar, Shell and
      Claude, each a program on its own pty with its own vt screen (termSession; c.t/c.master/c.tu =
      the one on show; `margin term` stays on Shell). Claude = margin-init itself run as
      `margin-init claude` (/proc/self/exe, chrooted into Alpine like the shell; also
      /usr/local/bin/claude in Alpine for ssh), started the first time its tab opens. It looks like
      Claude Code (welcome box, ● tool calls with └ results, spinner, input between two rules) and
      streams the Messages API (key asked once, kept 0600 with the conversation in
      /root/.margin-claude; /model opus|sonnet|haiku, default claude-sonnet-5-5). Tools: bash, read_file,
      write_file, edit_file, publish_to_github. Fix flow: edit /root/margin-src, then `update --local`
      (update.sh: build what's there, no download) restarts Margin; update.sh leaves
      /data/condor/claude-reopen (MARGIN_FROM_CLAUDE set), Margin reopens the Claude tab, and the
      saved conversation resumes with the pending tool call answered "Margin restarted". Publishing:
      GitHub's git data API (blobs, tree, commit, ref), one commit of the files changed since
      download, found against /root/margin-src/.margin-manifest (md5 list written by update.sh, or at
      boot if missing); plain `update` refuses while there are unpublished changes (`--force`
      overrides). Tests: claude_test.go, termtabs_linux_test.go. **Not yet run on the tablet.**
- [x] **Workstation feel + Soma's training** (2026-10-29, work.go, work_drag.go, work_compose.go,
      work_library.go, scroll.go, fastscroll.go, ui/draw.go, ringset.go, exercises/): pages drawn are
      kept (wkPageCache, 5, keyed by workKey: somaUI + somaGen (counts every change to Soma's
      records) + a 5-minute clock + scroll), so going back to a page is a copy (PC: 4 ms vs 15-50 ms);
      the header haze is drawn once, card sheens are fills, corner coverage is a table per radius,
      rings skip pixels off every ring (calendar 53 → 32 ms on the PC). The tab bar and the rail
      answer on touch-down (instantTap, only in the fixed header and page.left). The rail is a fixed
      column (page.left: blitPage blits it unscrolled, scrollFrame leaves its columns alone, pageYAt
      and onScreen don't scroll it). The running timer (and the rest timer on Training) is a fixed
      band at the bottom (page.footer = wkTimerH) redrawn alone each second (startTimerTicks).
      Planner: hold a to-do (block or list row) to drag it, snapped to the quarter hour over a
      day's hours (page.drops: dropHours/dropDay), onto a day's heading = that day's list, let go
      unmoved = its sheet; week/day/month swipes follow the finger and slide in. New to-dos type
      into a composer card (day chips, time and length steppers, priority; chips don't end the
      typing; composerTap on touch-down). Habits show Soma's name→lucide icons (habitIcons, plus
      more). Training: Workout (Soma's splits to load, exercises from the library, each set's kg and
      reps ± in place, tick → rest timer with Soma's restForSet times, Finish), Library (Soma's
      BASE_EXERCISE_DB with its 56 photos embedded from public/exercises, muscle chips, search, S/A
      tiers), an exercise's page. Health: Log today (sleep ± and quality, naps, creatine, weight,
      measurements) as the phone's Body tab. Icons added to icons/lucide.png with the same Chromium
      method (lucide-static SVGs, 96 px). **Not yet verified on the tablet.**
- [ ] microSD bind into Alpine

Update this checklist as things are done.

## Safety rules (always)
1. **Never write to the tablet without explicit approval for that exact command**:
   `fastboot flash|erase|format|oem|update`, `dd of=/dev/block/...`, wipe/reset actions.
   Reading (`getvar`, `dd if=`, `pull`, `getprop`) is fine.
2. Before the first flash: we must have the **stock TRA-901G 4.2.2 firmware** + Intel
   flash tool downloaded and a recon backup (`margin-recon-*.zip`) saved off the tablet.
3. `fastboot boot` is unavailable here. Don't use `fastboot flash` (it rewrites the OSIP in
   sector 0). Never touch sector 0 or the boot/fastboot/recovery OSIP slots: the firmware
   enforces signatures, so changing them gains nothing. The only /system change is the hook
   file, written with `margin takeover install-hook` (md5-checked, /system back to ro).
   /system backup: `C:\Users\pro\condor-backup\dumps\system.img` (+ microSD `condor/`).
4. Explain risk in plain words before anything that could fail to boot.
5. The user is a beginner-to-intermediate: short steps, exact commands, PowerShell syntax.

## Conventions
- Go code in `cli/` (Windows tool) and `os/` (tablet userspace). `gofmt`, `go vet` before commit.
- The tablet is **32-bit (386)**: before pushing margin-init, `GOOS=linux GOARCH=386 go build` and
  `GOARCH=386 go test ./...` (a 1<<62 constant once broke the tablet's own `update` build).
- Work in github.com/aminoulogie/margin, branch `main` (the project moved there from the
  condor-linux folder of aminoulogie/ipakill, which is private now). No model names in commits.
