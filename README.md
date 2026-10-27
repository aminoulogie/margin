# Margin

Margin is our own Linux for a **Condor TRA-901G** tablet (Intel Atom Z2580 "Clover Trail+", 2 GB RAM,
8.9" 1920×1200, Android 4.2.2). It boots straight into Alpine Linux 3.24 and our Go
`margin-init`, with a console on the screen, touch, Wi-Fi and SSH, on top of Condor's own
signed kernel (which already has the drivers). Android stays installed underneath.

- How it works, and the daily commands: [`CLAUDE.md`](CLAUDE.md)
- What's done and what's next: [`PLAN.md`](PLAN.md)
- Everything learned about the hardware, with sources: [`docs/KNOWLEDGE.md`](docs/KNOWLEDGE.md)

## Layout

| Path | What |
|---|---|
| `cli/` | `margin.exe`, the Windows command-line tool that drives the tablet over USB (adb) |
| `os/margin-init/` | the program that runs on the tablet instead of Android's UI (Go, linux/386) |
| `os/margin-init/vt/` | the terminal engine behind the on-screen console |
| `os/margin-init/ui/` | text rendering with the Go fonts, and a simple launcher (fallback) |
| `dev.cmd`, `dev.ps1` | build `margin-init` and run it on the tablet in one step |
| `recon.sh` | the original read-only inspection script (bash); `margin recon` replaced it |
| `docs/` | knowledge base and the original handoff prompt |

## Build

```
cd cli
go build -o margin.exe .
.\margin.exe setup          # puts 'margin' on PATH; downloads adb on first use
cd ..
.\dev.cmd                   # builds os/margin-init for linux/386 and installs it on the tablet
```

## Commands

```
margin doctor                          check adb, cable, and authorization
margin info                            model, Android, kernel, battery, storage
margin term                            the tablet's console from the PC (Ctrl+] leaves)
margin ssh setup | margin ssh          log in over Wi-Fi (setup once, over USB)
margin net                             internet for the tablet over USB (leave it running)
margin alpine install|status|remove    Alpine Linux root on the tablet
margin alpine run <command>            run a command inside the tablet's Alpine
margin takeover status                 hook, autostart, margin-init, logs
margin takeover auto on|off            boot into Margin every time / back to Android
margin takeover arm|disarm             one-shot takeover for the next boot / cancel
margin takeover push <bin> | restart   install a new margin-init / restart it in place
margin takeover install-hook           update the startup hook in /system (one file)
margin bootimg info|unpack|pack        Intel OSIP boot images (files on the PC)
margin recon | backup                  read-only inspection / copy the tablet's files
margin shell | reboot | screenshot     the usual adb helpers
```

On the tablet (Alpine): `wifi scan | connect SSID [password] | status | off | forget | debug`,
and `apk` for everything else.
