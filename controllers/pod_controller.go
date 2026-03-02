package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/pacoxu/k8s-agent-trigger/pkg/dispatcher"
	"github.com/pacoxu/k8s-agent-trigger/pkg/recorder"
)

const (
	// crashLoopRestartThreshold is the minimum restart count to consider a pod in CrashLoopBackOff.
	crashLoopRestartThreshold int32 = 3
)

// PodReconciler watches Pod resources and triggers Agent runs when a Pod enters CrashLoopBackOff.
type PodReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Dispatcher *dispatcher.HTTPDispatcher
	Recorder   *recorder.ConfigMapRecorder
}

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch

// Reconcile is triggered when a Pod is detected to be in CrashLoopBackOff state.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pod := &corev1.Pod{}
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !isPodCrashLooping(pod) {
		return ctrl.Result{}, nil
	}

	logger.Info("Pod CrashLoopBackOff detected, dispatching trigger",
		"namespace", pod.Namespace,
		"name", pod.Name,
	)

	triggerEvent := dispatcher.TriggerEvent{
		TriggerType: "PodCrashLoop",
		Namespace:   pod.Namespace,
		Name:        pod.Name,
		Reason:      "CrashLoopBackOff",
	}

	agentResp, err := r.Dispatcher.Dispatch(ctx, triggerEvent)
	if err != nil {
		logger.Error(err, "failed to dispatch trigger event")
		return ctrl.Result{}, err
	}

	key := fmt.Sprintf("%s/%s_crashloop", pod.Namespace, pod.Name)
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

// isPodCrashLooping returns true when any container in the Pod has a high restart count and is not ready.
func isPodCrashLooping(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodSucceeded {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount >= crashLoopRestartThreshold && !cs.Ready {
			return true
		}
	}
	return false
}

// podCrashLoopPredicate only passes Pod events where the Pod appears to be CrashLooping.
type podCrashLoopPredicate struct {
	predicate.Funcs
}

func (podCrashLoopPredicate) Update(e event.UpdateEvent) bool {
	pod, ok := e.ObjectNew.(*corev1.Pod)
	if !ok {
		return false
	}
	return isPodCrashLooping(pod)
}

func (podCrashLoopPredicate) Create(e event.CreateEvent) bool {
	pod, ok := e.Object.(*corev1.Pod)
	if !ok {
		return false
	}
	return isPodCrashLooping(pod)
}

// SetupWithManager registers the PodReconciler with the controller manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(podCrashLoopPredicate{}).
		Complete(r)
}
