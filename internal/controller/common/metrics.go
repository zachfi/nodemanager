package common

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// configSetApplyTotal counts ConfigSet apply operations, labelled by node,
	// configset name, and result ("success" or "error").
	configSetApplyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_configset_apply_total",
		Help: "Total number of ConfigSet apply operations.",
	}, []string{"node", "configset", "result"})

	// configSetApplyDuration records how long each ConfigSet apply takes.
	configSetApplyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nodemanager_configset_apply_duration_seconds",
		Help:    "Duration of ConfigSet apply operations in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"node", "configset"})

	// packageOperationsTotal counts package manager operations (install/remove/upgrade).
	packageOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_package_operations_total",
		Help: "Total number of package manager operations.",
	}, []string{"node", "operation", "result"})

	// serviceOperationsTotal counts service manager operations (start/stop/restart/enable/disable).
	serviceOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_service_operations_total",
		Help: "Total number of service manager operations.",
	}, []string{"node", "operation", "result"})

	// fileChangesTotal counts files that were changed during a ConfigSet apply.
	fileChangesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_file_changes_total",
		Help: "Total number of file changes applied by ConfigSet reconciliation.",
	}, []string{"node", "configset", "result"})

	// upgradeTotal counts node upgrade attempts.
	upgradeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nodemanager_upgrade_total",
		Help: "Total number of node upgrade operations.",
	}, []string{"node", "result"})

	// upgradeDuration records how long node upgrades take.
	upgradeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nodemanager_upgrade_duration_seconds",
		Help:    "Duration of node upgrade operations in seconds.",
		Buckets: []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600},
	}, []string{"node"})

	// lastUpgradeTimestamp records the Unix timestamp of the last successful upgrade per node.
	lastUpgradeTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nodemanager_last_upgrade_timestamp_seconds",
		Help: "Unix timestamp of the last successful node upgrade.",
	}, []string{"node"})

	// lastConfigSetApplyTimestamp records the Unix timestamp of the last successful ConfigSet apply.
	lastConfigSetApplyTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nodemanager_last_configset_apply_timestamp_seconds",
		Help: "Unix timestamp of the last successful ConfigSet apply on a node.",
	}, []string{"node", "configset"})
)

func init() {
	metrics.Registry.MustRegister(
		configSetApplyTotal,
		configSetApplyDuration,
		packageOperationsTotal,
		serviceOperationsTotal,
		fileChangesTotal,
		upgradeTotal,
		upgradeDuration,
		lastUpgradeTimestamp,
		lastConfigSetApplyTimestamp,
	)
}
