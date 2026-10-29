# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo aminoulogie/margin-results.

## Sound: play the chime on the chip's first output
```send
aplay -D plughw:4,0 /tmp/playbox-test.wav
```

## Sound: list the chip's switches and volumes
```send
amixer -c 4 scontrols
```

## Sound: what Margin logged about the sound card
```send
grep "sound card" /proc/1/root/data/condor/init.log | tail -2
```

## Update Margin from GitHub
```
update
```
