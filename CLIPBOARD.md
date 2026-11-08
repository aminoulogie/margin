# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo aminoulogie/margin-results.

## Sound 16: is Android's media server holding the speaker
```send
F=/tmp/t440.wav
[ -f $F ] || /usr/bin/ffmpeg -loglevel error -y -f lavfi -i sine=frequency=440:duration=3 -ar 48000 -ac 2 $F
A=/system/bin/alsa_amixer
R=/proc/1/root
I=$(pidof condor-init | cut -d' ' -f1)
W=$(tr '\0' '\n' </proc/$I/environ | grep ANDROID_PROPERTY_WORKSPACE)
prop(){ env $W chroot $R /system/bin/$1 $2 $3; }
holders(){ for p in /proc/[0-9]*; do l=$(ls -l $p/fd 2>/dev/null | grep -E "/dev/(snd|intel_sst)" | sed 's/.*-> //' | tr '\n' ' '); [ -n "$l" ] && echo "  $(basename $p) $(tr '\0' ' ' <$p/cmdline | cut -c1-50): $l"; done; }
tone(){ (sleep 1; grep -E "state|hw_ptr" /proc/asound/card4/pcm0p/sub0/status | tr '\n' ' '; echo; sleep 2; grep hw_ptr /proc/asound/card4/pcm0p/sub0/status) & timeout 7 /system/bin/alsa_aplay -q -D plughw:4,0 $F >/dev/null 2>&1; echo "  aplay exit $? (0 = played the whole tone)"; wait; }
echo "--- Android services"
prop getprop | grep -E "init.svc.(media|audio|drm|zygote)"
echo "--- who has the sound devices open"
holders
echo "--- every sound stream on the chip"
for f in /proc/asound/card4/pcm*/sub*/status; do echo "$f: $(head -1 $f)"; done
echo "=== A: speaker mixer on PCM only (1), media server running"
$A -q -c 4 cset name='LPE IHF mixer' 1
tone
echo "=== B: media server stopped, mixer Mixed (3)"
prop setprop ctl.stop media
sleep 2
M=$(pidof mediaserver)
[ -n "$M" ] && { echo "  still running ($M): killing it"; kill $M; sleep 1; }
echo "  media: $(prop getprop init.svc.media)"
holders
$A -q -c 4 cset name='LPE IHF mixer' 3
tone
echo "=== C: media server stopped, mixer PCM only (1)"
$A -q -c 4 cset name='LPE IHF mixer' 1
tone
$A -q -c 4 cset name='LPE IHF mixer' 3
echo "--- sound kernel messages"
dmesg | grep -i -E "sst|lpe|clk0|OSC_CLK|cs42|asoc" | tail -15
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
