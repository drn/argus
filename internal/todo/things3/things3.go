// Package things3 implements the "things3" todo.Backend by driving the
// Things 3 macOS app via AppleScript (osascript). It is stateless — every
// call shells out fresh — and macOS-only: Things 3 does not exist elsewhere.
package things3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/todo"
)

func init() {
	todo.Register("things3", New)
}

// fieldSep/recordSep delimit the Go-parseable rows AppleScript prints to
// stdout. Both are non-printable control characters chosen to be vanishingly
// unlikely inside a real to-do title/notes; escapeAS strips them from any
// user-supplied text anyway, as a defensive backstop against desyncing the
// parser.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// notFoundSentinel is returned by the update/complete/delete scripts in place
// of a data row when the id does not exist in Things 3, so Go can report a
// clean "not found" error instead of parsing (or mis-parsing) an AppleScript
// exception's stderr text.
const notFoundSentinel = "__ARGUS_TODO_NOTFOUND__"

// osascriptTimeout bounds a single AppleScript round trip. Generous — Things 3
// is a local app with no network calls — but present so a wedged app (e.g. a
// blocking permission dialog) cannot hang an MCP tool call forever.
const osascriptTimeout = 10 * time.Second

// Backend drives Things 3 via osascript. Safe for concurrent use — it holds
// no mutable state beyond the immutable project/tag config.
type Backend struct {
	project string
	tag     string
	// run executes an AppleScript program and returns its trimmed stdout.
	// A field (not a bare package func) so tests substitute a fake instead of
	// touching the real Things 3 app.
	run func(ctx context.Context, script string) (string, error)
}

// New builds a Things 3 backend from cfg.Things3. Returns an error on any
// non-macOS host, since there is nothing to configure — Things 3 does not run
// there.
func New(cfg config.TodoConfig) (todo.Backend, error) {
	return newForOS(runtime.GOOS, cfg)
}

// newForOS is New with the OS injected, so the non-macOS error path is
// testable without a build-tag matrix (this test host is itself macOS).
func newForOS(goos string, cfg config.TodoConfig) (todo.Backend, error) {
	if goos != "darwin" {
		return nil, fmt.Errorf("things3: only supported on macOS (host is %s)", goos)
	}
	return &Backend{
		project: strings.TrimSpace(cfg.Things3.Project),
		tag:     strings.TrimSpace(cfg.Things3.Tag),
		run:     runOsascript,
	}, nil
}

func runOsascript(ctx context.Context, script string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, osascriptTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("osascript: %s", msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// escapeAS escapes s for embedding inside a double-quoted AppleScript string
// literal, and strips this package's own field/record separators so a
// pathological title/notes value can never desynchronize output parsing.
func escapeAS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, fieldSep, " ")
	s = strings.ReplaceAll(s, recordSep, " ")
	return s
}

// destination returns the AppleScript object expression for the configured
// project, or the Inbox when none is configured.
func (b *Backend) destination() string {
	if b.project != "" {
		return fmt.Sprintf(`project "%s"`, escapeAS(b.project))
	}
	return `list "Inbox"`
}

// rowExpr is the AppleScript fragment (given a to-do reference named `t`)
// that prints one field/record-delimited row: id, title, notes, status word
// ("open"/"completed"/"canceled").
const rowExpr = `(id of t) & "` + fieldSep + `" & (name of t) & "` + fieldSep + `" & (notes of t) & "` + fieldSep + `" & ((status of t) as string)`

func parseItem(row string) (todo.Item, error) {
	fields := strings.SplitN(row, fieldSep, 4)
	if len(fields) != 4 {
		return todo.Item{}, fmt.Errorf("things3: unparseable response %q", row)
	}
	return todo.Item{
		ID:    fields[0],
		Title: fields[1],
		Notes: fields[2],
		Done:  fields[3] != "open",
	}, nil
}

func parseItems(raw string) ([]todo.Item, error) {
	if raw == "" {
		return nil, nil
	}
	records := strings.Split(raw, recordSep)
	items := make([]todo.Item, 0, len(records))
	for _, rec := range records {
		item, err := parseItem(rec)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// Create implements todo.Backend.
func (b *Backend) Create(ctx context.Context, in todo.CreateInput) (todo.Item, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return todo.Item{}, errors.New("things3: title is required")
	}

	props := []string{fmt.Sprintf(`name:"%s"`, escapeAS(title))}
	if in.Notes != "" {
		props = append(props, fmt.Sprintf(`notes:"%s"`, escapeAS(in.Notes)))
	}
	if b.tag != "" {
		props = append(props, fmt.Sprintf(`tag names:"%s"`, escapeAS(b.tag)))
	}

	script := fmt.Sprintf(`tell application "Things3"
	set t to make new to do with properties {%s} at beginning of %s
	return %s
end tell`, strings.Join(props, ", "), b.destination(), rowExpr)

	out, err := b.run(ctx, script)
	if err != nil {
		return todo.Item{}, fmt.Errorf("things3: create: %w", err)
	}
	return parseItem(out)
}

// List implements todo.Backend. Returns only open (not completed/canceled)
// items from the configured project, or the Inbox when none is configured.
func (b *Backend) List(ctx context.Context) ([]todo.Item, error) {
	script := fmt.Sprintf(`tell application "Things3"
	set out to ""
	set theItems to (to dos of %s whose status is open)
	repeat with t in theItems
		if out is not "" then set out to out & "%s"
		set out to out & %s
	end repeat
	return out
end tell`, b.destination(), recordSep, rowExpr)

	out, err := b.run(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("things3: list: %w", err)
	}
	return parseItems(out)
}

// existsGuard is the AppleScript fragment every id-addressed mutation opens
// with, so a missing id resolves to notFoundSentinel instead of an
// AppleScript runtime exception whose stderr text this package would
// otherwise have to pattern-match.
func existsGuard(id string) string {
	return fmt.Sprintf(`if not (exists to do id "%s") then
		return "%s"
	end if
	set t to to do id "%s"`, escapeAS(id), notFoundSentinel, escapeAS(id))
}

func (b *Backend) runMutation(ctx context.Context, id, body string) (todo.Item, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return todo.Item{}, errors.New("things3: id is required")
	}
	script := fmt.Sprintf(`tell application "Things3"
	%s
	%s
	return %s
end tell`, existsGuard(id), body, rowExpr)

	out, err := b.run(ctx, script)
	if err != nil {
		return todo.Item{}, err
	}
	if out == notFoundSentinel {
		return todo.Item{}, fmt.Errorf("things3: to-do %q not found", id)
	}
	return parseItem(out)
}

// Update implements todo.Backend. A non-nil Title must not be blank — Things 3
// to-dos always have a name, so this mirrors Create's same requirement rather
// than silently clearing the item's title to empty.
func (b *Backend) Update(ctx context.Context, id string, in todo.UpdateInput) (todo.Item, error) {
	var sets []string
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return todo.Item{}, errors.New("things3: title cannot be empty")
		}
		sets = append(sets, fmt.Sprintf(`set name of t to "%s"`, escapeAS(title)))
	}
	if in.Notes != nil {
		sets = append(sets, fmt.Sprintf(`set notes of t to "%s"`, escapeAS(*in.Notes)))
	}
	item, err := b.runMutation(ctx, id, strings.Join(sets, "\n\t"))
	if err != nil {
		return todo.Item{}, fmt.Errorf("things3: update: %w", err)
	}
	return item, nil
}

// Complete implements todo.Backend. Sets the to-do's status to completed.
func (b *Backend) Complete(ctx context.Context, id string) (todo.Item, error) {
	item, err := b.runMutation(ctx, id, `set status of t to completed`)
	if err != nil {
		return todo.Item{}, fmt.Errorf("things3: complete: %w", err)
	}
	return item, nil
}

// Delete implements todo.Backend. Removes the to-do from Things 3 — the same
// "move to Trash" semantics as deleting it in the app itself.
func (b *Backend) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("things3: id is required")
	}
	script := fmt.Sprintf(`tell application "Things3"
	%s
	delete t
	return "OK"
end tell`, existsGuard(id))

	out, err := b.run(ctx, script)
	if err != nil {
		return fmt.Errorf("things3: delete: %w", err)
	}
	if out == notFoundSentinel {
		return fmt.Errorf("things3: to-do %q not found", id)
	}
	return nil
}
