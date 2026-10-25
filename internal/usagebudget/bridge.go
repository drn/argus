package usagebudget

import "sync"

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

// CachedClaudePctForAccount never borrows the default probe for a named
// account. The old hidden probe only measures Claude's default account.
func CachedClaudePctForAccount(account string) (float64, bool) {
	if reading, ok := bridgeReading(account); ok {
		return reading.Percentage, true
	}
	if account != "" && account != "default" {
		return 0, false
	}
	return CachedClaudePct()
}
