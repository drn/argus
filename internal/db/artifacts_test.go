package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestUpsertArtifact_RoundTrip(t *testing.T) {
	d := testDB(t)

	a, err := d.UpsertArtifact(&model.Artifact{
		TaskID:   "task1",
		Name:     "Coaching report",
		Filename: "coaching.html",
		Type:     model.ArtifactHTML,
		Size:     1234,
	})
	testutil.NoError(t, err)
	testutil.True(t, a.ID != "")
	testutil.True(t, !a.CreatedAt.IsZero())

	got, err := d.GetArtifact("task1", "coaching.html")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, a.ID)
	testutil.Equal(t, got.Name, "Coaching report")
	testutil.Equal(t, got.Type, model.ArtifactHTML)
	testutil.Equal(t, got.Size, int64(1234))
}

func TestUpsertArtifact_Defaults(t *testing.T) {
	d := testDB(t)
	// Name and Type omitted → default to filename / text.
	a, err := d.UpsertArtifact(&model.Artifact{TaskID: "t", Filename: "out.bin"})
	testutil.NoError(t, err)
	testutil.Equal(t, a.Name, "out.bin")
	testutil.Equal(t, a.Type, model.ArtifactText)
}

func TestUpsertArtifact_Validation(t *testing.T) {
	d := testDB(t)
	_, err := d.UpsertArtifact(&model.Artifact{Filename: "x.html"})
	testutil.True(t, err != nil) // missing task_id
	_, err = d.UpsertArtifact(&model.Artifact{TaskID: "t"})
	testutil.True(t, err != nil) // missing filename
}

func TestUpsertArtifact_OverwriteKeepsStableID(t *testing.T) {
	d := testDB(t)
	first, err := d.UpsertArtifact(&model.Artifact{TaskID: "t", Filename: "r.html", Type: model.ArtifactHTML, Size: 10})
	testutil.NoError(t, err)

	// Re-register same (task, filename) with new metadata → last write wins,
	// id stays stable, no duplicate row.
	second, err := d.UpsertArtifact(&model.Artifact{TaskID: "t", Filename: "r.html", Name: "v2", Type: model.ArtifactHTML, Size: 99})
	testutil.NoError(t, err)
	testutil.Equal(t, second.ID, first.ID)
	testutil.Equal(t, second.Size, int64(99))
	testutil.Equal(t, second.Name, "v2")

	all, err := d.Artifacts("t")
	testutil.NoError(t, err)
	testutil.Equal(t, len(all), 1)
}

func TestArtifacts_ListAndScope(t *testing.T) {
	d := testDB(t)
	_, _ = d.UpsertArtifact(&model.Artifact{TaskID: "t1", Filename: "a.html", Type: model.ArtifactHTML})
	_, _ = d.UpsertArtifact(&model.Artifact{TaskID: "t1", Filename: "b.pdf", Type: model.ArtifactPDF})
	_, _ = d.UpsertArtifact(&model.Artifact{TaskID: "t2", Filename: "c.png", Type: model.ArtifactImage})

	t1, err := d.Artifacts("t1")
	testutil.NoError(t, err)
	testutil.Equal(t, len(t1), 2)

	t2, err := d.Artifacts("t2")
	testutil.NoError(t, err)
	testutil.Equal(t, len(t2), 1)

	none, err := d.Artifacts("nope")
	testutil.NoError(t, err)
	testutil.Equal(t, len(none), 0)
}

func TestGetArtifact_NotFound(t *testing.T) {
	d := testDB(t)
	got, err := d.GetArtifact("t", "missing.html")
	testutil.NoError(t, err)
	testutil.Nil(t, got)
}

func TestDeleteArtifactsForTask(t *testing.T) {
	d := testDB(t)
	_, _ = d.UpsertArtifact(&model.Artifact{TaskID: "t", Filename: "a.html", Type: model.ArtifactHTML})
	_, _ = d.UpsertArtifact(&model.Artifact{TaskID: "t", Filename: "b.md", Type: model.ArtifactMarkdown})
	_, _ = d.UpsertArtifact(&model.Artifact{TaskID: "other", Filename: "c.txt"})

	n, err := d.DeleteArtifactsForTask("t")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 2)

	remaining, err := d.Artifacts("t")
	testutil.NoError(t, err)
	testutil.Equal(t, len(remaining), 0)

	// Unrelated task untouched.
	other, err := d.Artifacts("other")
	testutil.NoError(t, err)
	testutil.Equal(t, len(other), 1)
}
