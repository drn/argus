//go:build darwin

package agent

import (
	"os"

	"golang.org/x/sys/unix"
)

// listTaggedProcs returns pid → session tag for every same-user process whose
// environment carries a session tag. Per-process read failures (exited
// mid-scan, other users, SIP-protected) are skipped silently; such a process
// still inherits the tag of a readable tagged ancestor.
func listTaggedProcs() (map[int]string, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid()) //nolint:gosec // uid fits uint32
	out := map[int]string{}
	ppid := map[int]int{}
	for i := range procs {
		p := &procs[i]
		pid := int(p.Proc.P_pid)
		if pid <= 0 || p.Eproc.Ucred.Uid != uid {
			continue
		}
		ppid[pid] = int(p.Eproc.Ppid)
		buf, err := unix.SysctlRaw("kern.procargs2", pid)
		if err != nil {
			continue
		}
		if tag, ok := sessionTagFromEnv(procArgs2Env(buf)); ok {
			out[pid] = tag
		}
	}
	inheritAncestorTags(ppid, out)
	return out, nil
}
