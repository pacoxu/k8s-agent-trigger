/*
k8s-agent-trigger is a lightweight Kubernetes-native event trigger skeleton
designed for agent-driven acceptance testing, diagnostics, and recommendations.

It watches Deployment, Job, and Pod resources and dispatches trigger events
to a configured Agent HTTP endpoint when notable events occur.

Usage:

	controller --agent-endpoint=http://agent-service:8080/api/v1/agent/run \
	           --recorder-namespace=default \
	           --metrics-bind-address=:8080 \
	           --health-probe-bind-address=:8081 \
	           --leader-elect=true
*/
package main

import (
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins to ensure cloud providers work.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/pacoxu/k8s-agent-trigger/controllers"
	"github.com/pacoxu/k8s-agent-trigger/pkg/dispatcher"
	"github.com/pacoxu/k8s-agent-trigger/pkg/recorder"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var agentEndpoint string
	var recorderNamespace string
	var agentTimeout time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the metrics endpoint binds to. Use :0 to disable.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"The address the health probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager to ensure only one active controller.")
	flag.StringVar(&agentEndpoint, "agent-endpoint", "",
		"HTTP endpoint of the Agent service (e.g. http://agent-service:8080/api/v1/agent/run).")
	flag.StringVar(&recorderNamespace, "recorder-namespace", "default",
		"Namespace where the agent-run-history ConfigMap will be created/updated.")
	flag.DurationVar(&agentTimeout, "agent-timeout", 30*time.Second,
		"Timeout for Agent HTTP requests.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if agentEndpoint == "" {
		setupLog.Error(nil, "agent-endpoint flag is required")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "k8s-agent-trigger.pacoxu.github.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	disp := dispatcher.NewHTTPDispatcher(agentEndpoint, agentTimeout)
	rec := recorder.NewConfigMapRecorder(mgr.GetClient(), recorderNamespace)

	if err = (&controllers.DeploymentReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Dispatcher: disp,
		Recorder:   rec,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Deployment")
		os.Exit(1)
	}

	if err = (&controllers.JobReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Dispatcher: disp,
		Recorder:   rec,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Job")
		os.Exit(1)
	}

	if err = (&controllers.PodReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Dispatcher: disp,
		Recorder:   rec,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
