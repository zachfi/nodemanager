package freebsd

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// jailOperationsTotal counts jail lifecycle operations, labelled by node,
	// jail name, operation (provision/start/stop/delete), and result.
	jailOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_jail_operations_total",
		Help: "Total number of jail lifecycle operations.",
	}, []string{"node", "jail", "operation", "result"})

	// jailProvisionDuration records how long jail provisioning takes.  This
	// covers the EnsureJail path: release download, ZFS clone, conf/fstab
	// write.  Buckets are tuned for operations that commonly take seconds to
	// minutes.
	jailProvisionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nodemanager_jail_provision_duration_seconds",
		Help:    "Duration of jail provisioning operations in seconds.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600},
	}, []string{"node", "jail"})
)

func init() {
	metrics.Registry.MustRegister(
		jailOperationsTotal,
		jailProvisionDuration,
	)
}
