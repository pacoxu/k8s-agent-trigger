package recorder

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

type conflictOnceClient struct {
	client.Client
	mu            sync.Mutex
	conflictFired bool
}

func (c *conflictOnceClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	shouldConflict := false
	c.mu.Lock()
	if !c.conflictFired && obj.GetName() == RunHistoryConfigMap {
		c.conflictFired = true
		shouldConflict = true
	}
	c.mu.Unlock()

	if shouldConflict {
		return apierrors.NewConflict(
			schema.GroupResource{Group: "", Resource: "configmaps"},
			obj.GetName(),
			errors.New("simulated update conflict"),
		)
	}
	return c.Client.Update(ctx, obj, opts...)
}

func TestEnvTestRecordRetriesOnConflict(t *testing.T) {
	if os.Getenv("RUN_ENVTEST") != "1" {
		t.Skip("set RUN_ENVTEST=1 to run envtest integration suite")
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(corev1) error = %v", err)
	}

	testEnv := &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest start error: %v", err)
	}
	defer func() {
		if stopErr := testEnv.Stop(); stopErr != nil {
			t.Fatalf("envtest stop error: %v", stopErr)
		}
	}()

	baseClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New() error = %v", err)
	}

	ctx := context.Background()
	if err := ensureNamespace(ctx, baseClient, "default"); err != nil {
		t.Fatalf("ensureNamespace() error = %v", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RunHistoryConfigMap,
			Namespace: "default",
		},
		Data: map[string]string{},
	}
	if err := baseClient.Create(ctx, cm); err != nil {
		t.Fatalf("Create(configmap) error = %v", err)
	}

	wrappedClient := &conflictOnceClient{Client: baseClient}
	rec := NewConfigMapRecorder(wrappedClient, "default", 100)

	err = rec.Record(ctx, "run.test.conflict", RunRecord{
		Status:    "passed",
		Summary:   "retry succeeded",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	stored := &corev1.ConfigMap{}
	if err := baseClient.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      RunHistoryConfigMap,
	}, stored); err != nil {
		t.Fatalf("Get(configmap) error = %v", err)
	}

	if _, ok := stored.Data["run.test.conflict"]; !ok {
		t.Fatal("expected conflict-retry write to exist in ConfigMap")
	}
}

func ensureNamespace(ctx context.Context, c client.Client, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
