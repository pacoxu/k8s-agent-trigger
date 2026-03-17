package dispatcher

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

type fakeNetError struct {
	timeout bool
}

func (e fakeNetError) Error() string   { return "fake net error" }
func (e fakeNetError) Timeout() bool   { return e.timeout }
func (e fakeNetError) Temporary() bool { return true }

func TestDispatchErrorHelpers(t *testing.T) {
	t.Parallel()

	root := errors.New("root")
	errWithStatus := newDispatchError(errorTypePermanent, "failed", root, 400)
	if !strings.Contains(errWithStatus.Error(), "status=400") {
		t.Fatalf("error string %q missing status code", errWithStatus.Error())
	}
	if !errors.Is(errWithStatus, root) {
		t.Fatalf("expected wrapped root cause")
	}

	if IsTransient(errWithStatus) {
		t.Fatal("expected permanent error classification")
	}

	transient := NewTransientError("temporary", root)
	if !IsTransient(transient) {
		t.Fatal("expected transient classification")
	}

	permanent := NewPermanentError("bad request", root)
	if IsTransient(permanent) {
		t.Fatal("expected permanent classification")
	}
}

func TestClassifyTransportError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		err           error
		wantTransient bool
	}{
		{
			name:          "context canceled",
			err:           context.Canceled,
			wantTransient: false,
		},
		{
			name:          "deadline exceeded",
			err:           context.DeadlineExceeded,
			wantTransient: true,
		},
		{
			name:          "net timeout",
			err:           fakeNetError{timeout: true},
			wantTransient: true,
		},
		{
			name:          "net non-timeout",
			err:           fakeNetError{timeout: false},
			wantTransient: true,
		},
		{
			name:          "unknown",
			err:           errors.New("unknown"),
			wantTransient: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTransportError(tt.err)
			if IsTransient(got) != tt.wantTransient {
				t.Fatalf("IsTransient(classifyTransportError(%v)) = %v, want %v", tt.err, IsTransient(got), tt.wantTransient)
			}
		})
	}
}

func TestRetryDelayBounds(t *testing.T) {
	t.Parallel()

	d := NewHTTPDispatcherWithOptions("http://example.com", 5*time.Second, DispatchOptions{
		Enabled:   true,
		RetryBase: time.Millisecond,
	})

	delay := d.retryDelay(0)
	if delay < time.Millisecond {
		t.Fatalf("retryDelay(0) = %s, want >= 1ms", delay)
	}

	delay = d.retryDelay(20)
	if delay > maxRetryDelay {
		t.Fatalf("retryDelay(20) = %s, want <= %s", delay, maxRetryDelay)
	}
}

func TestObserveDispatchMetrics_UnknownTriggerType(t *testing.T) {
	t.Parallel()
	observeDispatchMetrics("", "success", "", 10*time.Millisecond)
}

func TestNewHTTPDispatcherDefaultConstructor(t *testing.T) {
	t.Parallel()
	d := NewHTTPDispatcher("http://example.com", time.Second)
	if d == nil {
		t.Fatal("NewHTTPDispatcher() returned nil")
	}
	if !d.enabled {
		t.Fatal("default dispatcher should be enabled")
	}
	if d.maxRetries != defaultMaxRetries {
		t.Fatalf("default max retries = %d, want %d", d.maxRetries, defaultMaxRetries)
	}
}

func TestNewHTTPDispatcherWithZeroOptionsUsesDefaults(t *testing.T) {
	t.Parallel()
	d := NewHTTPDispatcherWithOptions("http://example.com", time.Second, DispatchOptions{})
	if d.maxRetries != defaultMaxRetries {
		t.Fatalf("max retries = %d, want default %d", d.maxRetries, defaultMaxRetries)
	}
	if !d.enabled {
		t.Fatal("zero options should enable dispatch by default")
	}
}

func TestDispatchPayloadTooLarge(t *testing.T) {
	t.Parallel()

	d := NewHTTPDispatcherWithOptions("http://example.com", 2*time.Second, DispatchOptions{
		Enabled:         true,
		MaxPayloadBytes: 16,
	})

	_, err := d.Dispatch(context.Background(), TriggerEvent{
		TriggerType: "DeploymentUpdate",
		Namespace:   "default",
		Name:        strings.Repeat("x", 64),
	})
	if err == nil {
		t.Fatal("expected payload too large error")
	}
	if IsTransient(err) {
		t.Fatal("payload too large should be permanent")
	}
}

func TestClassifyTransportError_WithWrappedNetError(t *testing.T) {
	t.Parallel()
	wrapped := &net.OpError{Err: fakeNetError{timeout: true}}
	err := classifyTransportError(wrapped)
	if !IsTransient(err) {
		t.Fatal("wrapped net timeout should be transient")
	}
}
