package recorder

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConfigMapRecorder_RecordAndHasRecord(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	rec := NewConfigMapRecorder(c, "default", 5)

	runRecord := RunRecord{
		Status:    "passed",
		Summary:   "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if err := rec.Record(context.Background(), "run.test.1", runRecord); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	exists, err := rec.HasRecord(context.Background(), "run.test.1")
	if err != nil {
		t.Fatalf("HasRecord() error = %v", err)
	}
	if !exists {
		t.Fatal("HasRecord() = false, want true")
	}
}

func TestConfigMapRecorder_HasRecordConfigMapMissing(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	rec := NewConfigMapRecorder(c, "default", 5)

	exists, err := rec.HasRecord(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("HasRecord() error = %v", err)
	}
	if exists {
		t.Fatal("HasRecord() = true, want false")
	}
}

func TestConfigMapRecorder_PrunesOldestEntriesByTimestamp(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	oldest := mustRecordJSON(t, RunRecord{
		Status:    "passed",
		Summary:   "oldest",
		Timestamp: "2026-01-01T00:00:00Z",
	})
	middle := mustRecordJSON(t, RunRecord{
		Status:    "passed",
		Summary:   "middle",
		Timestamp: "2026-01-01T01:00:00Z",
	})
	latest := mustRecordJSON(t, RunRecord{
		Status:    "passed",
		Summary:   "latest",
		Timestamp: "2026-01-01T02:00:00Z",
	})

	initialCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RunHistoryConfigMap,
			Namespace: "default",
		},
		Data: map[string]string{
			"run.oldest": oldest,
			"run.middle": middle,
			"run.latest": latest,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initialCM).Build()
	rec := NewConfigMapRecorder(c, "default", 3)

	if err := rec.Record(context.Background(), "run.newest", RunRecord{
		Status:    "passed",
		Summary:   "newest",
		Timestamp: "2026-01-01T03:00:00Z",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), types.NamespacedName{
		Namespace: "default",
		Name:      RunHistoryConfigMap,
	}, cm); err != nil {
		t.Fatalf("Get() configmap error = %v", err)
	}

	if len(cm.Data) != 3 {
		t.Fatalf("data length = %d, want 3", len(cm.Data))
	}
	if _, ok := cm.Data["run.oldest"]; ok {
		t.Fatal("expected oldest entry to be pruned")
	}
	if _, ok := cm.Data["run.middle"]; !ok {
		t.Fatal("expected run.middle entry to remain")
	}
	if _, ok := cm.Data["run.latest"]; !ok {
		t.Fatal("expected run.latest entry to remain")
	}
	if _, ok := cm.Data["run.newest"]; !ok {
		t.Fatal("expected run.newest entry to remain")
	}
}

func mustRecordJSON(t *testing.T, record RunRecord) string {
	t.Helper()
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(raw)
}
