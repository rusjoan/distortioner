package media

import (
	"testing"
)

func TestParseProbe(t *testing.T) {
	raw := []byte(`{
		"streams": [
			{"codec_type": "video", "codec_name": "vp9", "width": 512, "height": 288,
			 "avg_frame_rate": "30000/1001", "tags": {"alpha_mode": "1"}},
			{"codec_type": "video", "codec_name": "mjpeg", "width": 10, "height": 10, "avg_frame_rate": "0/0"},
			{"codec_type": "audio", "codec_name": "opus"}
		],
		"format": {"duration": "2.500000"}
	}`)
	info, err := parseProbe(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasVideo || !info.HasAudio || !info.Alpha || info.Width != 512 || info.VideoCodec != "vp9" {
		t.Fatalf("%+v", info)
	}
	if info.Duration != 2.5 || info.FPS < 29.9 || info.FPS > 30 {
		t.Fatalf("duration %v fps %v", info.Duration, info.FPS)
	}
}

func TestParseProbeFallbacks(t *testing.T) {
	info, err := parseProbe([]byte(`{"streams": [{"codec_type": "video", "avg_frame_rate": "0/0",
		"r_frame_rate": "25/1", "duration": "4.0"}], "format": {"duration": "N/A"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.FPS != 25 || info.Duration != 4 || info.HasAudio {
		t.Fatalf("%+v", info)
	}
	if _, err := parseProbe([]byte(`not json`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestDistortText(t *testing.T) {
	cases := map[string]string{
		"hello world": "hElLo WoRlD",
		"Привет!":     "пРиВеТ!",
		"":            "",
		"123 ab":      "123 aB",
	}
	for in, want := range cases {
		if got := DistortText(in); got != want {
			t.Errorf("DistortText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStrength(t *testing.T) {
	if Strength(50).keep() != 50 || Strength(95).keep() != 10 || Strength(0).keep() != 90 {
		t.Fatal("keep bounds")
	}
	if vibrato(50) != "vibrato=f=6:d=0.70" || vibrato(90) != "vibrato=f=6:d=1.00" {
		t.Fatalf("vibrato %s %s", vibrato(50), vibrato(90))
	}
}
