package dispatcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
					json.NewEncoder(w).Encode(tt.respBody) //nolint:errcheck
				}
			}))
			defer server.Close()

			d := NewHTTPDispatcher(server.URL, 5*time.Second)
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

func TestTriggerEvent_Timestamp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event TriggerEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		if event.Timestamp == "" {
			t.Error("expected non-empty timestamp")
		}
		json.NewEncoder(w).Encode(AgentResponse{Status: "passed"}) //nolint:errcheck
	}))
	defer server.Close()

	d := NewHTTPDispatcher(server.URL, 5*time.Second)
	_, err := d.Dispatch(context.Background(), TriggerEvent{
		TriggerType: "DeploymentUpdate",
		Namespace:   "default",
		Name:        "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
