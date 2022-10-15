package controllers

import (
	"context"
	"fmt"
	"os"

	commonv1 "github.com/xaque208/nodemanager/api/v1"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var poudriereLabelGate map[string]string = map[string]string{"poudriere.freebsd.znet/builder": "true"}

// nodeLabelMatch returns an error if not all of the matched key/value pairs are not matched against the given nodes labels.  If the hostname for the node running this controller is not found, a new node is created.
func nodeLabelMatch(ctx context.Context, log logr.Logger, r client.Reader, w client.Writer, req ctrl.Request, matchers map[string]string) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
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
		return createInitialNode(ctx, w, &node)
	}

	matchAll := func(labels, matchers map[string]string) bool {
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

	if matchAll(node.Labels, matchers) {
		return nil
	}

	return fmt.Errorf("unmatched labels: %s to matchers: %s", node.Labels, matchers)
}

func createInitialNode(ctx context.Context, w client.Writer, obj client.Object) error {
	if err := w.Create(ctx, obj); err != nil {
		return errors.Wrap(err, "failed to craete initial node")
	}
	return nil
}
