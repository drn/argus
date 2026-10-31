package voicetask

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

const sourceJev = "jev"

var (
	ErrNoProject       = errors.New(`no project: say "in project <name>" or set defaults.voice_project`)
	ErrEmptyTranscript = errors.New("transcript has no task text")
)

type CreateFunc func(name, prompt, project, backend string) (*model.Task, error)

// Launcher turns a transcript into a started task: resolve the project
// (spoken → Jev → default), create the task, and stamp it as new.
type Launcher struct {
	Projects       func() ([]string, error)
	DefaultProject func() string
	Classifier     Classifier
	MinConfidence  float64
	Create         CreateFunc
	Stamp          func(taskID string) error
	SanitizeName   func(prompt string) string
}

type Launched struct {
	Task          *model.Task
	Project       string
	Prompt        string
	Source        string
	SpokenProject string
}

func (l *Launcher) Launch(ctx context.Context, transcript, backend string) (Launched, error) {
	trimmed := strings.TrimSpace(transcript)
	if trimmed == "" {
		uxlog.Log("[voice] rejected: empty transcript")
		return Launched{}, ErrEmptyTranscript
	}

	projects, err := l.Projects()
	if err != nil {
		return Launched{}, fmt.Errorf("load projects: %w", err)
	}
	projects = slices.Sorted(slices.Values(projects))

	r := Parse(trimmed, projects, "")
	out := Launched{Prompt: r.Prompt, SpokenProject: r.SpokenProject}
	if r.Source == sourceSpoken {
		out.Project, out.Source = r.Project, sourceSpoken
	}
	if out.Project == "" && l.Classifier != nil {
		out.Project, out.Source = l.classify(ctx, trimmed, projects)
	}
	if out.Project == "" && l.DefaultProject != nil {
		if name, ok := lookup(l.DefaultProject(), projects); ok {
			out.Project, out.Source = name, sourceDefault
		}
	}
	if out.Project == "" {
		uxlog.Log("[voice] rejected: no project resolvable len=%d", len(trimmed))
		return Launched{}, ErrNoProject
	}
	if out.Prompt == "" {
		uxlog.Log("[voice] rejected: empty prompt after project removal len=%d", len(trimmed))
		return Launched{}, ErrEmptyTranscript
	}

	name := out.Prompt
	if l.SanitizeName != nil {
		name = l.SanitizeName(out.Prompt)
	}
	task, err := l.Create(name, out.Prompt, out.Project, backend)
	if err != nil {
		uxlog.Log("[voice] create failed project=%s err=%v", out.Project, err)
		return Launched{}, err
	}
	out.Task = task

	if l.Stamp != nil {
		if err := l.Stamp(task.ID); err != nil {
			uxlog.Log("[voice] meta stamp failed id=%s err=%v", task.ID, err)
		}
	}
	uxlog.Log("[voice] created task id=%s project=%s source=%s", task.ID, out.Project, out.Source)
	return out, nil
}

func (l *Launcher) classify(ctx context.Context, transcript string, projects []string) (string, string) {
	choice, prob, err := l.Classifier.Classify(ctx, transcript, projects)
	if err != nil {
		uxlog.Log("[voice] jev classify failed: %v", err)
		return "", ""
	}
	minConf := l.MinConfidence
	if minConf <= 0 {
		minConf = config.DefaultJevMinConfidence
	}
	name, ok := lookup(choice, projects)
	if !ok || prob < minConf {
		uxlog.Log("[voice] jev no confident match listed=%t prob=%.2f min=%.2f", ok, prob, minConf)
		return "", ""
	}
	uxlog.Log("[voice] jev classified project=%s prob=%.2f", name, prob)
	return name, sourceJev
}

// NewLauncherFromConfig builds a Launcher whose Jev classifier is enabled
// only when cfg.Voice.Jev.APIKey resolves to a non-empty secret.
func NewLauncherFromConfig(cfg config.Config, projects func() ([]string, error), create CreateFunc, stamp func(string) error, sanitize func(string) string) *Launcher {
	defaultProject := cfg.Defaults.VoiceProject
	l := &Launcher{
		Projects:       projects,
		DefaultProject: func() string { return defaultProject },
		MinConfidence:  cfg.Voice.Jev.MinConfidence,
		Create:         create,
		Stamp:          stamp,
		SanitizeName:   sanitize,
	}
	if src := cfg.Voice.Jev.APIKey; src != "" {
		if key, ok := resolveSecret(cfg.Secrets, src); ok && key != "" {
			l.Classifier = NewJevClassifier(cfg.Voice.Jev, key, nil)
		} else {
			uxlog.Log("[voice] jev api key unresolved; jev disabled")
		}
	}
	return l
}

var resolveSecret = agent.Resolve
