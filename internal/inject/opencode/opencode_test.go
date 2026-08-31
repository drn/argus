package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readMCP parses opencode.json and returns its mcp.argus entry (or nil).
func readMCP(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	mcp, _ := data["mcp"].(map[string]any)
	argus, _ := mcp["argus"].(map[string]any)
	return argus
}

// readSkills parses opencode.json and returns its skills array (or nil).
func readSkills(t *testing.T, path string) []any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	skills, _ := data["skills"].([]any)
	return skills
}

func TestInjectGlobal_UsesHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "") // force the ~/.config path
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := InjectGlobal(7742, ""); err != nil {
		t.Fatalf("InjectGlobal: %v", err)
	}
	argus := readMCP(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if argus["url"] != "http://localhost:7742/mcp" {
		t.Errorf("url = %v", argus["url"])
	}
	if argus["type"] != "remote" {
		t.Errorf("type = %v, want remote", argus["type"])
	}
}

func TestInjectGlobal_HonorsXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir()) // must NOT be used when XDG_CONFIG_HOME is set
	if err := InjectGlobal(7742, ""); err != nil {
		t.Fatalf("InjectGlobal: %v", err)
	}
	argus := readMCP(t, filepath.Join(xdg, "opencode", "opencode.json"))
	if argus["url"] != "http://localhost:7742/mcp" {
		t.Errorf("url = %v", argus["url"])
	}
}

func TestInjectGlobal_MkdirError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root")
	}
	t.Setenv("XDG_CONFIG_HOME", "") // force the ~/.config path
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Make ~/.config a file so MkdirAll of ~/.config/opencode fails.
	if err := os.WriteFile(filepath.Join(home, ".config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InjectGlobal(7742, ""); err == nil {
		t.Fatal("expected MkdirAll error when ~/.config is a file")
	}
}

func TestInjectGlobal_AddsSkillsEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillsDir := filepath.Join(home, ".argus", "skills", ".claude", "skills")
	if err := InjectGlobal(7742, skillsDir); err != nil {
		t.Fatalf("InjectGlobal: %v", err)
	}
	skills := readSkills(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if len(skills) != 1 || skills[0] != skillsDir {
		t.Errorf("skills = %v, want [%q]", skills, skillsDir)
	}
}

func TestInjectOpencodeJSON_CreatesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")

	if err := injectOpencodeJSON(path, 7742, ""); err != nil {
		t.Fatalf("injectOpencodeJSON: %v", err)
	}
	argus := readMCP(t, path)
	if argus["type"] != "remote" {
		t.Errorf("type = %v, want remote", argus["type"])
	}
	if argus["url"] != "http://localhost:7742/mcp" {
		t.Errorf("url = %v", argus["url"])
	}
	if argus["enabled"] != true {
		t.Errorf("enabled = %v, want true", argus["enabled"])
	}
}

func TestInjectOpencodeJSON_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")

	if err := injectOpencodeJSON(path, 7742, ""); err != nil {
		t.Fatalf("first inject: %v", err)
	}
	data1, _ := os.ReadFile(path)
	if err := injectOpencodeJSON(path, 7742, ""); err != nil {
		t.Fatalf("second inject: %v", err)
	}
	data2, _ := os.ReadFile(path)
	if string(data1) != string(data2) {
		t.Error("idempotency failure: file changed on second call")
	}
}

func TestInjectOpencodeJSON_PortChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")

	if err := injectOpencodeJSON(path, 7742, ""); err != nil {
		t.Fatalf("inject 7742: %v", err)
	}
	if err := injectOpencodeJSON(path, 8888, ""); err != nil {
		t.Fatalf("inject 8888: %v", err)
	}
	argus := readMCP(t, path)
	if argus["url"] != "http://localhost:8888/mcp" {
		t.Errorf("url = %v, want port 8888", argus["url"])
	}
	raw, _ := os.ReadFile(path)
	if want := "7742"; strings.Contains(string(raw), want) {
		t.Errorf("old port %s still present after re-inject", want)
	}
}

func TestInjectOpencodeJSON_PreservesUnrelated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	seed := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"theme":   "dark",
		"mcp": map[string]any{
			"other": map[string]any{"type": "local", "command": []any{"foo"}},
		},
	}
	raw, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := injectOpencodeJSON(path, 7742, ""); err != nil {
		t.Fatalf("inject: %v", err)
	}

	out, _ := os.ReadFile(path)
	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatal(err)
	}
	if data["theme"] != "dark" || data["$schema"] != "https://opencode.ai/config.json" {
		t.Error("unrelated top-level keys not preserved")
	}
	mcp := data["mcp"].(map[string]any)
	if _, ok := mcp["other"]; !ok {
		t.Error("unrelated mcp entry not preserved")
	}
	if _, ok := mcp["argus"]; !ok {
		t.Error("argus entry not added")
	}
}

func TestInjectOpencodeJSON_WriteError(t *testing.T) {
	// Parent dir does not exist → CreateTemp inside writeJSON fails.
	// (injectOpencodeJSON does not MkdirAll; only InjectGlobal does.)
	missing := filepath.Join(t.TempDir(), "no-such-dir", "opencode.json")
	if err := injectOpencodeJSON(missing, 7742, ""); err == nil {
		t.Fatal("expected write error when the target directory is missing")
	}
}

func TestInjectOpencodeJSON_InvalidJSONUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	bad := []byte("{ this is not json")
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := injectOpencodeJSON(path, 7742, ""); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	out, _ := os.ReadFile(path)
	if string(out) != string(bad) {
		t.Error("invalid-JSON file was modified")
	}
}

func TestInjectOpencodeJSON_SkillsEntryAddedWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	skillsDir := "/x/.argus/skills/.claude/skills"

	if err := injectOpencodeJSON(path, 7742, skillsDir); err != nil {
		t.Fatalf("injectOpencodeJSON: %v", err)
	}
	skills := readSkills(t, path)
	if len(skills) != 1 || skills[0] != skillsDir {
		t.Errorf("skills = %v, want [%q]", skills, skillsDir)
	}
}

func TestInjectOpencodeJSON_SkillsIdempotentOnRepeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	skillsDir := "/x/.argus/skills/.claude/skills"

	if err := injectOpencodeJSON(path, 7742, skillsDir); err != nil {
		t.Fatalf("first inject: %v", err)
	}
	data1, _ := os.ReadFile(path)
	if err := injectOpencodeJSON(path, 7742, skillsDir); err != nil {
		t.Fatalf("second inject: %v", err)
	}
	data2, _ := os.ReadFile(path)
	if string(data1) != string(data2) {
		t.Error("idempotency failure: file changed on second call")
	}
	skills := readSkills(t, path)
	if len(skills) != 1 {
		t.Errorf("skills = %v, want exactly one entry (no duplication)", skills)
	}
}

func TestInjectOpencodeJSON_SkillsPreservesOtherEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	seed := map[string]any{
		"skills": []any{"./team-skills", "~/shared/opencode-skills"},
	}
	raw, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	skillsDir := "/x/.argus/skills/.claude/skills"
	if err := injectOpencodeJSON(path, 7742, skillsDir); err != nil {
		t.Fatalf("inject: %v", err)
	}

	skills := readSkills(t, path)
	if len(skills) != 3 {
		t.Fatalf("skills = %v, want 3 entries (2 existing + argus)", skills)
	}
	found := map[string]bool{}
	for _, s := range skills {
		found[s.(string)] = true
	}
	for _, want := range []string{"./team-skills", "~/shared/opencode-skills", skillsDir} {
		if !found[want] {
			t.Errorf("expected %q to be present in skills array %v", want, skills)
		}
	}
}

func TestInjectOpencodeJSON_EmptySkillsDirSkipsEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")

	if err := injectOpencodeJSON(path, 7742, ""); err != nil {
		t.Fatalf("inject: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if _, ok := data["skills"]; ok {
		t.Errorf("expected no skills key when skillsDir is empty, got %v", data["skills"])
	}
}
