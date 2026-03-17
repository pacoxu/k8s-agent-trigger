package controllers

import (
	"context"

	"github.com/pacoxu/k8s-agent-trigger/pkg/dispatcher"
	"github.com/pacoxu/k8s-agent-trigger/pkg/recorder"
)

// EventDispatcher dispatches trigger events to Agent services.
type EventDispatcher interface {
	Dispatch(ctx context.Context, event dispatcher.TriggerEvent) (*dispatcher.AgentResponse, error)
}

// RunRecorder persists and deduplicates trigger run records.
type RunRecorder interface {
	HasRecord(ctx context.Context, key string) (bool, error)
	Record(ctx context.Context, key string, record recorder.RunRecord) error
}
