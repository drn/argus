package agent

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestDetachReader_NoCtrlQ(t *testing.T) {
	input := bytes.NewReader([]byte("hello world"))
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("dr-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	dr := &detachReader{
		reader:  input,
		session: sess,
	}

	buf := make([]byte, 64)
	n, err := dr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(buf[:n]) != "hello world" {
		t.Errorf("Read() = %q, want %q", string(buf[:n]), "hello world")
	}
}

func TestDetachReader_CtrlQAtStart(t *testing.T) {
	// 0x11 is ctrl+q, followed by normal data
	input := bytes.NewReader([]byte{0x11, 'a', 'b', 'c'})
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("dr-2", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	dr := &detachReader{
		reader:  input,
		session: sess,
	}

	buf := make([]byte, 64)
	n, err := dr.Read(buf)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
	// Should return remaining bytes after ctrl+q removed
	if n != 3 {
		t.Errorf("Read() returned %d bytes, want 3", n)
	}
	if string(buf[:n]) != "abc" {
		t.Errorf("Read() = %q, want %q", string(buf[:n]), "abc")
	}
}

func TestDetachReader_CtrlQInMiddle(t *testing.T) {
	input := bytes.NewReader([]byte{'a', 'b', 0x11, 'c', 'd'})
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("dr-3", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	dr := &detachReader{
		reader:  input,
		session: sess,
	}

	buf := make([]byte, 64)
	n, err := dr.Read(buf)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
	// 5 bytes read, ctrl+q removed = 4 bytes
	if n != 4 {
		t.Errorf("Read() returned %d bytes, want 4", n)
	}
	if string(buf[:n]) != "abcd" {
		t.Errorf("Read() = %q, want %q", string(buf[:n]), "abcd")
	}
}

func TestDetachReader_CtrlQOnly(t *testing.T) {
	input := bytes.NewReader([]byte{0x11})
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("dr-4", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	dr := &detachReader{
		reader:  input,
		session: sess,
	}

	buf := make([]byte, 64)
	n, err := dr.Read(buf)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
	if n != 0 {
		t.Errorf("Read() returned %d bytes, want 0", n)
	}
}

func TestDetachReader_EmptyRead(t *testing.T) {
	input := bytes.NewReader([]byte{})
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("dr-5", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	dr := &detachReader{
		reader:  input,
		session: sess,
	}

	buf := make([]byte, 64)
	n, err := dr.Read(buf)
	if n != 0 {
		t.Errorf("Read() returned %d bytes, want 0", n)
	}
	if err != io.EOF {
		t.Errorf("expected io.EOF from empty reader, got %v", err)
	}
}

func TestHeaderWriter_Write(t *testing.T) {
	var buf bytes.Buffer
	hw := &headerWriter{
		inner:    &buf,
		taskName: "test-task",
		cols:     80,
		rows:     24,
	}

	data := []byte("hello from agent")
	n, err := hw.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(data) {
		t.Errorf("Write() returned %d, want %d", n, len(data))
	}
	if buf.String() != "hello from agent" {
		t.Errorf("inner writer got %q, want %q", buf.String(), "hello from agent")
	}
}

func TestHeaderWriter_DrawHeader(t *testing.T) {
	var buf bytes.Buffer
	hw := &headerWriter{
		inner:    &buf,
		taskName: "my-task",
		cols:     120,
		rows:     40,
	}

	hw.drawHeader()

	output := buf.String()
	if len(output) == 0 {
		t.Fatal("drawHeader produced no output")
	}
	if !strings.Contains(output, "ARGUS") {
		t.Error("drawHeader output should contain 'ARGUS'")
	}
	if !strings.Contains(output, "my-task") {
		t.Error("drawHeader output should contain the task name")
	}
}

func TestHeaderWriter_DrawHeader_NarrowTerminal(t *testing.T) {
	var buf bytes.Buffer
	hw := &headerWriter{
		inner:    &buf,
		taskName: "a-very-long-task-name-that-should-get-truncated",
		cols:     40,
		rows:     24,
	}

	// Should not panic with narrow terminal
	hw.drawHeader()

	output := buf.String()
	if len(output) == 0 {
		t.Fatal("drawHeader produced no output")
	}
	if !strings.Contains(output, "ARGUS") {
		t.Error("drawHeader output should contain 'ARGUS'")
	}
}

func TestHeaderWriter_DrawHeader_VeryNarrow(t *testing.T) {
	var buf bytes.Buffer
	hw := &headerWriter{
		inner:    &buf,
		taskName: "task",
		cols:     10,
		rows:     24,
	}

	// Should not panic even with extremely narrow terminal
	hw.drawHeader()

	// Just verify it produced some output without panicking
	if buf.Len() == 0 {
		t.Fatal("drawHeader produced no output")
	}
}

func TestAttachCmd_SetStdinStdout(t *testing.T) {
	ac := &AttachCmd{}

	var r bytes.Buffer
	var w bytes.Buffer
	ac.SetStdin(&r)
	ac.SetStdout(&w)

	if ac.stdin == nil {
		t.Error("stdin not set")
	}
	if ac.stdout == nil {
		t.Error("stdout not set")
	}
}

func TestAttachCmd_SetStderr(t *testing.T) {
	ac := &AttachCmd{}
	// SetStderr is a no-op; just ensure it doesn't panic
	ac.SetStderr(&bytes.Buffer{})
	ac.SetStderr(nil)
}
