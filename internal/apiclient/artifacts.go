package apiclient

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/drn/argus/internal/model"
)

func artifactPath(taskID, filename string) string {
	return "/api/tasks/" + url.PathEscape(taskID) + "/artifacts/" + url.PathEscape(filename)
}

// Artifacts returns the per-task registered manifest.
func (c *Client) Artifacts(ctx context.Context, taskID string) ([]*model.Artifact, error) {
	var out struct {
		Artifacts []*model.Artifact `json:"artifacts"`
	}
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+url.PathEscape(taskID)+"/artifacts", nil, &out); err != nil {
		return nil, err
	}
	return out.Artifacts, nil
}

// ReadArtifact copies at most limit bytes into dst; a Range request keeps
// previews cheap while io.LimitReader guards a server that ignores Range.
func (c *Client) ReadArtifact(ctx context.Context, taskID, filename string, dst io.Writer, limit int64) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("artifact limit must be positive")
	}
	resp, err := c.do(ctx, "GET", artifactPath(taskID, filename), nil, "", map[string]string{"Range": fmt.Sprintf("bytes=0-%d", limit-1)})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	n, err := io.Copy(dst, io.LimitReader(resp.Body, limit))
	return n, err
}

// DownloadArtifact streams a registered artifact with a hard size limit.
func (c *Client) DownloadArtifact(ctx context.Context, taskID, filename string, dst io.Writer, limit int64) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("artifact limit must be positive")
	}
	// A user-requested media transfer may legitimately take longer than the
	// ordinary 30-second API timeout. Context cancellation owns its lifetime.
	hc := *c.hc
	hc.Timeout = 0
	resp, err := c.doWithClient(&hc, ctx, "GET", artifactPath(taskID, filename), nil, "", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	n, err := io.Copy(dst, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, fmt.Errorf("artifact exceeds %d byte limit", limit)
	}
	return n, nil
}
