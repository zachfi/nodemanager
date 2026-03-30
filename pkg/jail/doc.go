// Package jail manages FreeBSD jails via ZFS snapshots and clones.
//
// # Dataset layout
//
//	<dataset>/releases/<version>          — extracted FreeBSD base (shared template)
//	<dataset>/jails/<name>                — per-jail container dataset
//	<dataset>/jails/<name>/root           — ZFS clone of releases/<version>@<name>
//
// # Jail creation flow
//
//  1. Ensure releases/<version> dataset exists (zfs.Manager.Ensure).
//  2. Download base.txz from the FreeBSD mirror if not already extracted.
//  3. Snapshot the release: zfs snapshot releases/<version>@<name>.
//  4. Clone the snapshot: zfs clone releases/<version>@<name> jails/<name>/root.
//  5. Copy /etc/resolv.conf and /etc/localtime into the jail root.
//  6. Write per-jail fstab to jails/<name>/fstab (if mounts are declared).
//  7. Write /etc/jail.conf.d/<name>.conf.
//
// # Jail deletion flow
//
//  1. Remove /etc/jail.conf.d/<name>.conf.
//  2. Remove jails/<name>/fstab.
//  3. zfs destroy -r jails/<name>  (destroys root clone and container dataset).
//  4. zfs destroy releases/<version>@<name>  (remove the origin snapshot).
package jail
