{
  prometheusAlerts+: {
    groups+: [
      (import 'alerts/nodemanager.libsonnet'),
    ],
  },
}
