package apistore

import (
	"strings"
	"time"

	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// splitCSV splits a comma-separated string, trimming whitespace and dropping
// empty entries. Mirrors how the server's SettingsUpdate handler joins the
// inverse — keeps round trips lossless.
func splitCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// projectFromAPI converts a wire apiclient.ProjectJSON back to a
// config.Project. The Sandbox section travels as a generic map so the
// untyped JSON tristate (nil pointer for "inherit") survives the round trip.
func projectFromAPI(p apiclient.ProjectJSON) config.Project {
	out := config.Project{
		Path:    p.Path,
		Branch:  p.Branch,
		Backend: p.Backend,
	}
	if p.Sandbox != nil {
		if v, ok := p.Sandbox["enabled"]; ok && v != nil {
			if b, ok := v.(bool); ok {
				out.Sandbox.Enabled = &b
			}
		}
		out.Sandbox.DenyRead = stringSliceFromAny(p.Sandbox["deny_read"])
		out.Sandbox.ExtraWrite = stringSliceFromAny(p.Sandbox["extra_write"])
		out.Sandbox.AllowAppleEvents = stringSliceFromAny(p.Sandbox["allow_apple_events"])
	}
	return out
}

// projectToAPI is the inverse — packs config.Project into wire form.
func projectToAPI(name string, p config.Project) apiclient.ProjectJSON {
	out := apiclient.ProjectJSON{
		Name:    name,
		Path:    p.Path,
		Branch:  p.Branch,
		Backend: p.Backend,
	}
	if p.Sandbox.Enabled != nil || len(p.Sandbox.DenyRead) > 0 || len(p.Sandbox.ExtraWrite) > 0 || len(p.Sandbox.AllowAppleEvents) > 0 {
		sb := map[string]any{}
		if p.Sandbox.Enabled != nil {
			sb["enabled"] = *p.Sandbox.Enabled
		}
		sb["deny_read"] = stringsOrEmpty(p.Sandbox.DenyRead)
		sb["extra_write"] = stringsOrEmpty(p.Sandbox.ExtraWrite)
		sb["allow_apple_events"] = stringsOrEmpty(p.Sandbox.AllowAppleEvents)
		out.Sandbox = sb
	}
	return out
}

// scheduleReqFromModel marshals every field of a model.ScheduledTask into
// the partial-update ScheduleReq the API expects.
//
// RunOnceAt is only set when the source has a non-zero value, because the
// server treats `RunOnceAt != nil && raw == ""` as "clear the field". A
// recurring schedule (RunOnceAt zero) round-tripping through Update would
// otherwise be no-op-cleared every call; a one-shot schedule edited with
// any other field touched would lose its RunOnceAt entirely.
func scheduleReqFromModel(s *model.ScheduledTask) apiclient.ScheduleReq {
	name := s.Name
	project := s.Project
	prompt := s.Prompt
	backend := s.Backend
	schedule := s.Schedule
	enabled := s.Enabled
	req := apiclient.ScheduleReq{
		Name:     &name,
		Project:  &project,
		Prompt:   &prompt,
		Backend:  &backend,
		Schedule: &schedule,
		Enabled:  &enabled,
	}
	if !s.RunOnceAt.IsZero() {
		runOnceAt := s.RunOnceAt.Format(time.RFC3339)
		req.RunOnceAt = &runOnceAt
	}
	return req
}

func stringsOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func stringSliceFromAny(v any) []string {
	if v == nil {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// timeParse tries RFC3339Nano then RFC3339. Mirrors the server's parse path.
func timeParse(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}
