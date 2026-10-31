package voicetask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/drn/argus/internal/config"
)

const (
	jevTimeout       = 3 * time.Second
	jevQuestion      = "project"
	jevNoneChoice    = "none"
	jevMaxRespBytes  = 1 << 20
	jevInstructions  = "Which listed software project is this spoken task request about? Choose none unless the request clearly names or describes one of them."
	jevNoneCriterion = "The request does not clearly name or describe any listed project"
)

// Classifier picks the project a transcript is about. An empty project with
// a nil error means no confident match.
type Classifier interface {
	Classify(ctx context.Context, transcript string, projects []string) (project string, prob float64, err error)
}

type jevClassifier struct {
	endpoint string
	model    string
	apiKey   string
	hc       *http.Client
}

// NewJevClassifier returns a Classifier backed by the TypeSafe Jev decision
// API. A nil hc uses a fresh default client.
func NewJevClassifier(cfg config.JevConfig, apiKey string, hc *http.Client) Classifier {
	if hc == nil {
		hc = &http.Client{}
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = config.DefaultJevEndpoint
	}
	return &jevClassifier{endpoint: endpoint, model: cfg.Model, apiKey: apiKey, hc: hc}
}

type jevQuestionSpec struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type jevRequest struct {
	Model     string                     `json:"model,omitempty"`
	State     string                     `json:"state"`
	Questions map[string]jevQuestionSpec `json:"questions"`
}

type jevAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type jevResponse struct {
	Answers map[string]jevAnswer `json:"answers"`
}

func (c *jevClassifier) Classify(ctx context.Context, transcript string, projects []string) (string, float64, error) {
	criteria := make(map[string]string, len(projects)+1)
	for _, p := range projects {
		criteria[p] = fmt.Sprintf("Software project %q", p)
	}
	criteria[jevNoneChoice] = jevNoneCriterion

	body, err := json.Marshal(jevRequest{
		Model: c.model,
		State: transcript,
		Questions: map[string]jevQuestionSpec{
			jevQuestion: {Type: "choice", Instructions: jevInstructions, Criteria: criteria},
		},
	})
	if err != nil {
		return "", 0, fmt.Errorf("jev: encode request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, jevTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		// *url.Error embeds only the URL, never request headers.
		return "", 0, fmt.Errorf("jev: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, jevMaxRespBytes))
	if err != nil {
		return "", 0, fmt.Errorf("jev: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", 0, fmt.Errorf("jev: unexpected status %d", resp.StatusCode)
	}

	var out jevResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, fmt.Errorf("jev: malformed response: %w", err)
	}
	ans, ok := out.Answers[jevQuestion]
	if !ok || ans.Choice == "" {
		return "", 0, errors.New("jev: response missing project answer")
	}
	if ans.Choice == jevNoneChoice {
		return "", 0, nil
	}
	return ans.Choice, ans.Probabilities[ans.Choice], nil
}
