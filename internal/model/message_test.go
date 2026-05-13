package model

import "testing"

func TestValidMessageKind(t *testing.T) {
	cases := []struct {
		kind MessageKind
		want bool
	}{
		{KindNote, true},
		{KindQuestion, true},
		{KindAnswer, true},
		{"", false},
		{"bogus", false},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			if got := ValidMessageKind(c.kind); got != c.want {
				t.Errorf("ValidMessageKind(%q) = %v, want %v", c.kind, got, c.want)
			}
		})
	}
}

func TestTaskMessage_Validate(t *testing.T) {
	cases := []struct {
		name    string
		msg     TaskMessage
		wantErr bool
	}{
		{
			name: "happy note",
			msg:  TaskMessage{From: "A", To: "B", Kind: KindNote, Body: "hi"},
		},
		{
			name:    "missing from",
			msg:     TaskMessage{To: "B", Kind: KindNote, Body: "hi"},
			wantErr: true,
		},
		{
			name:    "missing to",
			msg:     TaskMessage{From: "A", Kind: KindNote, Body: "hi"},
			wantErr: true,
		},
		{
			name:    "missing body",
			msg:     TaskMessage{From: "A", To: "B", Kind: KindNote},
			wantErr: true,
		},
		{
			name:    "invalid kind",
			msg:     TaskMessage{From: "A", To: "B", Kind: "bogus", Body: "x"},
			wantErr: true,
		},
		{
			name:    "answer without in_reply_to",
			msg:     TaskMessage{From: "A", To: "B", Kind: KindAnswer, Body: "x"},
			wantErr: true,
		},
		{
			name: "answer with in_reply_to",
			msg:  TaskMessage{From: "A", To: "B", Kind: KindAnswer, Body: "x", InReplyTo: "q1"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.msg.Validate()
			if (err != nil) != c.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}
