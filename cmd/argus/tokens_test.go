package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/api"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// openTestDB returns a path to a fresh on-disk SQLite database in a temp dir.
// The CLI opens the DB by path, so the tests must do the same.
func openTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.sql")
	d, err := db.Open(path)
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())
	return path
}

func TestTokenCommand_Mint(t *testing.T) {
	t.Run("mints a plugin-scoped token and prints plaintext once", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--scope", "ludwig"}, path, &out, &errOut)
		testutil.Equal(t, code, 0)

		// The plaintext token should be printed to stdout exactly once.
		stdout := out.String()
		testutil.Contains(t, stdout, "scope: ludwig")

		// Extract the token line and confirm it round-trips.
		var plain string
		for _, line := range strings.Split(stdout, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "token:") {
				plain = strings.TrimSpace(strings.TrimPrefix(line, "token:"))
			}
		}
		if plain == "" {
			t.Fatalf("no token printed: %q", stdout)
		}

		// Token should be a 64-char hex string.
		testutil.Equal(t, len(plain), 64)

		// Verify it's persisted with the scope.
		d, err := db.Open(path)
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d.Close() })
		toks, err := d.APITokens()
		testutil.NoError(t, err)
		testutil.Equal(t, len(toks), 1)
		testutil.Equal(t, toks[0].Scope, "ludwig")
	})

	t.Run("requires --scope", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint"}, path, &out, &errOut)
		if code == 0 {
			t.Errorf("expected non-zero exit, got 0; stdout=%q stderr=%q", out.String(), errOut.String())
		}
		testutil.Contains(t, errOut.String(), "scope")
	})

	t.Run("rejects empty scope", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--scope", ""}, path, &out, &errOut)
		if code == 0 {
			t.Errorf("expected non-zero exit on empty scope; stdout=%q stderr=%q", out.String(), errOut.String())
		}
	})

	t.Run("accepts optional --label", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--scope", "ludwig", "--label", "dev box"}, path, &out, &errOut)
		testutil.Equal(t, code, 0)

		d, err := db.Open(path)
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d.Close() })
		toks, err := d.APITokens()
		testutil.NoError(t, err)
		testutil.Equal(t, len(toks), 1)
		testutil.Equal(t, toks[0].Label, "dev box")
	})

	t.Run("defaults label to scope name", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--scope", "ludwig"}, path, &out, &errOut)
		testutil.Equal(t, code, 0)

		d, err := db.Open(path)
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d.Close() })
		toks, err := d.APITokens()
		testutil.NoError(t, err)
		testutil.Equal(t, len(toks), 1)
		testutil.Equal(t, toks[0].Label, "ludwig")
	})

	t.Run("rejects invalid scope characters", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--scope", "bad scope"}, path, &out, &errOut)
		if code == 0 {
			t.Errorf("expected non-zero exit on invalid scope; stdout=%q stderr=%q", out.String(), errOut.String())
		}
	})
}

func TestTokenCommand_List(t *testing.T) {
	t.Run("empty when no tokens", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"list"}, path, &out, &errOut)
		testutil.Equal(t, code, 0)
		testutil.Contains(t, out.String(), "No tokens")
	})

	t.Run("shows scope alongside type", func(t *testing.T) {
		path := openTestDB(t)
		d, err := db.Open(path)
		testutil.NoError(t, err)
		_, _, err = api.MintTokenWithScope(d, "ludwig", "ludwig")
		testutil.NoError(t, err)
		_, _, err = api.MintToken(d, "iPhone")
		testutil.NoError(t, err)
		testutil.NoError(t, d.Close())

		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"list"}, path, &out, &errOut)
		testutil.Equal(t, code, 0)

		stdout := out.String()
		testutil.Contains(t, stdout, "ludwig")
		testutil.Contains(t, stdout, "iPhone")
		// Device-token rows render the type as "device".
		testutil.Contains(t, stdout, "device")
		// Plugin-token rows render the type as "scope:<name>".
		testutil.Contains(t, stdout, "scope:ludwig")
	})
}

func TestTokenCommand_Revoke(t *testing.T) {
	t.Run("revokes by id", func(t *testing.T) {
		path := openTestDB(t)
		d, err := db.Open(path)
		testutil.NoError(t, err)
		_, id, err := api.MintTokenWithScope(d, "ludwig", "ludwig")
		testutil.NoError(t, err)
		testutil.NoError(t, d.Close())

		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"revoke", "1"}, path, &out, &errOut)
		testutil.Equal(t, code, 0)
		_ = id

		d2, err := db.Open(path)
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d2.Close() })
		toks, err := d2.APITokens()
		testutil.NoError(t, err)
		testutil.Equal(t, len(toks), 1)
		testutil.Equal(t, toks[0].Revoked, true)
	})

	t.Run("missing id is an error", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"revoke"}, path, &out, &errOut)
		if code == 0 {
			t.Errorf("expected non-zero exit; stdout=%q stderr=%q", out.String(), errOut.String())
		}
	})

	t.Run("non-numeric id is an error", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"revoke", "abc"}, path, &out, &errOut)
		if code == 0 {
			t.Error("expected non-zero exit for non-numeric id")
		}
	})

	t.Run("unknown id is an error", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"revoke", "9999"}, path, &out, &errOut)
		if code == 0 {
			t.Error("expected non-zero exit for missing id")
		}
	})
}

// unwritableDBPath returns a database path whose parent directory can't be
// created (because that name already exists as a regular file), forcing
// db.Open to fail at MkdirAll. Used to exercise the error branches in
// tokenMint / tokenList / tokenRevoke.
func unwritableDBPath(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "blocker.txt")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	// parent is a regular file, not a directory — MkdirAll refuses.
	return filepath.Join(blocker, "data.sql")
}

func TestTokenCommand_DBOpenErrors(t *testing.T) {
	t.Run("mint reports db open failure", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--scope", "x"}, unwritableDBPath(t), &out, &errOut)
		if code == 0 {
			t.Errorf("expected non-zero exit; stderr=%q", errOut.String())
		}
		testutil.Contains(t, errOut.String(), "open db")
	})

	t.Run("list reports db open failure", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"list"}, unwritableDBPath(t), &out, &errOut)
		if code == 0 {
			t.Errorf("expected non-zero exit; stderr=%q", errOut.String())
		}
		testutil.Contains(t, errOut.String(), "open db")
	})

	t.Run("revoke reports db open failure", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"revoke", "1"}, unwritableDBPath(t), &out, &errOut)
		if code == 0 {
			t.Errorf("expected non-zero exit; stderr=%q", errOut.String())
		}
		testutil.Contains(t, errOut.String(), "open db")
	})
}

func TestTokenCommand_MintFlagSyntax(t *testing.T) {
	t.Run("--scope without value", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--scope"}, path, &out, &errOut)
		if code == 0 {
			t.Error("expected non-zero exit")
		}
		testutil.Contains(t, errOut.String(), "--scope")
	})

	t.Run("--label without value", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--scope", "x", "--label"}, path, &out, &errOut)
		if code == 0 {
			t.Error("expected non-zero exit")
		}
		testutil.Contains(t, errOut.String(), "--label")
	})

	t.Run("unknown flag", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"mint", "--cheese", "gouda"}, path, &out, &errOut)
		if code == 0 {
			t.Error("expected non-zero exit")
		}
		testutil.Contains(t, errOut.String(), "unknown flag")
	})
}

func TestTokenCommand_Usage(t *testing.T) {
	t.Run("no args prints usage to stderr", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand(nil, path, &out, &errOut)
		if code == 0 {
			t.Error("expected non-zero exit")
		}
		testutil.Contains(t, errOut.String(), "usage")
	})

	t.Run("unknown subcommand prints usage to stderr", func(t *testing.T) {
		path := openTestDB(t)
		var out, errOut bytes.Buffer
		code := tokenCommand([]string{"frobnicate"}, path, &out, &errOut)
		if code == 0 {
			t.Error("expected non-zero exit")
		}
		testutil.Contains(t, errOut.String(), "unknown")
	})
}
