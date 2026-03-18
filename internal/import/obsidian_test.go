package imports

import "testing"

func TestParseTasks_BasicArgusTag(t *testing.T) {
	content := "- [ ] Fix the login bug  #argus\n- [ ] Other task\n- [x] Done task  #argus\n"
	tasks := ParseTasks(content, "backlog.md")

	// Only unchecked tasks with #argus should be parsed.
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1: %v", len(tasks), tasks)
	}
	if tasks[0].Name != "Fix the login bug" {
		t.Errorf("name: got %q", tasks[0].Name)
	}
	if tasks[0].Project != "" {
		t.Errorf("project: got %q, want empty", tasks[0].Project)
	}
	if tasks[0].SourceFile != "backlog.md" {
		t.Errorf("source file: got %q", tasks[0].SourceFile)
	}
}

func TestParseTasks_ArgusWithProject(t *testing.T) {
	content := "- [ ] Implement search  #argus/my-project\n"
	tasks := ParseTasks(content, "tasks.md")

	if len(tasks) != 1 {
		t.Fatalf("got %d tasks", len(tasks))
	}
	if tasks[0].Name != "Implement search" {
		t.Errorf("name: got %q", tasks[0].Name)
	}
	if tasks[0].Project != "my-project" {
		t.Errorf("project: got %q, want my-project", tasks[0].Project)
	}
}

func TestParseTasks_NoDuplicates(t *testing.T) {
	content := "- [ ] Fix bug  #argus\n- [ ] Fix bug  #argus\n"
	tasks := ParseTasks(content, "dup.md")

	if len(tasks) != 1 {
		t.Errorf("duplicate deduplication failed: got %d tasks, want 1", len(tasks))
	}
}

func TestParseTasks_EmptyContent(t *testing.T) {
	tasks := ParseTasks("", "empty.md")
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(tasks))
	}
}

func TestParseTasks_DataviewProject(t *testing.T) {
	content := "- [ ] Build CI pipeline  [project:: infra]  #argus\n"
	tasks := ParseTasks(content, "plan.md")

	if len(tasks) == 0 {
		t.Fatal("got 0 tasks, want 1")
	}
	if tasks[0].Project != "infra" {
		t.Errorf("project: got %q, want infra", tasks[0].Project)
	}
}

func TestToTask(t *testing.T) {
	p := ParsedTask{
		Name:       "My Task",
		Project:    "myproject",
		SourceFile: "file.md",
	}
	task := p.ToTask()

	if task.Name != "My Task" {
		t.Errorf("name: got %q", task.Name)
	}
	if task.Project != "myproject" {
		t.Errorf("project: got %q", task.Project)
	}
}
