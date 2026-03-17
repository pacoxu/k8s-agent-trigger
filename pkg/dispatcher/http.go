// Package dispatcher provides the HTTP dispatcher for sending trigger events to Agent services.
package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	defaultMaxRetries      = 3
	defaultRetryBase       = 500 * time.Millisecond
	defaultMaxPayloadBytes = 64 * 1024
	defaultMaxResponseSize = 1 << 20 // 1 MiB
	maxRetryDelay          = 10 * time.Second
)

var validAgentStatuses = map[string]struct{}{
	"passed":  {},
	"failed":  {},
	"warning": {},
}

var (
	jitterRNGMu sync.Mutex
	jitterRNG   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// DispatchOptions configures retry and transport behavior for HTTPDispatcher.
type DispatchOptions struct {
	MaxRetries      int
	RetryBase       time.Duration
	Enabled         bool
	AuthToken       string
	MaxPayloadBytes int
	MaxResponseSize int64
}

// TriggerEvent represents a trigger event payload sent to the Agent service.
type TriggerEvent struct {
	TriggerType string `json:"triggerType"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Generation  int64  `json:"generation,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Timestamp   string `json:"timestamp"`
	EventID     string `json:"eventID,omitempty"`
	ResourceUID string `json:"resourceUID,omitempty"`
	ObservedAt  string `json:"observedAt,omitempty"`
}

// AgentResponse represents the response from the Agent service.
type AgentResponse struct {
	Status  string   `json:"status"`
	Summary string   `json:"summary"`
	Actions []string `json:"actions"`
}

// HTTPDispatcher sends trigger events to an Agent service via HTTP POST.
type HTTPDispatcher struct {
	endpoint        string
	httpClient      *http.Client
	maxRetries      int
	retryBase       time.Duration
	enabled         bool
	authToken       string
	maxPayloadBytes int
	maxResponseSize int64
}

// NewHTTPDispatcher creates a new HTTPDispatcher with default retry options.
func NewHTTPDispatcher(endpoint string, timeout time.Duration) *HTTPDispatcher {
	return NewHTTPDispatcherWithOptions(endpoint, timeout, DispatchOptions{
		MaxRetries:      defaultMaxRetries,
		RetryBase:       defaultRetryBase,
		Enabled:         true,
		MaxPayloadBytes: defaultMaxPayloadBytes,
		MaxResponseSize: defaultMaxResponseSize,
	})
}

// NewHTTPDispatcherWithOptions creates a new HTTPDispatcher with explicit options.
func NewHTTPDispatcherWithOptions(endpoint string, timeout time.Duration, opts DispatchOptions) *HTTPDispatcher {
	zeroOpts := DispatchOptions{}

	maxRetries := opts.MaxRetries
	if opts == zeroOpts {
		maxRetries = defaultMaxRetries
	}
	if maxRetries < 0 {
		maxRetries = defaultMaxRetries
	}

	retryBase := opts.RetryBase
	if retryBase <= 0 {
		retryBase = defaultRetryBase
	}

	enabled := opts.Enabled
	if !opts.Enabled && opts.MaxRetries == 0 && opts.RetryBase == 0 &&
		opts.AuthToken == "" && opts.MaxPayloadBytes == 0 && opts.MaxResponseSize == 0 {
		// Preserve old constructor behavior where dispatch is enabled by default.
		enabled = true
	}

	maxPayloadBytes := opts.MaxPayloadBytes
	if maxPayloadBytes <= 0 {
		maxPayloadBytes = defaultMaxPayloadBytes
	}

	maxResponseSize := opts.MaxResponseSize
	if maxResponseSize <= 0 {
		maxResponseSize = defaultMaxResponseSize
	}

	return &HTTPDispatcher{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		maxRetries:      maxRetries,
		retryBase:       retryBase,
		enabled:         enabled,
		authToken:       opts.AuthToken,
		maxPayloadBytes: maxPayloadBytes,
		maxResponseSize: maxResponseSize,
	}
}

// Dispatch sends a TriggerEvent to the configured Agent endpoint and returns the AgentResponse.
func (d *HTTPDispatcher) Dispatch(ctx context.Context, event TriggerEvent) (*AgentResponse, error) {
	if !d.enabled {
		observeDispatchMetrics(event.TriggerType, "disabled", "none", 0)
		return &AgentResponse{
			Status:  "warning",
			Summary: "dispatch disabled by configuration",
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if event.Timestamp == "" {
		event.Timestamp = now
	}
	if event.ObservedAt == "" {
		event.ObservedAt = now
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return nil, newDispatchError(errorTypePermanent, "failed to marshal trigger event", err, 0)
	}
	if len(payload) > d.maxPayloadBytes {
		return nil, newDispatchError(
			errorTypePermanent,
			fmt.Sprintf("trigger payload too large: %d bytes (max=%d)", len(payload), d.maxPayloadBytes),
			nil,
			0,
		)
	}

	maxAttempts := d.maxRetries + 1
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		start := time.Now()
		agentResp, dispatchErr := d.dispatchOnce(ctx, payload)
		if dispatchErr == nil {
			observeDispatchMetrics(event.TriggerType, "success", "none", time.Since(start))
			return agentResp, nil
		}

		errorType := "permanent"
		if IsTransient(dispatchErr) {
			errorType = "transient"
		}

		if IsTransient(dispatchErr) && attempt < maxAttempts {
			observeDispatchMetrics(event.TriggerType, "retry", errorType, time.Since(start))
			retryDelay := d.retryDelay(attempt)
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, newDispatchError(
					errorTypePermanent,
					"context canceled while waiting to retry dispatch",
					ctx.Err(),
					0,
				)
			case <-timer.C:
			}
			continue
		}

		observeDispatchMetrics(event.TriggerType, "failure", errorType, time.Since(start))
		return nil, dispatchErr
	}

	return nil, newDispatchError(errorTypePermanent, "dispatch attempts exhausted", nil, 0)
}

func (d *HTTPDispatcher) dispatchOnce(ctx context.Context, payload []byte) (*AgentResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, newDispatchError(errorTypePermanent, "failed to create HTTP request", err, 0)
	}
	req.Header.Set("Content-Type", "application/json")
	if d.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+d.authToken)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, classifyTransportError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, classifyStatusCode(resp.StatusCode)
	}

	limitedBody := io.LimitReader(resp.Body, d.maxResponseSize+1)
	bodyBytes, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, newDispatchError(errorTypePermanent, "failed reading agent response body", err, resp.StatusCode)
	}
	if int64(len(bodyBytes)) > d.maxResponseSize {
		return nil, newDispatchError(
			errorTypePermanent,
			fmt.Sprintf("agent response too large: %d bytes (max=%d)", len(bodyBytes), d.maxResponseSize),
			nil,
			resp.StatusCode,
		)
	}

	var agentResp AgentResponse
	if err := json.Unmarshal(bodyBytes, &agentResp); err != nil {
		return nil, newDispatchError(errorTypePermanent, "failed to decode agent response", err, resp.StatusCode)
	}
	if _, ok := validAgentStatuses[agentResp.Status]; !ok {
		return nil, newDispatchError(
			errorTypePermanent,
			fmt.Sprintf("agent response has invalid status %q", agentResp.Status),
			nil,
			resp.StatusCode,
		)
	}

	return &agentResp, nil
}

func classifyTransportError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return newDispatchError(errorTypePermanent, "dispatch canceled", err, 0)
	case errors.Is(err, context.DeadlineExceeded):
		return newDispatchError(errorTypeTransient, "dispatch timed out", err, 0)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return newDispatchError(errorTypeTransient, "network timeout while dispatching trigger event", err, 0)
		}
		return newDispatchError(errorTypeTransient, "network error while dispatching trigger event", err, 0)
	}

	return newDispatchError(errorTypeTransient, "failed to dispatch trigger event", err, 0)
}

func classifyStatusCode(statusCode int) error {
	if statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError {
		return newDispatchError(
			errorTypeTransient,
			"agent service returned retryable status code",
			nil,
			statusCode,
		)
	}
	return newDispatchError(
		errorTypePermanent,
		"agent service returned non-retryable status code",
		nil,
		statusCode,
	)
}

func (d *HTTPDispatcher) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := d.retryBase
	for i := 1; i < attempt; i++ {
		if delay >= maxRetryDelay/2 {
			delay = maxRetryDelay
			break
		}
		delay *= 2
	}
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}

	jitterRNGMu.Lock()
	jitterMultiplier := 0.8 + (jitterRNG.Float64() * 0.4)
	jitterRNGMu.Unlock()
	jittered := time.Duration(float64(delay) * jitterMultiplier)
	if jittered < time.Millisecond {
		return time.Millisecond
	}
	if jittered > maxRetryDelay {
		return maxRetryDelay
	}
	return jittered
}
