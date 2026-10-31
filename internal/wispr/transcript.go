package wispr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/drn/argus/internal/uxlog"
)

var (
	ErrDismissed = errors.New("wispr: transcript dismissed or failed")
	ErrTimeout   = errors.New("wispr: timed out waiting for transcript")
	ErrNoDB      = errors.New("wispr: history database not found")
)

// Transcript is one finished dictation from Wispr's history.
type Transcript struct {
	ID, Text, Status, App string
	At                    time.Time
}

const tsLayout = "2006-01-02 15:04:05.000 -07:00"

const historyQuery = `SELECT transcriptEntityId, asrText, formattedText, CAST(timestamp AS TEXT), status, app
FROM History WHERE timestamp >= ? ORDER BY timestamp ASC`

// WaitTranscript polls Wispr's history until a final transcript recorded at or
// after since appears, the entry is dismissed, or timeout elapses.
func (d *Driver) WaitTranscript(ctx context.Context, since time.Time, timeout time.Duration) (Transcript, error) {
	path := d.dbPath()
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Transcript{}, ErrNoDB
		}
		return Transcript{}, fmt.Errorf("wispr: stat db: %w", err)
	}

	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return Transcript{}, fmt.Errorf("wispr: open db: %w", err)
	}
	defer db.Close() //nolint:errcheck
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sinceStr := since.UTC().Format(tsLayout)
	ticker := time.NewTicker(d.poll())
	defer ticker.Stop()

	for {
		tr, done, err := queryFinal(ctx, db, sinceStr)
		switch {
		case done && err == nil:
			uxlog.Log("[wispr] transcript ready: status=%s len=%d", tr.Status, len(tr.Text))
			return tr, nil
		case done:
			uxlog.Log("[wispr] transcript dismissed")
			return Transcript{}, err
		case err != nil && ctx.Err() == nil:
			uxlog.Log("[wispr] history query failed: %v", err)
		}

		select {
		case <-ctx.Done():
			uxlog.Log("[wispr] wait ended without transcript: %v", ctx.Err())
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return Transcript{}, ErrTimeout
			}
			return Transcript{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// queryFinal reports done=true with a transcript, or done=true with
// ErrDismissed, or done=false when the caller should keep waiting.
func queryFinal(ctx context.Context, db *sql.DB, since string) (Transcript, bool, error) {
	rows, err := db.QueryContext(ctx, historyQuery, since)
	if err != nil {
		return Transcript{}, false, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, asr, formatted, ts, status, app sql.NullString
		if err := rows.Scan(&id, &asr, &formatted, &ts, &status, &app); err != nil {
			return Transcript{}, false, err
		}
		var text string
		switch status.String {
		case "formatted":
			text = strings.TrimSpace(formatted.String)
			if text == "" {
				text = strings.TrimSpace(asr.String)
			}
		case "raw_transcript":
			text = strings.TrimSpace(asr.String)
		case "dismissed", "error":
			return Transcript{}, true, ErrDismissed
		}
		if text == "" {
			continue
		}
		at, _ := time.Parse(tsLayout, ts.String)
		return Transcript{ID: id.String, Text: text, Status: status.String, App: app.String, At: at}, true, nil
	}
	return Transcript{}, false, rows.Err()
}
