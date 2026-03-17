package dispatcher

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	dispatchMetricsOnce sync.Once

	dispatchAttemptsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "k8s_agent_trigger_dispatch_attempts_total",
			Help: "Total number of dispatch attempts partitioned by result and error type.",
		},
		[]string{"trigger_type", "result", "error_type"},
	)

	dispatchDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "k8s_agent_trigger_dispatch_duration_seconds",
			Help:    "Dispatch attempt duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"trigger_type", "result"},
	)
)

func init() {
	registerMetrics()
}

func registerMetrics() {
	dispatchMetricsOnce.Do(func() {
		metrics.Registry.MustRegister(dispatchAttemptsTotal, dispatchDurationSeconds)
	})
}

func observeDispatchMetrics(triggerType string, result string, errorType string, elapsed time.Duration) {
	if triggerType == "" {
		triggerType = "unknown"
	}
	if errorType == "" {
		errorType = "none"
	}
	dispatchAttemptsTotal.WithLabelValues(triggerType, result, errorType).Inc()
	dispatchDurationSeconds.WithLabelValues(triggerType, result).Observe(elapsed.Seconds())
}
