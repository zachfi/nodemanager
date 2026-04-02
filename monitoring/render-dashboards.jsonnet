// Render all Grafana dashboards from the mixin to stdout as a JSON object
// keyed by filename.
//
// Usage:
//   jsonnet render-dashboards.jsonnet | jq 'to_entries[] | .key, .value' -r
//
// Or to write individual files:
//   jsonnet render-dashboards.jsonnet | jq -r 'to_entries[] | "\(.key)\t\(.value|tojson)"' \
//     | while IFS=$'\t' read name json; do echo "$json" > "$name"; done
local mixin = import 'mixin.libsonnet';
mixin.grafanaDashboards
