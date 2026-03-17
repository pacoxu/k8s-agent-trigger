package dispatcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPDispatcher_Dispatch(t *testing.T) {
	tests := []struct {
		name       string
		event      TriggerEvent
		respStatus int
		respBody   AgentResponse
		wantErr    bool
	}{
		{
			name: "successful dispatch",
			event: TriggerEvent{
				TriggerType: "DeploymentUpdate",
				Namespace:   "default",
				Name:        "nginx-app",
				Generation:  3,
			},
			respStatus: http.StatusOK,
			respBody: AgentResponse{
				Status:  "passed",
				Summary: "Deployment nginx-app rolled out successfully.",
				Actions: []string{},
			},
			wantErr: false,
		},
		{
			name: "agent returns error status",
			event: TriggerEvent{
				TriggerType: "JobFailed",
				Namespace:   "default",
				Name:        "data-processing",
			},
			respStatus: http.StatusInternalServerError,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("expected Content-Type application/json")
				}
				w.WriteHeader(tt.respStatus)
				if tt.respStatus >= 200 && tt.respStatus < 300 {
					_ = json.NewEncoder(w).Encode(tt.respBody)
				}
			}))
			defer server.Close()

			d := NewHTTPDispatcherWithOptions(server.URL, 5*time.Second, DispatchOptions{
				MaxRetries: 1,
				Enabled:    true,
			})
			resp, err := d.Dispatch(context.Background(), tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("Dispatch() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && resp.Status != tt.respBody.Status {
				t.Errorf("Dispatch() status = %q, want %q", resp.Status, tt.respBody.Status)
			}
		})
	}
}

func TestHTTPDispatcher_RetrysTransientFailure(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&callCount, 1)
		if call < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(AgentResponse{Status: "passed", Summary: "ok"})
	}))
	defer server.Close()

	d := NewHTTPDispatcherWithOptions(server.URL, 5*time.Second, DispatchOptions{
		MaxRetries: 3,
		RetryBase:  time.Millisecond,
		Enabled:    true,
	})
	resp, err := d.Dispatch(context.Background(), TriggerEvent{
		TriggerType: "DeploymentUpdate",
		Namespace:   "default",
		Name:        "test",
	})
	if err != nil {
		t.Fatalf("Dispatch() returned unexpected error: %v", err)
	}
	if resp.Status != "passed" {
		t.Fatalf("Dispatch() status = %q, want passed", resp.Status)
	}
	if got := atomic.LoadInt32(&callCount); got != 3 {
		t.Fatalf("call count = %d, want 3", got)
	}
}

func TestHTTPDispatcher_DoesNotRetryPermanentFailure(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	d := NewHTTPDispatcherWithOptions(server.URL, 5*time.Second, DispatchOptions{
		MaxRetries: 3,
		RetryBase:  time.Millisecond,
		Enabled:    true,
	})

	_, err := d.Dispatch(context.Background(), TriggerEvent{
		TriggerType: "DeploymentUpdate",
		Namespace:   "default",
		Name:        "test",
	})
	if err == nil {
		t.Fatal("Dispatch() expected an error")
	}
	if IsTransient(err) {
		t.Fatalf("Dispatch() error should be permanent, got transient: %v", err)
	}
	if got := atomic.LoadInt32(&callCount); got != 1 {
		t.Fatalf("call count = %d, want 1", got)
	}
}

func TestHTTPDispatcher_Disabled(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(AgentResponse{Status: "passed"})
	}))
	defer server.Close()

	d := NewHTTPDispatcherWithOptions(server.URL, 5*time.Second, DispatchOptions{
		MaxRetries: 3,
		Enabled:    false,
	})
	resp, err := d.Dispatch(context.Background(), TriggerEvent{
		TriggerType: "PodCrashLoop",
		Namespace:   "default",
		Name:        "pod-1",
	})
	if err != nil {
		t.Fatalf("Dispatch() unexpected error: %v", err)
	}
	if resp.Status != "warning" {
		t.Fatalf("Dispatch() status = %q, want warning", resp.Status)
	}
	if got := atomic.LoadInt32(&callCount); got != 0 {
		t.Fatalf("call count = %d, want 0", got)
	}
}

func TestHTTPDispatcher_AuthorizationHeader(t *testing.T) {
	const token = "secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Bearer " + token
		if got := r.Header.Get("Authorization"); got != want {
			t.Fatalf("Authorization header = %q, want %q", got, want)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(AgentResponse{Status: "passed"})
	}))
	defer server.Close()

	d := NewHTTPDispatcherWithOptions(server.URL, 5*time.Second, DispatchOptions{
		Enabled:   true,
		AuthToken: token,
	})
	if _, err := d.Dispatch(context.Background(), TriggerEvent{
		TriggerType: "JobFailed",
		Namespace:   "default",
		Name:        "job-1",
	}); err != nil {
		t.Fatalf("Dispatch() unexpected error: %v", err)
	}
}

func TestHTTPDispatcher_InvalidAgentStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(AgentResponse{Status: "ok"})
	}))
	defer server.Close()

	d := NewHTTPDispatcherWithOptions(server.URL, 5*time.Second, DispatchOptions{
		Enabled: true,
	})
	_, err := d.Dispatch(context.Background(), TriggerEvent{
		TriggerType: "DeploymentUpdate",
		Namespace:   "default",
		Name:        "demo",
	})
	if err == nil {
		t.Fatal("Dispatch() expected error for invalid status")
	}
	if IsTransient(err) {
		t.Fatalf("invalid response status should be permanent: %v", err)
	}
}

func TestTriggerEventTimestampAndObservedAt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event TriggerEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		if event.Timestamp == "" {
			t.Error("expected non-empty timestamp")
		}
		if event.ObservedAt == "" {
			t.Error("expected non-empty observedAt")
		}
		_ = json.NewEncoder(w).Encode(AgentResponse{Status: "passed"})
	}))
	defer server.Close()

	d := NewHTTPDispatcherWithOptions(server.URL, 5*time.Second, DispatchOptions{
		Enabled: true,
	})
	_, err := d.Dispatch(context.Background(), TriggerEvent{
		TriggerType: "DeploymentUpdate",
		Namespace:   "default",
		Name:        "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
