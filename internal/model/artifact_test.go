package model

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestInferArtifactType(t *testing.T) {
	tests := []struct {
		name string
		file string
		want ArtifactType
	}{
		{"html", "report.html", ArtifactHTML},
		{"htm", "page.HTM", ArtifactHTML},
		{"markdown md", "notes.md", ArtifactMarkdown},
		{"markdown long", "notes.markdown", ArtifactMarkdown},
		{"pdf", "doc.pdf", ArtifactPDF},
		{"png", "shot.png", ArtifactImage},
		{"jpeg upper", "photo.JPEG", ArtifactImage},
		{"svg", "diagram.svg", ArtifactImage},
		{"unknown defaults to text", "data.bin", ArtifactText},
		{"no ext defaults to text", "README", ArtifactText},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, InferArtifactType(tc.file), tc.want)
		})
	}
}

func TestValidArtifactType(t *testing.T) {
	for _, ty := range []ArtifactType{ArtifactHTML, ArtifactMarkdown, ArtifactPDF, ArtifactImage, ArtifactText} {
		testutil.True(t, ValidArtifactType(ty))
	}
	testutil.True(t, !ValidArtifactType(ArtifactType("video")))
	testutil.True(t, !ValidArtifactType(ArtifactType("")))
}

func TestArtifactContentType(t *testing.T) {
	tests := []struct {
		name string
		art  Artifact
		want string
	}{
		{"html", Artifact{Type: ArtifactHTML, Filename: "r.html"}, "text/html; charset=utf-8"},
		{"markdown", Artifact{Type: ArtifactMarkdown, Filename: "r.md"}, "text/markdown; charset=utf-8"},
		{"pdf", Artifact{Type: ArtifactPDF, Filename: "r.pdf"}, "application/pdf"},
		{"text", Artifact{Type: ArtifactText, Filename: "r.txt"}, "text/plain; charset=utf-8"},
		{"image png", Artifact{Type: ArtifactImage, Filename: "r.png"}, "image/png"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, ArtifactContentType(tc.art), tc.want)
		})
	}
}

func TestArtifactContentType_ImageFallback(t *testing.T) {
	// An image artifact whose extension is unknown to the mime db falls back
	// to image/png rather than leaking a wrong/empty type.
	got := ArtifactContentType(Artifact{Type: ArtifactImage, Filename: "weird.unknownext"})
	testutil.Equal(t, got, "image/png")
}

func TestSanitizeArtifactFilename(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{"plain basename", "report.html", "report.html", false},
		{"strips abs dir", "/tmp/coaching-reports/coaching-2026-05-30.html", "coaching-2026-05-30.html", false},
		{"strips relative dir", "sub/dir/file.md", "file.md", false},
		{"trims whitespace", "  spaced.pdf  ", "spaced.pdf", false},
		{"traversal collapses to base", "../../etc/passwd", "passwd", false},
		{"dotdot alone rejected", "..", "", true},
		{"dot alone rejected", ".", "", true},
		{"empty rejected", "", "", true},
		{"whitespace only rejected", "   ", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SanitizeArtifactFilename(tc.path)
			if tc.wantErr {
				testutil.True(t, errors.Is(err, ErrInvalidArtifactName))
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, got, tc.want)
		})
	}
}
