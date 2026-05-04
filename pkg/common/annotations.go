package common

// AnnotationUpgradeHold disables automatic upgrades on a ManagedNode when set
// to any non-empty value. Remove the annotation to re-enable upgrades.
//
//	kubectl annotate managednode <name> upgrade.nodemanager/hold=true
//	kubectl annotate managednode <name> upgrade.nodemanager/hold-   # remove
const AnnotationUpgradeHold = "upgrade.nodemanager/hold"

// AnnotationUpgradeForce triggers an immediate upgrade on the next reconcile,
// bypassing the schedule, delay, and forgiveness-window checks. The group lock
// is still honoured so upgrades within a group remain staggered. The controller
// removes this annotation once the upgrade has been initiated, preventing a
// repeated upgrade after reboot.
//
//	kubectl annotate managednode <name> upgrade.nodemanager/force=true
const AnnotationUpgradeForce = "upgrade.nodemanager/force"
