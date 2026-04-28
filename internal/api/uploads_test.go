package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// buildMultipart constructs a multipart/form-data body for tests. Fields is a
// flat list of (name, value) pairs; files is a flat list of (fieldName,
// filename, contents) triples.
func buildMultipart(t *testing.T, fields, files [][3]string) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		// Two-element pair encoded as a triple with empty third slot.
		if err := mw.WriteField(f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		fw, err := mw.CreateFormFile(f[0], f[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(f[2])); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return mw.FormDataContentType(), &buf
}

func TestSanitizeAttachmentName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		err  error
	}{
		{"plain", "hello.png", "hello.png", nil},
		{"strip_unix_path", "/etc/passwd", "passwd", nil},
		{"strip_windows_path", `C:\Users\evil\boot.ini`, "boot.ini", nil},
		{"strip_traversal", "../../etc/hosts", "hosts", nil},
		{"reject_dot", ".", "", errBadAttachmentName},
		{"reject_dotdot", "..", "", errBadAttachmentName},
		{"reject_empty", "", "", errBadAttachmentName},
		{"replaces_control", "ev\x00il.txt", "ev_il.txt", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitizeAttachmentName(tc.in)
			if tc.err != nil {
				testutil.ErrorIs(t, err, tc.err)
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, got, tc.want)
		})
	}
}

func TestSanitizeAttachmentName_TruncatesLongNames(t *testing.T) {
	long := strings.Repeat("a", 200) + ".png"
	got, err := sanitizeAttachmentName(long)
	testutil.NoError(t, err)
	if len(got) > 100 {
		t.Errorf("len(got)=%d, want <=100", len(got))
	}
	if !strings.HasSuffix(got, ".png") {
		t.Errorf("extension lost: %q", got)
	}
}

// TestHandleUploadFiles_WritesToContext verifies the mid-session upload
// endpoint saves files into <worktree>/.context/ and returns relative paths.
func TestHandleUploadFiles_WritesToContext(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	// Seed a task with a real worktree dir on disk.
	wt := t.TempDir()
	task := &model.Task{Name: "t", Project: "p", Worktree: wt}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}

	ct, body := buildMultipart(t, nil, [][3]string{
		{"files", "screenshot.png", "fake-png-bytes"},
		{"files", "log.txt", "hello log"},
	})
	req := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/upload", body)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(resp.Paths))
	}
	for _, p := range resp.Paths {
		if !strings.HasPrefix(p, "./.context/") {
			t.Errorf("path %q missing ./.context/ prefix", p)
		}
	}

	// Files actually exist on disk.
	for _, name := range []string{"screenshot.png", "log.txt"} {
		if _, err := os.Stat(filepath.Join(wt, ".context", name)); err != nil {
			t.Errorf("file %s not written: %v", name, err)
		}
	}
}

// TestHandleUploadFiles_DedupesNames verifies that uploading a file with the
// same name twice produces foo.png and foo-1.png (no clobber).
func TestHandleUploadFiles_DedupesNames(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	wt := t.TempDir()
	task := &model.Task{Name: "t", Project: "p", Worktree: wt}
	d.Add(task) //nolint:errcheck

	upload := func() []string {
		ct, body := buildMultipart(t, nil, [][3]string{
			{"files", "image.png", "data"},
		})
		req := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/upload", body)
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		var resp struct {
			Paths []string `json:"paths"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
		return resp.Paths
	}
	first := upload()
	second := upload()
	if first[0] == second[0] {
		t.Fatalf("expected different paths, got %q twice", first[0])
	}
	if !strings.Contains(second[0], "image-1.png") {
		t.Errorf("second upload should be image-1.png, got %q", second[0])
	}
}

// TestHandleUploadFiles_RejectsEmpty verifies that a request with no file
// parts returns a 400, not a silent success.
func TestHandleUploadFiles_RejectsEmpty(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	wt := t.TempDir()
	task := &model.Task{Name: "t", Project: "p", Worktree: wt}
	d.Add(task) //nolint:errcheck

	ct, body := buildMultipart(t, nil, nil)
	req := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/upload", body)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

// TestHandleUploadFiles_TaskNotFound verifies 404 for unknown task IDs.
func TestHandleUploadFiles_TaskNotFound(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	ct, body := buildMultipart(t, nil, [][3]string{
		{"files", "x.txt", "x"},
	})
	req := httptest.NewRequest("POST", "/api/tasks/missing/upload", body)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusNotFound)
}

// TestParseMultipartTaskForm_RoundTrips verifies the multipart parser pulls
// out fields and files correctly. We test the parser directly because the
// full handler requires a working CreateAndStart pipeline.
func TestParseMultipartTaskForm_RoundTrips(t *testing.T) {
	ct, body := buildMultipart(t,
		[][3]string{
			{"name", "task-name", ""},
			{"prompt", "do the thing", ""},
			{"project", "p1", ""},
		},
		[][3]string{
			{"files", "a.txt", "alpha"},
			{"files", "b.png", "beta"},
		},
	)
	req := httptest.NewRequest("POST", "/api/tasks", body)
	req.Header.Set("Content-Type", ct)

	name, prompt, project, atts, err := parseMultipartTaskForm(req)
	testutil.NoError(t, err)
	testutil.Equal(t, name, "task-name")
	testutil.Equal(t, prompt, "do the thing")
	testutil.Equal(t, project, "p1")
	testutil.Equal(t, len(atts), 2)
	testutil.Equal(t, atts[0].Name, "a.txt")
	testutil.Equal(t, string(atts[0].Data), "alpha")
	testutil.Equal(t, atts[1].Name, "b.png")
}

// TestParseMultipartTaskForm_EnforcesPerFileCap verifies the 10MB per-file
// cap returns the typed error so the handler can map it to 413.
func TestParseMultipartTaskForm_EnforcesPerFileCap(t *testing.T) {
	// Build a body with one file just over the cap.
	huge := bytes.Repeat([]byte("X"), int(maxAttachmentBytes+1))
	ct, body := buildMultipart(t, nil, [][3]string{
		{"files", "huge.bin", string(huge)},
	})
	req := httptest.NewRequest("POST", "/api/tasks", body)
	req.Header.Set("Content-Type", ct)

	_, _, _, _, err := parseMultipartTaskForm(req)
	if !errors.Is(err, errAttachmentTooLarge) {
		t.Fatalf("got %v, want errAttachmentTooLarge", err)
	}
}

func TestStatusForUploadErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"too_large", errAttachmentTooLarge, http.StatusRequestEntityTooLarge},
		{"total_large", errAttachmentTotalLarge, http.StatusRequestEntityTooLarge},
		{"too_many", errTooManyAttachments, http.StatusRequestEntityTooLarge},
		{"bad_name", errBadAttachmentName, http.StatusBadRequest},
		{"empty", errEmptyAttachment, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, statusForUploadErr(tc.err), tc.want)
		})
	}
}

// TestHandleCreateTask_MultipartDispatch verifies that a multipart POST to
// /api/tasks invokes the multipart handler (which exercises CreateAndStart).
// We don't require the create to succeed (no real project is configured); we
// only assert that the handler reaches CreateAndStart by checking the error
// shape — a "project not found" response means dispatch worked.
func TestHandleCreateTask_MultipartDispatch(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	ct, body := buildMultipart(t,
		[][3]string{
			{"name", "x", ""},
			{"prompt", "p", ""},
			{"project", "no-such-project", ""},
		},
		[][3]string{
			{"files", "a.txt", "a"},
		},
	)
	req := httptest.NewRequest("POST", "/api/tasks", body)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Project doesn't exist → 500 from CreateAndStart. JSON dispatch path
	// would have hit the handleCreateTask JSON decoder which would yield
	// invalid JSON (multipart is not valid JSON), so a 400 here means we
	// took the wrong branch.
	if w.Code == http.StatusBadRequest {
		t.Fatalf("multipart took JSON path; body=%s", w.Body.String())
	}
}

// Avoid unused-import warning when agent isn't directly referenced — we use
// it transitively via Attachment in handler tests.
var _ = agent.Attachment{}
