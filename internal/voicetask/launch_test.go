package voicetask

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

type stubClassifier struct {
	project string
	prob    float64
	err     error
	calls   int
	got     string
}

func (s *stubClassifier) Classify(_ context.Context, transcript string, _ []string) (string, float64, error) {
	s.calls++
	s.got = transcript
	return s.project, s.prob, s.err
}

type createCall struct{ Name, Prompt, Project, Backend string }

type launchHarness struct {
	l       *Launcher
	creates []createCall
	stamped []string
}

func newHarness(defaultProject string, cls Classifier) *launchHarness {
	h := &launchHarness{}
	h.l = &Launcher{
		Projects:       func() ([]string, error) { return []string{"forge", "argus"}, nil },
		DefaultProject: func() string { return defaultProject },
		Classifier:     cls,
		Create: func(name, prompt, project, backend string) (*model.Task, error) {
			h.creates = append(h.creates, createCall{name, prompt, project, backend})
			return &model.Task{ID: "t1", Name: name}, nil
		},
		Stamp: func(id string) error {
			h.stamped = append(h.stamped, id)
			return nil
		},
		SanitizeName: strings.ToUpper,
	}
	return h
}

func TestLauncher_Resolution(t *testing.T) {
	tests := []struct {
		name        string
		transcript  string
		defaultProj string
		cls         *stubClassifier
		minConf     float64
		wantProject string
		wantPrompt  string
		wantSource  string
		wantSpoken  string
		wantClsCall int
	}{
		{
			name: "spoken wins over classifier", transcript: "fix login in project argus", defaultProj: "forge",
			cls: &stubClassifier{project: "forge", prob: 0.99}, wantProject: "argus", wantPrompt: "fix login", wantSource: "spoken",
		},
		{
			name: "jev confident", transcript: "  tweak the tab bar ", defaultProj: "forge",
			cls: &stubClassifier{project: "argus", prob: 0.9}, wantProject: "argus", wantPrompt: "tweak the tab bar", wantSource: "jev", wantClsCall: 1,
		},
		{
			name: "jev canonicalizes case", transcript: "tweak it",
			cls: &stubClassifier{project: "ARGUS", prob: 0.9}, wantProject: "argus", wantPrompt: "tweak it", wantSource: "jev", wantClsCall: 1,
		},
		{
			name: "jev below threshold falls to default", transcript: "tweak it", defaultProj: "forge",
			cls: &stubClassifier{project: "argus", prob: 0.5}, wantProject: "forge", wantPrompt: "tweak it", wantSource: "default", wantClsCall: 1,
		},
		{
			name: "custom threshold accepts", transcript: "tweak it", defaultProj: "forge", minConf: 0.4,
			cls: &stubClassifier{project: "argus", prob: 0.5}, wantProject: "argus", wantPrompt: "tweak it", wantSource: "jev", wantClsCall: 1,
		},
		{
			name: "jev unlisted project falls to default", transcript: "tweak it", defaultProj: "forge",
			cls: &stubClassifier{project: "nope", prob: 0.99}, wantProject: "forge", wantPrompt: "tweak it", wantSource: "default", wantClsCall: 1,
		},
		{
			name: "jev none falls to default", transcript: "tweak it", defaultProj: "forge",
			cls: &stubClassifier{}, wantProject: "forge", wantPrompt: "tweak it", wantSource: "default", wantClsCall: 1,
		},
		{
			name: "jev error falls to default", transcript: "tweak it", defaultProj: "Forge",
			cls: &stubClassifier{err: errors.New("boom")}, wantProject: "forge", wantPrompt: "tweak it", wantSource: "default", wantClsCall: 1,
		},
		{
			name: "unknown spoken project carried", transcript: "do it in project zzzzzz", defaultProj: "forge",
			wantProject: "forge", wantPrompt: "do it in project zzzzzz", wantSource: "default", wantSpoken: "zzzzzz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cls Classifier
			if tt.cls != nil {
				cls = tt.cls
			}
			h := newHarness(tt.defaultProj, cls)
			h.l.MinConfidence = tt.minConf
			got, err := h.l.Launch(context.Background(), tt.transcript, "codex")
			testutil.NoError(t, err)
			testutil.Equal(t, got.Project, tt.wantProject)
			testutil.Equal(t, got.Prompt, tt.wantPrompt)
			testutil.Equal(t, got.Source, tt.wantSource)
			testutil.Equal(t, got.SpokenProject, tt.wantSpoken)
			testutil.Equal(t, got.Task.ID, "t1")
			testutil.DeepEqual(t, h.creates, []createCall{{strings.ToUpper(tt.wantPrompt), tt.wantPrompt, tt.wantProject, "codex"}})
			testutil.DeepEqual(t, h.stamped, []string{"t1"})
			if tt.cls != nil {
				testutil.Equal(t, tt.cls.calls, tt.wantClsCall)
				if tt.wantClsCall > 0 {
					testutil.Equal(t, tt.cls.got, strings.TrimSpace(tt.transcript))
				}
			}
		})
	}
}

func TestLauncher_Errors(t *testing.T) {
	t.Run("empty transcript", func(t *testing.T) {
		h := newHarness("forge", nil)
		_, err := h.l.Launch(context.Background(), "  \n ", "")
		testutil.ErrorIs(t, err, ErrEmptyTranscript)
		testutil.Equal(t, len(h.creates), 0)
	})

	t.Run("empty prompt after project removal", func(t *testing.T) {
		h := newHarness("forge", nil)
		_, err := h.l.Launch(context.Background(), "in project argus", "")
		testutil.ErrorIs(t, err, ErrEmptyTranscript)
	})

	t.Run("no project", func(t *testing.T) {
		h := newHarness("", &stubClassifier{})
		_, err := h.l.Launch(context.Background(), "do it", "")
		testutil.ErrorIs(t, err, ErrNoProject)
	})

	t.Run("default not in list", func(t *testing.T) {
		h := newHarness("ghost", nil)
		_, err := h.l.Launch(context.Background(), "do it", "")
		testutil.ErrorIs(t, err, ErrNoProject)
	})

	t.Run("nil default func", func(t *testing.T) {
		h := newHarness("", nil)
		h.l.DefaultProject = nil
		_, err := h.l.Launch(context.Background(), "do it", "")
		testutil.ErrorIs(t, err, ErrNoProject)
	})

	t.Run("projects error", func(t *testing.T) {
		h := newHarness("forge", nil)
		boom := errors.New("db down")
		h.l.Projects = func() ([]string, error) { return nil, boom }
		_, err := h.l.Launch(context.Background(), "do it", "")
		testutil.ErrorIs(t, err, boom)
	})

	t.Run("create error", func(t *testing.T) {
		h := newHarness("forge", nil)
		boom := errors.New("create failed")
		h.l.Create = func(_, _, _, _ string) (*model.Task, error) { return nil, boom }
		_, err := h.l.Launch(context.Background(), "do it", "")
		testutil.ErrorIs(t, err, boom)
		testutil.Equal(t, len(h.stamped), 0)
	})

	t.Run("stamp error not fatal", func(t *testing.T) {
		h := newHarness("forge", nil)
		h.l.Stamp = func(string) error { return errors.New("meta") }
		got, err := h.l.Launch(context.Background(), "do it", "")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Task.ID, "t1")
	})

	t.Run("nil stamp and sanitize", func(t *testing.T) {
		h := newHarness("forge", nil)
		h.l.Stamp = nil
		h.l.SanitizeName = nil
		_, err := h.l.Launch(context.Background(), "do it", "")
		testutil.NoError(t, err)
		testutil.Equal(t, h.creates[0].Name, "do it")
	})
}

func TestNewLauncherFromConfig(t *testing.T) {
	stubResolve := func(t *testing.T, val string, ok bool) *[]string {
		t.Helper()
		var seen []string
		prev := resolveSecret
		resolveSecret = func(_ config.SecretsConfig, src string) (string, bool) {
			seen = append(seen, src)
			return val, ok
		}
		t.Cleanup(func() { resolveSecret = prev })
		return &seen
	}
	base := func(apiKey string) config.Config {
		cfg := config.DefaultConfig()
		cfg.Defaults.VoiceProject = "forge"
		cfg.Voice.Jev.APIKey = apiKey
		cfg.Voice.Jev.MinConfidence = 0.8
		return cfg
	}
	projects := func() ([]string, error) { return []string{"forge"}, nil }

	t.Run("no api key disables jev", func(t *testing.T) {
		seen := stubResolve(t, "k", true)
		l := NewLauncherFromConfig(base(""), projects, nil, nil, nil)
		testutil.Nil(t, l.Classifier)
		testutil.Equal(t, len(*seen), 0)
		testutil.Equal(t, l.DefaultProject(), "forge")
		testutil.Equal(t, l.MinConfidence, 0.8)
	})

	t.Run("resolved key enables jev", func(t *testing.T) {
		seen := stubResolve(t, "resolved", true)
		l := NewLauncherFromConfig(base("op://Private/TypeSafe/credential"), projects, nil, nil, nil)
		testutil.NotNil(t, l.Classifier)
		testutil.DeepEqual(t, *seen, []string{"op://Private/TypeSafe/credential"})
		jc := l.Classifier.(*jevClassifier)
		testutil.Equal(t, jc.apiKey, "resolved")
		testutil.Equal(t, jc.endpoint, config.DefaultJevEndpoint)
	})

	t.Run("unresolved key disables jev", func(t *testing.T) {
		stubResolve(t, "", false)
		l := NewLauncherFromConfig(base("MISSING_ENV"), projects, nil, nil, nil)
		testutil.Nil(t, l.Classifier)
	})

	t.Run("empty resolved key disables jev", func(t *testing.T) {
		stubResolve(t, "", true)
		l := NewLauncherFromConfig(base("EMPTY_ENV"), projects, nil, nil, nil)
		testutil.Nil(t, l.Classifier)
	})
}
