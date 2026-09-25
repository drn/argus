package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestArtifactBrowserNavigationAndPreview(t *testing.T) {
	b := NewArtifactBrowser("Task")
	b.SetEntries([]*model.Artifact{{Name: "one", Type: model.ArtifactText}, {Name: "two", Type: model.ArtifactPDF}})
	var previewed, opened bool
	b.OnPreview = func(*model.Artifact) { previewed = true }
	b.OnOpen = func(*model.Artifact) { opened = true }
	b.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), nil)
	testutil.True(t, previewed)
	b.SetPreview("one", []byte("line\x1b[31m\nnext"), false)
	testutil.True(t, strings.Contains(b.preview[0], "�"))
	b.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), nil)
	testutil.Nil(t, b.preview)
	b.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, 0), nil)
	b.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), nil)
	testutil.True(t, opened)
}

func TestArtifactBrowserDraw(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	defer screen.Fini()
	screen.SetSize(80, 20)
	b := NewArtifactBrowser("Task")
	b.SetRect(0, 0, 80, 20)
	b.SetEntries([]*model.Artifact{{Name: "report.md", Type: model.ArtifactMarkdown, Size: 1024}})
	b.Draw(screen)
	screen.Show()
	x, y := findScreenText(screen, "report.md")
	if x < 0 || y < 0 {
		t.Fatal("artifact title missing from rendered browser")
	}
	b.SetError(errors.New("offline"))
	b.Draw(screen)
	screen.Show()
	x, y = findScreenText(screen, "Error: offline")
	if x < 0 || y < 0 {
		t.Fatal("refresh error missing")
	}
	x, y = findScreenText(screen, "report.md")
	if x < 0 || y < 0 {
		t.Fatal("last successful list disappeared after refresh error")
	}
	b.SetEntries(nil)
	b.Draw(screen)
	screen.Show()
	x, y = findScreenText(screen, "No artifacts registered")
	if x < 0 || y < 0 {
		t.Fatal("empty state missing")
	}
}
