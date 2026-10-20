package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExposition(t *testing.T) {
	m := New("v-test")
	m.Request("video", "ok")
	m.Input("video", 2<<20, 12)
	m.Output("video", 1<<20)
	m.TelegramCall("sendVideo", "200", 300*time.Millisecond, 1<<20, 512)
	m.Tool("ffmpeg", true, time.Second, 2*time.Second)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "distortioner-a"), make([]byte, 100), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "other"), make([]byte, 50), 0o600)
	m.Dir("temp", dir, "distortioner-")
	m.Volume("temp", dir)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`distortioner_build_info{version="v-test"} 1`,
		`distortioner_requests_total{kind="video",status="ok"} 1`,
		`distortioner_file_bytes_total{direction="in",kind="video"} 2.097152e+06`,
		`distortioner_telegram_bytes_total{direction="sent"} 1.048576e+06`,
		`distortioner_tool_cpu_seconds_total{tool="ffmpeg"} 2`,
		`distortioner_dir_bytes{dir="temp"} 100`,
		`distortioner_disk_total_bytes{volume="temp"}`,
		`process_cpu_seconds_total`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %s", want)
		}
	}
}
