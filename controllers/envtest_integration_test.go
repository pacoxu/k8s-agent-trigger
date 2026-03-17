package controllers

import (
	"context"
	"os"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/pacoxu/k8s-agent-trigger/pkg/dispatcher"
	"github.com/pacoxu/k8s-agent-trigger/pkg/recorder"
)

func TestEnvTestDeploymentReconcileDispatchAndDeduplicate(t *testing.T) {
	if os.Getenv("RUN_ENVTEST") != "1" {
		t.Skip("set RUN_ENVTEST=1 to run envtest integration suite")
	}

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(appsv1) error = %v", err)
	}
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

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New() error = %v", err)
	}

	ctx := context.Background()
	if err := ensureNamespace(ctx, k8sClient, "default"); err != nil {
		t.Fatalf("ensureNamespace() error = %v", err)
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "web"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "web"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "web", Image: "nginx:1.25"},
					},
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, deploy); err != nil {
		t.Fatalf("Create(deployment) error = %v", err)
	}

	disp := &mockDispatcher{
		resp: &dispatcher.AgentResponse{
			Status:  "passed",
			Summary: "ok",
		},
	}
	rec := recorder.NewConfigMapRecorder(k8sClient, "default", 100)
	reconciler := &DeploymentReconciler{
		Client:     k8sClient,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "web",
		},
	}
	if _, err := reconciler.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile(first) error = %v", err)
	}
	if disp.callCount != 1 {
		t.Fatalf("dispatch count after first reconcile = %d, want 1", disp.callCount)
	}

	if _, err := reconciler.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile(second) error = %v", err)
	}
	if disp.callCount != 1 {
		t.Fatalf("dispatch count after second reconcile = %d, want 1 (deduplicated)", disp.callCount)
	}

	cm := &corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      recorder.RunHistoryConfigMap,
	}, cm); err != nil {
		t.Fatalf("Get(run history configmap) error = %v", err)
	}
	if len(cm.Data) != 1 {
		t.Fatalf("run history entries = %d, want 1", len(cm.Data))
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
