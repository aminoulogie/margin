# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo user445879785/margin-results.

## Soma: food and habits, tablet vs phone
```send
/proc/1/root/data/condor/condor-init soma-check food
```

## Playbox: how fast the picture plays
```send
grep -E "playbox: (picture|.*frames late|lighter)" /proc/1/root/data/condor/init.log | tail -15
echo "--- GPU drawing: $(ls /proc/1/root/data/condor/gpu 2>/dev/null && echo on || echo off)"
```

## Playbox: why Torrentio and Knightcrawler won't install
```send
UA="margin-books/1.0 (Condor TRA-901G tablet reader)"
for u in https://torrentio.strem.fun/manifest.json https://knightcrawler.elfhosted.com/manifest.json; do
  echo "--- $u, as Margin asks"
  curl -s -m 30 -A "$UA" -o /tmp/man.json -w "HTTP %{http_code} · %{time_total}s · type %{content_type}\n" $u
  head -c 200 /tmp/man.json; echo
  echo "--- same, following redirects"
  curl -s -L -m 30 -A "$UA" -o /tmp/man.json -w "HTTP %{http_code} · ended at %{url_effective}\n" $u
  head -c 200 /tmp/man.json; echo
done
echo "--- Margin's log"
grep -E "playbox: (installing|add-on)|proxy:" /proc/1/root/data/condor/init.log | tail -12
```

## Playbox: why video stops after the sound error
```send
L=/proc/1/root/data/condor/init.log
grep -E "playbox:|sound card|flush:" $L | tail -40
echo "--- programs still holding the sound (stat D = stuck in the driver)"
ps -o pid,stat,args | grep -E "aplay|ffmpeg|alsa" | grep -v grep
echo "--- sound chip"
cat /proc/asound/card4/pcm0p/sub0/status 2>&1
cat /proc/asound/card4/pcm0p/sub0/hw_params 2>&1
```

## Covers: kept Home rows and covers read ahead
```send
L=/proc/1/root/data/condor/init.log
grep -E "home: |store: cover" $L | tail -20
ls -la /proc/1/root/data/condor/home-lists.json
echo "covers on the tablet: $(ls /proc/1/root/data/condor/covers/s | wc -l) scaled, $(ls /proc/1/root/data/condor/covers | grep -c none) without a cover"
```

## Soma: add the Campus France plan
```send
/proc/1/root/data/condor/condor-init soma-plan "Campus France" 2026-11-30 <<'EOF'
2026-10-31 | Documents | Create / open Études en France account |
2026-11-01 | Documents | Scan every document |
2026-11-02 | Documents | Translations at the sworn translator | waiting:translator
2026-11-02 | Documents | Work certificate from the design office | waiting:employer
2026-11-03 | Documents | Shortlist 12-15 programmes |
2026-11-04 | TCF | Upload TCF convocation |
2026-11-09 | TCF | TCF exam | milestone
2026-11-12 | Platform | Enter education + experience |
2026-11-14 | Platform | Final programme choices |
2026-11-15 | Platform | CV + study project |
2026-11-16 | Platform | Explanatory letter for the 2025 gap |
2026-11-20 | Letters | Upload translations |
2026-11-23 | Letters | All motivation letters |
2026-11-26 | Submit | Full proofread |
2026-11-27 | Submit | Submit, pay, book appointment | milestone
           | After | Add TCF results |
2027-03-15 | After | Campus France interview (deadline) | milestone
EOF
```

## Time: yesterday's hours, tablet vs Soma's sync
```send
/proc/1/root/data/condor/condor-init soma-check
```

## Slow or frozen: what Margin wrote
```send
L=/proc/1/root/data/condor/init.log
grep -n -E "margin-init starting|panic|fatal error|out of memory|slow page|soma:|memory:|views:" $L | tail -60
echo "--- last lines"
tail -25 $L
echo "--- memory"
grep -E "MemTotal|MemFree|MemAvailable|^Cached" /proc/meminfo
P=$(pidof condor-init); [ -n "$P" ] && grep -E "VmRSS|VmPeak" /proc/$P/status
```

## Update Margin from GitHub
```
update
```
