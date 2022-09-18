package controllers

import (
	"context"
	"fmt"
	"os"
	commonv1 "znet/nodemanager/api/v1"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var poudriereLabelGate map[string]string = map[string]string{"poudriere.freebsd.znet/builder": "true"}

// nodeLabelMatch returns an error if not all of the matched key/value pairs are not matched against the given nodes labels.
func nodeLabelMatch(ctx context.Context, r client.Reader, req ctrl.Request, matchers map[string]string) error {
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
		return err
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
