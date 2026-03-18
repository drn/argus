package db

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/kb"
)

func TestKBUpsertAndGet(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	defer d.Close()

	doc := &kb.Document{
		Path:       "test/hello.md",
		Title:      "Hello World",
		Body:       "This is the body of the test document.",
		Tags:       []string{"go", "testing"},
		Tier:       "hot",
		ModifiedAt: time.Now().Truncate(time.Second),
		IngestedAt: time.Now().Truncate(time.Second),
		WordCount:  8,
	}

	if err := d.KBUpsert(doc); err != nil {
		t.Fatalf("KBUpsert: %v", err)
	}

	got, err := d.KBGet("test/hello.md")
	if err != nil {
		t.Fatalf("KBGet: %v", err)
	}

	if got.Title != doc.Title {
		t.Errorf("title: got %q, want %q", got.Title, doc.Title)
	}
	if got.Body != doc.Body {
		t.Errorf("body: got %q, want %q", got.Body, doc.Body)
	}
	if got.WordCount != doc.WordCount {
		t.Errorf("word count: got %d, want %d", got.WordCount, doc.WordCount)
	}
	if len(got.Tags) != 2 {
		t.Errorf("tags: got %v, want 2 tags", got.Tags)
	}
}

func TestKBUpsert_Update(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	defer d.Close()

	doc := &kb.Document{
		Path:       "notes/update.md",
		Title:      "Original Title",
		Body:       "original body",
		Tier:       "hot",
		ModifiedAt: time.Now(),
		IngestedAt: time.Now(),
		WordCount:  2,
	}
	if err := d.KBUpsert(doc); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Update the same path.
	doc.Title = "Updated Title"
	doc.Body = "updated body with more words"
	doc.WordCount = 5
	if err := d.KBUpsert(doc); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := d.KBGet("notes/update.md")
	if err != nil {
		t.Fatalf("KBGet after update: %v", err)
	}
	if got.Title != "Updated Title" {
		t.Errorf("title: got %q, want Updated Title", got.Title)
	}
	if got.WordCount != 5 {
		t.Errorf("word count: got %d, want 5", got.WordCount)
	}
}

func TestKBDelete(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	defer d.Close()

	doc := &kb.Document{
		Path:       "notes/delete-me.md",
		Title:      "Delete Me",
		Body:       "body",
		Tier:       "hot",
		ModifiedAt: time.Now(),
		IngestedAt: time.Now(),
		WordCount:  1,
	}
	if err := d.KBUpsert(doc); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := d.KBDelete("notes/delete-me.md"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = d.KBGet("notes/delete-me.md")
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestKBDocumentCount(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	defer d.Close()

	if count := d.KBDocumentCount(); count != 0 {
		t.Errorf("initial count: got %d, want 0", count)
	}

	for i := 0; i < 3; i++ {
		doc := &kb.Document{
			Path:       "notes/doc" + string(rune('0'+i)) + ".md",
			Title:      "Doc",
			Body:       "body",
			Tier:       "hot",
			ModifiedAt: time.Now(),
			IngestedAt: time.Now(),
			WordCount:  1,
		}
		if err := d.KBUpsert(doc); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	if count := d.KBDocumentCount(); count != 3 {
		t.Errorf("count after 3 inserts: got %d, want 3", count)
	}
}

func TestKBList(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	defer d.Close()

	paths := []string{"projects/a.md", "projects/b.md", "notes/c.md"}
	for _, p := range paths {
		doc := &kb.Document{
			Path:       p,
			Title:      "Title",
			Body:       "body",
			Tier:       "hot",
			ModifiedAt: time.Now(),
			IngestedAt: time.Now(),
			WordCount:  1,
		}
		if err := d.KBUpsert(doc); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}

	// List all.
	all, err := d.KBList("", 100)
	if err != nil {
		t.Fatalf("KBList all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("list all: got %d, want 3", len(all))
	}

	// List with prefix.
	proj, err := d.KBList("projects/", 100)
	if err != nil {
		t.Fatalf("KBList projects: %v", err)
	}
	if len(proj) != 2 {
		t.Errorf("list projects/: got %d, want 2", len(proj))
	}
}

func TestKBSearch(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	defer d.Close()

	doc := &kb.Document{
		Path:       "notes/golang.md",
		Title:      "Go Programming",
		Body:       "Go is an open source programming language that makes it easy to build simple reliable scalable software.",
		Tier:       "hot",
		ModifiedAt: time.Now(),
		IngestedAt: time.Now(),
		WordCount:  19,
	}
	if err := d.KBUpsert(doc); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	results, err := d.KBSearch("programming", 10)
	if err != nil {
		t.Fatalf("KBSearch: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one result, got none")
	}
	if results[0].Path != "notes/golang.md" {
		t.Errorf("result path: got %q, want notes/golang.md", results[0].Path)
	}
}

func TestKBPendingTasks(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	defer d.Close()

	if err := d.KBAddPendingTask("Refactor auth", "myproject", "tasks/backlog.md"); err != nil {
		t.Fatalf("KBAddPendingTask: %v", err)
	}

	// Duplicate should be ignored.
	if err := d.KBAddPendingTask("Refactor auth", "myproject", "tasks/backlog.md"); err != nil {
		t.Fatalf("KBAddPendingTask duplicate: %v", err)
	}

	tasks := d.KBPendingTasks()
	if len(tasks) != 1 {
		t.Errorf("pending tasks: got %d, want 1", len(tasks))
	}
	if tasks[0].Name != "Refactor auth" {
		t.Errorf("task name: got %q, want Refactor auth", tasks[0].Name)
	}

	if err := d.KBDeletePendingTask(tasks[0].ID); err != nil {
		t.Fatalf("KBDeletePendingTask: %v", err)
	}
	if remaining := d.KBPendingTasks(); len(remaining) != 0 {
		t.Errorf("after delete: got %d tasks, want 0", len(remaining))
	}
}
