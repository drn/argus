// Package settings owns the plugin-section registry that drives PR 7 of the
// plugin substrate. The registry holds typed [Section] descriptors registered
// by plugins via `POST /api/plugins/settings/sections`; the TUI consumes the
// list when building the Settings rail and renders form fields natively.
//
// Two section types are accepted on the wire:
//
//   - `form` — a typed spec the TUI renders as inline rows. On user save, the
//     `{key: value}` map is POSTed to the plugin's `callback_url`.
//   - `stream` — a WebSocket-backed live block. When the section is focused,
//     argus opens a WebSocket to `callback_url`; the plugin pushes ANSI bytes
//     and the TUI renders them via the streampane widget. Stream sections
//     have no fields — `callback_url` is the only required attribute.
//
// Field rendering rules live in `internal/tui/settings.go`. The package is
// transport-agnostic: it parses, validates, and stores section descriptors;
// it does not speak HTTP and does not touch tcell. Persistence is owned by
// `*db.DB` (the section is round-tripped through the `plugin_settings`
// table) so the TUI and daemon processes see the same set.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// FieldType is the discriminator for a form-section field's value space.
type FieldType string

// Field types accepted in form-section specs. Anything else fails validation.
const (
	FieldBool   FieldType = "bool"
	FieldInt    FieldType = "int"
	FieldString FieldType = "string"
	FieldEnum   FieldType = "enum"
)

// IsValid reports whether t is one of the accepted field types.
func (t FieldType) IsValid() bool {
	switch t {
	case FieldBool, FieldInt, FieldString, FieldEnum:
		return true
	}
	return false
}

// SectionType is the discriminator for a registered section's content.
type SectionType string

// Section content types. Stream is reserved for a future PR (see package doc).
const (
	TypeForm   SectionType = "form"
	TypeStream SectionType = "stream"
)

// FormField is one field in a [FormSpec]. The JSON shape mirrors the
// substrate plan's "Settings section schemas" reference: `key`, `label`,
// `type`, `default`, plus per-type extras (`min`/`max` for int, `options`
// for enum). Unknown fields on the wire are tolerated by encoding/json's
// default behavior — plugins can ship additional metadata without breaking
// older argus builds.
type FormField struct {
	Key     string    `json:"key"`
	Label   string    `json:"label"`
	Type    FieldType `json:"type"`
	Default any       `json:"default,omitempty"`
	// Int-only.
	Min *int `json:"min,omitempty"`
	Max *int `json:"max,omitempty"`
	// Enum-only.
	Options []string `json:"options,omitempty"`
}

// FormSpec is the parsed form-section schema. The `fields` array is the
// ordered list of inputs argus renders; iteration order is preserved on
// parse so the plugin controls layout.
type FormSpec struct {
	Fields []FormField `json:"fields"`
}

// Section is the persisted shape of a registered plugin settings section.
// One row in `plugin_settings`. `Spec` is the typed FormSpec when `Type` is
// `form`; stream sections leave `Spec` nil and only carry `CallbackURL`.
type Section struct {
	// Scope is the plugin scope from the auth token. The TUI groups every
	// section by scope when rendering the "Plugins" header.
	Scope string
	// Title is the human-readable label shown in the rail. Unique per scope —
	// re-registering the same (scope, title) replaces the prior entry. Plugins
	// register at most one section per the substrate plan, so per-scope
	// uniqueness is also effectively per-plugin uniqueness today.
	Title string
	// Type discriminates form vs stream.
	Type SectionType
	// Spec is the parsed form-section schema. Nil for stream sections — those
	// have no fields and stream ANSI bytes directly from the callback URL.
	Spec *FormSpec
	// CallbackURL is where argus interacts with the plugin. For form sections,
	// argus POSTs `{key: value}` on user save. For stream sections, argus
	// opens a WebSocket and pipes received bytes into the rail's streampane.
	CallbackURL string
	// AuthHeader is the verbatim Authorization-header value the daemon's
	// callback proxy MUST set when POSTing form submissions to CallbackURL.
	// Empty means no header. Plugins that gate their callback endpoint on a
	// static header (e.g. hera's MCP listener requiring Authorization: <token>
	// on /mcp/*) put that value here at registration time.
	AuthHeader string
}

// Errors surfaced by ParseSection. HTTP handlers map ErrInvalid* to 400.
var (
	ErrInvalidScope        = errors.New("settings: scope must be non-empty")
	ErrInvalidTitle        = errors.New("settings: title must be non-empty")
	ErrInvalidType         = errors.New("settings: type must be form or stream")
	ErrMissingCallbackURL  = errors.New("settings: callback_url is required")
	ErrStreamHasFields     = errors.New("settings: stream sections must not declare fields")
	ErrEmptyForm           = errors.New("settings: form must declare at least one field")
	ErrFieldMissingKey     = errors.New("settings: field key must be non-empty")
	ErrFieldMissingLabel   = errors.New("settings: field label must be non-empty")
	ErrFieldDuplicateKey   = errors.New("settings: duplicate field key")
	ErrFieldInvalidType    = errors.New("settings: field type must be one of bool, int, string, enum")
	ErrFieldEnumNoOptions  = errors.New("settings: enum field requires non-empty options")
	ErrFieldIntBounds      = errors.New("settings: int field min must be ≤ max")
	ErrFieldDefaultBadType = errors.New("settings: field default does not match declared type")
)

// rawSection is the on-the-wire shape; we parse into it first so we can
// distinguish "missing type" from "type:form" and surface a sharp error.
type rawSection struct {
	Title       string          `json:"title"`
	Type        SectionType     `json:"type"`
	CallbackURL string          `json:"callback_url"`
	AuthHeader  string          `json:"auth_header,omitempty"`
	Spec        json.RawMessage `json:"spec,omitempty"`
	// Older drafts of the plan inline fields next to spec; accept either by
	// preferring `spec` when present. Fields at the top level is the example
	// used in the plan, so we accept it transparently.
	Fields []FormField `json:"fields,omitempty"`
}

// ParseSection decodes raw section JSON, validates every constraint, and
// returns the typed [Section]. Scope is supplied by the caller (resolved
// from the request's auth token) — plugins cannot register into another
// plugin's namespace. Returns one of the Err* sentinels on validation
// failure so HTTP handlers can branch on the error.
func ParseSection(scope string, raw []byte) (Section, error) {
	if scope == "" {
		return Section{}, ErrInvalidScope
	}
	var r rawSection
	if err := json.Unmarshal(raw, &r); err != nil {
		return Section{}, fmt.Errorf("settings: parse section: %w", err)
	}
	if r.Title == "" {
		return Section{}, ErrInvalidTitle
	}
	// Default type:form when omitted so plain `{title, fields, callback_url}`
	// payloads (the README-style example in the plan) work.
	if r.Type == "" {
		r.Type = TypeForm
	}
	if r.Type != TypeForm && r.Type != TypeStream {
		return Section{}, ErrInvalidType
	}
	if r.CallbackURL == "" {
		return Section{}, ErrMissingCallbackURL
	}
	if r.Type == TypeStream {
		// Stream sections carry no schema — fields/spec on the wire are a
		// shape mismatch; reject loudly rather than silently dropping.
		if len(r.Spec) > 0 || len(r.Fields) > 0 {
			return Section{}, ErrStreamHasFields
		}
		return Section{
			Scope:       scope,
			Title:       r.Title,
			Type:        r.Type,
			CallbackURL: r.CallbackURL,
			AuthHeader:  r.AuthHeader,
		}, nil
	}
	spec, err := parseFormSpec(r.Spec, r.Fields)
	if err != nil {
		return Section{}, err
	}
	return Section{
		Scope:       scope,
		Title:       r.Title,
		Type:        r.Type,
		Spec:        spec,
		CallbackURL: r.CallbackURL,
		AuthHeader:  r.AuthHeader,
	}, nil
}

// parseFormSpec resolves the form spec from either the `spec` envelope or
// the inline `fields` array, then validates every field. Validating once
// at registration keeps every later read free of error paths.
func parseFormSpec(specBytes json.RawMessage, inline []FormField) (*FormSpec, error) {
	var fields []FormField
	switch {
	case len(specBytes) > 0:
		var s FormSpec
		if err := json.Unmarshal(specBytes, &s); err != nil {
			return nil, fmt.Errorf("settings: parse spec: %w", err)
		}
		fields = s.Fields
	default:
		fields = inline
	}
	if len(fields) == 0 {
		return nil, ErrEmptyForm
	}
	seen := make(map[string]bool, len(fields))
	for i := range fields {
		f := &fields[i]
		if f.Key == "" {
			return nil, ErrFieldMissingKey
		}
		if seen[f.Key] {
			return nil, fmt.Errorf("%w: %s", ErrFieldDuplicateKey, f.Key)
		}
		seen[f.Key] = true
		if f.Label == "" {
			return nil, ErrFieldMissingLabel
		}
		if !f.Type.IsValid() {
			return nil, fmt.Errorf("%w: %s (field %s)", ErrFieldInvalidType, f.Type, f.Key)
		}
		if err := validateFieldExtras(f); err != nil {
			return nil, err
		}
	}
	return &FormSpec{Fields: fields}, nil
}

// validateFieldExtras checks the per-type attributes — int bounds, enum
// options, default-value type. Defaults are stored as `any` because JSON
// numbers come back as float64; the type-specific validators coerce and
// surface a sharp error when the wire type doesn't match.
func validateFieldExtras(f *FormField) error {
	switch f.Type {
	case FieldInt:
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			return fmt.Errorf("%w: field %s", ErrFieldIntBounds, f.Key)
		}
		if f.Default != nil {
			if _, ok := coerceInt(f.Default); !ok {
				return fmt.Errorf("%w: field %s (expected int default)", ErrFieldDefaultBadType, f.Key)
			}
		}
	case FieldBool:
		if f.Default != nil {
			if _, ok := f.Default.(bool); !ok {
				return fmt.Errorf("%w: field %s (expected bool default)", ErrFieldDefaultBadType, f.Key)
			}
		}
	case FieldString:
		if f.Default != nil {
			if _, ok := f.Default.(string); !ok {
				return fmt.Errorf("%w: field %s (expected string default)", ErrFieldDefaultBadType, f.Key)
			}
		}
	case FieldEnum:
		if len(f.Options) == 0 {
			return fmt.Errorf("%w: field %s", ErrFieldEnumNoOptions, f.Key)
		}
		if f.Default != nil {
			s, ok := f.Default.(string)
			if !ok {
				return fmt.Errorf("%w: field %s (expected enum string default)", ErrFieldDefaultBadType, f.Key)
			}
			found := false
			for _, opt := range f.Options {
				if opt == s {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: field %s (default %q not in options)", ErrFieldDefaultBadType, f.Key, s)
			}
		}
	}
	return nil
}

// coerceInt returns the int representation of v when v is a JSON number
// (which comes back as float64 from encoding/json) or an int literal. The
// bool return is false when v cannot be represented as a finite int.
func coerceInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case string:
		// Accept stringified ints for resilience against plugins that JSON-
		// encode int defaults as strings. Strict-mode rejection would be
		// fine too; we err on tolerant.
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// DefaultValue returns the field's default value as a typed Go value. The
// return type matches FieldType: bool, int, string, or string (enum). When
// no default was supplied, returns the zero value for the field's type
// (false / 0 / "").
func (f *FormField) DefaultValue() any {
	switch f.Type {
	case FieldBool:
		if b, ok := f.Default.(bool); ok {
			return b
		}
		return false
	case FieldInt:
		if n, ok := coerceInt(f.Default); ok {
			return n
		}
		return 0
	case FieldString, FieldEnum:
		if s, ok := f.Default.(string); ok {
			return s
		}
		return ""
	}
	return nil
}
