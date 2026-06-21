package gitutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/drn/argus/internal/model"
)

// PRResult is the per-branch outcome of a batched GraphQL PR-state lookup.
// It mirrors the (state, url) pair FetchPRState returns for a single branch,
// minus the error — a batched query either succeeds for the whole group
// (per-branch results) or fails wholesale (the caller keeps every cached
// value stale; see Decision 4 in the change design doc).
type PRResult struct {
	State model.PRState
	URL   string
}

// prGraphQLRunner is the test seam for executing `gh api graphql`, mirroring
// the prRunner seam used by FetchPRState. The real implementation writes the
// query to a temp file (avoiding argv limits on large aliased queries) and
// invokes `gh api graphql -F query=@<file>`, returning the combined
// stdout+stderr output, the exit code (0 on success), and any exec-level
// error. Tests swap this variable to inject canned output without spawning a
// process or touching the network.
var prGraphQLRunner = func(ctx context.Context, dir string, args ...string) (string, int, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", 0, errGhAbsent
	}
	// Use the literal "gh" (not the LookPath-resolved path) as the command name
	// so the binary is a constant — exec.CommandContext does its own PATH
	// resolution. A variable command name trips gosec G204; the literal keeps
	// it constant while args stay a fixed call-site list.
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return buf.String(), exitCode, runErr
}

// graphQLConnection is the pullRequests connection shape we read per alias.
type graphQLConnection struct {
	Nodes []prJSON `json:"nodes"`
}

// graphQLBatchResponse is the subset of the `gh api graphql` JSON envelope we
// parse. The `repo` object's keys are the per-branch aliases (sanitized task
// ids) supplied by the caller; json.Unmarshal into a map preserves them so we
// can map each alias back to its branch.
type graphQLBatchResponse struct {
	Data struct {
		RateLimit struct {
			Cost      int `json:"cost"`
			Remaining int `json:"remaining"`
		} `json:"rateLimit"`
		Repo map[string]json.RawMessage `json:"repo"`
	} `json:"data"`
}

// FetchPRStatesBatch resolves PR state for every branch in branches with a
// single aliased GraphQL query against repo ("owner/name"). branches maps each
// branch name to its alias id (already sanitized by the caller into a valid
// GraphQL identifier); the returned map is keyed by branch name.
//
// Decision 1/3 (design.md): one query per repo group, billed at ~1 GraphQL
// complexity point regardless of branch count. Chunking a group that exceeds
// the alias cap is the caller's (daemon's) responsibility — this primitive
// issues exactly one query for whatever branches it is handed.
//
// Return contract:
//   - empty branches → (empty map, nil), no query issued.
//   - whole-query transport/JSON error → (nil, err); the caller keeps every
//     cached value in the group stale (Decision 4).
//   - success → map[branch]PRResult; an empty nodes array maps to PRNone, the
//     state mapping reusing the same table as FetchPRState (mapPRState).
func FetchPRStatesBatch(ctx context.Context, repo string, branches map[string]string) (map[string]PRResult, error) {
	out := map[string]PRResult{}
	if len(branches) == 0 {
		return out, nil
	}

	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil, fmt.Errorf("invalid repo %q (want owner/name)", repo)
	}

	query := buildBatchQuery(owner, name, branches)

	const timeout = 10 * time.Second
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tmp, err := os.CreateTemp("", "argus-prquery-*.graphql")
	if err != nil {
		return nil, fmt.Errorf("create graphql query temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.WriteString(query); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("write graphql query temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close graphql query temp file: %w", err)
	}

	raw, code, runErr := prGraphQLRunner(fetchCtx, "", "api", "graphql", "-F", "query=@"+tmpPath)
	if runErr != nil {
		return nil, fmt.Errorf("gh api graphql: %w", runErr)
	}
	if code != 0 {
		return nil, fmt.Errorf("gh api graphql exited %d: %s", code, strings.TrimSpace(raw))
	}

	var resp graphQLBatchResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("parse graphql json: %w", err)
	}

	// Invert branch→alias so we can map each alias key back to its branch.
	aliasToBranch := make(map[string]string, len(branches))
	for branch, alias := range branches {
		aliasToBranch[alias] = branch
	}

	for alias, rawConn := range resp.Data.Repo {
		branch, known := aliasToBranch[alias]
		if !known {
			continue
		}
		var conn graphQLConnection
		if err := json.Unmarshal(rawConn, &conn); err != nil {
			return nil, fmt.Errorf("parse graphql alias %q: %w", alias, err)
		}
		out[branch] = mapBatchNode(conn.Nodes)
	}

	return out, nil
}

// mapBatchNode converts a pullRequests connection's nodes into a PRResult,
// reusing the exact state-mapping table FetchPRState relies on via
// mapPRStateFromJSON. An empty nodes slice is an authoritative "no PR" → PRNone.
func mapBatchNode(nodes []prJSON) PRResult {
	if len(nodes) == 0 {
		return PRResult{State: model.PRNone}
	}
	state, url := mapPRStateFromJSON(nodes[0])
	return PRResult{State: state, URL: url}
}

// buildBatchQuery assembles the single aliased GraphQL query for a repo group.
// Aliases are emitted in sorted order for deterministic output (stable tests
// and logs). Each alias is a sanitized task id the caller guarantees is a
// valid GraphQL identifier.
func buildBatchQuery(owner, name string, branches map[string]string) string {
	type pair struct{ branch, alias string }
	pairs := make([]pair, 0, len(branches))
	for branch, alias := range branches {
		pairs = append(pairs, pair{branch: branch, alias: alias})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].alias < pairs[j].alias })

	var b strings.Builder
	b.WriteString("query {\n")
	b.WriteString("  rateLimit { cost remaining }\n")
	fmt.Fprintf(&b, "  repo: repository(owner: %q, name: %q) {\n", owner, name)
	for _, p := range pairs {
		fmt.Fprintf(&b,
			"    %s: pullRequests(headRefName: %q, first: 1, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { state isDraft reviewDecision url } }\n",
			p.alias, p.branch)
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// splitRepo splits "owner/name" into its parts. Returns false for anything that
// is not exactly two non-empty slash-separated segments.
func splitRepo(repo string) (owner, name string, ok bool) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
