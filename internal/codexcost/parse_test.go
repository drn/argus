package codexcost

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/pricing"
	"github.com/drn/argus/internal/testutil"
)

func readFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/rollout.jsonl")
	testutil.NoError(t, err)
	return raw
}

func freshCursor() db.CodexCostCursor { return db.CodexCostCursor{RolloutPath: "/r", LastTotal: -1} }

func TestParse_Fixture(t *testing.T) {
	raw := readFixture(t)
	calls, next, err := Parse(bytes.NewReader(raw), freshCursor())
	testutil.NoError(t, err)

	// Two billable calls: the duplicate, null-info, non-json, and
	// token_usage_record lines are skipped.
	testutil.Equal(t, len(calls), 2)

	first := calls[0]
	testutil.Equal(t, first.Model, "gpt-a")
	testutil.DeepEqual(t, first.Deltas, pricing.TokenDeltas{Input: 400, CacheRead: 600, Output: 50})
	testutil.Equal(t, first.InputTokens, int64(1000))
	testutil.Equal(t, first.At, time.Date(2026, 10, 31, 2, 0, 56, 495_000_000, time.UTC))

	second := calls[1]
	testutil.Equal(t, second.Model, "gpt-b")
	// Reasoning tokens are already inside output_tokens: 500, not 700.
	testutil.DeepEqual(t, second.Deltas, pricing.TokenDeltas{Input: 10_000, CacheRead: 290_000, CacheWrite5m: 100, Output: 500})
	testutil.Equal(t, second.InputTokens, int64(300_000))

	// The torn final line is not consumed.
	tornStart := int64(bytes.LastIndexByte(raw, '\n') + 1)
	testutil.Equal(t, next.Offset, tornStart)
	testutil.Equal(t, next.LastTotal, int64(301_550))
	testutil.Equal(t, next.Model, "gpt-b")
	testutil.Equal(t, next.RolloutPath, "/r")
}

func TestParse_TornLineCompletesNextSweep(t *testing.T) {
	raw := readFixture(t)
	_, cur, err := Parse(bytes.NewReader(raw), freshCursor())
	testutil.NoError(t, err)

	completed := append(append([]byte{}, raw...), []byte(`0000,"cache_write_input_tokens":0,"output_tokens":600,"reasoning_output_tokens":0,"total_tokens":302600},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":50,"reasoning_output_tokens":0,"total_tokens":1050}}}}`+"\n")...)
	calls, next, err := Parse(bytes.NewReader(completed[cur.Offset:]), cur)
	testutil.NoError(t, err)
	testutil.Equal(t, len(calls), 1)
	testutil.Equal(t, calls[0].Model, "gpt-b") // carried from the cursor
	testutil.Equal(t, next.Offset, int64(len(completed)))
}

func TestParse_SplitReadsMatchSingleRead(t *testing.T) {
	raw := readFixture(t)
	all, _, err := Parse(bytes.NewReader(raw), freshCursor())
	testutil.NoError(t, err)

	// Cut right after the first token_count line: the identical re-emitted
	// event that follows must still be de-duplicated via the cursor.
	cut := bytes.Index(raw, []byte(`"ordinal":20`))
	cut = bytes.LastIndexByte(raw[:cut], '\n') + 1
	a, cur, err := Parse(bytes.NewReader(raw[:cut]), freshCursor())
	testutil.NoError(t, err)
	b, _, err := Parse(bytes.NewReader(raw[cut:]), cur)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, append(a, b...), all)
}

func TestParse_EmptyAndGarbage(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"blank lines", "\n\n"},
		{"garbage", "not json\n{\"type\":\"event_msg\"}\n"},
		{"token_count missing last usage", `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":5}}}}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, next, err := Parse(strings.NewReader(tc.in), freshCursor())
			testutil.NoError(t, err)
			testutil.Equal(t, len(calls), 0)
			testutil.Equal(t, next.Offset, int64(len(tc.in)))
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestParse_ReadErrorSurfaces(t *testing.T) {
	_, _, err := Parse(errReader{}, freshCursor())
	testutil.True(t, err != nil)
}

func TestParse_TurnContextWithoutModelKeepsPrevious(t *testing.T) {
	in := `{"type":"turn_context","payload":{"model":"gpt-a"}}` + "\n" +
		`{"type":"turn_context","payload":{}}` + "\n"
	_, next, err := Parse(strings.NewReader(in), freshCursor())
	testutil.NoError(t, err)
	testutil.Equal(t, next.Model, "gpt-a")
}
