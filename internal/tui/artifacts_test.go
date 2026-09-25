package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

type delayedArtifactStore struct {
	stubStore
	call    atomic.Int32
	started chan int
	release [2]chan struct{}
}

func (s *delayedArtifactStore) Artifacts(string) ([]*model.Artifact, error) {
	i := int(s.call.Add(1)) - 1
	s.started <- i
	<-s.release[i]
	return []*model.Artifact{{Name: []string{"old", "new"}[i]}}, nil
}

func TestArtifactListDropsStaleRefresh(t *testing.T) {
	tapp, _, _ := simApp(t)
	tapp.SetRoot(NewArtifactBrowser("Task"), true)
	stop := runApp(t, tapp)
	defer stop()
	s := &delayedArtifactStore{started: make(chan int, 2), release: [2]chan struct{}{make(chan struct{}), make(chan struct{})}}
	b := NewArtifactBrowser("Task")
	a := &App{tapp: tapp, db: s, artifactBrowser: b, artifactTaskID: "task", artifactPrevMode: modeAgent}
	readUI(t, tapp, func() { a.loadArtifacts() })
	select {
	case <-s.started:
	case <-time.After(uiTimeout):
		t.Fatal("first load did not start")
	}
	readUI(t, tapp, func() { a.loadArtifacts() })
	select {
	case <-s.started:
	case <-time.After(uiTimeout):
		t.Fatal("second load did not start")
	}
	close(s.release[1])
	syncUI(t, tapp)
	readUI(t, tapp, func() { testutil.Equal(t, b.entries[0].Name, "new") })
	close(s.release[0])
	syncUI(t, tapp)
	readUI(t, tapp, func() { testutil.Equal(t, b.entries[0].Name, "new") })
}

type artifactRemoteStub struct{ stubStore }

func (artifactRemoteStub) DownloadArtifact(_ context.Context, _, _ string, dst io.Writer, _ int64) (int64, error) {
	n, err := dst.Write([]byte("remote artifact"))
	return int64(n), err
}
func (artifactRemoteStub) ReadArtifact(_ context.Context, _, _ string, dst io.Writer, _ int64) (int64, error) {
	n, err := dst.Write([]byte("preview"))
	return int64(n), err
}

func TestRemoteArtifactExternalOpenStreamsToPrivateTempFile(t *testing.T) {
	tapp, _, _ := simApp(t)
	tapp.SetRoot(NewArtifactBrowser("Task"), true)
	stop := runApp(t, tapp)
	defer stop()
	opened := make(chan string, 1)
	prior := artifactFileOpener
	artifactFileOpener = func(path string) error { opened <- path; return nil }
	defer func() { artifactFileOpener = prior }()
	a := &App{tapp: tapp, db: artifactRemoteStub{}, artifactBrowser: NewArtifactBrowser("Task"), artifactTaskID: "task"}
	art := &model.Artifact{TaskID: "task", Filename: "song.mp3", Name: "Song", Type: model.ArtifactAudio}
	readUI(t, tapp, func() { a.openArtifactExternal(art) })
	var path string
	select {
	case path = <-opened:
	case <-time.After(uiTimeout):
		t.Fatal("external opener not called")
	}
	syncUI(t, tapp)
	content, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, string(content), "remote artifact")
	info, err := os.Stat(path)
	testutil.NoError(t, err)
	testutil.Equal(t, info.Mode().Perm(), os.FileMode(0600))
	stop()
	a.cleanupArtifactTemps()
	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("temporary artifact remains: %v", err)
	}
}

func TestLocalArtifactFileManifestAndPathGuard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { testutil.NoError(t, d.Close()) })
	dir := agent.ArtifactsDir("task")
	testutil.NoError(t, os.MkdirAll(dir, 0700))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "x.txt"), []byte("good"), 0600))
	_, _, err = localArtifactFile(d, "task", "x.txt")
	if err == nil {
		t.Fatal("unregistered file opened")
	}
	_, err = d.UpsertArtifact(&model.Artifact{TaskID: "task", Filename: "x.txt", Type: model.ArtifactText})
	testutil.NoError(t, err)
	_, path, err := localArtifactFile(d, "task", "x.txt")
	testutil.NoError(t, err)
	wantPath, err := filepath.EvalSymlinks(filepath.Join(dir, "x.txt"))
	testutil.NoError(t, err)
	testutil.Equal(t, path, wantPath)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	testutil.NoError(t, os.WriteFile(outside, []byte("bad"), 0600))
	testutil.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.txt")))
	_, err = d.UpsertArtifact(&model.Artifact{TaskID: "task", Filename: "link.txt", Type: model.ArtifactText})
	testutil.NoError(t, err)
	_, _, err = localArtifactFile(d, "task", "link.txt")
	if err == nil {
		t.Fatal("symlink escape opened")
	}
}
