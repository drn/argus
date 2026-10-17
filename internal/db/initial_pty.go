package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const initialPTYLayoutKey = "ui.initial_pty_layout"

// InitialPTYReportTTL prevents a closed viewer's geometry becoming a lasting
// launch default. Live viewers refresh at a shorter heartbeat.
const InitialPTYReportTTL = 2 * time.Minute

type PTYSize struct{ Rows, Cols uint16 }

func (s PTYSize) Valid() bool { return s.Rows >= 5 && s.Cols >= 20 }

// InitialPTYLayout is a launch preference, never a reconciled-history anchor.
type InitialPTYLayout struct {
	Active      PTYSize
	Coordinator PTYSize
	Worker      PTYSize
	ReportedAt  time.Time
}

func (d *DB) PublishInitialPTYLayout(layout InitialPTYLayout) error {
	if !layout.Active.Valid() || layout.ReportedAt.IsZero() {
		return fmt.Errorf("initial PTY layout requires a valid active pane and timestamp")
	}
	data, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	return d.SetConfigValue(initialPTYLayoutKey, string(data))
}

// InitialPTYSize returns zero when there is no recent usable viewer report.
// Callers retain their existing defaults and explicit-size precedence.
func (d *DB) InitialPTYSize(role HeraRoleKind, now time.Time) (PTYSize, error) {
	d.mu.Lock()
	var raw string
	err := d.conn.QueryRow("SELECT value FROM config WHERE key = ?", initialPTYLayoutKey).Scan(&raw)
	d.mu.Unlock()
	if errors.Is(err, sql.ErrNoRows) {
		return PTYSize{}, nil
	}
	if err != nil {
		return PTYSize{}, err
	}
	var layout InitialPTYLayout
	if err := json.Unmarshal([]byte(raw), &layout); err != nil {
		return PTYSize{}, err
	}
	age := now.Sub(layout.ReportedAt)
	if age < 0 || age >= InitialPTYReportTTL || !layout.Active.Valid() {
		return PTYSize{}, nil
	}
	size := layout.Active
	if role == HeraKindCoordinator && layout.Coordinator.Valid() {
		size = layout.Coordinator
	}
	if (role == HeraKindWorker || role == HeraKindFreelance) && layout.Worker.Valid() {
		size = layout.Worker
	}
	return size, nil
}
