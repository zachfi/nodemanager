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
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"k8s.io/client-go/tools/clientcmd"
)

const (
	bootstrapClusterRoleName = "nodemanager-node"
	defaultBootstrapTokenTTL = 8760 * time.Hour // 1 year
)

// nodeClusterRoleRules defines the minimal permissions a bare-metal nodemanager
// instance needs. This is applied by the bootstrap command and mirrored in
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

// runBootstrap implements the `nodemanager bootstrap` subcommand.  It uses a
// broad kubeconfig to create a node-scoped ServiceAccount, ClusterRoleBinding,
// and token, then writes a minimal kubeconfig that the main controller uses on
// all subsequent runs.
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

	if *hostname == "" {
		h, err := os.Hostname()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: could not determine hostname: %v\n", err)
			os.Exit(1)
		}
		*hostname = h
	}

	ctx := context.Background()

	// Load the broad bootstrap kubeconfig.
	bootstrapCfg, err := clientcmd.BuildConfigFromFlags("", *bootstrapKubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading bootstrap kubeconfig: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(bootstrapCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: building Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	saName := "nodemanager-" + *hostname
	bindingName := "nodemanager-" + *hostname

	// 1. Ensure the namespace exists.
	if err := ensureNamespace(ctx, clientset, *namespace); err != nil {
		fmt.Fprintf(os.Stderr, "error: ensuring namespace %q: %v\n", *namespace, err)
		os.Exit(1)
	}
	fmt.Printf("namespace %q ready\n", *namespace)

	// 2. Apply the ClusterRole.
	if err := applyClusterRole(ctx, clientset); err != nil {
		fmt.Fprintf(os.Stderr, "error: applying ClusterRole %q: %v\n", bootstrapClusterRoleName, err)
		os.Exit(1)
	}
	fmt.Printf("ClusterRole %q ready\n", bootstrapClusterRoleName)

	// 3. Ensure the node's ServiceAccount.
	if err := ensureServiceAccount(ctx, clientset, *namespace, saName); err != nil {
		fmt.Fprintf(os.Stderr, "error: ensuring ServiceAccount %q: %v\n", saName, err)
		os.Exit(1)
	}
	fmt.Printf("ServiceAccount %q ready\n", saName)

	// 4. Ensure the ClusterRoleBinding.
	if err := ensureClusterRoleBinding(ctx, clientset, bindingName, *namespace, saName); err != nil {
		fmt.Fprintf(os.Stderr, "error: ensuring ClusterRoleBinding %q: %v\n", bindingName, err)
		os.Exit(1)
	}
	fmt.Printf("ClusterRoleBinding %q ready\n", bindingName)

	// 5. Issue a bound token.
	ttlSecs := int64(tokenTTL.Seconds())
	tokenReq, err := clientset.CoreV1().ServiceAccounts(*namespace).CreateToken(ctx, saName,
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
	fmt.Printf("token issued (expires %s)\n", tokenReq.Status.ExpirationTimestamp.UTC().Format(time.RFC3339))

	// 6. Extract server URL and CA data from the bootstrap kubeconfig.
	rawBootstrap, err := clientcmd.LoadFromFile(*bootstrapKubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: re-loading bootstrap kubeconfig: %v\n", err)
		os.Exit(1)
	}
	server, caData, err := serverAndCA(rawBootstrap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading server/CA from bootstrap kubeconfig: %v\n", err)
		os.Exit(1)
	}

	// 7. Build and write the node-scoped kubeconfig.
	nodeCfg := buildKubeconfig(*hostname, *namespace, server, caData, tokenReq.Status.Token)

	if err := os.MkdirAll(filepath.Dir(*outputKubeconfig), 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating kubeconfig directory: %v\n", err)
		os.Exit(1)
	}
	if err := clientcmd.WriteToFile(*nodeCfg, *outputKubeconfig); err != nil {
		fmt.Fprintf(os.Stderr, "error: writing kubeconfig to %q: %v\n", *outputKubeconfig, err)
		os.Exit(1)
	}

	fmt.Printf("\nkubeconfig written to %s\n", *outputKubeconfig)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Delete the bootstrap kubeconfig: rm %s\n", *bootstrapKubeconfig)
	fmt.Printf("  2. Start the service:\n")
	fmt.Printf("       Linux:   systemctl enable --now nodemanager\n")
	fmt.Printf("       FreeBSD: sysrc nodemanager_enable=YES && service nodemanager start\n")
}

// ensureNamespace creates the namespace if it does not already exist.
func ensureNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// applyClusterRole creates or updates the nodemanager-node ClusterRole.
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

// ensureServiceAccount creates the ServiceAccount if it does not exist.
func ensureServiceAccount(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	_, err := cs.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// ensureClusterRoleBinding creates or updates a ClusterRoleBinding that binds
// the node's ServiceAccount to the nodemanager-node ClusterRole.
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

// serverAndCA extracts the API server URL and CA certificate data from the
// active context in a loaded kubeconfig.
func serverAndCA(cfg *clientcmdapi.Config) (server string, caData []byte, err error) {
	ctx, ok := cfg.Contexts[cfg.CurrentContext]
	if !ok {
		return "", nil, errors.New("no current context in bootstrap kubeconfig")
	}
	cluster, ok := cfg.Clusters[ctx.Cluster]
	if !ok {
		return "", nil, fmt.Errorf("cluster %q not found in bootstrap kubeconfig", ctx.Cluster)
	}
	return cluster.Server, cluster.CertificateAuthorityData, nil
}

// buildKubeconfig constructs a minimal clientcmdapi.Config for the node.
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

// defaultNodeKubeconfigPath returns the platform-appropriate default path for
// the node's scoped kubeconfig.
func defaultNodeKubeconfigPath() string {
	if _, err := os.Stat("/usr/local/etc"); err == nil {
		// FreeBSD convention
		return "/usr/local/etc/nodemanager/kubeconfig"
	}
	return "/etc/nodemanager/kubeconfig"
}
