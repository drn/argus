//go:build !darwin

package hotkey

// Tap is a no-op placeholder on platforms without a global event tap.
type Tap struct{}

// InputMonitoringGranted always reports false off macOS.
func InputMonitoringGranted() bool { return false }

// RequestInputMonitoring always reports false off macOS.
func RequestInputMonitoring() bool { return false }

// StartTap always returns ErrUnsupported off macOS.
func StartTap(Combo, func(Event)) (*Tap, error) { return nil, ErrUnsupported }

// Close is a no-op.
func (t *Tap) Close() {}
