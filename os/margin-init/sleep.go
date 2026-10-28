package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"os/exec"
	"time"

	"margin-init/fonts"
	"margin-init/ui"
)

// Sleep, as the tablet beside the bed sees it:
//
//   - "Going to sleep" (Home and the Soma tab) starts the night in Soma (settings.sleepStart,
//     as Soma's sleep clock keeps it) and turns the screen off;
//   - the alarm (Settings > Sleep) brightens the screen for 15 minutes before its time, a
//     sunrise, then rings: a sound and the vibrator, where the tablet has them. A tap is "I'm
//     up": the night is logged in Soma (nutrition/<day>.sleep, hours, start and end) and the
//     morning screen shows the time, Algiers' weather and the day's plan;
//   - wind-down (from its time to 5 in the morning): the screen goes warm and dim, and Soma
//     opens on today's list for the evening review.

// --- the night, in Soma (sleep-clock.ts) ----------------------------------------------------

func somaAsleepSince() int64 { return int64(num(jsonValue(soma.get("settings.sleepStart")))) }

func somaGoToSleep() { soma.put("settings.sleepStart", fmt.Sprint(time.Now().UnixMilli())) }

// somaWakeUp ends the night and logs it, as Soma's wakeUp: under half an hour or over 16
// hours isn't a night. Returns what to say.
func somaWakeUp() string {
	start := somaAsleepSince()
	if start == 0 {
		return ""
	}
	soma.put("settings.sleepStart", "")
	end := time.Now()
	hours := float64(end.UnixMilli()-start) / 3.6e6
	switch {
	case hours < 0.5:
		return "Under half an hour: not logged"
	case hours > 16:
		return "Over 16 hours: log that night in Soma by hand"
	}
	hours = math.Round(hours*10) / 10
	date := somaDate(end)
	day := somaObj("nutrition/" + date)
	if day == nil {
		day = obj{"goals": obj{"cals": 2400, "protein": 160, "carbs": 260, "fat": 70, "water": 3500}, "water": 0, "creatine": 0, "items": []any{}}
	}
	sl, _ := day["sleep"].(obj)
	if sl == nil {
		sl = obj{}
	}
	sl["hours"], sl["start"], sl["end"] = hours, start, end.UnixMilli()
	day["sleep"] = sl
	somaPut("nutrition/"+date, day)
	return fmt.Sprintf("Slept %.1f h: logged in Soma", hours)
}

// --- the alarm --------------------------------------------------------------------------------

const sunriseMin = 15

func hhmm(m int) string { return fmt.Sprintf("%02d:%02d", m/60%24, m%60) }

// at is today's time for minutes after midnight.
func at(now time.Time, mins int) time.Time {
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return d.Add(time.Duration(mins) * time.Minute)
}

// sleepTick runs every few seconds: the sunrise, the alarm, wind-down. Caller holds drawMu.
func (c *console) sleepTick(now time.Time) {
	now = now.In(displayZone)
	today := now.Format("2006-01-02")
	if c.cfg.AlarmOn && c.alarmDay != today {
		ring := at(now, c.cfg.AlarmMin)
		switch {
		case c.alarm == "" && !c.cfg.NoSunrise && now.After(ring.Add(-sunriseMin*time.Minute)) && now.Before(ring):
			c.startAlarm("sunrise")
		case (c.alarm == "" || c.alarm == "sunrise") && !now.Before(ring) && now.Before(ring.Add(30*time.Minute)):
			c.startAlarm("ringing")
		}
		if c.alarm == "sunrise" { // brighter and brighter, up to the usual brightness at the time
			k := 1 - ring.Sub(now).Minutes()/sunriseMin
			setBacklight(3 + int(float64(c.cfg.Brightness-3)*min(max(k, 0), 1)))
			c.drawAlarm()
		}
	}
	if c.alarm != "" {
		c.lastInput = now // the screen stays on
	}

	// Wind-down: from its time until five in the morning.
	wind := false
	if c.cfg.WindOn {
		m := now.Hour()*60 + now.Minute()
		wind = m >= c.cfg.WindMin || m < 5*60
	}
	if wind != c.winding {
		c.winding = wind
		c.s.warm = wind
		if c.screenOn && c.alarm == "" {
			setBacklight(c.brightNow())
			if wind {
				c.somaUI.view = "today"
				c.somaUI.msg = "Evening review: tick what's done, and move the rest to tomorrow."
				c.setMode(modeSoma)
				c.toastMsg("Time to wind down")
			}
			c.redrawAll()
		}
	}
}

// brightNow is the backlight to use: dimmer while winding down.
func (c *console) brightNow() int {
	if c.winding {
		return min(c.cfg.Brightness, 30)
	}
	return c.cfg.Brightness
}

// startAlarm wakes the screen for the sunrise or the alarm. Caller holds drawMu.
func (c *console) startAlarm(state string) {
	log.Printf("alarm: %s", state)
	if !c.screenOn {
		c.setScreen(true)
	}
	c.alarm = state
	if state == "sunrise" {
		setBacklight(3)
	} else {
		setBacklight(c.cfg.Brightness)
		go ringAlarm(func() bool { drawMu.Lock(); defer drawMu.Unlock(); return c.alarm == "ringing" })
	}
	c.redrawAll()
}

// alarmTouch: a tap while the alarm is up is "I'm up"; on the morning screen, it goes on to
// the lock screen. Caller holds drawMu.
func (c *console) alarmTouch(p TouchPoint) {
	if !p.Up {
		return
	}
	if c.alarm == "morning" {
		c.alarm = ""
		c.redrawAll()
		return
	}
	c.alarm = "morning"
	c.alarmDay = time.Now().In(displayZone).Format("2006-01-02")
	setBacklight(c.brightNow())
	if msg := somaWakeUp(); msg != "" {
		c.toastMsg(msg)
		go soma.sync()
	}
	c.loadWeather()
	c.redrawAll()
}

// ringAlarm plays the alarm until going() is false, or for ten minutes.
func ringAlarm(going func() bool) {
	wav := marginHome + "/alarm.wav"
	if _, err := os.Stat(wav); err != nil {
		os.WriteFile(wav, alarmWAV(), 0o644)
		os.WriteFile(alpineRoot+"/root/alarm.wav", alarmWAV(), 0o644) // for Alpine's aplay
	}
	deadline := time.Now().Add(10 * time.Minute)
	for going() && time.Now().Before(deadline) {
		vibrate(600)
		playSound(wav)
		time.Sleep(1500 * time.Millisecond)
	}
}

// vibrate runs the vibrator for ms milliseconds, if the tablet has one.
func vibrate(ms int) {
	os.WriteFile("/sys/class/timed_output/vibrator/enable", []byte(fmt.Sprint(ms)), 0o644)
}

// playSound plays a WAV through Android's tinyplay, or Alpine's aplay.
func playSound(wav string) {
	for _, try := range [][]string{{"/system/bin/tinyplay", wav}, {"/system/xbin/tinyplay", wav}} {
		if _, err := os.Stat(try[0]); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := exec.CommandContext(ctx, try[0], try[1:]...).Run()
			cancel()
			if err == nil {
				return
			}
		}
	}
	if err := runInAlpine("/usr/bin/aplay", "-q", "/root/alarm.wav"); err != nil {
		log.Printf("alarm sound: %v", err)
	}
}

// alarmWAV is a soft rising three-note chime, 16-bit mono 44.1 kHz.
func alarmWAV() []byte {
	const rate = 44100
	var pcm []int16
	for i, f := range []float64{660, 880, 1046} {
		n := rate * 35 / 100
		for k := 0; k < n; k++ {
			t := float64(k) / rate
			env := math.Min(1, t*40) * math.Exp(-t*5)
			pcm = append(pcm, int16(math.Sin(2*math.Pi*f*t)*env*9000*float64(i+2)/4))
		}
	}
	b := make([]byte, 44+2*len(pcm))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(pcm)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(pcm)))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}

// --- the morning: time, weather, the day --------------------------------------------------------

var weatherURL = "https://api.open-meteo.com/v1/forecast?latitude=36.75&longitude=3.06&current=temperature_2m,weather_code&daily=temperature_2m_max,temperature_2m_min&timezone=Africa%2FAlgiers&forecast_days=1"

type weatherNow struct {
	Temp, Hi, Lo float64
	Code         int
	At           time.Time
}

// loadWeather fetches Algiers' weather (Open-Meteo, no account), at most every half hour.
func (c *console) loadWeather() {
	if time.Since(c.weather.At) < 30*time.Minute {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var r struct {
			Current struct {
				Temp float64 `json:"temperature_2m"`
				Code int     `json:"weather_code"`
			} `json:"current"`
			Daily struct {
				Max []float64 `json:"temperature_2m_max"`
				Min []float64 `json:"temperature_2m_min"`
			} `json:"daily"`
		}
		if err := getJSON(ctx, weatherURL, &r); err != nil {
			log.Printf("weather: %v", err)
			return
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		c.weather = weatherNow{Temp: r.Current.Temp, Code: r.Current.Code, At: time.Now()}
		if len(r.Daily.Max) > 0 && len(r.Daily.Min) > 0 {
			c.weather.Hi, c.weather.Lo = r.Daily.Max[0], r.Daily.Min[0]
		}
		if c.alarm == "morning" && c.screenOn {
			c.drawAlarm()
		}
	}()
}

// weatherWords is a WMO weather code in words.
func weatherWords(code int) string {
	switch {
	case code == 0:
		return "Clear"
	case code <= 2:
		return "Partly cloudy"
	case code == 3:
		return "Cloudy"
	case code <= 48:
		return "Fog"
	case code <= 57:
		return "Drizzle"
	case code <= 67:
		return "Rain"
	case code <= 77:
		return "Snow"
	case code <= 82:
		return "Showers"
	default:
		return "Thunderstorm"
	}
}

// drawAlarm paints the sunrise, the ringing alarm, or the morning. Caller holds drawMu.
func (c *console) drawAlarm() {
	f := apple()
	w, h := c.s.W, c.s.H-c.barH
	img := canvas(w, h)
	now := time.Now().In(displayZone)
	// A sunrise: deep blue at the top to orange at the horizon.
	sky, glow := rgb(0x0b1a3a), rgb(0xff8a3d)
	if c.alarm == "morning" {
		sky, glow = rgb(0x1d4e89), rgb(0xf6c177)
	}
	for y := 0; y < h; y++ {
		ui.Fill(img, image.Rect(0, y, w, y+1), blend(sky, glow, math.Pow(float64(y)/float64(h), 1.6)))
	}
	white := rgb(0xffffff)
	clock := textFace("inter-semibold", fonts.InterSemiBold, true, 190)
	apTextCenter(img, f.headline, w/2, 150, white, now.Format("Monday 2 January"))
	apTextCenter(img, clock, w/2, 320, white, now.Format("15:04"))

	switch c.alarm {
	case "sunrise", "ringing":
		label := "Good morning"
		if c.alarm == "sunrise" {
			label = "Alarm at " + hhmm(c.cfg.AlarmMin)
		}
		apTextCenter(img, f.title, w/2, 520, white, label)
		r := image.Rect(w/2-260, h-420, w/2+260, h-300)
		ui.RoundRect(img, r, 60, blend(white, glow, 0.5))
		apTextCenter(img, f.title, w/2, (r.Min.Y+r.Max.Y)/2, rgb(0x1c1c1e), "I'm up")
	case "morning":
		y := 480
		if !c.weather.At.IsZero() {
			apTextCenter(img, f.title, w/2, y, white, fmt.Sprintf("Algiers  %.0f°  %s", c.weather.Temp, weatherWords(c.weather.Code)))
			apTextCenter(img, f.callout, w/2, y+56, blend(white, sky, 0.2), fmt.Sprintf("High %.0f°  ·  Low %.0f°", c.weather.Hi, c.weather.Lo))
		}
		y += 140
		date := somaToday()
		n := len(somaPlan(date))
		for _, t := range somaTodos() {
			if t.active(date) && t.Scope == "day" && !t.Done {
				n++
			}
		}
		card := image.Rect(80, y, w-80, min(h-200, y+190+n*56))
		glass(img, card, 40, rgb(0x000000), 0.28)
		ty := card.Min.Y + 80
		apText(img, f.title, card.Min.X+50, ty, white, "Today")
		ty += 70
		for _, b := range somaPlan(date) {
			if ty > card.Max.Y-200 {
				break
			}
			ui.Circle(img, card.Min.X+64, ty-10, 10, somaColour(b.Color, apBlue))
			apText(img, f.body, card.Min.X+90, ty, white, clockOf(b.Start)+"  "+b.Label)
			ty += 56
		}
		ty += 20
		for _, t := range somaTodos() {
			if !t.active(date) || t.Scope != "day" || t.Done {
				continue
			}
			if ty > card.Max.Y-60 {
				break
			}
			ui.Circle(img, card.Min.X+64, ty-10, 12, white)
			ui.Circle(img, card.Min.X+64, ty-10, 9, blend(sky, rgb(0x000000), 0.35))
			text := t.Text
			if t.SlotDate == date {
				text = clockOf(float64(t.SlotStart)/60) + "  " + text
			}
			apText(img, f.body, card.Min.X+90, ty, white, clip(f.body, text, card.Dx()-140))
			ty += 56
		}
		apTextCenter(img, f.callout, w/2, h-110, blend(white, glow, 0.3), "Tap to continue")
	}
	c.s.blitRGBA(img, 0, c.barH)
	c.s.Flush()
}
