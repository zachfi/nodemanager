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

	// poudriereBulkRunsTotal counts every poudriere bulk invocation, labelled
	// by node, the PoudriereBulk resource name, and the result
	// ("success" or "error").  Used to alert on repeated build failures and
	// to feed a Grafana dashboard tile of build success rate.
	poudriereBulkRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_poudriere_bulk_runs_total",
		Help: "Total number of poudriere bulk runs, labelled by result.",
	}, []string{"node", "bulk", "result"})

	// poudriereBulkDuration records how long a single poudriere bulk run
	// takes (portshaker sync + bulk).  Buckets cover seconds (a no-op rerun
	// of an up-to-date repo) up to multiple hours (a full rebuild after a
	// jail or tree change).
	poudriereBulkDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nodemanager_poudriere_bulk_duration_seconds",
		Help:    "Duration of poudriere bulk runs in seconds.",
		Buckets: []float64{10, 30, 60, 300, 900, 1800, 3600, 7200, 14400},
	}, []string{"node", "bulk"})

	// poudriereLastBulkTimestamp is the unix-second timestamp of the most
	// recent poudriere bulk completion (regardless of result).  Used to alert
	// when a build host has gone silent: time() - this metric > expected
	// ReconcilePeriod.
	poudriereLastBulkTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nodemanager_poudriere_last_bulk_timestamp_seconds",
		Help: "Unix timestamp of the most recent poudriere bulk completion.",
	}, []string{"node", "bulk"})
)

func init() {
	metrics.Registry.MustRegister(
		jailOperationsTotal,
		jailProvisionDuration,
		poudriereBulkRunsTotal,
		poudriereBulkDuration,
		poudriereLastBulkTimestamp,
	)
}
