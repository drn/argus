package wispr

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func fixtureDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Wispr Flow", "flow.sqlite")
	testutil.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE History (
		transcriptEntityId TEXT PRIMARY KEY, asrText TEXT, formattedText TEXT,
		timestamp DATETIME, status VARCHAR, app TEXT, duration FLOAT)`)
	testutil.NoError(t, err)
	return path, db
}

func insert(t *testing.T, db *sql.DB, id string, at time.Time, asr, formatted, status any) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO History (transcriptEntityId, asrText, formattedText, timestamp, status, app)
		VALUES (?, ?, ?, ?, ?, 'com.test.app')`, id, asr, formatted, at.UTC().Format(tsLayout), status)
	testutil.NoError(t, err)
}

func TestDriver_URLs(t *testing.T) {
	var got []string
	d := &Driver{Open: func(_ context.Context, u string) error { got = append(got, u); return nil }}
	testutil.NoError(t, d.StartRecording(context.Background()))
	testutil.NoError(t, d.StopRecording(context.Background()))
	testutil.DeepEqual(t, got, []string{"wispr-flow://start-hands-free", "wispr-flow://stop-hands-free"})
}

func TestDriver_OpenError(t *testing.T) {
	boom := errors.New("boom")
	d := &Driver{Open: func(context.Context, string) error { return boom }}
	testutil.ErrorIs(t, d.StartRecording(context.Background()), boom)
	testutil.ErrorIs(t, d.StopRecording(context.Background()), boom)
}

func TestDriver_DefaultOpenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	testutil.Error(t, openURL(ctx, "wispr-flow://noop"))
}

func TestDefaultDBPath(t *testing.T) {
	t.Setenv("HOME", "/h")
	testutil.Equal(t, DefaultDBPath(), "/h/Library/Application Support/Wispr Flow/flow.sqlite")
	testutil.Equal(t, (&Driver{}).dbPath(), DefaultDBPath())
	testutil.Equal(t, (&Driver{DBPath: "/x"}).dbPath(), "/x")
	testutil.Equal(t, (&Driver{}).poll(), defaultPoll)
}

func TestWaitTranscript_NoDB(t *testing.T) {
	d := &Driver{DBPath: filepath.Join(t.TempDir(), "missing.sqlite")}
	_, err := d.WaitTranscript(context.Background(), time.Now(), time.Second)
	testutil.ErrorIs(t, err, ErrNoDB)
}

func TestWaitTranscript_Statuses(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	since := now.Add(-time.Second)
	tests := []struct {
		name      string
		asr, fmtd any
		status    any
		wantText  string
		wantErr   error
	}{
		{"formatted", "raw", " formatted text ", "formatted", "formatted text", nil},
		{"formatted falls back to asr", "raw words", "", "formatted", "raw words", nil},
		{"raw_transcript", "raw words", nil, "raw_transcript", "raw words", nil},
		{"dismissed", "x", "y", "dismissed", "", ErrDismissed},
		{"error", "x", "y", "error", "", ErrDismissed},
		{"processing times out", "x", "y", "processing", "", ErrTimeout},
		{"null status times out", "x", "y", nil, "", ErrTimeout},
		{"empty final text times out", "", "", "raw_transcript", "", ErrTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, db := fixtureDB(t)
			insert(t, db, "id1", now, tt.asr, tt.fmtd, tt.status)
			d := &Driver{DBPath: path, Poll: 10 * time.Millisecond}
			tr, err := d.WaitTranscript(context.Background(), since, 150*time.Millisecond)
			if tt.wantErr != nil {
				testutil.ErrorIs(t, err, tt.wantErr)
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, tr.Text, tt.wantText)
			testutil.Equal(t, tr.ID, "id1")
			testutil.Equal(t, tr.App, "com.test.app")
			testutil.Equal(t, tr.Status, tt.status.(string))
			testutil.True(t, tr.At.Equal(now))
		})
	}
}

func TestWaitTranscript_IgnoresOlderRows(t *testing.T) {
	path, db := fixtureDB(t)
	now := time.Now()
	insert(t, db, "old", now.Add(-time.Hour), "old", "old", "formatted")
	d := &Driver{DBPath: path, Poll: 10 * time.Millisecond}
	_, err := d.WaitTranscript(context.Background(), now.Add(-time.Minute), 100*time.Millisecond)
	testutil.ErrorIs(t, err, ErrTimeout)
}

func TestWaitTranscript_SkipsEmptyThenReturnsNext(t *testing.T) {
	path, db := fixtureDB(t)
	now := time.Now()
	insert(t, db, "a", now, "", "", "raw_transcript")
	insert(t, db, "b", now.Add(10*time.Millisecond), "hello", "Hello.", "formatted")
	d := &Driver{DBPath: path, Poll: 10 * time.Millisecond}
	tr, err := d.WaitTranscript(context.Background(), now.Add(-time.Second), time.Second)
	testutil.NoError(t, err)
	testutil.Equal(t, tr.ID, "b")
}

func TestWaitTranscript_ConcurrentInsert(t *testing.T) {
	path, db := fixtureDB(t)
	now := time.Now()
	insert(t, db, "id1", now, "x", "", "processing")
	go func() {
		time.Sleep(60 * time.Millisecond)
		_, _ = db.Exec(`UPDATE History SET formattedText='Done.', status='formatted' WHERE transcriptEntityId='id1'`)
	}()
	d := &Driver{DBPath: path, Poll: 10 * time.Millisecond}
	tr, err := d.WaitTranscript(context.Background(), now.Add(-time.Second), 5*time.Second)
	testutil.NoError(t, err)
	testutil.Equal(t, tr.Text, "Done.")
}

func TestWaitTranscript_ContextCancelled(t *testing.T) {
	path, _ := fixtureDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(40*time.Millisecond, cancel)
	d := &Driver{DBPath: path, Poll: 10 * time.Millisecond}
	_, err := d.WaitTranscript(ctx, time.Now(), 5*time.Second)
	testutil.ErrorIs(t, err, context.Canceled)
}

func TestWaitTranscript_QueryErrorKeepsPolling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.sqlite")
	db, err := sql.Open("sqlite", path)
	testutil.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE Other (x TEXT)`)
	testutil.NoError(t, err)
	_ = db.Close()
	d := &Driver{DBPath: path, Poll: 10 * time.Millisecond}
	_, err = d.WaitTranscript(context.Background(), time.Now(), 60*time.Millisecond)
	testutil.ErrorIs(t, err, ErrTimeout)
}
