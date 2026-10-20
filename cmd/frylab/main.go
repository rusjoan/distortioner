// Command frylab is a local tool for tuning the fry preset on a real video.
// The video lives in memory: ffmpeg reads it from a loopback HTTP endpoint
// and writes the preview to a pipe, so nothing touches the disk.
//
//	go run ./cmd/frylab [-addr 127.0.0.1:8095] [-dir testfiles]
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/graynk/distortioner/internal/media"
)

//go:embed index.html
var page []byte

const maxUpload = 512 << 20

type lab struct {
	dir  string
	self string // base URL ffmpeg uses to read sources

	mu      sync.Mutex
	sources map[string][]byte // name → file contents
}

func main() {
	def := "127.0.0.1:8095"
	if port := os.Getenv("PORT"); port != "" {
		def = "127.0.0.1:" + port // set by preview launchers
	}
	addr := flag.String("addr", def, "listen address")
	dir := flag.String("dir", "testfiles", "directory with videos offered in the picker")
	flag.Parse()

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	l := &lab{dir: *dir, self: "http://" + ln.Addr().String(), sources: map[string][]byte{}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /api/files", l.files)
	mux.HandleFunc("POST /api/load", l.load)
	mux.HandleFunc("POST /api/upload", l.upload)
	mux.HandleFunc("GET /api/defaults", l.defaults)
	mux.HandleFunc("POST /api/render", l.render)
	mux.HandleFunc("GET /src/{name}", l.src)

	log.Printf("frylab on %s", l.self) //nolint:gosec // our own listen address
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}

// files lists videos in -dir.
func (l *lab) files(w http.ResponseWriter, _ *http.Request) {
	entries, _ := os.ReadDir(l.dir)
	var names []string
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".mp4", ".mov", ".webm", ".mkv", ".gif":
			names = append(names, e.Name())
		}
	}
	writeJSON(w, names)
}

// load reads a file from -dir into memory once.
func (l *lab) load(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Query().Get("name"))
	data, err := os.ReadFile(filepath.Join(l.dir, name)) //nolint:gosec // a local dev tool reading its own directory
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	l.keep(w, r.Context(), name, data)
}

// upload takes a file from the browser into memory.
func (l *lab) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	l.keep(w, r.Context(), "upload-"+filepath.Base(hdr.Filename), data)
}

func (l *lab) keep(w http.ResponseWriter, ctx context.Context, name string, data []byte) {
	l.mu.Lock()
	l.sources[name] = data
	l.mu.Unlock()
	info, err := l.probe(ctx, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, map[string]any{"name": name, "size": len(data), "duration": info.Duration,
		"width": info.Width, "height": info.Height, "audio": info.HasAudio})
}

func (l *lab) probe(ctx context.Context, name string) (media.Info, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json", //nolint:gosec // local dev tool
		"-show_format", "-show_streams", l.self+"/src/"+name).Output()
	if err != nil {
		return media.Info{}, fmt.Errorf("ffprobe: %w", err)
	}
	var p struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return media.Info{}, err
	}
	var info media.Info
	info.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if !info.HasVideo {
				info.HasVideo, info.Width, info.Height = true, s.Width, s.Height
			}
		case "audio":
			info.HasAudio = true
		}
	}
	return info, nil
}

// src serves a source from memory, with range requests so ffmpeg can seek.
func (l *lab) src(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	l.mu.Lock()
	data, ok := l.sources[name]
	l.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (l *lab) defaults(w http.ResponseWriter, r *http.Request) {
	s, err := strconv.Atoi(r.URL.Query().Get("strength"))
	if err != nil {
		s = 50
	}
	writeJSON(w, media.FryDefaults(media.Strength(s)))
}

type renderRequest struct {
	Name    string          `json:"name"`
	Start   float64         `json:"start"`
	Seconds float64         `json:"seconds"`
	CRF     int             `json:"crf"`
	Params  media.FryParams `json:"params"`
	Picture bool            `json:"picture"` // apply picture filters
	Sound   bool            `json:"sound"`   // apply earrape
}

// render runs ffmpeg on the in-memory source and streams fragmented mp4
// back. A newer request from the page aborts the older one.
func (l *lab) render(w http.ResponseWriter, r *http.Request) {
	var req renderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	l.mu.Lock()
	_, ok := l.sources[req.Name]
	l.mu.Unlock()
	if !ok {
		http.Error(w, "load a video first", http.StatusNotFound)
		return
	}

	vf := "scale=w=min(512\\,iw):h=min(512\\,ih):force_original_aspect_ratio=decrease"
	if req.Picture {
		vf += "," + req.Params.Video(false)
	}
	vf += ",scale=trunc(iw/2)*2:trunc(ih/2)*2"
	args := []string{"-nostdin", "-v", "error",
		"-ss", strconv.FormatFloat(req.Start, 'f', 2, 64),
		"-t", strconv.FormatFloat(max(req.Seconds, 0.5), 'f', 2, 64),
		"-i", l.self + "/src/" + req.Name,
		"-map", "0:v:0", "-map", "0:a:0?", "-vf", vf}
	if req.Sound {
		args = append(args, "-af", req.Params.Audio())
	}
	// CRF 0 is lossless, which switches x264 to a profile browsers cannot
	// play; 1 looks the same.
	crf := min(max(req.CRF, 1), 51)
	args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-crf", strconv.Itoa(crf),
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")

	var stderr bytes.Buffer
	cmd := exec.CommandContext(r.Context(), "ffmpeg", args...) //nolint:gosec // local dev tool
	cmd.Stderr = &stderr
	start := time.Now()
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) {
			return // superseded by a newer render
		}
		http.Error(w, strings.TrimSpace(stderr.String()), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("X-Render-Ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	w.Header().Set("X-Filters", strings.ReplaceAll(vf+" | "+req.Params.Audio(), "\n", " "))
	_, _ = w.Write(out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
