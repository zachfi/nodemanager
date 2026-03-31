# Template data reference

Files in a `ConfigSet` can use [gomplate](https://docs.gomplate.ca/) templates.
The template data is available as the `data` datasource:

```
{{ (ds "data").<field> }}
```

## Top-level structure

| Field | Type | Description |
|---|---|---|
| `Node` | `NodeData` | The local node — its labels, secrets, configmaps, and observed status. |
| `Nodes` | `[]NodeInfo` | All `ManagedNode` objects in the namespace, including this node. Useful for generating mesh configuration. |

## NodeData (current node)

| Field | Type | Description |
|---|---|---|
| `Node.Labels` | `map[string]string` | Labels on the local `ManagedNode`. |
| `Node.ConfigMaps` | `map[string]string` | Data from all `configMapRefs` listed on the file. |
| `Node.Secrets` | `map[string][]byte` | Data from all `secretRefs` listed on the file. |
| `Node.Status` | `ManagedNodeStatus` | Observed state of the local node. |

## NodeInfo (all nodes)

Each entry in `Nodes` has:

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Hostname / `ManagedNode` object name. |
| `Labels` | `map[string]string` | Labels on that node. |
| `Status` | `ManagedNodeStatus` | Observed state of that node. |

## ManagedNodeStatus

| Field | Type | Description |
|---|---|---|
| `release` | `string` | OS release string (e.g. `"rolling"`, `"14.2-RELEASE"`). |
| `interfaces` | `map[string]NetworkInterface` | Non-loopback network interfaces keyed by interface name. |
| `sshHostKeys` | `[]SSHHostKey` | SSH host key fingerprints in SSHFP record format. |
| `wireGuard` | `[]WireGuardInterface` | WireGuard interface identities (public key + listen port). |
| `configsets` | `[]ConfigSetApplyStatus` | Per-ConfigSet reconciliation results. |

### NetworkInterface

| Field | Type | Description |
|---|---|---|
| `ipv4` | `[]string` | Non-loopback IPv4 addresses. |
| `ipv6` | `[]string` | Global unicast IPv6 addresses. |

### SSHHostKey

Each entry corresponds to one SSHFP DNS record (RFC 4255):

| Field | Type | Description |
|---|---|---|
| `algorithm` | `int` | SSHFP algorithm: `1`=RSA, `2`=DSA, `3`=ECDSA, `4`=Ed25519. |
| `fingerprintType` | `int` | Fingerprint type: `1`=SHA-1, `2`=SHA-256. |
| `fingerprint` | `string` | Hex-encoded fingerprint. |

### WireGuardInterface

| Field | Type | Description |
|---|---|---|
| `name` | `string` | Interface name (e.g. `wg0`). |
| `publicKey` | `string` | Base64-encoded Curve25519 public key. |
| `listenPort` | `int` | UDP listen port, if set. |

## Examples

### Reference a Secret value

```yaml
files:
  - path: /etc/app/secret.conf
    ensure: file
    secretRefs:
      - my-app-secret
    template: |
      password={{ index (ds "data").Node.Secrets "password" | strings.FromBytes }}
```

### Write a file with the node's hostname and IP

```yaml
template: |
  # {{ (ds "data").Node.Labels["kubernetes.io/hostname"] }}
  address={{ index (index (ds "data").Node.Status.Interfaces "eth0").IPv4 0 }}
```

### Generate WireGuard peer config from all nodes

```yaml
template: |
  [Interface]
  PrivateKey = {{ (ds "data").Node.Secrets | index "privateKey" | strings.FromBytes }}
  ListenPort = 51820

  {{ range (ds "data").Nodes -}}
  {{ if .Status.WireGuard -}}
  {{ $wg := index .Status.WireGuard 0 -}}
  [Peer]
  # {{ .Name }}
  PublicKey = {{ $wg.PublicKey }}
  Endpoint = {{ index (index .Status.Interfaces "eth0").IPv4 0 }}:{{ $wg.ListenPort }}
  AllowedIPs = 10.0.0.0/24
  PersistentKeepalive = 25

  {{ end -}}
  {{ end }}
```

### Generate SSHFP DNS records for all nodes

```yaml
template: |
  {{ range (ds "data").Nodes -}}
  {{ $name := .Name -}}
  {{ range .Status.SSHHostKeys -}}
  {{ $name }} IN SSHFP {{ .Algorithm }} {{ .FingerprintType }} {{ .Fingerprint }}
  {{ end -}}
  {{ end }}
```
