# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## Soma: add the Campus France plan
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

## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo aminoulogie/margin-results.

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
