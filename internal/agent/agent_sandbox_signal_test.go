package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// Command-construction tests do not execute sandbox-exec, so exercise both
// availability outcomes on every platform without depending on the host binary.
func sandboxAvailabilityForSignalTest(t *testing.T, available bool) {
	t.Helper()
	ResetSandboxCache()
	sandboxOnce.Do(func() { sandboxExists = available })
	t.Cleanup(ResetSandboxCache)
}

func TestBuildCmd_SandboxSignal(t *testing.T) {
	for _, backend := range []string{"claude", "codex", "pi", "opencode", "agy", "cursor", "custom"} {
		t.Run(backend, func(t *testing.T) {
			for _, launch := range []string{"fresh", "resume"} {
				t.Run(launch, func(t *testing.T) {
					for _, wrapped := range []bool{false, true} {
						name := "unwrapped"
						if wrapped {
							name = "wrapped"
						}
						t.Run(name, func(t *testing.T) {
							t.Setenv("HOME", t.TempDir())
							unsetForTest(t, "ARGUS_SANDBOXED")
							sandboxAvailabilityForSignalTest(t, true)
							stubCodexOverlay(t, filepath.Join(t.TempDir(), "overlay"))
							readLog := captureUXLog(t)
							command := map[string]string{"claude": "claude", "codex": "codex", "pi": "pi", "opencode": "opencode", "agy": "agy", "cursor": "cursor-agent", "custom": "my-agent"}[backend]
							cfg := config.Config{
								Defaults: config.Defaults{Backend: backend},
								Backends: map[string]config.Backend{backend: {
									Command: command,
								}},
								Sandbox: config.SandboxConfig{Enabled: wrapped},
							}
							task := &model.Task{ID: "signal", Name: "signal", Worktree: t.TempDir(), Prompt: "seed", Sandboxed: !wrapped}
							if launch == "resume" {
								task.SessionID = "existing-session"
							}
							cmd, cleanup, err := BuildCmd(task, cfg, launch == "resume")
							testutil.NoError(t, err)
							if cleanup != nil {
								t.Cleanup(cleanup)
							}
							testutil.Equal(t, cleanup != nil, wrapped)
							testutil.Equal(t, strings.Contains(cmd.Args[2], "sandbox-exec"), wrapped)
							var entries []string
							for _, entry := range cmd.Env {
								if strings.HasPrefix(entry, "ARGUS_SANDBOXED=") {
									entries = append(entries, entry)
								}
							}
							if wrapped {
								testutil.DeepEqual(t, entries, []string{"ARGUS_SANDBOXED=1"})
								testutil.Contains(t, readLog(), "[sandbox] task \"signal\": wrapped=true")
							} else {
								testutil.Equal(t, len(entries), 0)
								testutil.Contains(t, readLog(), "[sandbox] task \"signal\": wrapped=false")
							}
						})
					}
				})
			}
		})
	}
}

func TestBuildCmd_SandboxSignalReserved(t *testing.T) {
	for _, source := range []string{"parent-one", "parent-zero", "parent-empty", "backend", "cache"} {
		t.Run(source, func(t *testing.T) {
			for _, wrapped := range []bool{false, true} {
				name := "unwrapped"
				if wrapped {
					name = "wrapped"
				}
				t.Run(name, func(t *testing.T) {
					t.Setenv("HOME", t.TempDir())
					unsetForTest(t, "ARGUS_SANDBOXED")
					sandboxAvailabilityForSignalTest(t, true)
					backend := config.Backend{Command: "my-agent"}
					cfg := config.Config{Defaults: config.Defaults{Backend: "custom"}, Sandbox: config.SandboxConfig{Enabled: wrapped}}
					switch source {
					case "parent-one":
						t.Setenv("ARGUS_SANDBOXED", "1")
					case "parent-zero":
						t.Setenv("ARGUS_SANDBOXED", "0")
					case "parent-empty":
						t.Setenv("ARGUS_SANDBOXED", "")
					case "backend":
						t.Setenv("ARGUS_TEST_SIGNAL_SOURCE", "configured-value")
						backend.EnvVars = map[string]string{"ARGUS_SANDBOXED": "ARGUS_TEST_SIGNAL_SOURCE"}
					case "cache":
						cfg.CacheDirs = map[string]string{"ARGUS_SANDBOXED": "fake-signal"}
					}
					cfg.Backends = map[string]config.Backend{"custom": backend}
					cmd, cleanup, err := BuildCmd(&model.Task{ID: "reserved", Worktree: t.TempDir()}, cfg, false)
					testutil.NoError(t, err)
					if cleanup != nil {
						t.Cleanup(cleanup)
					}
					value, found := envLast(cmd.Env, "ARGUS_SANDBOXED")
					testutil.Equal(t, found, wrapped)
					if wrapped {
						testutil.Equal(t, value, "1")
					}
				})
			}
		})
	}
}

func TestBuildCmd_SandboxSignalFallback(t *testing.T) {
	for _, reason := range []string{"unavailable", "profile-generation-failed"} {
		t.Run(reason, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("ARGUS_SANDBOXED", "1")
			sandboxAvailabilityForSignalTest(t, reason != "unavailable")
			if reason == "profile-generation-failed" {
				t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
			}
			cfg := config.Config{
				Defaults: config.Defaults{Backend: "custom"},
				Backends: map[string]config.Backend{"custom": {Command: "my-agent"}},
				Sandbox:  config.SandboxConfig{Enabled: true},
			}
			task := &model.Task{ID: "fallback", Worktree: t.TempDir(), Sandboxed: true}
			cmd, cleanup, err := BuildCmd(task, cfg, false)
			testutil.NoError(t, err)
			testutil.Nil(t, cleanup)
			testutil.False(t, strings.Contains(cmd.Args[2], "sandbox-exec"))
			_, found := envLast(cmd.Env, "ARGUS_SANDBOXED")
			testutil.False(t, found)
		})
	}
}
