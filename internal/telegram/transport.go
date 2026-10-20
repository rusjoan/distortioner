package telegram

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Observer receives one Bot API call: the method (never the token), the
// HTTP status or "error", latency until the body was read, and bytes.
type Observer func(method, code string, d time.Duration, sent, received int64)

// Instrument wraps a transport to report every Bot API call.
func Instrument(base http.RoundTripper, observe Observer) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base, observe: observe}
}

type transport struct {
	base    http.RoundTripper
	observe Observer
}

// method extracts the API method from /bot<token>/<method> and
// /file/bot<token>/<path>.
func method(path string) string {
	if strings.HasPrefix(path, "/file/") {
		return "file"
	}
	if i := strings.LastIndexByte(path, '/'); i >= 0 && i < len(path)-1 {
		return path[i+1:]
	}
	return "unknown"
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	sent := &counter{}
	if req.Body != nil {
		req.Body = &countingBody{ReadCloser: req.Body, n: sent}
	}
	name := method(req.URL.Path)
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.observe(name, "error", time.Since(start), sent.load(), 0)
		return nil, err
	}
	code := strconv.Itoa(resp.StatusCode)
	received := &counter{}
	resp.Body = &countingBody{ReadCloser: resp.Body, n: received, onClose: func() {
		t.observe(name, code, time.Since(start), sent.load(), received.load())
	}}
	return resp, nil
}

type counter struct{ v atomic.Int64 }

func (c *counter) load() int64 { return c.v.Load() }

type countingBody struct {
	io.ReadCloser
	n       *counter
	onClose func()
	closed  atomic.Bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.v.Add(int64(n))
	return n, err
}

func (b *countingBody) Close() error {
	err := b.ReadCloser.Close()
	if b.onClose != nil && b.closed.CompareAndSwap(false, true) {
		b.onClose()
	}
	return err
}
