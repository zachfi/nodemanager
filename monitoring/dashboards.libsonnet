{
  grafanaDashboards+: (import 'dashboards/nodemanager-configset.libsonnet') +
                      (import 'dashboards/nodemanager-agent-health.libsonnet'),
}
