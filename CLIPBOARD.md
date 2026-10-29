# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo aminoulogie/margin-results.

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
