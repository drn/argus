package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"text/tabwriter"

	"github.com/drn/argus/internal/api"
	"github.com/drn/argus/internal/db"
)

// scopeRe constrains scope identifiers to safe ASCII so they round-trip
// cleanly into the `X-Argus-Auth: scope:<name>` header and into the MCP tool
// name prefix (`<scope>_<tool>`) that PR 4 will enforce.
var scopeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// runTokenCommand handles `argus token <subcommand> [args...]`. Wraps
// tokenCommand so main.go can keep its existing thin dispatch.
func runTokenCommand(args []string) {
	os.Exit(tokenCommand(args, db.DefaultPath(), os.Stdout, os.Stderr))
}

// tokenCommand implements the `argus token ...` CLI. Split out from
// runTokenCommand so tests can drive it with an in-memory DB path and capture
// stdout/stderr. Returns the process exit code.
//
// Subcommands:
//   - `mint --scope <name> [--label <label>]` — mint a plugin token; prints the
//     plaintext once.
//   - `list` — list every token with type (master|device|scope:<name>) and
//     basic metadata.
//   - `revoke <id>` — revoke the row with the given id.
//
// The CLI talks to the SQLite database directly (WAL mode allows a writer
// while the daemon holds reads/writes). It does NOT require the daemon to be
// running, which matters for first-run plugin setup where the daemon may not
// be installed yet.
func tokenCommand(args []string, dbPath string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fprintln(errOut, "usage: argus token <mint|list|revoke> [args...]")
		return 1
	}
	switch args[0] {
	case "mint":
		return tokenMint(args[1:], dbPath, out, errOut)
	case "list":
		return tokenList(args[1:], dbPath, out, errOut)
	case "revoke":
		return tokenRevoke(args[1:], dbPath, out, errOut)
	default:
		fprintf(errOut, "unknown token subcommand: %s\n", args[0])
		return 1
	}
}

// fprintf/fprintln wrap fmt.Fprintf/Fprintln so the error returns can be
// discarded without sprinkling `_, _ =` across every diagnostic write. CLI
// output to stdout/stderr never recovers from a write error in a useful way.
func fprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func fprintln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, a...)
}

func tokenMint(args []string, dbPath string, out, errOut io.Writer) int {
	var scope, label string
	scopeSet := false
	for len(args) > 0 {
		head := args[0]
		switch head {
		case "--scope":
			if len(args) < 2 {
				fprintln(errOut, "--scope requires a value")
				return 1
			}
			scope = args[1]
			scopeSet = true
			args = args[2:]
		case "--label":
			if len(args) < 2 {
				fprintln(errOut, "--label requires a value")
				return 1
			}
			label = args[1]
			args = args[2:]
		default:
			fprintf(errOut, "unknown flag: %s\n", head)
			return 1
		}
	}
	if !scopeSet {
		fprintln(errOut, "usage: argus token mint --scope <name> [--label <label>]")
		return 1
	}
	if !scopeRe.MatchString(scope) {
		fprintln(errOut, "invalid scope: must match [a-z0-9][a-z0-9_-]{0,63}")
		return 1
	}
	if label == "" {
		label = scope
	}

	d, err := db.Open(dbPath)
	if err != nil {
		fprintf(errOut, "open db: %v\n", err)
		return 1
	}
	defer d.Close() //nolint:errcheck

	plain, id, err := api.MintTokenWithScope(d, label, scope)
	if err != nil {
		fprintf(errOut, "mint: %v\n", err)
		return 1
	}
	fprintf(out, "id:    %d\n", id)
	fprintf(out, "scope: %s\n", scope)
	fprintf(out, "label: %s\n", label)
	fprintf(out, "token: %s\n", plain)
	fprintln(out, "")
	fprintln(out, "Store this token now — it will not be shown again.")
	return 0
}

func tokenList(_ []string, dbPath string, out, errOut io.Writer) int {
	d, err := db.Open(dbPath)
	if err != nil {
		fprintf(errOut, "open db: %v\n", err)
		return 1
	}
	defer d.Close() //nolint:errcheck

	toks, err := d.APITokens()
	if err != nil {
		fprintf(errOut, "list: %v\n", err)
		return 1
	}
	if len(toks) == 0 {
		fprintln(out, "No tokens. Mint one with: argus token mint --scope <name>")
		return 0
	}

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fprintln(tw, "ID\tTYPE\tLABEL\tLAST4\tCREATED\tLAST USED\tREVOKED")
	for _, t := range toks {
		typ := "device"
		if t.Scope != "" {
			typ = "scope:" + t.Scope
		}
		lastUsed := "-"
		if !t.LastUsed.IsZero() {
			lastUsed = t.LastUsed.Format("2006-01-02 15:04")
		}
		fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%v\n",
			t.ID, typ, t.Label, t.Last4,
			t.CreatedAt.Format("2006-01-02 15:04"), lastUsed, t.Revoked)
	}
	_ = tw.Flush()
	return 0
}

func tokenRevoke(args []string, dbPath string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fprintln(errOut, "usage: argus token revoke <id>")
		return 1
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		fprintf(errOut, "invalid id: %s\n", args[0])
		return 1
	}
	d, err := db.Open(dbPath)
	if err != nil {
		fprintf(errOut, "open db: %v\n", err)
		return 1
	}
	defer d.Close() //nolint:errcheck

	if err := d.RevokeAPIToken(id); err != nil {
		fprintf(errOut, "revoke: %v\n", err)
		return 1
	}
	fprintf(out, "revoked %d\n", id)
	return 0
}
