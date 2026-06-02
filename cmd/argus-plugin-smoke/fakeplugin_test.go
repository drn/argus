package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/drn/argus/internal/testutil"
)

func TestFakePlugin_RejectsMissingAuth(t *testing.T) {
	p := newFakePlugin()
	t.Cleanup(p.Stop)

	resp, err := http.Post(p.URL()+"/mcp/foo", "application/json", strings.NewReader(`{}`))
	testutil.NoError(t, err)
	defer resp.Body.Close()
	testutil.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	testutil.Equal(t, len(p.Recorded()), 0)
}

func TestFakePlugin_RecordsAuthedRequest(t *testing.T) {
	p := newFakePlugin()
	t.Cleanup(p.Stop)

	req, err := http.NewRequest(http.MethodPost, p.URL()+"/mcp/foo", strings.NewReader(`{"hello":"world"}`))
	testutil.NoError(t, err)
	req.Header.Set("Authorization", p.AuthHeader())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	testutil.Equal(t, resp.StatusCode, http.StatusOK)
	testutil.Contains(t, string(body), `"ok":true`)

	rec := p.Recorded()
	testutil.Equal(t, len(rec), 1)
	testutil.Equal(t, rec[0].Method, http.MethodPost)
	testutil.Equal(t, rec[0].Path, "/mcp/foo")
	testutil.Equal(t, string(rec[0].Body), `{"hello":"world"}`)
}

func TestFakePlugin_WSHandlerRejectedWhenUnregistered(t *testing.T) {
	p := newFakePlugin()
	t.Cleanup(p.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, p.WSURL()+"/stream/foo", nil)
	if err == nil {
		t.Fatal("expected dial error when no ws handler is registered")
	}
	if resp != nil {
		testutil.Equal(t, resp.StatusCode, http.StatusBadRequest)
	}
}

func TestFakePlugin_WSHandlerDispatchesWhenRegistered(t *testing.T) {
	p := newFakePlugin()
	t.Cleanup(p.Stop)

	got := make(chan []byte, 1)
	p.SetWSHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := acceptWS(w, r)
		if err != nil {
			return
		}
		defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()
		_, data, err := c.Read(r.Context())
		if err == nil {
			got <- bytes.Clone(data)
		}
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, p.WSURL()+"/stream/foo", nil)
	testutil.NoError(t, err)
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()
	testutil.NoError(t, c.Write(ctx, websocket.MessageBinary, []byte("hello")))

	select {
	case b := <-got:
		testutil.Equal(t, string(b), "hello")
	case <-time.After(2 * time.Second):
		t.Fatal("ws handler did not receive frame")
	}
}

func TestFakePlugin_AuthHeaderIsRandomAndStable(t *testing.T) {
	p1 := newFakePlugin()
	t.Cleanup(p1.Stop)
	p2 := newFakePlugin()
	t.Cleanup(p2.Stop)

	if p1.AuthHeader() == p2.AuthHeader() {
		t.Fatal("two fake plugins minted the same auth header — randHex broken")
	}
	if !strings.HasPrefix(p1.AuthHeader(), "Bearer ") {
		t.Fatalf("expected Bearer prefix, got %q", p1.AuthHeader())
	}
	// Stable across calls on the same instance.
	testutil.Equal(t, p1.AuthHeader(), p1.AuthHeader())
}
