package usagebudget

import (
	"sync"
	"time"
)

var bridgeReader struct {
	sync.RWMutex
	read func(string) (Reading, bool)
}

// SetBridgeReader installs the daemon's account-scoped bridge measurement
// source. The restore supports isolated tests and daemon shutdown.
func SetBridgeReader(read func(string) (Reading, bool)) func() {
	bridgeReader.Lock()
	previous := bridgeReader.read
	bridgeReader.read = read
	bridgeReader.Unlock()
	return func() { bridgeReader.Lock(); bridgeReader.read = previous; bridgeReader.Unlock() }
}

func bridgeReading(account string) (Reading, bool) {
	bridgeReader.RLock()
	read := bridgeReader.read
	bridgeReader.RUnlock()
	if read == nil {
		return Reading{}, false
	}
	return read(account)
}

// CachedClaudePctForAccount reads only this account's bridge or probe cache.
func CachedClaudePctForAccount(account string) (float64, bool) {
	reading, ok := CachedReadingForAccount(account)
	return reading.Percentage, ok
}

// CachedReadingForAccount returns this account's full reading (both windows)
// from its bridge measurement, else its probe cache. Blank and "default"
// address the default account. Never triggers a live probe.
func CachedReadingForAccount(account string) (Reading, bool) {
	if reading, ok := bridgeReading(account); ok {
		return reading, true
	}
	return snapshot(account)
}

// LiveWindows returns the reading's 5-hour and weekly windows that are still
// valid at now (not reset, not older than CacheMaxAge); invalid ones are nil.
func LiveWindows(r Reading, now time.Time) (fiveHour, sevenDay *Window) {
	live := func(w *Window) *Window {
		if w == nil || !w.ResetAt.After(now) {
			return nil
		}
		at := w.LastProbedAt
		if at.IsZero() {
			at = r.LastProbedAt
		}
		if now.Sub(at) > CacheMaxAge {
			return nil
		}
		return w
	}
	return live(r.FiveHour), live(r.SevenDay)
}
