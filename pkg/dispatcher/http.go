// Package dispatcher provides the HTTP dispatcher for sending trigger events to Agent services.
package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TriggerEvent represents a trigger event payload sent to the Agent service.
type TriggerEvent struct {
	TriggerType string `json:"triggerType"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Generation  int64  `json:"generation,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Timestamp   string `json:"timestamp"`
}

// AgentResponse represents the response from the Agent service.
type AgentResponse struct {
	Status  string   `json:"status"`
	Summary string   `json:"summary"`
	Actions []string `json:"actions"`
}

// HTTPDispatcher sends trigger events to an Agent service via HTTP POST.
type HTTPDispatcher struct {
	endpoint   string
	httpClient *http.Client
}

// NewHTTPDispatcher creates a new HTTPDispatcher with the given endpoint and timeout.
func NewHTTPDispatcher(endpoint string, timeout time.Duration) *HTTPDispatcher {
	return &HTTPDispatcher{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Dispatch sends a TriggerEvent to the configured Agent endpoint and returns the AgentResponse.
func (d *HTTPDispatcher) Dispatch(ctx context.Context, event TriggerEvent) (*AgentResponse, error) {
	event.Timestamp = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal trigger event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to dispatch trigger event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("agent service returned non-2xx status: %d", resp.StatusCode)
	}

	var agentResp AgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&agentResp); err != nil {
		return nil, fmt.Errorf("failed to decode agent response: %w", err)
	}

	return &agentResp, nil
}
