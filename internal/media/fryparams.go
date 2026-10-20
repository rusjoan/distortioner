package media

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// FryParams are the knobs of the fry preset. Every knob is an amount of
// distortion: zero means that part is off, so all zeros leave the media
// as it is. FryDefaults gives the values the bot uses; cmd/frylab lets you
// tune them on a real video.
type FryParams struct {
	// Picture.
	Saturation float64 `json:"saturation"` // added to saturation 1
	Contrast   float64 `json:"contrast"`   // added to contrast 1
	Brightness float64 `json:"brightness"` // added to brightness 0
	GammaR     float64 `json:"gammaR"`     // added to red gamma 1
	Sharpen    float64 `json:"sharpen"`    // unsharp amount on a 7×7 matrix
	Noise      int     `json:"noise"`      // temporal noise strength, 0…100
	Shrink     float64 `json:"shrink"`     // fake compression: the picture goes down 1+Shrink times and back
	Shake      float64 `json:"shake"`      // frame shake amplitude, share of the frame

	// Sound (earrape). With Loudness on, loudness is normalised after the
	// EQ, so the rest acts the same on quiet and loud inputs.
	HighPass  float64 `json:"highPass"`  // rumble cut, Hz
	Bass      float64 `json:"bass"`      // bass boost, dB
	BassFreq  float64 `json:"bassFreq"`  // where the bass boost is centred, Hz (a setting, not an amount)
	Treble    float64 `json:"treble"`    // treble boost, dB
	Loudness  float64 `json:"loudness"`  // normalisation target, LUFS (negative); 0 = no normalisation
	Gain      float64 `json:"gain"`      // gain into the clipper, dB
	Clip      float64 `json:"clip"`      // how much the clipper cuts: threshold = 1 − Clip
	Crush     int     `json:"crush"`     // bit crushing: 16 − Crush bits
	FinalGain float64 `json:"finalGain"` // gain into the last clipper, dB
}

// FryDefaults returns the bot's fry at a strength: 50 is the reference
// (tuned in cmd/frylab); strength moves the shake (6%…26% at 20…80) and the
// gain into the clipper (+1…+19 dB). The clip threshold stays put: the
// hard clipper does not restore loudness, so a lower threshold would make
// a stronger fry quieter.
func FryDefaults(s Strength) FryParams {
	d := float64(s) - 50
	return FryParams{
		Saturation: 2.2, Contrast: 0.7, Brightness: 0.06, GammaR: 0.25,
		Sharpen: 2.5, Noise: 25, Shrink: 0.5,
		Shake: 0.16 * float64(s) / 50,

		HighPass: 0, Bass: 48, BassFreq: 40, Treble: 10,
		Loudness:  -10,
		Gain:      10 + d*0.3,
		Clip:      0.8,
		Crush:     2,
		FinalGain: 1,
	}
}

// num prints a knob value without float noise: 1+2.2 is "3.2".
func num(f float64) string {
	return strconv.FormatFloat(math.Round(f*1e4)/1e4, 'f', -1, 64)
}

// Video returns the ffmpeg video filters. Stills get no shake.
func (p FryParams) Video(still bool) string {
	var f []string
	if p.Saturation != 0 || p.Contrast != 0 || p.Brightness != 0 || p.GammaR != 0 {
		f = append(f, fmt.Sprintf("eq=saturation=%s:contrast=%s:brightness=%s:gamma_r=%s",
			num(1+p.Saturation), num(1+p.Contrast), num(p.Brightness), num(1+p.GammaR)))
	}
	if p.Sharpen > 0 {
		f = append(f, "unsharp=7:7:"+num(p.Sharpen))
	}
	if p.Noise > 0 {
		f = append(f, fmt.Sprintf("noise=alls=%d:allf=t", p.Noise))
	}
	if p.Shrink > 0 {
		k := num(1 + p.Shrink)
		f = append(f, "scale=iw/"+k+":-2,scale=iw*"+k+":-2:flags=neighbor")
	}
	if !still && p.Shake > 0 {
		f = append(f, shakeFilter(p.Shake))
	}
	if len(f) == 0 {
		return "null"
	}
	return strings.Join(f, ",")
}

// Audio returns the ffmpeg audio filters.
func (p FryParams) Audio() string {
	var f []string
	if p.HighPass > 0 {
		f = append(f, "highpass=f="+num(p.HighPass))
	}
	if p.Bass != 0 {
		f = append(f, fmt.Sprintf("bass=g=%s:f=%s:w=0.8", num(p.Bass), num(p.BassFreq)))
	}
	if p.Treble != 0 {
		f = append(f, "treble=g="+num(p.Treble))
	}
	if p.Loudness < 0 {
		f = append(f, fmt.Sprintf("loudnorm=I=%s:TP=-1:LRA=7,aresample=48000", num(p.Loudness)))
	}
	if p.Gain != 0 || p.Clip > 0 {
		f = append(f, fmt.Sprintf("volume=%.1fdB,asoftclip=type=hard:threshold=%.3f", p.Gain, min(max(1-p.Clip, 0.01), 1)))
	}
	if p.Crush > 0 {
		f = append(f, fmt.Sprintf("acrusher=bits=%d:mode=lin:aa=0", max(16-p.Crush, 1)))
	}
	if p.FinalGain != 0 {
		f = append(f, "volume="+num(p.FinalGain)+"dB,asoftclip=type=hard,volume=-0.5dB")
	}
	if len(f) == 0 {
		return "anull"
	}
	// Earrape is mono: one loud channel instead of two that cancel out.
	return strings.Join(append([]string{"aformat=channel_layouts=mono"}, f...), ",")
}

// shakeFilter crops a randomly moving window that moves by up to amp of the frame.
func shakeFilter(amp float64) string {
	win := 1 - amp - 0.02
	return fmt.Sprintf("crop=w=iw*%.3f:h=ih*%.3f:x=(iw-ow)/2+(random(1)-0.5)*iw*%.3f:y=(ih-oh)/2+(random(2)-0.5)*ih*%.3f",
		win, win, amp, amp)
}
