//go:build !darwin && !linux

package agent

// listTaggedProcs has no implementation on this platform: the session reaper
// does nothing.
func listTaggedProcs() (map[int]string, error) { return nil, nil }
