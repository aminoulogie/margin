# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo aminoulogie/margin-results.

## Sound 11: what is powered while the speaker stalls
```send
F=/tmp/t440.wav
[ -f $F ] || /usr/bin/ffmpeg -loglevel error -y -f lavfi -i sine=frequency=440:duration=3 -ar 48000 -ac 2 $F
mount -t debugfs none /sys/kernel/debug 2>&1 | tail -1
D=/sys/kernel/debug/asoc
echo "--- debugfs"
ls $D 2>&1 | head -8
echo "--- Android's route domains"
f=/system/etc/parameter-framework/Settings/Audio/AudioConfigurableDomains.xml
grep -n "<ConfigurableDomain Name=" $f | cut -c1-110 | head -50
(timeout 7 /usr/bin/aplay -q -D plughw:4,0 $F >/dev/null 2>&1 &)
sleep 3
echo "--- chip while the tone is stalled"
cat /proc/asound/card4/pcm0p/sub0/hw_params
grep -E "state|hw_ptr|appl_ptr" /proc/asound/card4/pcm0p/sub0/status
echo "--- powered widgets"
cd $D 2>/dev/null && grep -r -H "" . 2>/dev/null | grep "/dapm/" | grep -E ": On|: Off" | sed 's#^\./##' | cut -c1-110 | head -170
sleep 5
```

## Sound 10: Android's aplay against Alpine's, same tone
```send
A=/system/bin/alsa_aplay
F=/tmp/t440.wav
/usr/bin/ffmpeg -loglevel error -y -f lavfi -i sine=frequency=440:duration=3 -ar 48000 -ac 2 $F
ls -l $F
echo "--- Android's aplay, 3 s tone"
( sleep 1; echo "[1 s]"; grep -E "state|hw_ptr|appl_ptr" /proc/asound/card4/pcm0p/sub0/status; sleep 1; echo "[2 s]"; grep -E "state|hw_ptr|appl_ptr" /proc/asound/card4/pcm0p/sub0/status ) &
timeout 8 $A -D plughw:4,0 $F >/tmp/a1.log 2>&1
echo "android aplay exit $? (0 = played, 124 = hung)"
tail -3 /tmp/a1.log
wait
echo "--- Alpine's aplay, same tone"
( sleep 1; echo "[1 s]"; grep -E "state|hw_ptr|appl_ptr" /proc/asound/card4/pcm0p/sub0/status; sleep 1; echo "[2 s]"; grep -E "state|hw_ptr|appl_ptr" /proc/asound/card4/pcm0p/sub0/status ) &
timeout 8 aplay -D plughw:4,0 $F >/tmp/a2.log 2>&1
echo "alpine aplay exit $? (0 = played, 124 = hung)"
tail -3 /tmp/a2.log
wait
echo "--- chip after"
cat /proc/asound/card4/pcm0p/sub0/status
dmesg | tail -8
```

## Sound 9: why the speaker stops taking sound
```send
L=/proc/1/root/data/condor/init.log
echo "--- Margin's sound log"
grep -E "playbox:.*(sound|route|stopped|aplay|mixer|chime)|aplay" $L | tail -30
echo "--- sound programs (stat D = stuck in the driver)"
ps -o pid,stat,args | grep -E "aplay|ffmpeg" | grep -v grep
echo "--- chip state, twice, 2 s apart"
cat /proc/asound/card4/pcm0p/sub0/status 2>&1
sleep 2
cat /proc/asound/card4/pcm0p/sub0/status 2>&1
echo "--- route now"
A=/system/bin/alsa_amixer
for n in 'SPK Amp Switch' 'SPKLO Amp Switch' 'HP Amp Switch' 'LPE IHF mixer' 'ESL-ASP Mux' 'SPK-ASP Mux' 'IHFAMP_SD_N Switch'; do $A -c 4 cget name="$n" 2>&1 | grep -E "name=|: values="; done
echo "--- kernel messages about sound"
dmesg 2>&1 | grep -i -E "sst|lpe|cs42|snd|underrun|xrun|audio" | tail -25
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

## Sound 8: switch the speaker on the way Android does, then play the chime
```send
A=/system/bin/alsa_amixer
$A -q -c 4 cset name='SPK Amp Switch' on
$A -q -c 4 cset name='SPKLO Amp Switch' on
$A -q -c 4 cset name='HP Amp Switch' off
$A -q -c 4 cset name='EAR Amp Switch' off
$A -q -c 4 cset name='LPE IHF mixer' 3
$A -q -c 4 cset name='ESL-ASP Mux' 1
$A -q -c 4 cset name='SPK-ASP Mux' 0
$A -q -c 4 cset name='IHFAMP_SD_N Switch' on
echo "speaker route set; playing the chime"
timeout 8 aplay -D plughw:4,0 /tmp/playbox-test.wav
echo "aplay exit $?"
cat /proc/asound/card4/pcm0p/sub0/status
```

## Update Margin from GitHub
```
update
```
