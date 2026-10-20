package telegram

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInstrumentReportsCallsWithoutToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasSuffix(r.URL.Path, "/fail") {
			w.WriteHeader(http.StatusTooManyRequests)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	type obs struct {
		method, code   string
		sent, received int64
	}
	var mu sync.Mutex
	var got []obs
	client := &http.Client{Transport: Instrument(nil, func(method, code string, _ time.Duration, sent, received int64) {
		mu.Lock()
		got = append(got, obs{method, code, sent, received})
		mu.Unlock()
	})}

	for _, path := range []string{"/bot123:SECRET/sendMessage", "/bot123:SECRET/fail", "/file/bot123:SECRET/videos/a.mp4"} {
		resp, err := client.Post(srv.URL+path, "text/plain", strings.NewReader("hello"))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	want := []obs{{"sendMessage", "200", 5, 11}, {"fail", "429", 5, 11}, {"file", "200", 5, 11}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d: got %+v, want %+v", i, got[i], want[i])
		}
		if strings.Contains(got[i].method, "SECRET") {
			t.Fatal("token leaked into a label")
		}
	}
}
