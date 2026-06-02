package db

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/settings"
)

func testPluginSection(scope, title string) PluginSection {
	return PluginSection{
		Scope:       scope,
		Title:       title,
		Type:        "form",
		SpecJSON:    `{"fields":[{"key":"k","label":"l","type":"bool","default":false}]}`,
		CallbackURL: "http://127.0.0.1/save",
	}
}

func TestUpsertPluginSection_Insert(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id, err := d.UpsertPluginSection(testPluginSection("scope", "Title"))
	testutil.NoError(t, err)
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}
}

func TestUpsertPluginSection_Replaces(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	id1, err := d.UpsertPluginSection(testPluginSection("scope", "Title"))
	testutil.NoError(t, err)

	updated := testPluginSection("scope", "Title")
	updated.CallbackURL = "http://127.0.0.1/v2"
	id2, err := d.UpsertPluginSection(updated)
	testutil.NoError(t, err)
	testutil.Equal(t, id1, id2)

	sections, err := d.ListPluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, len(sections), 1)
	testutil.Equal(t, sections[0].CallbackURL, "http://127.0.0.1/v2")
}

func TestUpsertPluginSection_Validation(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	cases := []PluginSection{
		{Scope: "", Title: "t", CallbackURL: "http://x"},
		{Scope: "s", Title: "", CallbackURL: "http://x"},
		{Scope: "s", Title: "t", CallbackURL: ""},
	}
	for _, p := range cases {
		_, err := d.UpsertPluginSection(p)
		if !errors.Is(err, ErrPluginSectionInvalid) {
			t.Fatalf("expected ErrPluginSectionInvalid, got %v (input %+v)", err, p)
		}
	}
}

func TestUpsertPluginSection_DefaultsType(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	p := testPluginSection("scope", "Title")
	p.Type = ""
	_, err = d.UpsertPluginSection(p)
	testutil.NoError(t, err)

	sections, _ := d.ListPluginSections()
	testutil.Equal(t, len(sections), 1)
	testutil.Equal(t, sections[0].Type, "form")
}

func TestListPluginSections_OrderedByTitleThenScope(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	for _, sec := range []PluginSection{
		testPluginSection("z", "Bravo"),
		testPluginSection("a", "Alpha"),
		testPluginSection("a", "Bravo"),
	} {
		_, err := d.UpsertPluginSection(sec)
		testutil.NoError(t, err)
	}

	sections, err := d.ListPluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, len(sections), 3)
	testutil.Equal(t, sections[0].Title, "Alpha")
	testutil.Equal(t, sections[1].Title, "Bravo")
	testutil.Equal(t, sections[1].Scope, "a")
	testutil.Equal(t, sections[2].Title, "Bravo")
	testutil.Equal(t, sections[2].Scope, "z")
}

func TestDeletePluginSection(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	_, err = d.UpsertPluginSection(testPluginSection("scope", "Title"))
	testutil.NoError(t, err)

	removed, err := d.DeletePluginSection("scope", "missing")
	testutil.NoError(t, err)
	testutil.Equal(t, removed, false)

	removed, err = d.DeletePluginSection("scope", "Title")
	testutil.NoError(t, err)
	testutil.Equal(t, removed, true)

	removed, err = d.DeletePluginSection("scope", "Title")
	testutil.NoError(t, err)
	testutil.Equal(t, removed, false)
}

func TestDeletePluginSection_RejectsEmpty(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	_, err = d.DeletePluginSection("", "t")
	if !errors.Is(err, ErrPluginSectionInvalid) {
		t.Fatalf("expected ErrPluginSectionInvalid, got %v", err)
	}
	_, err = d.DeletePluginSection("s", "")
	if !errors.Is(err, ErrPluginSectionInvalid) {
		t.Fatalf("expected ErrPluginSectionInvalid, got %v", err)
	}
}

func TestDeletePluginSectionsByScope(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	_, err = d.UpsertPluginSection(testPluginSection("a", "T1"))
	testutil.NoError(t, err)
	_, err = d.UpsertPluginSection(testPluginSection("a", "T2"))
	testutil.NoError(t, err)
	_, err = d.UpsertPluginSection(testPluginSection("b", "T1"))
	testutil.NoError(t, err)

	n, err := d.DeletePluginSectionsByScope("")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 0)

	n, err = d.DeletePluginSectionsByScope("a")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 2)

	sections, err := d.ListPluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, len(sections), 1)
	testutil.Equal(t, sections[0].Scope, "b")
}

func TestPluginSection_RoundTripThroughSettings(t *testing.T) {
	original, err := settings.ParseSection("scope", []byte(`{
		"title": "Hello",
		"callback_url": "http://127.0.0.1/save",
		"fields": [{"key":"k","label":"l","type":"enum","options":["x","y"],"default":"x"}]
	}`))
	testutil.NoError(t, err)

	pluginRow, err := FromSection(original)
	testutil.NoError(t, err)
	if pluginRow.SpecJSON == "" {
		t.Fatal("spec_json should be encoded")
	}

	restored, err := pluginRow.ToSection()
	testutil.NoError(t, err)
	testutil.Equal(t, restored.Scope, original.Scope)
	testutil.Equal(t, restored.Title, original.Title)
	testutil.Equal(t, restored.CallbackURL, original.CallbackURL)
	testutil.Equal(t, len(restored.Spec.Fields), 1)
	testutil.Equal(t, restored.Spec.Fields[0].Key, "k")
}

func TestPluginSection_ToSectionCorruptJSON(t *testing.T) {
	p := PluginSection{Scope: "s", Title: "t", Type: "form", SpecJSON: "{not valid", CallbackURL: "http://x"}
	_, err := p.ToSection()
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDB_PluginSections_TypedConversion(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	_, err = d.UpsertPluginSection(testPluginSection("scope", "Hello"))
	testutil.NoError(t, err)

	sections, err := d.PluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, len(sections), 1)
	testutil.Equal(t, sections[0].Scope, "scope")
	testutil.Equal(t, sections[0].Title, "Hello")
	if sections[0].Spec == nil || len(sections[0].Spec.Fields) != 1 {
		t.Fatalf("expected one parsed field, got spec=%+v", sections[0].Spec)
	}
}

func TestDB_PluginSections_SkipsCorrupt(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	// One good, one corrupt-spec row.
	_, err = d.UpsertPluginSection(testPluginSection("scope", "Good"))
	testutil.NoError(t, err)
	_, err = d.UpsertPluginSection(PluginSection{
		Scope: "scope", Title: "Bad", Type: "form",
		SpecJSON: "{not json", CallbackURL: "http://x",
	})
	testutil.NoError(t, err)

	sections, err := d.PluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, len(sections), 1)
	testutil.Equal(t, sections[0].Title, "Good")
}

// TestPluginSection_RoundTripsAuthHeader pins the column round-trip for the
// callback-proxy auth header — the upsert/list pair MUST preserve the value
// so the submit handler can forward it as Authorization.
func TestPluginSection_RoundTripsAuthHeader(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	p := testPluginSection("scope", "Title")
	p.AuthHeader = "Bearer plugin-secret"
	_, err = d.UpsertPluginSection(p)
	testutil.NoError(t, err)

	sections, err := d.ListPluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, len(sections), 1)
	testutil.Equal(t, sections[0].AuthHeader, "Bearer plugin-secret")

	// Re-upsert with a new value to confirm ON CONFLICT updates auth_header.
	p.AuthHeader = "Bearer rotated"
	_, err = d.UpsertPluginSection(p)
	testutil.NoError(t, err)
	sections, err = d.ListPluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, sections[0].AuthHeader, "Bearer rotated")
}

func TestPluginSection_ToSectionEmptySpec(t *testing.T) {
	p := PluginSection{Scope: "s", Title: "t", Type: "form", SpecJSON: "", CallbackURL: "http://x"}
	sec, err := p.ToSection()
	testutil.NoError(t, err)
	if sec.Spec != nil {
		t.Fatalf("expected nil spec for empty JSON, got %+v", sec.Spec)
	}
}
