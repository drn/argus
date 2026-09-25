package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/drn/argus/internal/artifacts"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

type remoteArtifactReader interface {
	ReadArtifact(context.Context, string, string, io.Writer, int64) (int64, error)
	DownloadArtifact(context.Context, string, string, io.Writer, int64) (int64, error)
}

var artifactFileOpener = func(path string) error { return exec.Command("open", path).Start() }

func (a *App) refreshArtifactCount(taskID string, gen uint64) {
	go func() {
		list, err := a.db.Artifacts(taskID)
		a.tapp.QueueUpdateDraw(func() {
			selected := a.tasklist.SelectedTask()
			if gen != a.artifactCountGen || selected == nil || selected.ID != taskID {
				uxlog.Log("[artifacts] skip stale count task=%s", taskID)
				return
			}
			if err != nil {
				uxlog.Log("[artifacts] count failed task=%s: %v", taskID, err)
				a.taskDetail.SetArtifactCount("unavailable")
			} else {
				uxlog.Log("[artifacts] count task=%s count=%d", taskID, len(list))
				a.taskDetail.SetArtifactCount(fmt.Sprint(len(list)))
			}
		})
	}()
}

func (a *App) openAgentArtifacts() {
	if a.agentState.TaskID == "" {
		return
	}
	a.openArtifacts(&model.Task{ID: a.agentState.TaskID, Name: a.agentState.TaskName})
}

func (a *App) openArtifacts(task *model.Task) {
	if task == nil || task.ID == "" || a.artifactBrowser != nil {
		return
	}
	b := NewArtifactBrowser(task.Name)
	a.artifactBrowser = b
	a.artifactTaskID = task.ID
	a.artifactPrevPage, _ = a.pages.GetFrontPage()
	a.artifactPrevMode = a.mode
	a.mode = modeArtifacts
	b.OnClose = a.closeArtifacts
	b.OnRefresh = a.loadArtifacts
	b.OnPreview = a.previewArtifact
	b.OnOpen = a.openArtifactExternal
	a.pages.AddPage("artifacts", b, true, true)
	a.pages.SwitchToPage("artifacts")
	a.tapp.SetFocus(b)
	uxlog.Log("[artifacts] open task=%s", task.ID)
	a.loadArtifacts()
}

func (a *App) loadArtifacts() {
	b, taskID := a.artifactBrowser, a.artifactTaskID
	if b == nil {
		return
	}
	a.artifactFetchGen++
	gen := a.artifactFetchGen
	b.SetLoading()
	go func() {
		list, err := a.db.Artifacts(taskID)
		a.tapp.QueueUpdateDraw(func() {
			if a.artifactBrowser != b || a.artifactFetchGen != gen {
				uxlog.Log("[artifacts] skip stale list task=%s", taskID)
				return
			}
			if err != nil {
				uxlog.Log("[artifacts] list failed task=%s: %v", taskID, err)
				b.SetError(err)
				return
			}
			uxlog.Log("[artifacts] list task=%s count=%d", taskID, len(list))
			b.SetEntries(list)
			if a.artifactPrevMode == modeTaskList {
				a.artifactCountGen++
				a.refreshArtifactCount(taskID, a.artifactCountGen)
			}
		})
	}()
}

func (a *App) closeArtifacts() {
	if a.artifactBrowser == nil {
		return
	}
	uxlog.Log("[artifacts] close task=%s", a.artifactTaskID)
	a.artifactFetchGen++
	if a.artifactCancel != nil {
		a.artifactCancel()
		a.artifactCancel = nil
	}
	a.artifactOpenGen++
	a.artifactBrowser = nil
	a.artifactTaskID = ""
	a.pages.RemovePage("artifacts")
	a.mode = a.artifactPrevMode
	if a.artifactPrevPage == "agent" {
		a.pages.SwitchToPage("agent")
		if a.agentFocus == focusFiles {
			a.tapp.SetFocus(a.filePanel)
		} else {
			a.tapp.SetFocus(a.agentPane)
		}
		if sess := a.runner.Get(a.agentState.TaskID); sess != nil && sess.Alive() {
			a.startAgentRedrawLoop(a.agentState.TaskID, sess)
		}
	} else {
		a.restorePageFocus(a.artifactPrevPage)
	}
	a.artifactPrevPage = ""
}

func (a *App) previewArtifact(art *model.Artifact) {
	b, taskID := a.artifactBrowser, a.artifactTaskID
	if b == nil {
		return
	}
	a.artifactFetchGen++
	gen := a.artifactFetchGen
	b.SetLoading()
	go func() {
		var out bytes.Buffer
		_, err := a.copyArtifact(context.Background(), taskID, art, &out, artifactPreviewLimit+1, true)
		data := out.Bytes()
		truncated := len(data) > artifactPreviewLimit
		if truncated {
			data = data[:artifactPreviewLimit]
		}
		a.tapp.QueueUpdateDraw(func() {
			if a.artifactBrowser != b || a.artifactFetchGen != gen {
				uxlog.Log("[artifacts] skip stale preview task=%s", taskID)
				return
			}
			if err != nil {
				uxlog.Log("[artifacts] preview failed task=%s file=%s: %v", taskID, art.Filename, err)
				b.SetError(err)
				return
			}
			uxlog.Log("[artifacts] preview task=%s file=%s bytes=%d", taskID, art.Filename, len(data))
			b.SetPreview(art.Name, data, truncated)
		})
	}()
}

func (a *App) openArtifactExternal(art *model.Artifact) {
	b, taskID := a.artifactBrowser, a.artifactTaskID
	if b == nil {
		return
	}
	b.SetInfo("Opening " + art.Name + "… Esc cancels transfer")
	if a.artifactCancel != nil {
		a.artifactCancel()
	}
	a.artifactOpenGen++
	openGen := a.artifactOpenGen
	ctx, cancel := context.WithCancel(context.Background())
	a.artifactCancel = cancel
	a.artifactTransfers.Add(1)
	go func() {
		defer cancel()
		path := ""
		tempDir := ""
		var err error
		if d, ok := a.db.(*db.DB); ok {
			_, path, err = localArtifactFile(d, taskID, art.Filename)
		} else {
			if clean, e := model.SanitizeArtifactFilename(art.Filename); e != nil || clean != art.Filename {
				err = fmt.Errorf("invalid artifact filename")
			}
			if err == nil {
				tempDir, err = os.MkdirTemp("", "argus-artifact-*")
				if err == nil {
					path = filepath.Join(tempDir, art.Filename)
					var f *os.File
					f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
					if err == nil {
						_, err = a.copyArtifact(ctx, taskID, art, f, model.MaxBytesForType(art.Type), false)
						if cerr := f.Close(); err == nil {
							err = cerr
						}
					}
				}
			}
		}
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		if err == nil {
			err = artifactFileOpener(path)
		}
		if err != nil && tempDir != "" {
			_ = os.RemoveAll(tempDir)
			tempDir = ""
		}
		finalDir := tempDir
		if finalDir != "" {
			a.artifactTempMu.Lock()
			a.artifactTempDirs = append(a.artifactTempDirs, finalDir)
			a.artifactTempMu.Unlock()
		}
		a.artifactTransfers.Done()
		a.tapp.QueueUpdateDraw(func() {
			if a.artifactOpenGen == openGen {
				a.artifactCancel = nil
			}
			if a.artifactBrowser != b || a.artifactOpenGen != openGen {
				uxlog.Log("[artifacts] skip stale open task=%s file=%s", taskID, art.Filename)
				return
			}
			if err != nil {
				uxlog.Log("[artifacts] open failed task=%s file=%s: %v", taskID, art.Filename, err)
				b.SetInfo("Open failed: " + err.Error())
				return
			}
			uxlog.Log("[artifacts] opened task=%s file=%s", taskID, art.Filename)
			b.SetInfo("Opened " + art.Name)
		})
	}()
}

func localArtifactFile(d *db.DB, taskID, filename string) (*model.Artifact, string, error) {
	art, err := d.GetArtifact(taskID, filename)
	if err != nil {
		return nil, "", err
	}
	if art == nil {
		return nil, "", fmt.Errorf("artifact is not registered")
	}
	path, ok := artifacts.ResolvePath(taskID, art.Filename)
	if !ok {
		return nil, "", fmt.Errorf("artifact path is outside task directory")
	}
	if _, err = os.Stat(path); err != nil {
		return nil, "", err
	}
	return art, path, nil
}

func (a *App) copyArtifact(ctx context.Context, taskID string, art *model.Artifact, dst io.Writer, limit int64, preview bool) (int64, error) {
	if d, ok := a.db.(*db.DB); ok {
		_, path, err := localArtifactFile(d, taskID, art.Filename)
		if err != nil {
			return 0, err
		}
		f, err := os.Open(path) //nolint:gosec // validated against registered manifest + resolved artifact dir
		if err != nil {
			return 0, err
		}
		defer f.Close()
		return io.Copy(dst, io.LimitReader(f, limit))
	}
	r, ok := a.db.(remoteArtifactReader)
	if !ok {
		return 0, fmt.Errorf("artifact reader unavailable")
	}
	if preview {
		return r.ReadArtifact(ctx, taskID, art.Filename, dst, limit)
	}
	return r.DownloadArtifact(ctx, taskID, art.Filename, dst, limit)
}

func (a *App) cleanupArtifactTemps() {
	if a.artifactCancel != nil {
		a.artifactCancel()
	}
	a.artifactTransfers.Wait()
	a.artifactTempMu.Lock()
	defer a.artifactTempMu.Unlock()
	for _, dir := range a.artifactTempDirs {
		_ = os.RemoveAll(dir)
	}
	a.artifactTempDirs = nil
}
