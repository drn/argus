package apiclient

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestArtifactsAndBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/tasks/task/artifacts":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"artifacts":[{"task_id":"task","filename":"x.txt","name":"X","type":"text","size":6}]}`))
		case "/api/tasks/task/artifacts/x.txt":
			if r.Header.Get("Range") == "bytes=0-2" {
				_, _ = w.Write([]byte("abc"))
				return
			}
			_, _ = w.Write([]byte("abcdef"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "secret", WithHTTPClient(srv.Client()))
	list, err := c.Artifacts(context.Background(), "task")
	testutil.NoError(t, err)
	testutil.Equal(t, len(list), 1)
	testutil.Equal(t, list[0].Filename, "x.txt")
	var preview bytes.Buffer
	n, err := c.ReadArtifact(context.Background(), "task", "x.txt", &preview, 3)
	testutil.NoError(t, err)
	testutil.Equal(t, n, int64(3))
	testutil.Equal(t, preview.String(), "abc")
	var download bytes.Buffer
	n, err = c.DownloadArtifact(context.Background(), "task", "x.txt", &download, 5)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size limit, got n=%d err=%v", n, err)
	}
	_, err = c.DownloadArtifact(context.Background(), "task", "x.txt", &download, 0)
	if err == nil {
		t.Fatal("expected invalid limit error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.ReadArtifact(ctx, "task", "x.txt", &preview, 3)
	if err == nil {
		t.Fatal("expected canceled request")
	}
}

func TestDownloadArtifactUsesCancellationInsteadOfDefaultTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("media"))
	}))
	defer srv.Close()
	c := New(srv.URL, "secret", WithHTTPClient(srv.Client()), WithTimeout(time.Millisecond))
	var out bytes.Buffer
	_, err := c.DownloadArtifact(context.Background(), "task", "song.mp3", &out, 10)
	testutil.NoError(t, err)
	testutil.Equal(t, out.String(), "media")
}
