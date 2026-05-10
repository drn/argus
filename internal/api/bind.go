package api

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"time"
)

// tailscaleCGNAT is the IPv4 CGNAT range Tailscale uses for node addresses.
// RFC 6598 shared address space — also used by Cloudflare WARP, Twingate,
// and some ISP CGNAT tunnels, so the range alone is NOT sufficient to
// identify a Tailscale interface. Used by the fallback scan only.
var tailscaleCGNAT = func() *net.IPNet {
	_, n, err := net.ParseCIDR("100.64.0.0/10")
	if err != nil {
		panic(err)
	}
	return n
}()

// tailscaleIP returns the host's Tailscale IPv4 address, or nil if Tailscale
// is not running or no v4 address is assigned. Two-tier discovery:
//
//  1. Prefer `tailscale ip -4` — talks to the Tailscale LocalAPI socket and
//     returns the node's authoritative address. Distinguishes Tailscale from
//     other RFC 6598 CGNAT consumers.
//  2. Fallback: scan local interfaces for any 100.64.0.0/10 address. Less
//     precise — any CGNAT VPN matches — but covers users who installed
//     Tailscale without putting the CLI on PATH (the macOS App Store build
//     doesn't add it by default). On a host running multiple CGNAT VPNs
//     (Tailscale + Cloudflare WARP, etc.) the fallback may return the wrong
//     tunnel; the caller logs the chosen address so the user can spot it.
func tailscaleIP() net.IP {
	if ip := tailscaleIPFromCLI(); ip != nil {
		return ip
	}
	return tailscaleIPFromInterfaces()
}

// tailscaleIPFromCLI invokes `tailscale ip -4` with a 5s timeout. Returns
// the first parsed IPv4 CGNAT address, or nil on missing CLI / non-zero
// exit / unparseable output / non-CGNAT result. The CGNAT guard is
// defense-in-depth: a hostile `tailscale` shim earlier on $PATH could
// return 0.0.0.0 / loopback / arbitrary IPs and trick the daemon into
// binding interfaces it shouldn't. The fallback scan already filters
// through the same range, so both discovery paths are now consistent.
//
// 5s is generous for a local LocalAPI call (typical: <50ms) but leaves
// headroom under heavy parallel test stress where fork+exec+shell-startup
// for the test shim can spike above 1s on macOS race-enabled builds.
func tailscaleIPFromCLI() net.IP {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", "ip", "-4").Output()
	if err != nil {
		return nil
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if ip := net.ParseIP(strings.TrimSpace(line)); ip != nil {
			if v4 := ip.To4(); v4 != nil && tailscaleCGNAT.Contains(v4) {
				return v4
			}
		}
	}
	return nil
}

// tailscaleIPFromInterfaces scans every interface for an IPv4 address in the
// CGNAT range. Returns nil when no such address is configured. Cannot tell
// Tailscale apart from other CGNAT VPNs (WARP, Twingate) — see selectTailscaleIP.
func tailscaleIPFromInterfaces() net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var all []net.Addr
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		all = append(all, addrs...)
	}
	return selectTailscaleIP(all)
}

// selectTailscaleIP picks the first IPv4 address in the CGNAT range from a
// slice of interface addresses. Split out from tailscaleIPFromInterfaces so
// the matching logic can be tested without standing up real interfaces.
func selectTailscaleIP(addrs []net.Addr) net.IP {
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.To4()
		if ip == nil {
			continue
		}
		if tailscaleCGNAT.Contains(ip) {
			return ip
		}
	}
	return nil
}
