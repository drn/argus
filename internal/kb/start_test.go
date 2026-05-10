package kb

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestStart_IncrementalScanError exercises Start's incremental-error branch.
// Setup: KBMetadataMap returns non-empty, but the vault path doesn't exist so
// IncrementalScan fails on filepath.Walk's root.
func TestStart_IncrementalScanError(t *testing.T) {
	store := newMockStore()
	// Pre-populate so KBMetadataMap returns len>0.
	store.docs["seed.md"] = &Document{Path: "seed.md"}

	idx := NewIndexer(store, "/no/such/vault/dir")
	err := idx.Start()
	testutil.Error(t, err)
}
