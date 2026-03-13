package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// Store manages task persistence to a JSON file.
type Store struct {
	path  string
	mu    sync.Mutex
	tasks []*model.Task
}

// New creates a store at the default location.
func New() *Store {
	return &Store{
		path: filepath.Join(config.ConfigDir(), "tasks.json"),
	}
}

// NewWithPath creates a store at a specific path (for testing).
func NewWithPath(path string) *Store {
	return &Store{path: path}
}

// Load reads tasks from disk.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.tasks = nil
			return nil
		}
		return fmt.Errorf("reading tasks: %w", err)
	}

	var tasks []*model.Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return fmt.Errorf("parsing tasks: %w", err)
	}
	s.tasks = tasks
	return nil
}

// Save writes tasks to disk.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(s.tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling tasks: %w", err)
	}

	return os.WriteFile(s.path, data, 0o644)
}

// Tasks returns a copy of all tasks.
func (s *Store) Tasks() []*model.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*model.Task, len(s.tasks))
	copy(out, s.tasks)
	return out
}

// Add adds a new task and persists.
func (s *Store) Add(t *model.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t.ID == "" {
		t.ID = generateID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}

	s.tasks = append(s.tasks, t)
	return s.saveLocked()
}

// Update finds a task by ID and replaces it.
func (s *Store) Update(t *model.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.tasks {
		if existing.ID == t.ID {
			s.tasks[i] = t
			return s.saveLocked()
		}
	}
	return fmt.Errorf("task not found: %s", t.ID)
}

// Delete removes a task by ID.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.tasks {
		if t.ID == id {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("task not found: %s", id)
}

// Get returns a task by ID.
func (s *Store) Get(id string) (*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("task not found: %s", id)
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
