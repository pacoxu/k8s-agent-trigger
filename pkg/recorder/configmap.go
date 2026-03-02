// Package recorder provides the run recorder for persisting Agent execution results.
package recorder

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// RunHistoryConfigMap is the name of the ConfigMap used to store run history.
	RunHistoryConfigMap = "agent-run-history"
	// MaxHistoryEntries is the maximum number of run history entries to keep per ConfigMap.
	MaxHistoryEntries = 100
)

// RunRecord holds the result of a single Agent execution.
type RunRecord struct {
	Status    string   `json:"status"`
	Summary   string   `json:"summary"`
	Actions   []string `json:"actions,omitempty"`
	Timestamp string   `json:"timestamp"`
}

// ConfigMapRecorder records Agent run results into a Kubernetes ConfigMap.
type ConfigMapRecorder struct {
	client    client.Client
	namespace string
}

// NewConfigMapRecorder creates a new ConfigMapRecorder.
func NewConfigMapRecorder(c client.Client, namespace string) *ConfigMapRecorder {
	return &ConfigMapRecorder{
		client:    c,
		namespace: namespace,
	}
}

// Record persists a RunRecord into the agent-run-history ConfigMap.
// The key format is "<namespace>/<name>_<suffix>" (e.g. "prod/web-app_v3").
func (r *ConfigMapRecorder) Record(ctx context.Context, key string, record RunRecord) error {
	if record.Timestamp == "" {
		record.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	value, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal run record: %w", err)
	}

	cm := &corev1.ConfigMap{}
	namespacedName := types.NamespacedName{
		Namespace: r.namespace,
		Name:      RunHistoryConfigMap,
	}

	err = r.client.Get(ctx, namespacedName, cm)
	if errors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RunHistoryConfigMap,
				Namespace: r.namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "k8s-agent-trigger",
				},
			},
			Data: map[string]string{
				key: string(value),
			},
		}
		return r.client.Create(ctx, cm)
	}
	if err != nil {
		return fmt.Errorf("failed to get run history ConfigMap: %w", err)
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[key] = string(value)

	// Prune old entries if we exceed the limit.
	if len(cm.Data) > MaxHistoryEntries {
		pruneOldestEntries(cm.Data, MaxHistoryEntries)
	}

	return r.client.Update(ctx, cm)
}

// pruneOldestEntries removes oldest entries from data until len(data) <= maxEntries.
// Since map iteration order is non-deterministic, this removes arbitrary entries.
// In production a time-ordered ring buffer would be preferable.
func pruneOldestEntries(data map[string]string, maxEntries int) {
	for k := range data {
		if len(data) <= maxEntries {
			break
		}
		delete(data, k)
	}
}
