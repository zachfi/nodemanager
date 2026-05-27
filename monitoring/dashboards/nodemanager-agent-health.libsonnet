{
  // Agent Health dashboard — focused view for the agent-operator audience.
  //
  // Three panels:
  //   • Agent uptime        — seconds since process start; drops signal restarts.
  //   • In-flight reconcile ages — non-empty during incidents only.
  //   • Reconcile rate      — per-node ops/s; flat zero = wedged reflector.
  'nodemanager-agent-health.json': {
    title: 'nodemanager / Agent Health',
    uid: 'nodemanager-agent-health',
    tags: ['nodemanager'],
    timezone: 'browser',
    schemaVersion: 36,
    refresh: '30s',
    time: { from: 'now-6h', to: 'now' },
    templating: { list: [] },
    panels: [
      // Panel 1: Agent uptime (stat)
      {
        type: 'stat',
        title: 'Agent uptime',
        description: 'Seconds since the nodemanager process started on each agent. A drop indicates a recent restart (watchdog or operator).',
        gridPos: { x: 0, y: 0, w: 6, h: 6 },
        options: {
          reduceOptions: { calcs: ['lastNotNull'] },
          orientation: 'auto',
          colorMode: 'background',
          graphMode: 'none',
          textMode: 'auto',
          thresholds: {
            mode: 'absolute',
            steps: [
              { color: 'red', value: null },
              { color: 'yellow', value: 300 },
              { color: 'green', value: 3600 },
            ],
          },
        },
        fieldConfig: {
          defaults: { unit: 's' },
        },
        targets: [
          {
            expr: 'time() - process_start_time_seconds{job=~".*nodemanager.*"}',
            legendFormat: '{{instance}}',
          },
        ],
      },
      // Panel 2: In-flight reconcile ages (timeseries)
      {
        type: 'timeseries',
        title: 'In-flight reconciles (age)',
        description: 'Age of any in-flight Reconcile exceeding watchdog.slow-threshold. Empty in steady state; populated during incidents.',
        gridPos: { x: 6, y: 0, w: 12, h: 6 },
        options: {
          tooltip: { mode: 'multi', sort: 'desc' },
          legend: { displayMode: 'table', placement: 'right', calcs: ['lastNotNull'] },
        },
        fieldConfig: {
          defaults: {
            unit: 's',
            custom: { lineWidth: 1 },
          },
        },
        targets: [
          {
            expr: 'nodemanager_reconcile_in_flight_duration_seconds',
            legendFormat: '{{node}} / {{controller}} / {{key}}',
          },
        ],
      },
      // Panel 3: Reconcile rate (timeseries)
      {
        type: 'timeseries',
        title: 'Reconcile rate (5m)',
        description: 'Reconciles per second across all nodemanager controllers, by node. A flat-line at zero with steady-state events indicates a wedged reflector.',
        gridPos: { x: 18, y: 0, w: 6, h: 6 },
        options: {
          tooltip: { mode: 'multi', sort: 'desc' },
          legend: { displayMode: 'table', placement: 'right', calcs: ['lastNotNull'] },
        },
        fieldConfig: {
          defaults: {
            unit: 'ops',
            custom: { lineWidth: 1 },
          },
        },
        targets: [
          {
            expr: 'sum by (node) (rate(controller_runtime_reconcile_total{job=~".*nodemanager.*"}[5m]))',
            legendFormat: '{{node}}',
          },
        ],
      },
    ],
  },
}
