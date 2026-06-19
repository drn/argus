package model

import (
	"encoding/json"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestPRState_String(t *testing.T) {
	for _, tc := range []struct {
		s    PRState
		want string
	}{
		{PRNone, "none"},
		{PRDraft, "draft"},
		{PRAwaitingReview, "awaiting-review"},
		{PRChangesRequested, "changes-requested"},
		{PRApproved, "approved"},
		{PRMergedClosed, "merged-closed"},
		{PRUnknown, "unknown"},
		{PRState(99), "unknown(99)"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			testutil.Equal(t, tc.s.String(), tc.want)
		})
	}
}

func TestPRState_IsTerminal(t *testing.T) {
	for _, tc := range []struct {
		s    PRState
		want bool
	}{
		{PRNone, false},
		{PRDraft, false},
		{PRAwaitingReview, false},
		{PRChangesRequested, false},
		{PRApproved, false},
		{PRMergedClosed, true},
		{PRUnknown, false},
	} {
		t.Run(tc.s.String(), func(t *testing.T) {
			testutil.Equal(t, tc.s.IsTerminal(), tc.want)
		})
	}
}

func TestParsePRState(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    PRState
		wantErr bool
	}{
		{"none", PRNone, false},
		{"draft", PRDraft, false},
		{"awaiting-review", PRAwaitingReview, false},
		{"changes-requested", PRChangesRequested, false},
		{"approved", PRApproved, false},
		{"merged-closed", PRMergedClosed, false},
		{"unknown", PRUnknown, false},
		{"bogus", PRNone, true},
		{"", PRNone, true},
		{"OPEN", PRNone, true},
	} {
		t.Run(tc.input+"_"+boolStr(tc.wantErr), func(t *testing.T) {
			got, err := ParsePRState(tc.input)
			if tc.wantErr {
				testutil.Error(t, err)
			} else {
				testutil.NoError(t, err)
				testutil.Equal(t, got, tc.want)
			}
		})
	}
}

func TestPRState_MarshalUnmarshalText(t *testing.T) {
	for _, s := range []PRState{PRNone, PRDraft, PRAwaitingReview, PRChangesRequested, PRApproved, PRMergedClosed, PRUnknown} {
		t.Run(s.String(), func(t *testing.T) {
			data, err := s.MarshalText()
			testutil.NoError(t, err)
			var got PRState
			testutil.NoError(t, got.UnmarshalText(data))
			testutil.Equal(t, got, s)
		})
	}
}

func TestPRState_UnmarshalText_Invalid(t *testing.T) {
	var s PRState
	testutil.Error(t, s.UnmarshalText([]byte("nope")))
}

func TestPRState_JSONRoundtrip(t *testing.T) {
	type wrapper struct {
		S PRState `json:"pr_state"`
	}
	for _, s := range []PRState{PRNone, PRDraft, PRAwaitingReview, PRChangesRequested, PRApproved, PRMergedClosed, PRUnknown} {
		t.Run(s.String(), func(t *testing.T) {
			w := wrapper{S: s}
			data, err := json.Marshal(w)
			testutil.NoError(t, err)
			// Should serialize as a string, not an integer.
			testutil.Contains(t, string(data), `"`+s.String()+`"`)
			var got wrapper
			testutil.NoError(t, json.Unmarshal(data, &got))
			testutil.Equal(t, got.S, s)
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "err"
	}
	return "ok"
}
