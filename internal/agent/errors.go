package agent

import "errors"

var (
	ErrAlreadyAttached = errors.New("session is already attached")
	ErrNotRunning      = errors.New("process is not running")
	ErrSessionNotFound = errors.New("session not found")
)
