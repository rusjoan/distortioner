package media

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Info describes a media file.
type Info struct {
	Duration   float64 // seconds, 0 if unknown
	HasVideo   bool
	HasAudio   bool
	Width      int
	Height     int
	FPS        float64 // 0 if unknown
	VideoCodec string
	Alpha      bool // VP9 with an alpha channel (video stickers)
	SampleRate int  // of the first audio stream, 0 if none
}

type probeOutput struct {
	Streams []struct {
		CodecType    string            `json:"codec_type"`
		CodecName    string            `json:"codec_name"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		RFrameRate   string            `json:"r_frame_rate"`
		Duration     string            `json:"duration"`
		SampleRate   string            `json:"sample_rate"`
		Tags         map[string]string `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe inspects a file with ffprobe.
func (e *Engine) Probe(ctx context.Context, path string) (Info, error) {
	out, err := run(ctx, nil, e.FFprobe,
		"-v", "error",
		"-print_format", "json",
		"-show_format", "-show_streams",
		path)
	if err != nil {
		return Info{}, err
	}
	return parseProbe(out)
}

func parseProbe(raw []byte) (Info, error) {
	var p probeOutput
	if err := json.Unmarshal(raw, &p); err != nil {
		return Info{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	var info Info
	info.Duration = parseFloat(p.Format.Duration)
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if info.HasVideo {
				continue // the first video stream is the one we process
			}
			info.HasVideo = true
			info.Width, info.Height = s.Width, s.Height
			info.VideoCodec = s.CodecName
			info.FPS = parseRate(s.AvgFrameRate)
			if info.FPS == 0 {
				info.FPS = parseRate(s.RFrameRate)
			}
			info.Alpha = s.Tags["alpha_mode"] == "1" || s.Tags["ALPHA_MODE"] == "1"
			if info.Duration == 0 {
				info.Duration = parseFloat(s.Duration)
			}
		case "audio":
			if !info.HasAudio {
				info.SampleRate, _ = strconv.Atoi(s.SampleRate)
			}
			info.HasAudio = true
		}
	}
	return info, nil
}

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// parseRate parses ffprobe rationals like "30000/1001"; "0/0" yields 0.
func parseRate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return parseFloat(s)
	}
	n, d := parseFloat(num), parseFloat(den)
	if d == 0 {
		return 0
	}
	return n / d
}
