{
  // ConfigSet Rollout dashboard — shows how a ConfigSet change propagates
  // across nodes using the nodemanager_configset_applied_resource_version gauge.
  //
  // Recovery workflow for operators:
  //   • "Versions in circulation" tells you how many distinct resource_versions
  //     are currently active — 1 means the rollout is complete.
  //   • The per-node table shows which nodes are still on an old version.
  //   • The apply-rate panel shows the rollout wave as a timeseries.
  'nodemanager-configset.json': {
    title: 'nodemanager / ConfigSet Rollout',
    uid: 'nodemanager-configset-rollout',
    tags: ['nodemanager'],
    timezone: 'browser',
    schemaVersion: 36,
    refresh: '30s',
    time: { from: 'now-3h', to: 'now' },
    templating: {
      list: [
        {
          name: 'configset',
          label: 'ConfigSet',
          type: 'query',
          datasource: { type: 'prometheus' },
          query: 'label_values(nodemanager_configset_applied_resource_version, configset)',
          refresh: 2,
          includeAll: true,
          multi: true,
          allValue: '.*',
          sort: 1,
        },
      ],
    },
    panels: [
      // Row 0, Col 0-5: distinct resource_versions currently applied across all nodes
      {
        type: 'stat',
        title: 'Versions in circulation',
        description: 'Number of distinct ConfigSet resource_versions currently seen across all nodes. 1 = rollout complete.',
        gridPos: { x: 0, y: 0, w: 6, h: 4 },
        options: {
          reduceOptions: { calcs: ['lastNotNull'] },
          orientation: 'auto',
          colorMode: 'background',
          graphMode: 'none',
          textMode: 'auto',
          thresholds: {
            mode: 'absolute',
            steps: [
              { color: 'green', value: null },
              { color: 'yellow', value: 2 },
              { color: 'red', value: 4 },
            ],
          },
        },
        targets: [
          {
            expr: 'count(count by (resource_version) (nodemanager_configset_applied_resource_version{configset=~"$configset"}))',
            legendFormat: 'versions',
          },
        ],
      },
      // Row 0, Col 6-11: fraction of nodes on the latest (max) version
      {
        type: 'stat',
        title: 'Nodes on latest version',
        description: 'Number of nodes that have applied the most recently seen resource_version.',
        gridPos: { x: 6, y: 0, w: 6, h: 4 },
        options: {
          reduceOptions: { calcs: ['lastNotNull'] },
          orientation: 'auto',
          colorMode: 'background',
          graphMode: 'none',
          textMode: 'auto',
          thresholds: {
            mode: 'absolute',
            steps: [{ color: 'blue', value: null }],
          },
        },
        targets: [
          {
            expr: |||
              count(
                nodemanager_configset_applied_resource_version{configset=~"$configset"}
                  == on(configset) group_left()
                max by (configset) (
                  nodemanager_configset_applied_resource_version{configset=~"$configset"}
                )
              )
            |||,
            legendFormat: 'nodes on latest',
          },
        ],
      },
      // Row 1: per-node resource_version table
      {
        type: 'table',
        title: 'Node ConfigSet versions',
        description: 'Current resource_version applied on each node. Highlight rows where version differs from the maximum.',
        gridPos: { x: 0, y: 4, w: 12, h: 8 },
        options: {
          sortBy: [{ displayName: 'configset', desc: false }],
        },
        fieldConfig: {
          defaults: {},
          overrides: [
            {
              matcher: { id: 'byName', options: 'Value' },
              properties: [{ id: 'displayName', value: 'applied_at (unix)' }],
            },
          ],
        },
        transformations: [
          { id: 'labelsToFields', options: {} },
          {
            id: 'organize',
            options: {
              excludeByName: { Time: true, '__name__': true },
              renameByName: {},
              indexByName: { node: 0, configset: 1, resource_version: 2, Value: 3 },
            },
          },
        ],
        targets: [
          {
            expr: 'nodemanager_configset_applied_resource_version{configset=~"$configset"}',
            legendFormat: '{{node}} / {{configset}} @ {{resource_version}}',
            instant: true,
          },
        ],
      },
      // Row 2: apply-rate timeseries (rollout wave)
      {
        type: 'timeseries',
        title: 'ConfigSet apply rate (rollout wave)',
        description: 'Rate of successful ConfigSet applies per node. A wave pattern here shows a rolling rollout.',
        gridPos: { x: 0, y: 12, w: 24, h: 8 },
        options: {
          tooltip: { mode: 'multi', sort: 'desc' },
          legend: { displayMode: 'table', placement: 'right', calcs: ['lastNotNull', 'sum'] },
        },
        fieldConfig: {
          defaults: {
            unit: 'ops',
            custom: { lineWidth: 1 },
          },
        },
        targets: [
          {
            expr: 'sum by (node, configset) (rate(nodemanager_configset_apply_total{result="success",configset=~"$configset"}[5m]))',
            legendFormat: '{{node}} / {{configset}}',
          },
        ],
      },
    ],
  },
}
