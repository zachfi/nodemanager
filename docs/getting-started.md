# Getting started

This guide walks through declaring your first `ConfigSet` and seeing it
applied to a managed node.

## Prerequisites

- A running Kubernetes cluster (or a local one via `kind` or `k3s`)
- CRDs installed: `make install`
- nodemanager running on at least one host (see [deployment](deployment.md))

## 1. Observe the ManagedNode

When nodemanager starts on a host, it creates a `ManagedNode` object for that
hostname and labels it with OS and architecture information:

```sh
kubectl get managednodes -n nodemanager
```

```
NAME       AGE
myhost     2m
```

```sh
kubectl get managednode myhost -n nodemanager -o yaml
```

```yaml
metadata:
  labels:
    kubernetes.io/arch: amd64
    kubernetes.io/os: linux
    nodemanager.io/os: arch
status:
  release: "rolling"
  interfaces:
    eth0:
      ipv4: ["192.168.1.10"]
```

These labels are what `ConfigSet` selectors match against.

## 2. Write a ConfigSet

A `ConfigSet` declares desired state for any node whose labels match its
own labels. The following installs `chrony`, writes its config, and manages
the service — but only on Arch Linux nodes:

```yaml
apiVersion: common.nodemanager/v1
kind: ConfigSet
metadata:
  labels:
    nodemanager.io/os: arch
  name: clock
  namespace: nodemanager
spec:
  packages:
    - name: chrony
      ensure: installed
  files:
    - path: /etc/chrony.conf
      ensure: file
      owner: root
      group: root
      mode: "0644"
      content: |
        pool 2.arch.pool.ntp.org iburst
        rtcsync
  services:
    - name: chronyd
      ensure: running
      enable: true
      subscribe_files:
        - /etc/chrony.conf
```

Apply it:

```sh
kubectl apply -f clock.yaml
```

nodemanager on any Arch Linux node will pick this up within its next
reconcile cycle, install the package, write the file, and start the service.

## 3. Use label selectors to target specific nodes

`ConfigSet` labels act as a **selector**: the controller applies the
`ConfigSet` only if every label on the `ConfigSet` is also present on the
local `ManagedNode`. This is the same model as Kubernetes label selectors.

Target a single host:

```yaml
metadata:
  labels:
    kubernetes.io/hostname: db01
```

Target all FreeBSD nodes:

```yaml
metadata:
  labels:
    kubernetes.io/os: freebsd
```

Target ARM nodes running Alpine:

```yaml
metadata:
  labels:
    nodemanager.io/os: alpine
    kubernetes.io/arch: arm64
```

## 4. Use templates for dynamic content

Files can use [gomplate](https://docs.gomplate.ca/) templates to generate
content dynamically. Data from Kubernetes Secrets and ConfigMaps is available,
as well as the node's own labels and observed status:

```yaml
files:
  - path: /etc/hosts.extra
    ensure: file
    template: |
      # Generated for {{ (ds "data").Node.Labels["kubernetes.io/hostname"] }}
      {{ range (ds "data").Nodes -}}
      {{ range .Status.Interfaces -}}
      {{ range .IPv4 }}{{ . }}  {{ $.Name }}{{ end }}
      {{ end -}}
      {{ end }}
```

See the [template data reference](template-data.md) for the full data
structure available in templates.

## 5. Scheduled upgrades

Add an upgrade schedule to the `ManagedNode` spec to enable automatic
OS-level upgrades:

```yaml
spec:
  upgrade:
    schedule: "0 3 * * *"  # 3am daily
    delay: 23h              # don't re-upgrade within 23 hours
    group: workers          # only one node in this group upgrades at a time
```

nodemanager will run the OS package upgrade (`pacman -Syu`, `apk upgrade`,
`pkg upgrade`) at the scheduled time, cordon and drain the Kubernetes node
if applicable, then reboot.
