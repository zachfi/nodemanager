package common

// AnnotationUpgradeHold disables automatic upgrades on a ManagedNode when set
// to any non-empty value. Remove the annotation to re-enable upgrades.
//
//	kubectl annotate managednode <name> upgrade.nodemanager/hold=true
//	kubectl annotate managednode <name> upgrade.nodemanager/hold-   # remove
const AnnotationUpgradeHold = "upgrade.nodemanager/hold"
