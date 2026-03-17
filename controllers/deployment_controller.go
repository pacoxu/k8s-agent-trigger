// Package controllers contains Kubernetes controller reconcilers for k8s-agent-trigger.
package controllers

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/pacoxu/k8s-agent-trigger/pkg/dispatcher"
	"github.com/pacoxu/k8s-agent-trigger/pkg/recorder"
)

// DeploymentReconciler watches Deployment resources and triggers Agent runs on spec updates.
type DeploymentReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Dispatcher EventDispatcher
	Recorder   RunRecorder
}

// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch

// Reconcile is triggered when a Deployment's generation changes (spec update).
func (r *DeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, req.NamespacedName, deploy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	eventID := buildEventID(
		"DeploymentUpdate",
		deploy.Namespace,
		deploy.Name,
		string(deploy.UID),
		fmt.Sprintf("generation=%d", deploy.Generation),
	)
	recordKey := recordKeyForEvent(eventID)
	exists, err := r.Recorder.HasRecord(ctx, recordKey)
	if err != nil {
		logger.Error(err, "failed to query run history for duplicate suppression", "key", recordKey)
		return ctrl.Result{}, err
	}
	if exists {
		logger.Info("Duplicate deployment trigger suppressed", "eventID", eventID, "key", recordKey)
		return ctrl.Result{}, nil
	}

	observedAt := time.Now().UTC().Format(time.RFC3339)

	logger.Info("Deployment generation changed, dispatching trigger",
		"namespace", deploy.Namespace,
		"name", deploy.Name,
		"generation", deploy.Generation,
		"eventID", eventID,
	)

	event := dispatcher.TriggerEvent{
		TriggerType: "DeploymentUpdate",
		Namespace:   deploy.Namespace,
		Name:        deploy.Name,
		Generation:  deploy.Generation,
		EventID:     eventID,
		ResourceUID: string(deploy.UID),
		ObservedAt:  observedAt,
	}

	agentResp, err := r.Dispatcher.Dispatch(ctx, event)
	if err != nil {
		logger.Error(err, "failed to dispatch trigger event", "eventID", eventID)
		if dispatcher.IsTransient(err) {
			return ctrl.Result{}, err
		}

		recordErr := r.Recorder.Record(ctx, recordKey, recorder.RunRecord{
			Status:    "failed",
			Summary:   err.Error(),
			Timestamp: observedAt,
		})
		if recordErr != nil {
			logger.Error(recordErr, "failed to record permanent dispatch failure", "key", recordKey)
			return ctrl.Result{}, recordErr
		}
		return ctrl.Result{}, nil
	}

	runRecord := recorder.RunRecord{
		Status:    agentResp.Status,
		Summary:   agentResp.Summary,
		Actions:   agentResp.Actions,
		Timestamp: observedAt,
	}

	if err := r.Recorder.Record(ctx, recordKey, runRecord); err != nil {
		logger.Error(err, "failed to record agent run result", "key", recordKey)
		return ctrl.Result{}, err
	}

	logger.Info("Agent run recorded", "key", recordKey, "status", agentResp.Status)
	return ctrl.Result{}, nil
}

// SetupWithManager registers the DeploymentReconciler with the controller manager.
// Only Deployment spec changes (generation increments) are reconciled.
func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	rateLimiter := workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
		500*time.Millisecond,
		10*time.Second,
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
			RateLimiter:             rateLimiter,
		}).
		Complete(r)
}
