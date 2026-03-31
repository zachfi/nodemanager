package common

// ManagedNode upgrade annotations (upgrade.nodemanager/*)

// AnnotationLastUpgrade records the RFC3339 timestamp of the most recent
// successful node upgrade. Set by the controller; do not edit manually.
const AnnotationLastUpgrade = "upgrade.nodemanager/last"

// AnnotationKubernetesNodeCordoned records whether the Kubernetes node was
// cordoned by nodemanager before an upgrade.
const AnnotationKubernetesNodeCordoned = "upgrade.nodemanager/k8s-cordoned"

// AnnotationUpgradeHold disables automatic upgrades on a ManagedNode when set
// to any non-empty value. Remove the annotation to re-enable upgrades.
//
//	kubectl annotate managednode <name> upgrade.nodemanager/hold=true
//	kubectl annotate managednode <name> upgrade.nodemanager/hold-   # remove
const AnnotationUpgradeHold = "upgrade.nodemanager/hold"

// FreeBSD jail annotations (update.freebsd.nodemanager/*)

// AnnotationJailLastUpdate records the RFC3339 timestamp of the most recent
// successful freebsd-update run on a Jail. Set by the controller; do not edit manually.
const AnnotationJailLastUpdate = "update.freebsd.nodemanager/last"
