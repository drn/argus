package doctor

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// This file pins the add-secrets-resolver-registry change's binary-coherence
// delta spec ("Secrets bootstrap diagnostic"), mirroring profilelib_test.go's
// split between a pure classifier (TestDiagnoseSecretsBootstrap) and its
// rendered text (TestRenderSecretsBootstrap). It covers the first 3 of that
// requirement's 4 scenarios — the 4th ("Check does not change the exit-code
// contract") is pinned in cmd/argus/doctor_test.go, where runDoctor's
// os.Exit-governing doctor.Diagnose(actors) call actually lives.
//
// Fails to compile until Stage 5.1 adds SecretsBootstrapStatus (+ constants),
// DiagnoseSecretsBootstrap, and RenderSecretsBootstrap to
// internal/doctor/secretsstatus.go.

func TestDiagnoseSecretsBootstrap(t *testing.T) {
	tests := []struct {
		name       string
		configured bool
		resolved   bool
		want       SecretsBootstrapStatus
	}{
		{
			name:       "resolved: configured and resolves",
			configured: true,
			resolved:   true,
			want:       SecretsBootstrapResolved,
		},
		{
			name:       "not resolved: configured but fails",
			configured: true,
			resolved:   false,
			want:       SecretsBootstrapNotResolved,
		},
		{
			name:       "not configured: absent",
			configured: false,
			resolved:   false,
			want:       SecretsBootstrapNotConfigured,
		},
		{
			// "resolved" must never override an absent configuration — NOT
			// CONFIGURED is reported distinctly from NOT RESOLVED regardless.
			name:       "not configured: absent takes precedence over a stray resolved=true",
			configured: false,
			resolved:   true,
			want:       SecretsBootstrapNotConfigured,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiagnoseSecretsBootstrap(tt.configured, tt.resolved)
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestRenderSecretsBootstrap(t *testing.T) {
	tests := []struct {
		name         string
		status       SecretsBootstrapStatus
		wantContains []string
		wantExcludes []string
	}{
		{
			name:         "resolved",
			status:       SecretsBootstrapResolved,
			wantContains: []string{"RESOLVED"},
			wantExcludes: []string{"NOT RESOLVED", "NOT CONFIGURED"},
		},
		{
			name:         "not resolved",
			status:       SecretsBootstrapNotResolved,
			wantContains: []string{"NOT RESOLVED"},
			wantExcludes: []string{"NOT CONFIGURED"},
		},
		{
			name:         "not configured",
			status:       SecretsBootstrapNotConfigured,
			wantContains: []string{"NOT CONFIGURED"},
			wantExcludes: []string{"NOT RESOLVED"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderSecretsBootstrap(tt.status)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("RenderSecretsBootstrap(%v) = %q, want substring %q", tt.status, got, want)
				}
			}
			for _, unwanted := range tt.wantExcludes {
				if strings.Contains(got, unwanted) {
					t.Errorf("RenderSecretsBootstrap(%v) = %q, must NOT contain %q", tt.status, got, unwanted)
				}
			}
		})
	}
}
