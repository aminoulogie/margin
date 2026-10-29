# Clipboard

Commands to type on the tablet. Open **Terminal > Clipboard** (over the keyboard), tap one,
check it, then press return. Margin fetches this file from GitHub each time the Clipboard opens.

For whoever adds commands (people and AI models, see CLAUDE.md): newest first; each one is a
`## title` line followed by a fenced block. Keep titles short and plain. Open the fence with
` ```send ` instead of ` ``` ` when you need to see the output: the tablet then runs it through
`cbrun`, which sends the output to the private repo aminoulogie/margin-results.

## Sound 5: the chip's switches with Android's own tool
```send
/system/bin/alsa_amixer -c 4 contents 2>&1
echo "--- asound.conf"
cat /system/etc/asound.conf
```

## Sound 6: how Android names the speaker routes
```send
cd /system/etc/parameter-framework/Structure/Audio
wc -c *.xml
echo "--- CirrusLogic"
cat CirrusLogicSubsystem.xml
echo "--- LPEMixer"
cat LPEMixerSubsystem.xml
echo "--- LPE"
cat LPESubsystem.xml
```

## Sound 7a: Android's route settings, part 1 (packed, the AI unpacks it)
```send
f=/system/etc/parameter-framework/Settings/Audio/AudioConfigurableDomains.xml
wc -c $f
gzip -9c $f | base64 | head -c 190000
```

## Sound 7b: Android's route settings, part 2
```send
f=/system/etc/parameter-framework/Settings/Audio/AudioConfigurableDomains.xml
gzip -9c $f | base64 | tail -c +190001 | head -c 190000
```

## Sound 1: the sound chip and what the kernel says about it
```send
cat /proc/asound/cards
cat /proc/asound/pcm
dmesg | grep -i -E 'sst|snd|audio|cs42|asoc|codec|lpe|firmware' | tail -80
```

## Update Margin from GitHub
```
update
```
