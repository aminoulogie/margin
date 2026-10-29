# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo aminoulogie/margin-results.

## Sound 1: the sound chip and what the kernel says about it
```send
cat /proc/asound/cards
cat /proc/asound/pcm
dmesg | grep -i -E 'sst|snd|audio|cs42|asoc|codec|lpe|firmware' | tail -80
```

## Sound 2: the chip's switches, another way
```send
amixer -c 4 controls 2>&1 | head -150
echo "--- contents"
amixer -c 4 contents 2>&1 | head -60
echo "--- Android's sound tools"
ls /system/bin | grep -i -E 'alsa|tiny|mix|play|audio|pfw|param'
```

## Sound 3: Android's sound settings files
```send
ls /system/etc | grep -i -E 'audio|asound|mix|route|param|sound'
find /system/etc/parameter-framework -maxdepth 3 2>/dev/null | head -80
find /system/lib -maxdepth 1 -iname '*audio*' 2>/dev/null | head -40
```

## Sound 4: does the chime move at all (stops itself after 8 s)
```send
aplay -D plughw:4,0 /tmp/playbox-test.wav >/dev/null 2>&1 &
sleep 3
cat /proc/asound/card4/pcm0p/sub0/hw_params
cat /proc/asound/card4/pcm0p/sub0/status
sleep 2
cat /proc/asound/card4/pcm0p/sub0/status
kill $! 2>/dev/null
echo done
```

## Update Margin from GitHub
```
update
```
