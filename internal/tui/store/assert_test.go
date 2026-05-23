package store_test

import (
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/store"
)

// Compile-time assertion: *db.DB satisfies store.Store. Lives in _test.go so
// importing internal/db here doesn't drag the dep into production tui code.
// If a future db.DB refactor changes a signature, this assertion fails the
// build and pinpoints the drift.
var _ store.Store = (*db.DB)(nil)
