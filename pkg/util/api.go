package util

import (
	"context"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/handler"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetNode(ctx context.Context, r client.Reader, req ctrl.Request, h handler.NodeHandler) (*commonv1.ManagedNode, error) {
	var (
		err  error
		node commonv1.ManagedNode
	)

	hostname, err := h.Hostname()
	if err != nil {
		return nil, client.IgnoreNotFound(err)
	}

	if hostname != req.Name {
		return nil, nil
	}

	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return nil, client.IgnoreNotFound(err)
	}

	return &node, nil
}
