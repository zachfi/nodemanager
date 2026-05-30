// runbookBase is the published docs location for the per-alert runbooks. Every
// alert name maps 1:1 to a page under docs/runbooks/<AlertName>.md, so the
// runbook_url annotation is derived from the alert name below rather than
// repeated on every rule — new alerts get a runbook_url for free (add the page).
local runbookBase = 'https://zachfi.github.io/nodemanager/runbooks/';

local rules = [

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

  // ── Fleet version skew ───────────────────────────────────────────────────

  {
    alert: 'NodeManagerVersionSkew',
    // Fires when more than one distinct agent version is reporting across the
    // fleet. Agents are installed via the node package / FreeBSD port (not a
    // single cluster image tag), so they drift independently; this gives a
    // single-pane signal that a rollout is incomplete. Requires every node's
    // local alloy to scrape the nodemanager metrics endpoint — nodes that are
    // not scraped are invisible here (see the scrape-coverage gap).
    expr: |||
      count(count by (version) (nodemanager_build_info)) > 1
    |||,
    'for': '1h',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'nodemanager fleet is running {{ $value }} distinct agent versions.',
      description: |||
        {{ $value }} distinct nodemanager versions are reporting via
        nodemanager_build_info. Agents drift independently because they ship in
        the node package / FreeBSD port rather than a single image tag.
        Reconcile the fleet to a single version:
        count by (version) (nodemanager_build_info).
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

  // ── ConfigSet conflict metrics ───────────────────────────────────────────

  {
    alert: 'NodeManagerConfigSetConflict',
    // Fires immediately when two ConfigSets claim the same file or service on
    // the same node. The conflicting ConfigSet is not applied until resolved.
    expr: |||
      increase(nodemanager_configset_conflicts_total[10m]) > 0
    |||,
    'for': '0m',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'ConfigSet {{ $labels.configset }} has a resource conflict on node {{ $labels.node }}.',
      description: |||
        ConfigSet {{ $labels.configset }} on node {{ $labels.node }} shares a file
        path or service name with another ConfigSet targeting the same node.
        The conflicting ConfigSet is not being applied. Resolve by removing the
        duplicate resource from one of the ConfigSets.
        Check ManagedNode status (.status.configsets[].conflicts) for details.
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

  {
    alert: 'NodeManagerServiceStartLoop',
    expr: |||
      sum by (node, service) (rate(nodemanager_service_operations_total{result="exited"}[10m])) > 0
      or
      sum by (node, service) (rate(nodemanager_service_operations_total{operation="start"}[10m])) > (0.5/60)
    |||,
    'for': '15m',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'nodemanager keeps (re)starting {{ $labels.service }} on {{ $labels.node }}.',
      description: |||
        {{ $labels.service }} on {{ $labels.node }} either exits shortly
        after start (result="exited"), or has been started more than
        0.5/min for 15 minutes. Almost always a broken config file —
        inspect the service's recent stdout/journal and the
        nodemanager controller logs for "service exited shortly after"
        warnings.
      |||,
    },
  },

  // ── Agent health (watchdog) ──────────────────────────────────────────────

  {
    alert: 'NodeManagerReconcileStuck',
    expr: |||
      nodemanager_reconcile_in_flight_duration_seconds > 0
    |||,
    'for': '5m',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'reconcile stuck on {{ $labels.node }} ({{ $labels.controller }} / {{ $labels.key }}).',
      description: |||
        A reconcile in controller {{ $labels.controller }} for {{ $labels.key }} has
        been in-flight on {{ $labels.node }} for {{ $value | humanizeDuration }}.
        Likely an uncancellable syscall, deadlock, or upstream service hang.
        Before restarting: capture goroutine dump via /debug/pprof/goroutine?debug=2.
      |||,
    },
  },

  {
    alert: 'NodeManagerAgentRestartLoop',
    expr: |||
      changes(process_start_time_seconds{job=~".*nodemanager.*"}[30m]) > 3
    |||,
    'for': '5m',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'nodemanager on {{ $labels.instance }} has restarted >3 times in 30 minutes.',
      description: |||
        Likely the watchdog firing (exit code 74 — check supervisor logs for
        "watchdog: stale; exiting"). Investigate why Reconcile is not running:
        controller-runtime reflector health, apiserver connectivity, agent kubeconfig.
      |||,
    },
  },

  {
    alert: 'NodeManagerWatchdogProbeFailing',
    // A self-healing restart is the expected recovery, so a single blip must not
    // page: require sustained errors with no successes, with a 10m 'for'. Selects
    // on the node label (not job), which off-cluster agents actually carry.
    expr: |||
      rate(nodemanager_watchdog_probe_total{result="error"}[10m]) > 0
      and
      rate(nodemanager_watchdog_probe_total{result="success"}[10m]) == 0
    |||,
    'for': '10m',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'nodemanager on {{ $labels.node }} cannot reach the API server.',
      description: |||
        The watchdog connectivity probe on node {{ $labels.node }} has been
        failing with no successes for 10 minutes. The agent is losing contact
        with the Kubernetes API server; if this persists past the watchdog
        stale-threshold the agent will exit(74) for a supervised restart.
        Repeated firing indicates the restart is not recovering connectivity.
      |||,
    },
  },

  // ── Poudriere build metrics (FreeBSD) ────────────────────────────────────

  {
    alert: 'NodeManagerPoudriereBuildFailed',
    // Fires when a poudriere bulk run completes with result=error.  Both
    // executor types share this metric: InProcess records "error" when
    // `poudriere bulk` exits non-zero; Command records "error" when the
    // dispatch program fails or the status program reports state=failed.
    expr: |||
      increase(nodemanager_poudriere_bulk_runs_total{result="error"}[15m]) > 0
    |||,
    'for': '0m',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'Poudriere bulk {{ $labels.bulk }} failed on node {{ $labels.node }}.',
      description: |||
        Poudriere bulk {{ $labels.bulk }} on node {{ $labels.node }} produced
        {{ $value }} failed run(s) in the last 15 minutes.
        Check the bulk's status (kubectl describe poudrierebulk {{ $labels.bulk }})
        for the LastError detail; for Command-mode bulks the LastDispatchedRunURL
        field links directly to the upstream build log.
      |||,
    },
  },

  {
    alert: 'NodeManagerPoudriereBuildStale',
    // Fires when a bulk has not produced any run (success, failure, or
    // skip) in 24 hours.  Most production bulks have spec.reconcilePeriod
    // <= 6h, so 24h indicates either the controller is wedged, the build
    // host is offline, or the executor's bridge is silently dropping
    // dispatches.  Tune the threshold here against your longest expected
    // reconcilePeriod.
    expr: |||
      (time() - nodemanager_poudriere_last_bulk_timestamp_seconds) > 86400
    |||,
    'for': '15m',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'Poudriere bulk {{ $labels.bulk }} has not run recently on {{ $labels.node }}.',
      description: |||
        Poudriere bulk {{ $labels.bulk }} on node {{ $labels.node }} last
        completed {{ $value | humanizeDuration }} ago — well past the
        24-hour staleness threshold.  Either the controller is wedged, the
        build host is unreachable, or (for Command-mode bulks) the
        dispatch bridge is failing silently.  Check controller logs and
        the bulk's status conditions.
      |||,
    },
  },

  {
    alert: 'NodeManagerPoudriereBuildLong',
    // Fires when a single bulk run takes longer than the histogram's
    // top bucket (4h) — at that point the build is either stuck or
    // dramatically larger than expected.  Different from the "Stale"
    // alert because completions are still happening; they just take
    // forever.  Useful for catching a runaway build that's blocking
    // its sibling bulks behind the workqueue rate limiter.
    expr: |||
      histogram_quantile(0.95,
        sum by (node, bulk, le) (
          rate(nodemanager_poudriere_bulk_duration_seconds_bucket[1h])
        )
      ) > 14400
    |||,
    'for': '1h',
    labels: { severity: 'warning' },
    annotations: {
      summary: 'Poudriere bulk {{ $labels.bulk }} is taking unusually long on {{ $labels.node }}.',
      description: |||
        The 95th-percentile run duration for poudriere bulk {{ $labels.bulk }}
        on node {{ $labels.node }} has exceeded 4 hours over the last 1h
        window.  Current p95: {{ $value | humanizeDuration }}.
        Check whether one port in the list is hanging (poudriere -j {{ $labels.bulk }})
        or if a circular dependency was introduced.
      |||,
    },
  },

];

{
  name: 'nodemanager',
  rules: [
    rule {
      annotations+: { runbook_url: runbookBase + rule.alert + '/' },
    }
    for rule in rules
  ],
}
