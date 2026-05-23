package apiclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// OutputResult is the response from GetOutput: the byte tail plus the cursor
// the client should pass as ?since=N on a subsequent StreamOutput call to
// resume without overlap. Source is "ring", "log", or "live" (mirrors the
// X-Source header the SPA inspects for diagnostics).
type OutputResult struct {
	Data        []byte
	OutputTotal uint64
	Source      string
}

// GetOutput fetches the most recent terminal output for a task. tailBytes
// caps the response body; pass 0 for the server default (32 KiB). clean
// strips ANSI control sequences when true.
func (c *Client) GetOutput(ctx context.Context, id string, tailBytes int, clean bool) (*OutputResult, error) {
	q := ""
	if tailBytes > 0 || clean {
		q = "?"
		if tailBytes > 0 {
			q += "bytes=" + strconv.Itoa(tailBytes)
		}
		if clean {
			if q != "?" {
				q += "&"
			}
			q += "clean=1"
		}
	}
	resp, err := c.do(ctx, "GET", "/api/tasks/"+id+"/output"+q, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("apiclient: read output body: %w", err)
	}
	total := uint64(0)
	if v := resp.Header.Get("X-Output-Total"); v != "" {
		if n, perr := strconv.ParseUint(v, 10, 64); perr == nil {
			total = n
		}
	}
	return &OutputResult{Data: data, OutputTotal: total, Source: resp.Header.Get("X-Source")}, nil
}

// WriteInput sends bytes to the agent's PTY. Capped at 64 KiB by the server.
func (c *Client) WriteInput(ctx context.Context, id string, data []byte) error {
	resp, err := c.do(ctx, "POST", "/api/tasks/"+id+"/input", bytesReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// PTYSize is the cols/rows the active session is sized to.
type PTYSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// GetSize fetches the active PTY size for a task.
func (c *Client) GetSize(ctx context.Context, id string) (*PTYSize, error) {
	var resp PTYSize
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/size", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ResizeResp mirrors the resize handler's response envelope.
type ResizeResp struct {
	Cols       int  `json:"cols"`
	Rows       int  `json:"rows"`
	Rerendered bool `json:"rerendered"`
}

// Resize sends a SIGWINCH-equivalent resize to the agent's PTY.
func (c *Client) Resize(ctx context.Context, id string, rows, cols uint16) (*ResizeResp, error) {
	req := map[string]uint16{"rows": rows, "cols": cols}
	var resp ResizeResp
	if err := c.doJSON(ctx, "POST", "/api/tasks/"+id+"/resize", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// StreamOutput opens a long-lived SSE connection to the task's PTY output.
// The returned http.Response.Body is the raw event stream — caller is
// responsible for parsing the `data: <base64>\n\n` framing and closing the
// body. since is the cursor previously advertised by GetOutput.
//
// This bypasses the *http.Client.Timeout because SSE is long-lived; the
// caller controls cancellation via ctx.
func (c *Client) StreamOutput(ctx context.Context, id string, since uint64) (*http.Response, error) {
	path := "/api/tasks/" + id + "/stream"
	if since > 0 {
		path += "?since=" + strconv.FormatUint(since, 10)
	}
	// Bypass c.do because we want to return the live body without consuming
	// it. Use a Request directly so we can disable any client-side timeout.
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("apiclient: build stream req: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")
	// Use a stream-dedicated client without Timeout so chunked transfer can
	// sit idle indefinitely (server emits a 30s keepalive `:ping` to keep
	// proxies honest).
	hc := *c.hc
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: stream %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := readErrorMessage(resp.Body)
		resp.Body.Close()
		return nil, &Error{Status: resp.StatusCode, Method: "GET", Path: path, Message: msg}
	}
	return resp, nil
}
