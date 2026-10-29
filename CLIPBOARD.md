# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain.

## Sound: play the chime on the chip's first output
```
aplay -D plughw:4,0 /tmp/playbox-test.wav
```

## Sound: list the chip's switches and volumes
```
amixer -c 4 scontrols
```

## Sound: what Margin logged about the sound card
```
grep "sound card" /proc/1/root/data/condor/init.log | tail -2
```

## Update Margin from GitHub
```
update
```
