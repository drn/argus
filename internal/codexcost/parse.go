// Package codexcost accrues hera cost for Codex-backend sessions
// (add-codex-cost-accrual). Codex never runs Argus's Claude Stop hook or
// bridge mod, so the daemon instead reads the token usage Codex itself records
// in its rollout files and prices every API call at the current rate table.
package codexcost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/pricing"
)

// Call is one billable Codex API call, read from a rollout token_count event.
type Call struct {
	At    time.Time
	Model string
	// Deltas maps the call's usage onto the rate classes: uncached input,
	// cached input (CacheRead), cache writes (CacheWrite5m — Codex has a
	// single cache-write class), and output (reasoning already included).
	Deltas pricing.TokenDeltas
	// InputTokens is the call's total prompt size, cached included — what a
	// long-context tier threshold is compared against.
	InputTokens int64
}

var (
	turnContextMarker = []byte(`"turn_context"`)
	tokenCountMarker  = []byte(`"token_count"`)
)

type usage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type rolloutLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type  string `json:"type"`
		Model string `json:"model"`
		Info  *struct {
			Last  *usage `json:"last_token_usage"`
			Total *usage `json:"total_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// Parse reads r — a rollout file positioned at cur.Offset — and returns every
// new billable call plus the advanced cursor. Only complete lines are
// consumed: a trailing line without its newline (Codex mid-write) is left for
// the next sweep. Codex re-emits an identical token_count event after some
// turns, so an event whose running total_tokens equals the previous one
// (cur.LastTotal carries it across sweeps) is skipped. Each call is attributed
// to the model of the most recent turn_context, which cur.Model carries across
// sweeps when that turn_context sits before cur.Offset. Unparseable and
// irrelevant lines are skipped, never an error; only a read error is.
func Parse(r io.Reader, cur db.CodexCostCursor) ([]Call, db.CodexCostCursor, error) {
	br := bufio.NewReaderSize(r, 256*1024)
	var calls []Call
	for {
		line, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return calls, cur, err
		}
		if len(line) == 0 || line[len(line)-1] != '\n' {
			// EOF, possibly with a torn final line: leave it unconsumed.
			return calls, cur, nil
		}
		cur.Offset += int64(len(line))
		if call, ok := parseLine(line, &cur); ok {
			calls = append(calls, call)
		}
	}
}

func parseLine(line []byte, cur *db.CodexCostCursor) (Call, bool) {
	isTurn := bytes.Contains(line, turnContextMarker)
	if !isTurn && !bytes.Contains(line, tokenCountMarker) {
		return Call{}, false
	}
	var l rolloutLine
	if json.Unmarshal(line, &l) != nil {
		return Call{}, false
	}
	switch {
	case l.Type == "turn_context":
		if l.Payload.Model != "" {
			cur.Model = l.Payload.Model
		}
		return Call{}, false
	case l.Type == "event_msg" && l.Payload.Type == "token_count":
		info := l.Payload.Info
		if info == nil || info.Last == nil || info.Total == nil {
			return Call{}, false
		}
		if info.Total.TotalTokens == cur.LastTotal {
			return Call{}, false
		}
		cur.LastTotal = info.Total.TotalTokens
		u := info.Last
		at, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
		return Call{
			At:    at,
			Model: cur.Model,
			Deltas: pricing.TokenDeltas{
				Input:        max(u.InputTokens-u.CachedInputTokens, 0),
				CacheRead:    u.CachedInputTokens,
				CacheWrite5m: u.CacheWriteInputTokens,
				Output:       u.OutputTokens,
			},
			InputTokens: u.InputTokens,
		}, true
	}
	return Call{}, false
}
