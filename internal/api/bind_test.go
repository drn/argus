package api

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestSelectTailscaleIP(t *testing.T) {
	mk := func(cidrs ...string) []net.Addr {
		out := make([]net.Addr, 0, len(cidrs))
		for _, c := range cidrs {
			ip, ipnet, err := net.ParseCIDR(c)
			if err != nil {
				t.Fatalf("parse %q: %v", c, err)
			}
			out = append(out, &net.IPNet{IP: ip, Mask: ipnet.Mask})
		}
		return out
	}

	tests := []struct {
		name string
		in   []net.Addr
		want string // empty = expect nil
	}{
		{
			name: "tailscale CGNAT match",
			in:   mk("192.168.1.5/24", "100.96.42.7/32"),
			want: "100.96.42.7",
		},
		{
			name: "first CGNAT address wins",
			in:   mk("100.64.0.1/32", "100.127.255.254/32"),
			want: "100.64.0.1",
		},
		{
			name: "skips IPv6 only addresses",
			in:   mk("fe80::1/64", "100.100.100.100/32"),
			want: "100.100.100.100",
		},
		{
			name: "non-CGNAT 100.x not matched",
			// 100.0.0.1 is outside CGNAT (100.64-127); shouldn't match.
			in:   mk("100.0.0.1/24", "10.0.0.1/8"),
			want: "",
		},
		{
			name: "no addresses returns nil",
			in:   nil,
			want: "",
		},
		{
			name: "skips non-IPNet types",
			in:   []net.Addr{&net.TCPAddr{IP: net.ParseIP("100.96.0.1"), Port: 80}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectTailscaleIP(tt.in)
			if tt.want == "" {
				testutil.Nil(t, got)
				return
			}
			testutil.Equal(t, got.String(), tt.want)
		})
	}
}

// TestTailscaleIPFromCLI_RejectsBareNonCGNATOutput protects the defense-in-depth
// guard in `tailscaleIPFromCLI` against a PATH-poisoned `tailscale` shim that
// returns 0.0.0.0 (or any non-CGNAT address). We swap in a tiny shell shim via
// PATH and assert the function returns nil despite the shim "succeeding".
func TestTailscaleIPFromCLI_RejectsBareNonCGNATOutput(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "tailscale")
	// Print 0.0.0.0 — a real Tailscale node never assigns this, so any
	// return value here means the CGNAT guard didn't filter it.
	if err := os.WriteFile(shim, []byte("#!/bin/sh\necho 0.0.0.0\n"), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", dir)

	got := tailscaleIPFromCLI()
	testutil.Nil(t, got)
}

// TestTailscaleIPFromCLI_AcceptsCGNATOutput is the positive-path companion: a
// shim that returns a 100.x address (the only legitimate Tailscale output)
// must round-trip through the function unmodified.
func TestTailscaleIPFromCLI_AcceptsCGNATOutput(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "tailscale")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\necho 100.96.42.7\n"), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", dir)

	got := tailscaleIPFromCLI()
	if got == nil {
		t.Fatal("expected CLI to return 100.96.42.7, got nil")
	}
	testutil.Equal(t, got.String(), "100.96.42.7")
}

func TestBindWithRetry_Success(t *testing.T) {
	// Probe a free port (port 0 lets the OS pick), release, then ask
	// bindWithRetry to use that port. Avoids hardcoding ports.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	free := probe.Addr().(*net.TCPAddr).Port
	probe.Close() //nolint:errcheck

	ln, port, err := bindWithRetry("127.0.0.1", free, 1)
	testutil.NoError(t, err)
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck
	testutil.Equal(t, port, free)
}

func TestBindWithRetry_PortConflictAdvances(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	t.Cleanup(func() { hold.Close() }) //nolint:errcheck
	held := hold.Addr().(*net.TCPAddr).Port

	ln, actual, err := bindWithRetry("127.0.0.1", held, 2)
	testutil.NoError(t, err)
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck
	testutil.Equal(t, actual, held+1)
}

func TestBindWithRetry_AllPortsFail_WrapsSyscallError(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	t.Cleanup(func() { hold.Close() }) //nolint:errcheck
	held := hold.Addr().(*net.TCPAddr).Port

	_, _, err = bindWithRetry("127.0.0.1", held, 1)
	if err == nil {
		t.Fatal("expected bindWithRetry to fail when only port is occupied")
	}
	if !strings.Contains(err.Error(), "failed to bind") {
		t.Fatalf("error should explain bind failure, got: %v", err)
	}
	// Underlying syscall error must be unwrappable so daemon logs and
	// `errors.Is(err, syscall.EADDRINUSE)` checks still work.
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("expected wrapped EADDRINUSE, got: %v", err)
	}
}
