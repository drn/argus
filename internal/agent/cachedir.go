package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const contentAddressedMarker = ".argus-complete"

// materializeContentAddressed renames a temp dir into place so concurrent launches never see a partial dir.
func materializeContentAddressed(root string, files map[string][]byte) (string, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", n, len(files[n]))
		h.Write(files[n])
	}
	hashDir := filepath.Join(root, hex.EncodeToString(h.Sum(nil))[:16])
	if _, err := os.Stat(filepath.Join(hashDir, contentAddressedMarker)); err == nil {
		return hashDir, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, ".tmp-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, n := range names {
		dst := filepath.Join(tmp, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, files[n], 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, contentAddressedMarker), nil, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, hashDir); err != nil {
		if _, serr := os.Stat(filepath.Join(hashDir, contentAddressedMarker)); serr == nil {
			return hashDir, nil
		}
		return "", err
	}
	return hashDir, nil
}

func argusCacheDir(kind string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	return filepath.Join(home, ".argus", "cache", kind), nil
}
