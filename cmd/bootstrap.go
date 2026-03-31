package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	bootstrapClusterRoleName = "nodemanager-node"

	defaultBootstrapTokenTTL = 8760 * time.Hour // 1 year — used by `bootstrap`
	defaultTokenTTL          = 1 * time.Hour    // short-lived — used by `token`
)

// nodeClusterRoleRules defines the minimal permissions a bare-metal nodemanager
// instance needs. Mirrored in config/rbac/node-role.yaml and printed by
// `nodemanager rbac`.
var nodeClusterRoleRules = []rbacv1.PolicyRule{
	// ConfigSets: read-only
	{
		APIGroups: []string{"common.nodemanager"},
		Resources: []string{"configsets"},
		Verbs:     []string{"get", "list", "watch"},
	},
	// ManagedNodes: write own node + status
	{
		APIGroups: []string{"common.nodemanager"},
		Resources: []string{"managednodes", "managednodes/status", "managednodes/finalizers"},
		Verbs:     []string{"get", "list", "watch", "create", "update", "patch"},
	},
	// Secrets: read SecretRefs in ConfigSets; create WireGuard key secrets
	{
		APIGroups: []string{""},
		Resources: []string{"secrets"},
		Verbs:     []string{"get", "create"},
	},
	// ConfigMaps: read ConfigMapRefs in ConfigSets
	{
		APIGroups: []string{""},
		Resources: []string{"configmaps"},
		Verbs:     []string{"get", "list", "watch"},
	},
	// Leases: distributed locking for upgrade groups and jail update groups
	{
		APIGroups: []string{"coordination.k8s.io"},
		Resources: []string{"leases"},
		Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
	},
	// Jails: FreeBSD jail CRDs (no-op on Linux nodes — no controller runs)
	{
		APIGroups: []string{"freebsd.nodemanager"},
		Resources: []string{"jails", "jails/status", "jails/finalizers"},
		Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
	},
}

// runRBAC implements `nodemanager rbac`.
//
// Prints the Kubernetes RBAC manifests required for a node to stdout.
// Pipe the output into your RBAC management pipeline:
//
//	nodemanager rbac --hostname myhost | kubectl apply -f -
//
// The output is intentionally declarative and idempotent — applying it
// multiple times for the same hostname is safe.
//
// The shared ClusterRole (nodemanager-node) is included in the output so that
// a fresh cluster can be set up with a single apply. If you manage it
// separately, the duplicate apply is a no-op.
func runRBAC(args []string) {
	fs := flag.NewFlagSet("rbac", flag.ExitOnError)
	hostname := fs.String("hostname", "", "Node name (required)")
	namespace := fs.String("namespace", "nodemanager", "Kubernetes namespace for nodemanager objects")
	_ = fs.Parse(args)

	if *hostname == "" {
		fmt.Fprintln(os.Stderr, "error: --hostname is required")
		fs.Usage()
		os.Exit(1)
	}

	saName := "nodemanager-" + *hostname
	refreshRoleName := saName + "-token-refresh"

	objects := []any{
		// Namespace
		withTypeMeta(corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: *namespace},
		}, "v1", "Namespace"),

		// ServiceAccount for the node
		withTypeMeta(corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: *namespace},
		}, "v1", "ServiceAccount"),

		// Shared ClusterRole — same for all nodes
		withTypeMeta(rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: bootstrapClusterRoleName},
			Rules:      nodeClusterRoleRules,
		}, "rbac.authorization.k8s.io/v1", "ClusterRole"),

		// Per-node ClusterRoleBinding: SA → nodemanager-node
		withTypeMeta(rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: saName},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     bootstrapClusterRoleName,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: *namespace,
			}},
		}, "rbac.authorization.k8s.io/v1", "ClusterRoleBinding"),

		// Per-node Role: allow the SA to request tokens for itself only.
		// This is the "allow node $node to create token for $node" rule —
		// resourceNames restricts the TokenRequest to the node's own SA.
		withTypeMeta(rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: refreshRoleName, Namespace: *namespace},
			Rules: []rbacv1.PolicyRule{{
				APIGroups:     []string{""},
				Resources:     []string{"serviceaccounts/token"},
				Verbs:         []string{"create"},
				ResourceNames: []string{saName},
			}},
		}, "rbac.authorization.k8s.io/v1", "Role"),

		// Per-node RoleBinding: SA → token-refresh Role
		withTypeMeta(rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: refreshRoleName, Namespace: *namespace},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     refreshRoleName,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: *namespace,
			}},
		}, "rbac.authorization.k8s.io/v1", "RoleBinding"),
	}

	for _, obj := range objects {
		j, err := json.Marshal(obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: marshalling object: %v\n", err)
			os.Exit(1)
		}
		y, err := sigsyaml.JSONToYAML(j)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: converting to YAML: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("---\n%s", y)
	}
}

// runToken implements `nodemanager token`.
//
// Issues a short-lived kubeconfig for the named node. Run this from a machine
// with cluster access after applying the RBAC manifests:
//
//	nodemanager rbac --hostname myhost | kubectl apply -f -
//	nodemanager token --hostname myhost | ssh myhost \
//	  "cat > /tmp/bootstrap.kubeconfig && \
//	   nodemanager bootstrap --bootstrap-kubeconfig /tmp/bootstrap.kubeconfig"
//
// The node's ServiceAccount must already exist. RBAC is never modified.
func runToken(args []string) {
	fs := flag.NewFlagSet("token", flag.ExitOnError)

	var (
		adminKubeconfig = fs.String("kubeconfig", "", "Admin kubeconfig (defaults to KUBECONFIG env / ~/.kube/config)")
		outputFile      = fs.String("out", "", "Write the bootstrap kubeconfig to this file (default: stdout)")
		namespace       = fs.String("namespace", "nodemanager", "Kubernetes namespace for nodemanager objects")
		hostname        = fs.String("hostname", "", "Target node name (required)")
		ttl             = fs.Duration("ttl", defaultTokenTTL, "Lifetime of the temporary token")
	)
	_ = fs.Parse(args)

	if *hostname == "" {
		fmt.Fprintln(os.Stderr, "error: --hostname is required")
		fs.Usage()
		os.Exit(1)
	}

	ctx := context.Background()
	cs := mustClientset(*adminKubeconfig)
	rawCfg := mustLoadKubeconfig(*adminKubeconfig)

	token := mustIssueToken(ctx, cs, *namespace, *hostname, *ttl)
	server, caData := mustServerAndCA(rawCfg)

	kubecfg := buildKubeconfig(*hostname, *namespace, server, caData, token)
	kubecfgBytes, err := clientcmd.Write(*kubecfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: serialising kubeconfig: %v\n", err)
		os.Exit(1)
	}

	if *outputFile != "" {
		if err := os.MkdirAll(filepath.Dir(*outputFile), 0o750); err != nil {
			fmt.Fprintf(os.Stderr, "error: creating output directory: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*outputFile, kubecfgBytes, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "error: writing %s: %v\n", *outputFile, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "bootstrap kubeconfig written to %s (expires in %s)\n", *outputFile, ttl)
		fmt.Fprintf(os.Stderr, "copy to the new host, then run:\n")
		fmt.Fprintf(os.Stderr, "  nodemanager bootstrap --bootstrap-kubeconfig %s\n", *outputFile)
	} else {
		fmt.Print(string(kubecfgBytes))
	}
}

// runBootstrap implements `nodemanager bootstrap`.
//
// Uses a short-lived kubeconfig (from `nodemanager token`) or any kubeconfig
// with TokenRequest rights to write a long-lived node-scoped kubeconfig.
// RBAC is never modified — the ServiceAccount must already exist.
func runBootstrap(args []string) {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)

	var (
		bootstrapKubeconfig = fs.String("bootstrap-kubeconfig", "", "Path to a bootstrap kubeconfig (required)")
		outputKubeconfig    = fs.String("kubeconfig", defaultNodeKubeconfigPath(), "Path to write the node-scoped kubeconfig")
		namespace           = fs.String("namespace", "nodemanager", "Kubernetes namespace for nodemanager objects")
		hostname            = fs.String("hostname", "", "Node name (defaults to os.Hostname)")
		tokenTTL            = fs.Duration("token-ttl", defaultBootstrapTokenTTL, "Lifetime of the issued ServiceAccount token")
	)
	_ = fs.Parse(args)

	if *bootstrapKubeconfig == "" {
		fmt.Fprintln(os.Stderr, "error: --bootstrap-kubeconfig is required")
		fs.Usage()
		os.Exit(1)
	}

	*hostname = resolveHostname(*hostname)

	ctx := context.Background()
	cs := mustClientset(*bootstrapKubeconfig)
	rawCfg := mustLoadKubeconfig(*bootstrapKubeconfig)

	token := mustIssueToken(ctx, cs, *namespace, *hostname, *tokenTTL)
	server, caData := mustServerAndCA(rawCfg)

	writeKubeconfig(*outputKubeconfig, *hostname, *namespace, server, caData, token)

	fmt.Printf("kubeconfig written to %s\n", *outputKubeconfig)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Delete the bootstrap kubeconfig: rm %s\n", *bootstrapKubeconfig)
	fmt.Printf("  2. Start the service:\n")
	fmt.Printf("       Linux:   systemctl enable --now nodemanager\n")
	fmt.Printf("       FreeBSD: sysrc nodemanager_enable=YES && service nodemanager start\n")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func mustIssueToken(ctx context.Context, cs kubernetes.Interface, namespace, hostname string, ttl time.Duration) string {
	saName := "nodemanager-" + hostname
	ttlSecs := int64(ttl.Seconds())
	tokenReq, err := cs.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, saName,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: &ttlSecs,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: requesting token for %q: %v\n", saName, err)
		fmt.Fprintf(os.Stderr, "hint: ensure RBAC is applied first: nodemanager rbac --hostname %s | kubectl apply -f -\n", hostname)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "token issued (expires %s)\n", tokenReq.Status.ExpirationTimestamp.UTC().Format(time.RFC3339))
	return tokenReq.Status.Token
}

func mustClientset(kubeconfigPath string) kubernetes.Interface {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading kubeconfig %q: %v\n", kubeconfigPath, err)
		os.Exit(1)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: building Kubernetes client: %v\n", err)
		os.Exit(1)
	}
	return cs
}

func mustLoadKubeconfig(path string) *clientcmdapi.Config {
	if path == "" {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err := rules.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: loading default kubeconfig: %v\n", err)
			os.Exit(1)
		}
		return cfg
	}
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading kubeconfig %q: %v\n", path, err)
		os.Exit(1)
	}
	return cfg
}

func mustServerAndCA(cfg *clientcmdapi.Config) (server string, caData []byte) {
	s, ca, err := serverAndCA(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading server/CA from kubeconfig: %v\n", err)
		os.Exit(1)
	}
	return s, ca
}

func writeKubeconfig(outputPath, hostname, namespace, server string, caData []byte, token string) {
	nodeCfg := buildKubeconfig(hostname, namespace, server, caData, token)
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating kubeconfig directory: %v\n", err)
		os.Exit(1)
	}
	if err := clientcmd.WriteToFile(*nodeCfg, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: writing kubeconfig to %q: %v\n", outputPath, err)
		os.Exit(1)
	}
}

func resolveHostname(given string) string {
	if given != "" {
		return given
	}
	h, err := os.Hostname()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: could not determine hostname: %v\n", err)
		os.Exit(1)
	}
	return h
}

func serverAndCA(cfg *clientcmdapi.Config) (server string, caData []byte, err error) {
	ctx, ok := cfg.Contexts[cfg.CurrentContext]
	if !ok {
		return "", nil, errors.New("no current context in kubeconfig")
	}
	cluster, ok := cfg.Clusters[ctx.Cluster]
	if !ok {
		return "", nil, fmt.Errorf("cluster %q not found in kubeconfig", ctx.Cluster)
	}
	return cluster.Server, cluster.CertificateAuthorityData, nil
}

func buildKubeconfig(hostname, namespace, server string, caData []byte, token string) *clientcmdapi.Config {
	userName := "nodemanager-" + hostname
	return &clientcmdapi.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: map[string]*clientcmdapi.Cluster{
			"nodemanager": {
				Server:                   server,
				CertificateAuthorityData: caData,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			userName: {Token: token},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"nodemanager": {
				Cluster:   "nodemanager",
				AuthInfo:  userName,
				Namespace: namespace,
			},
		},
		CurrentContext: "nodemanager",
	}
}

func defaultNodeKubeconfigPath() string {
	if _, err := os.Stat("/usr/local/etc"); err == nil {
		return "/usr/local/etc/nodemanager/kubeconfig"
	}
	return "/etc/nodemanager/kubeconfig"
}

// withTypeMeta wraps any struct value with the given apiVersion and kind set,
// using an anonymous struct so the TypeMeta fields appear at the top level in
// the marshalled output.
func withTypeMeta(obj any, apiVersion, kind string) any {
	type typeMeta struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
	}
	// Marshal the object to a map, inject TypeMeta, return the merged map.
	data, _ := json.Marshal(obj)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	m["apiVersion"] = apiVersion
	m["kind"] = kind
	return m
}
