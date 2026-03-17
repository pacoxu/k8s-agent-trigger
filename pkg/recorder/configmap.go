// Package recorder provides the run recorder for persisting Agent execution results.
package recorder

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// RunHistoryConfigMap is the name of the ConfigMap used to store run history.
	RunHistoryConfigMap = "agent-run-history"
	// DefaultMaxHistoryEntries is the default maximum number of run history entries per ConfigMap.
	DefaultMaxHistoryEntries = 100
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
	client     client.Client
	namespace  string
	maxEntries int
}

// NewConfigMapRecorder creates a new ConfigMapRecorder.
func NewConfigMapRecorder(c client.Client, namespace string, maxEntries ...int) *ConfigMapRecorder {
	limit := DefaultMaxHistoryEntries
	if len(maxEntries) > 0 && maxEntries[0] > 0 {
		limit = maxEntries[0]
	}
	return &ConfigMapRecorder{
		client:     c,
		namespace:  namespace,
		maxEntries: limit,
	}
}

// HasRecord returns whether the run history ConfigMap already contains key.
func (r *ConfigMapRecorder) HasRecord(ctx context.Context, key string) (bool, error) {
	cm := &corev1.ConfigMap{}
	namespacedName := types.NamespacedName{
		Namespace: r.namespace,
		Name:      RunHistoryConfigMap,
	}

	err := r.client.Get(ctx, namespacedName, cm)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to read run history ConfigMap: %w", err)
	}
	_, exists := cm.Data[key]
	return exists, nil
}

// Record persists a RunRecord into the agent-run-history ConfigMap.
func (r *ConfigMapRecorder) Record(ctx context.Context, key string, record RunRecord) error {
	if record.Timestamp == "" {
		record.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	value, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal run record: %w", err)
	}

	namespacedName := types.NamespacedName{
		Namespace: r.namespace,
		Name:      RunHistoryConfigMap,
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm := &corev1.ConfigMap{}
		getErr := r.client.Get(ctx, namespacedName, cm)
		if apierrors.IsNotFound(getErr) {
			newCM := &corev1.ConfigMap{
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
			createErr := r.client.Create(ctx, newCM)
			if apierrors.IsAlreadyExists(createErr) {
				return apierrors.NewConflict(
					schema.GroupResource{Group: "", Resource: "configmaps"},
					RunHistoryConfigMap,
					createErr,
				)
			}
			return createErr
		}
		if getErr != nil {
			return getErr
		}

		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data[key] = string(value)
		pruneOldestEntries(cm.Data, r.maxEntries)

		return r.client.Update(ctx, cm)
	})
	if err != nil {
		return fmt.Errorf("failed to persist run history ConfigMap: %w", err)
	}
	return nil
}

type recordEntry struct {
	key       string
	timestamp time.Time
}

// pruneOldestEntries removes oldest entries from data until len(data) <= maxEntries.
func pruneOldestEntries(data map[string]string, maxEntries int) {
	if maxEntries <= 0 || len(data) <= maxEntries {
		return
	}

	entries := make([]recordEntry, 0, len(data))
	for key, raw := range data {
		ts := time.Time{}
		var record RunRecord
		if err := json.Unmarshal([]byte(raw), &record); err == nil && record.Timestamp != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, record.Timestamp); parseErr == nil {
				ts = parsed
			}
		}
		entries = append(entries, recordEntry{key: key, timestamp: ts})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].timestamp.Equal(entries[j].timestamp) {
			return entries[i].key < entries[j].key
		}
		return entries[i].timestamp.Before(entries[j].timestamp)
	})

	removeCount := len(entries) - maxEntries
	for i := 0; i < removeCount; i++ {
		delete(data, entries[i].key)
	}
}
