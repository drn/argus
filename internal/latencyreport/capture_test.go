package latencyreport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestCapture(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.Equal(t, r.URL.String(), "/debug/pprof/goroutine?debug=2")
		requests++
		_, _ = fmt.Fprint(w, "goroutine 1 [running]:\napp.onTick.func1()\n")
		if requests == 2 {
			cancel()
		}
	}))
	defer server.Close()
	r, err := Capture(ctx, server.Client(), server.URL, time.Millisecond)
	testutil.NoError(t, err)
	testutil.Contains(t, r, "count=1")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type readFunc func([]byte) (int, error)

func (f readFunc) Read(b []byte) (int, error) { return f(b) }

func TestCaptureDeadlineDuringBody(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(readFunc(func([]byte) (int, error) { cancel(); return 0, context.Canceled }))}, nil
	})}
	report, err := Capture(ctx, client, "http://example.invalid", time.Millisecond)
	testutil.NoError(t, err)
	testutil.Contains(t, report, "count=0")
}

func TestCaptureTransportFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		response *http.Response
		err      error
		want     string
	}{
		{"connection", nil, errors.New("connection failed"), "connection failed"},
		{"body", &http.Response{StatusCode: 200, Body: io.NopCloser(failingReader{})}, nil, "read failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return tc.response, tc.err })}
			_, err := Capture(context.Background(), client, "http://example.invalid", time.Millisecond)
			testutil.Equal(t, err != nil, true)
			if err != nil {
				testutil.Contains(t, err.Error(), tc.want)
			}
		})
	}
}

func TestCaptureErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		addr     string
		interval time.Duration
		want     string
	}{
		{name: "interval", interval: 0, want: "interval must be positive"},
		{name: "bad URL", addr: ":invalid", interval: time.Millisecond, want: "missing protocol"},
		{name: "status", status: 500, interval: time.Millisecond, want: "pprof status"},
		{name: "oversized", status: 200, body: strings.Repeat("x", 16*1024*1024+1), interval: time.Millisecond, want: "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); _, _ = fmt.Fprint(w, tc.body) }))
			defer server.Close()
			addr := tc.addr
			if addr == "" {
				addr = server.URL
			}
			_, err := Capture(context.Background(), server.Client(), addr, tc.interval)
			testutil.Equal(t, err != nil, true)
			if err != nil {
				testutil.Contains(t, err.Error(), tc.want)
			}
		})
	}
}
