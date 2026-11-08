# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo aminoulogie/margin-results.

## Playbox: why the add-ons don't answer
```send
echo "--- names (DNS)"
nslookup torrentio.strem.fun 2>&1 | tail -2
nslookup torrentio.strem.fun 1.1.1.1 2>&1 | tail -2
echo "--- Torrentio through Margin's proxy"
curl -s -m 15 -o /dev/null -w "HTTP %{http_code} · connected %{time_connect}s · %{time_total}s\n" https://torrentio.strem.fun/manifest.json
echo "--- Torrentio straight out (no proxy)"
curl --noproxy '*' -s -m 15 -o /dev/null -w "HTTP %{http_code} · connected %{time_connect}s\n" https://torrentio.strem.fun/manifest.json 2>&1
echo "--- Comet: Margin's name vs a browser's"
U=https://comet.elfhosted.com/stream/movie/tt0077651.json
curl -s -m 15 -A "margin-books/1.0 (Condor TRA-901G tablet reader)" -o /tmp/c1 -w "margin: HTTP %{http_code}\n" $U; head -c 160 /tmp/c1; echo
curl -s -m 15 -A "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36" -o /tmp/c2 -w "browser: HTTP %{http_code}\n" $U; head -c 160 /tmp/c2; echo
echo "--- MediaFusion"
curl -s -m 20 -o /tmp/m -w "HTTP %{http_code}\n" https://mediafusion.elfhosted.com/stream/movie/tt0077651.json; head -c 240 /tmp/m; echo
echo "--- other public torrent add-ons"
for u in https://thepiratebay-plus.strem.fun https://knightcrawler.elfhosted.com https://torrentsdb.com https://stremio-jackett.elfhosted.com; do curl -s -m 12 -o /dev/null -w "$u HTTP %{http_code} %{time_total}s\n" $u/manifest.json; done
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
