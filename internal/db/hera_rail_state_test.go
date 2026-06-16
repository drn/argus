package db

import "testing"

func TestRailState_AbsentReturnsEmpty(t *testing.T) {
	d := testDB(t)
	got, err := d.LoadRailState()
	if err != nil {
		t.Fatalf("LoadRailState: %v", err)
	}
	if got != "" {
		t.Errorf("LoadRailState on a fresh DB = %q, want \"\"", got)
	}
}

func TestRailState_RoundTrip(t *testing.T) {
	d := testDB(t)
	const blob = `{"collapsed":[1,2],"selection_ref":-3}`
	if err := d.SaveRailState(blob); err != nil {
		t.Fatalf("SaveRailState: %v", err)
	}
	got, err := d.LoadRailState()
	if err != nil {
		t.Fatalf("LoadRailState: %v", err)
	}
	if got != blob {
		t.Errorf("LoadRailState = %q, want %q", got, blob)
	}

	// Stored under the documented config key (a re-save overwrites in place).
	if v, _ := d.GetConfigValue(railStateConfigKey); v != blob {
		t.Errorf("config[%q] = %q, want %q", railStateConfigKey, v, blob)
	}
	if err := d.SaveRailState("{}"); err != nil {
		t.Fatalf("SaveRailState overwrite: %v", err)
	}
	got, _ = d.LoadRailState()
	if got != "{}" {
		t.Errorf("after overwrite = %q, want \"{}\"", got)
	}
}
