package controllers

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/pacoxu/k8s-agent-trigger/pkg/dispatcher"
	"github.com/pacoxu/k8s-agent-trigger/pkg/recorder"
)

// JobReconciler watches Job resources and triggers Agent runs when a Job fails.
type JobReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Dispatcher *dispatcher.HTTPDispatcher
	Recorder   *recorder.ConfigMapRecorder
}

// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch

// Reconcile is triggered when a Job transitions to a Failed condition.
func (r *JobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	job := &batchv1.Job{}
	if err := r.Get(ctx, req.NamespacedName, job); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only proceed if the job is actually failed.
	if !isJobFailed(job) {
		return ctrl.Result{}, nil
	}

	logger.Info("Job failed, dispatching trigger",
		"namespace", job.Namespace,
		"name", job.Name,
	)

	event := dispatcher.TriggerEvent{
		TriggerType: "JobFailed",
		Namespace:   job.Namespace,
		Name:        job.Name,
		Reason:      jobFailureReason(job),
	}

	agentResp, err := r.Dispatcher.Dispatch(ctx, event)
	if err != nil {
		logger.Error(err, "failed to dispatch trigger event")
		return ctrl.Result{}, err
	}

	key := fmt.Sprintf("%s/%s_failed", job.Namespace, job.Name)
	runRecord := recorder.RunRecord{
		Status:  agentResp.Status,
		Summary: agentResp.Summary,
		Actions: agentResp.Actions,
	}

	if err := r.Recorder.Record(ctx, key, runRecord); err != nil {
		logger.Error(err, "failed to record agent run result", "key", key)
		return ctrl.Result{}, err
	}

	logger.Info("Agent run recorded", "key", key, "status", agentResp.Status)
	return ctrl.Result{}, nil
}

// isJobFailed returns true when the Job has a Failed condition set to True.
func isJobFailed(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == "True" {
			return true
		}
	}
	return false
}

// jobFailureReason extracts the reason string from the Job's Failed condition.
func jobFailureReason(job *batchv1.Job) string {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == "True" {
			return cond.Reason
		}
	}
	return "Unknown"
}

// jobFailedPredicate is a predicate that only passes Job update events where the Job has become failed.
type jobFailedPredicate struct {
	predicate.Funcs
}

func (jobFailedPredicate) Update(e event.UpdateEvent) bool {
	newJob, ok := e.ObjectNew.(*batchv1.Job)
	if !ok {
		return false
	}
	return isJobFailed(newJob)
}

func (jobFailedPredicate) Create(e event.CreateEvent) bool {
	job, ok := e.Object.(*batchv1.Job)
	if !ok {
		return false
	}
	return isJobFailed(job)
}

// SetupWithManager registers the JobReconciler with the controller manager.
func (r *JobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&batchv1.Job{}).
		WithEventFilter(jobFailedPredicate{}).
		Complete(r)
}
