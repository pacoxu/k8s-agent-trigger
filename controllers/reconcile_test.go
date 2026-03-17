package controllers

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/pacoxu/k8s-agent-trigger/pkg/dispatcher"
	"github.com/pacoxu/k8s-agent-trigger/pkg/recorder"
)

type mockDispatcher struct {
	resp      *dispatcher.AgentResponse
	err       error
	callCount int
	lastEvent dispatcher.TriggerEvent
}

func (m *mockDispatcher) Dispatch(_ context.Context, event dispatcher.TriggerEvent) (*dispatcher.AgentResponse, error) {
	m.callCount++
	m.lastEvent = event
	return m.resp, m.err
}

type mockRecorder struct {
	hasRecord        bool
	hasRecordErr     error
	recordErr        error
	hasRecordCalls   int
	recordCalls      int
	lastKey          string
	lastRecordedData recorder.RunRecord
}

func (m *mockRecorder) HasRecord(_ context.Context, _ string) (bool, error) {
	m.hasRecordCalls++
	return m.hasRecord, m.hasRecordErr
}

func (m *mockRecorder) Record(_ context.Context, key string, record recorder.RunRecord) error {
	m.recordCalls++
	m.lastKey = key
	m.lastRecordedData = record
	return m.recordErr
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	must := func(err error) {
		if err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}
	must(appsv1.AddToScheme(scheme))
	must(batchv1.AddToScheme(scheme))
	must(corev1.AddToScheme(scheme))
	return scheme
}

func TestDeploymentReconcileSuccess(t *testing.T) {
	scheme := testScheme(t)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  "default",
			UID:        types.UID("deploy-uid"),
			Generation: 2,
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deploy).Build()
	disp := &mockDispatcher{
		resp: &dispatcher.AgentResponse{
			Status:  "passed",
			Summary: "ok",
		},
	}
	rec := &mockRecorder{}
	r := &DeploymentReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "web"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if disp.callCount != 1 {
		t.Fatalf("dispatch calls = %d, want 1", disp.callCount)
	}
	if rec.recordCalls != 1 {
		t.Fatalf("record calls = %d, want 1", rec.recordCalls)
	}
	if disp.lastEvent.EventID == "" {
		t.Fatal("expected non-empty EventID")
	}
	if disp.lastEvent.ResourceUID != "deploy-uid" {
		t.Fatalf("ResourceUID = %q, want deploy-uid", disp.lastEvent.ResourceUID)
	}
	if disp.lastEvent.ObservedAt == "" {
		t.Fatal("expected non-empty ObservedAt")
	}
	if rec.lastKey != recordKeyForEvent(disp.lastEvent.EventID) {
		t.Fatalf("record key = %q, want key from eventID", rec.lastKey)
	}
}

func TestDeploymentReconcileDuplicateSuppressed(t *testing.T) {
	scheme := testScheme(t)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  "default",
			UID:        types.UID("deploy-uid"),
			Generation: 2,
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deploy).Build()
	disp := &mockDispatcher{
		resp: &dispatcher.AgentResponse{Status: "passed"},
	}
	rec := &mockRecorder{hasRecord: true}
	r := &DeploymentReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "web"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if disp.callCount != 0 {
		t.Fatalf("dispatch calls = %d, want 0", disp.callCount)
	}
	if rec.recordCalls != 0 {
		t.Fatalf("record calls = %d, want 0", rec.recordCalls)
	}
}

func TestDeploymentReconcileTransientDispatchError(t *testing.T) {
	scheme := testScheme(t)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  "default",
			UID:        types.UID("deploy-uid"),
			Generation: 2,
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deploy).Build()
	disp := &mockDispatcher{
		err: dispatcher.NewTransientError("temporary", errors.New("agent unavailable")),
	}
	rec := &mockRecorder{}
	r := &DeploymentReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "web"},
	})
	if err == nil {
		t.Fatal("Reconcile() expected transient error")
	}
	if rec.recordCalls != 0 {
		t.Fatalf("record calls = %d, want 0", rec.recordCalls)
	}
}

func TestDeploymentReconcilePermanentDispatchErrorIsRecorded(t *testing.T) {
	scheme := testScheme(t)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  "default",
			UID:        types.UID("deploy-uid"),
			Generation: 2,
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deploy).Build()
	disp := &mockDispatcher{
		err: errors.New("bad request"),
	}
	rec := &mockRecorder{}
	r := &DeploymentReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "web"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil for permanent dispatch failure", err)
	}
	if rec.recordCalls != 1 {
		t.Fatalf("record calls = %d, want 1", rec.recordCalls)
	}
	if rec.lastRecordedData.Status != "failed" {
		t.Fatalf("recorded status = %q, want failed", rec.lastRecordedData.Status)
	}
}

func TestJobReconcileSuccess(t *testing.T) {
	scheme := testScheme(t)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-1",
			Namespace: "default",
			UID:       types.UID("job-uid"),
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build()
	disp := &mockDispatcher{
		resp: &dispatcher.AgentResponse{Status: "warning", Summary: "analyzed"},
	}
	rec := &mockRecorder{}
	r := &JobReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "job-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if disp.callCount != 1 {
		t.Fatalf("dispatch calls = %d, want 1", disp.callCount)
	}
	if rec.recordCalls != 1 {
		t.Fatalf("record calls = %d, want 1", rec.recordCalls)
	}
}

func TestJobReconcileNonFailedSkipsDispatch(t *testing.T) {
	scheme := testScheme(t)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-1",
			Namespace: "default",
			UID:       types.UID("job-uid"),
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build()
	disp := &mockDispatcher{
		resp: &dispatcher.AgentResponse{Status: "passed"},
	}
	rec := &mockRecorder{}
	r := &JobReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "job-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if disp.callCount != 0 {
		t.Fatalf("dispatch calls = %d, want 0", disp.callCount)
	}
}

func TestJobReconcileTransientDispatchError(t *testing.T) {
	scheme := testScheme(t)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-1",
			Namespace: "default",
			UID:       types.UID("job-uid"),
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build()
	disp := &mockDispatcher{
		err: dispatcher.NewTransientError("temporary", errors.New("agent unavailable")),
	}
	rec := &mockRecorder{}
	r := &JobReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "job-1"},
	})
	if err == nil {
		t.Fatal("Reconcile() expected transient error")
	}
	if rec.recordCalls != 0 {
		t.Fatalf("record calls = %d, want 0", rec.recordCalls)
	}
}

func TestJobReconcilePermanentDispatchErrorIsRecorded(t *testing.T) {
	scheme := testScheme(t)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-1",
			Namespace: "default",
			UID:       types.UID("job-uid"),
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build()
	disp := &mockDispatcher{
		err: errors.New("bad request"),
	}
	rec := &mockRecorder{}
	r := &JobReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "job-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil for permanent dispatch failure", err)
	}
	if rec.recordCalls != 1 {
		t.Fatalf("record calls = %d, want 1", rec.recordCalls)
	}
}

func TestPodReconcileSuccess(t *testing.T) {
	scheme := testScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			UID:       types.UID("pod-uid"),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 5, Ready: false},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	disp := &mockDispatcher{
		resp: &dispatcher.AgentResponse{Status: "failed", Summary: "detected crashloop"},
	}
	rec := &mockRecorder{}
	r := &PodReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "pod-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if disp.callCount != 1 {
		t.Fatalf("dispatch calls = %d, want 1", disp.callCount)
	}
	if rec.recordCalls != 1 {
		t.Fatalf("record calls = %d, want 1", rec.recordCalls)
	}
}

func TestPodReconcileDuplicateSuppressed(t *testing.T) {
	scheme := testScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			UID:       types.UID("pod-uid"),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 5, Ready: false},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	disp := &mockDispatcher{
		resp: &dispatcher.AgentResponse{Status: "passed"},
	}
	rec := &mockRecorder{hasRecord: true}
	r := &PodReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "pod-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if disp.callCount != 0 {
		t.Fatalf("dispatch calls = %d, want 0", disp.callCount)
	}
}

func TestPodReconcileTransientDispatchError(t *testing.T) {
	scheme := testScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			UID:       types.UID("pod-uid"),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 5, Ready: false},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	disp := &mockDispatcher{
		err: dispatcher.NewTransientError("temporary", errors.New("agent unavailable")),
	}
	rec := &mockRecorder{}
	r := &PodReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "pod-1"},
	})
	if err == nil {
		t.Fatal("Reconcile() expected transient error")
	}
	if rec.recordCalls != 0 {
		t.Fatalf("record calls = %d, want 0", rec.recordCalls)
	}
}

func TestPodReconcilePermanentDispatchErrorIsRecorded(t *testing.T) {
	scheme := testScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			UID:       types.UID("pod-uid"),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 5, Ready: false},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	disp := &mockDispatcher{
		err: errors.New("bad request"),
	}
	rec := &mockRecorder{}
	r := &PodReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "pod-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil for permanent dispatch failure", err)
	}
	if rec.recordCalls != 1 {
		t.Fatalf("record calls = %d, want 1", rec.recordCalls)
	}
}

func TestPodReconcileNonCrashLoopSkipsDispatch(t *testing.T) {
	scheme := testScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			UID:       types.UID("pod-uid"),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 1, Ready: true},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pod).Build()
	disp := &mockDispatcher{
		resp: &dispatcher.AgentResponse{Status: "passed"},
	}
	rec := &mockRecorder{}
	r := &PodReconciler{
		Client:     c,
		Scheme:     scheme,
		Dispatcher: disp,
		Recorder:   rec,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "pod-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if disp.callCount != 0 {
		t.Fatalf("dispatch calls = %d, want 0", disp.callCount)
	}
	if rec.recordCalls != 0 {
		t.Fatalf("record calls = %d, want 0", rec.recordCalls)
	}
}
