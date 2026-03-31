{
  name: 'nodemanager',
  rules: [

    // ── controller-runtime built-in metrics ──────────────────────────────────

    {
      alert: 'NodeManagerReconcileErrors',
      expr: |||
        rate(controller_runtime_reconcile_errors_total{job=~"nodemanager.*"}[5m]) > 0
      |||,
      'for': '5m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'NodeManager controller {{ $labels.controller }} has sustained reconcile errors.',
        description: |||
          Controller {{ $labels.controller }} on {{ $labels.instance }} has been
          producing reconcile errors for more than 5 minutes.
          Current error rate: {{ $value | humanize }} errors/s.
        |||,
      },
    },

    {
      alert: 'NodeManagerReconcileLatencyHigh',
      expr: |||
        histogram_quantile(0.99,
          rate(controller_runtime_reconcile_time_seconds_bucket{job=~"nodemanager.*"}[5m])
        ) > 30
      |||,
      'for': '10m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'NodeManager controller {{ $labels.controller }} reconcile latency is high.',
        description: |||
          p99 reconcile latency for controller {{ $labels.controller }} on
          {{ $labels.instance }} has exceeded 30s for 10 minutes.
          Current p99: {{ $value | humanizeDuration }}.
        |||,
      },
    },

    // ── ConfigSet apply metrics ──────────────────────────────────────────────

    {
      alert: 'NodeManagerConfigSetApplyError',
      expr: |||
        increase(nodemanager_configset_apply_total{result="error"}[15m]) > 0
      |||,
      'for': '5m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'ConfigSet {{ $labels.configset }} failed to apply on node {{ $labels.node }}.',
        description: |||
          ConfigSet {{ $labels.configset }} failed to apply on node {{ $labels.node }}
          {{ $value }} time(s) in the last 15 minutes.
          Check ManagedNode status for the error detail.
        |||,
      },
    },

    {
      alert: 'NodeManagerConfigSetApplyStale',
      // Fires when a ConfigSet has not been successfully applied in 30 minutes.
      expr: |||
        (time() - nodemanager_last_configset_apply_timestamp_seconds) > 1800
      |||,
      'for': '5m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'ConfigSet {{ $labels.configset }} has not been applied recently on {{ $labels.node }}.',
        description: |||
          ConfigSet {{ $labels.configset }} on node {{ $labels.node }} was last
          successfully applied {{ $value | humanizeDuration }} ago.
        |||,
      },
    },

    // ── Upgrade metrics ──────────────────────────────────────────────────────

    {
      alert: 'NodeManagerUpgradeFailed',
      expr: |||
        increase(nodemanager_upgrade_total{result="error"}[1h]) > 0
      |||,
      'for': '0m',
      labels: { severity: 'critical' },
      annotations: {
        summary: 'Node upgrade failed on {{ $labels.node }}.',
        description: |||
          Node {{ $labels.node }} experienced {{ $value }} upgrade failure(s)
          in the last hour. Check controller logs for details.
        |||,
      },
    },

    {
      alert: 'NodeManagerUpgradeStale',
      // Fires when a node has not had a successful upgrade in 7 days.
      expr: |||
        (time() - nodemanager_last_upgrade_timestamp_seconds) > 604800
      |||,
      'for': '1h',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'Node {{ $labels.node }} has not been upgraded recently.',
        description: |||
          Node {{ $labels.node }} was last upgraded {{ $value | humanizeDuration }} ago.
        |||,
      },
    },

    // ── Jail operation metrics (FreeBSD) ────────────────────────────────────

    {
      alert: 'NodeManagerJailOperationFailed',
      expr: |||
        increase(nodemanager_jail_operations_total{result="error"}[15m]) > 0
      |||,
      'for': '5m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'Jail {{ $labels.operation }} failed on node {{ $labels.node }}.',
        description: |||
          Jail {{ $labels.operation }} for jail {{ $labels.jail }} failed
          {{ $value }} time(s) on node {{ $labels.node }} in the last 15 minutes.
          Check controller logs and jail status conditions for details.
        |||,
      },
    },

    // ── File drift metrics ───────────────────────────────────────────────────

    {
      alert: 'NodeManagerFileDrift',
      // Fires when file changes are occurring continuously, indicating that
      // the rendered content never stabilises (e.g. non-deterministic template
      // output) and services are being restarted on every reconcile.
      expr: |||
        rate(nodemanager_file_changes_total{result="success"}[5m]) > 0
      |||,
      'for': '20m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'Perpetual file drift on node {{ $labels.node }} for ConfigSet {{ $labels.configset }}.',
        description: |||
          Files managed by ConfigSet {{ $labels.configset }} on node {{ $labels.node }}
          have been continuously changing for more than 20 minutes.
          This typically means template output is non-deterministic (e.g. unsorted
          node list) causing a hash mismatch and service restart every reconcile.
          Check the rendered template output for ordering instability.
        |||,
      },
    },

    // ── Package operation metrics ────────────────────────────────────────────

    {
      alert: 'NodeManagerPackageOperationFailed',
      expr: |||
        increase(nodemanager_package_operations_total{result="error"}[15m]) > 0
      |||,
      'for': '5m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'Package {{ $labels.operation }} failed on node {{ $labels.node }}.',
        description: |||
          Package {{ $labels.operation }} operation failed {{ $value }} time(s) on
          node {{ $labels.node }} in the last 15 minutes.
        |||,
      },
    },

    // ── Service operation metrics ────────────────────────────────────────────

    {
      alert: 'NodeManagerServiceOperationFailed',
      expr: |||
        increase(nodemanager_service_operations_total{result="error"}[15m]) > 0
      |||,
      'for': '5m',
      labels: { severity: 'warning' },
      annotations: {
        summary: 'Service {{ $labels.operation }} failed on node {{ $labels.node }}.',
        description: |||
          Service {{ $labels.operation }} operation failed {{ $value }} time(s) on
          node {{ $labels.node }} in the last 15 minutes.
        |||,
      },
    },

  ],
}
