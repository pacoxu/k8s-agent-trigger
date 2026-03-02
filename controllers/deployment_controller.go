// Package controllers contains Kubernetes controller reconcilers for k8s-agent-trigger.
package controllers

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/pacoxu/k8s-agent-trigger/pkg/dispatcher"
	"github.com/pacoxu/k8s-agent-trigger/pkg/recorder"
)

// DeploymentReconciler watches Deployment resources and triggers Agent runs on spec updates.
type DeploymentReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Dispatcher *dispatcher.HTTPDispatcher
	Recorder   *recorder.ConfigMapRecorder
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

	logger.Info("Deployment generation changed, dispatching trigger",
		"namespace", deploy.Namespace,
		"name", deploy.Name,
		"generation", deploy.Generation,
	)

	event := dispatcher.TriggerEvent{
		TriggerType: "DeploymentUpdate",
		Namespace:   deploy.Namespace,
		Name:        deploy.Name,
		Generation:  deploy.Generation,
	}

	agentResp, err := r.Dispatcher.Dispatch(ctx, event)
	if err != nil {
		logger.Error(err, "failed to dispatch trigger event")
		return ctrl.Result{}, err
	}

	key := fmt.Sprintf("%s/%s_v%d", deploy.Namespace, deploy.Name, deploy.Generation)
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

// SetupWithManager registers the DeploymentReconciler with the controller manager.
// Only Deployment spec changes (generation increments) are reconciled.
func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
