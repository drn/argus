package gitutil

import "testing"

func TestParseGitStatus(t *testing.T) {
	input := " M internal/ui/root.go\n?? newfile.go\nA  added.go\n"
	files := ParseGitStatus(input)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0].Status != "M" || files[0].Path != "internal/ui/root.go" {
		t.Errorf("file 0 = %+v", files[0])
	}
	if files[1].Status != "??" || files[1].Path != "newfile.go" {
		t.Errorf("file 1 = %+v", files[1])
	}
}

func TestParseGitStatus_Empty(t *testing.T) {
	if ParseGitStatus("") != nil {
		t.Error("expected nil for empty input")
	}
}

func TestParseGitDiffNameStatus(t *testing.T) {
	input := "M\tfile1.go\nA\tfile2.go\nD\tfile3.go\n"
	files := ParseGitDiffNameStatus(input)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0].Status != "M" || files[0].Path != "file1.go" {
		t.Errorf("file 0 = %+v", files[0])
	}
	if files[2].Status != "D" {
		t.Errorf("file 2 status = %q, want D", files[2].Status)
	}
}

func TestParseGitDiffNameStatus_Empty(t *testing.T) {
	if ParseGitDiffNameStatus("") != nil {
		t.Error("expected nil for empty input")
	}
}

func TestMergeChangedFiles(t *testing.T) {
	base := []ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
	}
	overlay := []ChangedFile{
		{Status: "D", Path: "a.go"}, // overlay wins
		{Status: "A", Path: "c.go"},
	}
	merged := MergeChangedFiles(base, overlay)
	if len(merged) != 3 {
		t.Fatalf("expected 3 files, got %d", len(merged))
	}
	// a.go should have overlay status
	for _, f := range merged {
		if f.Path == "a.go" && f.Status != "D" {
			t.Errorf("a.go status = %q, want D (overlay wins)", f.Status)
		}
	}
}

func TestMergeChangedFiles_BothEmpty(t *testing.T) {
	if MergeChangedFiles(nil, nil) != nil {
		t.Error("expected nil for both empty")
	}
}
