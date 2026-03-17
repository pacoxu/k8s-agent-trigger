package controllers

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsJobFailed(t *testing.T) {
	tests := []struct {
		name string
		job  *batchv1.Job
		want bool
	}{
		{
			name: "job with failed condition",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: "True"},
					},
				},
			},
			want: true,
		},
		{
			name: "job with complete condition",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobComplete, Status: "True"},
					},
				},
			},
			want: false,
		},
		{
			name: "job with no conditions",
			job:  &batchv1.Job{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isJobFailed(tt.job); got != tt.want {
				t.Errorf("isJobFailed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJobFailureReason(t *testing.T) {
	job := &batchv1.Job{
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: "True", Reason: "BackoffLimitExceeded"},
			},
		},
	}
	if got := jobFailureReason(job); got != "BackoffLimitExceeded" {
		t.Errorf("jobFailureReason() = %q, want %q", got, "BackoffLimitExceeded")
	}

	emptyJob := &batchv1.Job{}
	if got := jobFailureReason(emptyJob); got != "Unknown" {
		t.Errorf("jobFailureReason() = %q, want %q", got, "Unknown")
	}
}

func TestIsPodCrashLooping(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "pod with high restart count and not ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{RestartCount: 5, Ready: false},
					},
				},
			},
			want: true,
		},
		{
			name: "pod with low restart count",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{RestartCount: 1, Ready: false},
					},
				},
			},
			want: false,
		},
		{
			name: "pod with high restart count but ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{RestartCount: 5, Ready: true},
					},
				},
			},
			want: false,
		},
		{
			name: "succeeded pod with high restart count",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodSucceeded,
					ContainerStatuses: []corev1.ContainerStatus{
						{RestartCount: 5, Ready: false},
					},
				},
			},
			want: false,
		},
		{
			name: "pod with no container statuses",
			pod:  &corev1.Pod{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPodCrashLooping(tt.pod); got != tt.want {
				t.Errorf("isPodCrashLooping() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJobFailedPredicate_Update(t *testing.T) {
	p := jobFailedPredicate{}

	// Update to failed job should pass
	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: "True"},
			},
		},
	}
	if !p.Update(updateEvent(failedJob)) {
		t.Error("expected update predicate to pass for failed job")
	}

	// Update to completed job should not pass
	completeJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: "True"},
			},
		},
	}
	if p.Update(updateEvent(completeJob)) {
		t.Error("expected update predicate to fail for complete job")
	}
}

func TestPodCrashLoopPredicate_Update(t *testing.T) {
	p := podCrashLoopPredicate{}

	crashPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 5, Ready: false},
			},
		},
	}
	if !p.Update(updateEvent(crashPod)) {
		t.Error("expected update predicate to pass for crash-looping pod")
	}

	healthyPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 0, Ready: true},
			},
		},
	}
	if p.Update(updateEvent(healthyPod)) {
		t.Error("expected update predicate to fail for healthy pod")
	}
}

func TestJobFailedPredicate_Create(t *testing.T) {
	p := jobFailedPredicate{}

	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: "True"},
			},
		},
	}
	if !p.Create(createEvent(failedJob)) {
		t.Error("expected create predicate to pass for failed job")
	}

	completeJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: "True"},
			},
		},
	}
	if p.Create(createEvent(completeJob)) {
		t.Error("expected create predicate to fail for complete job")
	}
}

func TestPodCrashLoopPredicate_Create(t *testing.T) {
	p := podCrashLoopPredicate{}

	crashPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 5, Ready: false},
			},
		},
	}
	if !p.Create(createEvent(crashPod)) {
		t.Error("expected create predicate to pass for crash-looping pod")
	}

	healthyPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 0, Ready: true},
			},
		},
	}
	if p.Create(createEvent(healthyPod)) {
		t.Error("expected create predicate to fail for healthy pod")
	}
}
