package controllers

import (
	"context"
	"fmt"
	"os"

	commonv1 "github.com/xaque208/nodemanager/api/v1"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NodeData struct {
	Labels     map[string]string
	ConfigMaps []corev1.ConfigMap
	Secrets    []corev1.Secret
}

type Data struct {
	Node NodeData
}

var poudriereLabelGate map[string]string = map[string]string{"poudriere.freebsd.nodemanager/builder": "true"}

// createOrGetNode will query for the current node in the requested namespace.  If the node does not exist, it will be created.  Node results are returned, or an error.
func createOrGetNode(ctx context.Context, log logr.Logger, r client.Reader, w client.Writer, req ctrl.Request) (commonv1.ManagedNode, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return commonv1.ManagedNode{}, err
	}

	nodeName := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      hostname,
	}

	var node commonv1.ManagedNode
	if err := r.Get(ctx, nodeName, &node); err != nil {
		// We create the initial node if we aren't able to find one.
		node.SetName(hostname)
		node.SetNamespace(req.Namespace)
		log.Info("creating new node", "node", fmt.Sprintf("%+v", node))
		err := createInitialNode(ctx, w, &node)
		if err != nil {
			return commonv1.ManagedNode{}, err
		}
	}

	return node, nil
}

// nodeLabelMatch returns an error if not all of the matched key/value pairs are not matched against the given nodes labels.  If the hostname for the node running this controller is not found, a new node is created.
func nodeLabelMatch(node commonv1.ManagedNode, matchers map[string]string) error {
	if matchAllLabels(node.Labels, matchers) {
		return nil
	}

	return fmt.Errorf("unmatched labels: %s to matchers: %s", node.Labels, matchers)
}

// createInitialNode will create a ManagedNode object.
func createInitialNode(ctx context.Context, w client.Writer, obj client.Object) error {
	if err := w.Create(ctx, obj); err != nil {
		return errors.Wrap(err, "failed to craete initial node")
	}
	return nil
}

// Match all labels returns true if all of the received labels match the receiveed matchers.
func matchAllLabels(labels, matchers map[string]string) bool {
	for k, v := range matchers {
		if val, ok := labels[k]; ok {
			if val != v {
				return false
			}
		} else {
			return false
		}
	}

	return true
}
