package settings

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestParseSection_FormSpecEnvelope(t *testing.T) {
	raw := []byte(`{
		"title": "Hello",
		"type": "form",
		"callback_url": "http://127.0.0.1:9000/save",
		"spec": {"fields": [{"key": "enabled", "label": "Enabled", "type": "bool", "default": true}]}
	}`)
	got, err := ParseSection("test-plugin", raw)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Scope, "test-plugin")
	testutil.Equal(t, got.Title, "Hello")
	testutil.Equal(t, got.Type, TypeForm)
	testutil.Equal(t, got.CallbackURL, "http://127.0.0.1:9000/save")
	testutil.Equal(t, len(got.Spec.Fields), 1)
	testutil.Equal(t, got.Spec.Fields[0].Type, FieldBool)
	testutil.Equal(t, got.Spec.Fields[0].DefaultValue().(bool), true)
}

func TestParseSection_InlineFieldsArray(t *testing.T) {
	// The plan's example shape inlines `fields` next to `type`, no `spec`
	// envelope. Both shapes must parse identically.
	raw := []byte(`{
		"title": "Plugin name",
		"type": "form",
		"fields": [
			{"key": "interval", "label": "Check interval (s)", "type": "int", "min": 60, "max": 3600, "default": 300},
			{"key": "enabled", "label": "Enabled", "type": "bool", "default": true},
			{"key": "backend", "label": "Default backend", "type": "enum", "options": ["claude", "codex"], "default": "claude"},
			{"key": "label", "label": "Display label", "type": "string", "default": ""}
		],
		"callback_url": "http://127.0.0.1:9000/save"
	}`)
	got, err := ParseSection("scope", raw)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got.Spec.Fields), 4)
	// Field-type round-trip via DefaultValue.
	testutil.Equal(t, got.Spec.Fields[0].DefaultValue().(int), 300)
	testutil.Equal(t, got.Spec.Fields[1].DefaultValue().(bool), true)
	testutil.Equal(t, got.Spec.Fields[2].DefaultValue().(string), "claude")
	testutil.Equal(t, got.Spec.Fields[3].DefaultValue().(string), "")
}

// TestParseSection_AuthHeader covers the optional auth_header field — when
// present, it MUST round-trip onto Section.AuthHeader so the callback proxy
// can forward it as the Authorization header.
func TestParseSection_AuthHeader(t *testing.T) {
	raw := []byte(`{
		"title": "Hello",
		"callback_url": "http://127.0.0.1/save",
		"auth_header": "Bearer s3cr3t",
		"fields": [{"key":"k","label":"l","type":"bool","default":false}]
	}`)
	got, err := ParseSection("scope", raw)
	testutil.NoError(t, err)
	testutil.Equal(t, got.AuthHeader, "Bearer s3cr3t")

	// Omitted is zero-value (no auth header set).
	raw = []byte(`{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"bool"}]}`)
	got, err = ParseSection("scope", raw)
	testutil.NoError(t, err)
	testutil.Equal(t, got.AuthHeader, "")
}

func TestParseSection_DefaultTypeIsForm(t *testing.T) {
	// Omitting `type` should default to "form" (most concise plugin shape).
	raw := []byte(`{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"bool"}]}`)
	got, err := ParseSection("scope", raw)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Type, TypeForm)
}

func TestParseSection_StreamMinimal(t *testing.T) {
	// Stream sections have title + type + callback_url, no fields.
	raw := []byte(`{"title":"Live orchestrators","type":"stream","callback_url":"ws://127.0.0.1:9991/live"}`)
	got, err := ParseSection("ludwig", raw)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Scope, "ludwig")
	testutil.Equal(t, got.Title, "Live orchestrators")
	testutil.Equal(t, got.Type, TypeStream)
	testutil.Equal(t, got.CallbackURL, "ws://127.0.0.1:9991/live")
	testutil.Nil(t, got.Spec)
}

func TestParseSection_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		scope   string
		raw     string
		wantErr error
	}{
		{
			name:    "missing scope",
			scope:   "",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"bool"}]}`,
			wantErr: ErrInvalidScope,
		},
		{
			name:    "missing title",
			scope:   "s",
			raw:     `{"callback_url":"http://x","fields":[{"key":"k","label":"l","type":"bool"}]}`,
			wantErr: ErrInvalidTitle,
		},
		{
			name:    "stream type with fields rejected",
			scope:   "s",
			raw:     `{"title":"x","type":"stream","callback_url":"ws://x","fields":[{"key":"k","label":"l","type":"bool"}]}`,
			wantErr: ErrStreamHasFields,
		},
		{
			name:    "unknown type rejected",
			scope:   "s",
			raw:     `{"title":"x","type":"wat","callback_url":"http://x"}`,
			wantErr: ErrInvalidType,
		},
		{
			name:    "missing callback url",
			scope:   "s",
			raw:     `{"title":"x","fields":[{"key":"k","label":"l","type":"bool"}]}`,
			wantErr: ErrMissingCallbackURL,
		},
		{
			name:    "no fields",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[]}`,
			wantErr: ErrEmptyForm,
		},
		{
			name:    "field missing key",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"label":"l","type":"bool"}]}`,
			wantErr: ErrFieldMissingKey,
		},
		{
			name:    "duplicate field key",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"a","type":"bool"},{"key":"k","label":"b","type":"int"}]}`,
			wantErr: ErrFieldDuplicateKey,
		},
		{
			name:    "field missing label",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","type":"bool"}]}`,
			wantErr: ErrFieldMissingLabel,
		},
		{
			name:    "field invalid type",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"wat"}]}`,
			wantErr: ErrFieldInvalidType,
		},
		{
			name:    "enum without options",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"enum"}]}`,
			wantErr: ErrFieldEnumNoOptions,
		},
		{
			name:    "int bounds inverted",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"int","min":10,"max":5}]}`,
			wantErr: ErrFieldIntBounds,
		},
		{
			name:    "bool default has wrong type",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"bool","default":"yes"}]}`,
			wantErr: ErrFieldDefaultBadType,
		},
		{
			name:    "int default has wrong type",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"int","default":true}]}`,
			wantErr: ErrFieldDefaultBadType,
		},
		{
			name:    "int default has fractional value",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"int","default":1.5}]}`,
			wantErr: ErrFieldDefaultBadType,
		},
		{
			name:    "string default has wrong type",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"string","default":123}]}`,
			wantErr: ErrFieldDefaultBadType,
		},
		{
			name:    "enum default not in options",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"enum","options":["a","b"],"default":"c"}]}`,
			wantErr: ErrFieldDefaultBadType,
		},
		{
			name:    "enum default not a string",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","fields":[{"key":"k","label":"l","type":"enum","options":["a","b"],"default":1}]}`,
			wantErr: ErrFieldDefaultBadType,
		},
		{
			name:    "spec envelope invalid JSON",
			scope:   "s",
			raw:     `{"title":"x","callback_url":"http://x","spec":"notjson"}`,
			wantErr: nil, // matched by substring check
		},
		{
			name:    "top-level invalid JSON",
			scope:   "s",
			raw:     `{this is not json`,
			wantErr: nil, // matched by substring check
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSection(tc.scope, []byte(tc.raw))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
			if tc.wantErr == nil && !strings.Contains(err.Error(), "parse") {
				t.Fatalf("expected parse error, got %v", err)
			}
		})
	}
}

func TestParseSection_IntDefaultFromString(t *testing.T) {
	raw := []byte(`{"title":"x","callback_url":"http://x","fields":[{"key":"n","label":"l","type":"int","default":"42"}]}`)
	got, err := ParseSection("s", raw)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Spec.Fields[0].DefaultValue().(int), 42)
}

func TestParseSection_IntDefaultBadString(t *testing.T) {
	raw := []byte(`{"title":"x","callback_url":"http://x","fields":[{"key":"n","label":"l","type":"int","default":"not-a-number"}]}`)
	_, err := ParseSection("s", raw)
	if !errors.Is(err, ErrFieldDefaultBadType) {
		t.Fatalf("expected ErrFieldDefaultBadType, got %v", err)
	}
}

func TestFieldType_IsValid(t *testing.T) {
	cases := []struct {
		t    FieldType
		want bool
	}{
		{FieldBool, true},
		{FieldInt, true},
		{FieldString, true},
		{FieldEnum, true},
		{FieldType(""), false},
		{FieldType("wat"), false},
	}
	for _, c := range cases {
		testutil.Equal(t, c.t.IsValid(), c.want)
	}
}

func TestFormField_DefaultValueZeros(t *testing.T) {
	// Default missing → zero value per type.
	for _, c := range []struct {
		field FormField
		want  any
	}{
		{FormField{Type: FieldBool}, false},
		{FormField{Type: FieldInt}, 0},
		{FormField{Type: FieldString}, ""},
		{FormField{Type: FieldEnum}, ""},
	} {
		got := c.field.DefaultValue()
		testutil.Equal(t, got, c.want)
	}
}

func TestFormField_DefaultValueIgnoresMismatch(t *testing.T) {
	// DefaultValue is the runtime-safe accessor; even when the stored default
	// somehow doesn't match the declared type (which validation rejects but
	// could happen via Replace seeding a corrupt section), it falls back to
	// zero rather than panicking.
	got := (&FormField{Type: FieldBool, Default: "not a bool"}).DefaultValue()
	testutil.Equal(t, got.(bool), false)
}

func TestFormField_DefaultValueUnknownType(t *testing.T) {
	// Defensive: an unknown FieldType yields nil rather than panicking.
	got := (&FormField{Type: FieldType("unknown")}).DefaultValue()
	if got != nil {
		t.Fatalf("expected nil for unknown type, got %v", got)
	}
}

func TestCoerceInt_Variants(t *testing.T) {
	cases := []struct {
		in   any
		want int
		ok   bool
	}{
		{float64(7), 7, true},
		{float64(7.5), 0, false},
		{int(3), 3, true},
		{int64(9), 9, true},
		{"42", 42, true},
		{"x", 0, false},
		{true, 0, false},
	}
	for _, c := range cases {
		got, ok := coerceInt(c.in)
		testutil.Equal(t, ok, c.ok)
		if ok {
			testutil.Equal(t, got, c.want)
		}
	}
}
