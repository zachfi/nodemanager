package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	bootstrapClusterRoleName = "nodemanager-node"

	defaultBootstrapTokenTTL = 8760 * time.Hour // 1 year — used by `bootstrap`
	defaultTokenTTL          = 1 * time.Hour    // short-lived — used by `token`
)

// nodeClusterRoleRules defines the minimal permissions a bare-metal nodemanager
// instance needs. Applied by `bootstrap` and `token`; mirrored in
// config/rbac/node-role.yaml.
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

// runBootstrap implements `nodemanager bootstrap`.
//
// Uses a broad kubeconfig to provision all Kubernetes resources for the node
// (namespace, ClusterRole, ServiceAccount, ClusterRoleBinding, token-refresh
// Role) and writes a long-lived node-scoped kubeconfig.
//
// This command requires admin-level credentials. Once complete, discard the
// bootstrap kubeconfig — all future runs use the scoped one.
//
// To generate a short-lived, limited-scope credential instead of handing over
// admin credentials, use `nodemanager token` first and pass its output here.
func runBootstrap(args []string) {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)

	var (
		bootstrapKubeconfig = fs.String("bootstrap-kubeconfig", "", "Path to a kubeconfig with rights to create RBAC and ServiceAccounts (required)")
		outputKubeconfig    = fs.String("kubeconfig", defaultNodeKubeconfigPath(), "Path to write the node-scoped kubeconfig")
		namespace           = fs.String("namespace", "nodemanager", "Kubernetes namespace for nodemanager objects")
		hostname            = fs.String("hostname", "", "Node name to use for the ServiceAccount (defaults to os.Hostname)")
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

	if err := ensureNodeResources(ctx, cs, *namespace, *hostname); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	token := mustIssueToken(ctx, cs, *namespace, *hostname, *tokenTTL)
	server, caData := mustServerAndCA(rawCfg)

	writeKubeconfig(*outputKubeconfig, *hostname, *namespace, server, caData, token)

	fmt.Printf("\nkubeconfig written to %s\n", *outputKubeconfig)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Delete the bootstrap kubeconfig: rm %s\n", *bootstrapKubeconfig)
	fmt.Printf("  2. Start the service:\n")
	fmt.Printf("       Linux:   systemctl enable --now nodemanager\n")
	fmt.Printf("       FreeBSD: sysrc nodemanager_enable=YES && service nodemanager start\n")
}

// runToken implements `nodemanager token`.
//
// Creates all Kubernetes resources for the named node (idempotent) then issues
// a short-lived kubeconfig. Hand this to the new host; it runs
// `nodemanager bootstrap --bootstrap-kubeconfig <file>` to exchange it for a
// long-lived credential. The temporary kubeconfig expires automatically.
//
// Re-running `nodemanager token` for the same hostname is safe and can be used
// to hand out a fresh short-lived credential without disturbing the node's
// long-lived kubeconfig.
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

	if err := ensureNodeResources(ctx, cs, *namespace, *hostname); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

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

// ── shared helpers ────────────────────────────────────────────────────────────

// ensureNodeResources creates (or updates) all Kubernetes resources needed for
// a bare-metal nodemanager instance:
//   - namespace
//   - nodemanager-node ClusterRole
//   - nodemanager-<hostname> ServiceAccount
//   - nodemanager-<hostname> ClusterRoleBinding
//   - nodemanager-<hostname> token-refresh Role + RoleBinding (namespace-scoped,
//     allows the SA to request new tokens for itself only)
func ensureNodeResources(ctx context.Context, cs kubernetes.Interface, namespace, hostname string) error {
	saName := "nodemanager-" + hostname

	if err := ensureNamespace(ctx, cs, namespace); err != nil {
		return fmt.Errorf("ensuring namespace %q: %w", namespace, err)
	}
	fmt.Printf("namespace %q ready\n", namespace)

	if err := applyClusterRole(ctx, cs); err != nil {
		return fmt.Errorf("applying ClusterRole %q: %w", bootstrapClusterRoleName, err)
	}
	fmt.Printf("ClusterRole %q ready\n", bootstrapClusterRoleName)

	if err := ensureServiceAccount(ctx, cs, namespace, saName); err != nil {
		return fmt.Errorf("ensuring ServiceAccount %q: %w", saName, err)
	}
	fmt.Printf("ServiceAccount %q ready\n", saName)

	if err := ensureClusterRoleBinding(ctx, cs, saName, namespace, saName); err != nil {
		return fmt.Errorf("ensuring ClusterRoleBinding %q: %w", saName, err)
	}
	fmt.Printf("ClusterRoleBinding %q ready\n", saName)

	if err := ensureTokenRefreshRole(ctx, cs, namespace, saName); err != nil {
		return fmt.Errorf("ensuring token-refresh Role for %q: %w", saName, err)
	}
	fmt.Printf("token-refresh Role for %q ready\n", saName)

	return nil
}

// ensureTokenRefreshRole creates a namespace-scoped Role and RoleBinding that
// allow saName to request new tokens for itself only (via resourceNames). This
// lets the SA exchange a short-lived bootstrap token for a long-lived one
// without requiring any admin credentials.
func ensureTokenRefreshRole(ctx context.Context, cs kubernetes.Interface, namespace, saName string) error {
	roleName := saName + "-token-refresh"

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: namespace},
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{""},
			Resources:     []string{"serviceaccounts/token"},
			Verbs:         []string{"create"},
			ResourceNames: []string{saName}, // restricted to this SA only
		}},
	}
	_, err := cs.RbacV1().Roles(namespace).Create(ctx, role, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}

	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: namespace},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     roleName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: namespace,
		}},
	}
	_, err = cs.RbacV1().RoleBindings(namespace).Create(ctx, binding, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

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
		fmt.Fprintf(os.Stderr, "error: requesting token for ServiceAccount %q: %v\n", saName, err)
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
		// Fall back to default loading rules (KUBECONFIG env / ~/.kube/config).
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

// ── low-level k8s helpers ─────────────────────────────────────────────────────

func ensureNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func applyClusterRole(ctx context.Context, cs kubernetes.Interface) error {
	desired := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: bootstrapClusterRoleName},
		Rules:      nodeClusterRoleRules,
	}
	_, err := cs.RbacV1().ClusterRoles().Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !k8serrors.IsAlreadyExists(err) {
		return err
	}
	existing, err := cs.RbacV1().ClusterRoles().Get(ctx, bootstrapClusterRoleName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	existing.Rules = nodeClusterRoleRules
	_, err = cs.RbacV1().ClusterRoles().Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func ensureServiceAccount(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	_, err := cs.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func ensureClusterRoleBinding(ctx context.Context, cs kubernetes.Interface, name, namespace, saName string) error {
	desired := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     bootstrapClusterRoleName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: namespace,
		}},
	}
	_, err := cs.RbacV1().ClusterRoleBindings().Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !k8serrors.IsAlreadyExists(err) {
		return err
	}
	existing, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	existing.RoleRef = desired.RoleRef
	existing.Subjects = desired.Subjects
	_, err = cs.RbacV1().ClusterRoleBindings().Update(ctx, existing, metav1.UpdateOptions{})
	return err
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
