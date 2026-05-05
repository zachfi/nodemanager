package apply

import (
	"context"
	"log/slog"
	"strings"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
)

// LocalResolver implements DataResolver for offline apply mode. It returns
// locally-available data only: node labels and status from the supplied
// ManagedNode, and the managed-path index built from the in-memory ConfigSet
// list. SecretRefs and ConfigMapRefs are skipped with a warning.
type LocalResolver struct {
	logger     *slog.Logger
	node       commonv1.ManagedNode
	configSets []commonv1.ConfigSet
}

// NewLocalResolver returns a LocalResolver seeded with the local node and all
// ConfigSets loaded from the manifest directory.
func NewLocalResolver(logger *slog.Logger, node commonv1.ManagedNode, configSets []commonv1.ConfigSet) *LocalResolver {
	return &LocalResolver{logger: logger, node: node, configSets: configSets}
}

// CollectTemplateData returns template data populated from the local node.
// SecretRefs and ConfigMapRefs are not resolved and are logged as warnings.
func (r *LocalResolver) CollectTemplateData(_ context.Context, _ string, file commonv1.File, node commonv1.ManagedNode) (Data, error) {
	if len(file.SecretRefs) > 0 {
		r.logger.Warn("secretRefs are not supported in offline apply mode, skipping",
			"path", file.Path, "refs", file.SecretRefs)
	}
	if len(file.ConfigMapRefs) > 0 {
		r.logger.Warn("configMapRefs are not supported in offline apply mode, skipping",
			"path", file.Path, "refs", file.ConfigMapRefs)
	}

	nodeData := NodeData{
		Labels: node.Labels,
		Status: node.Status,
	}

	data := Data{
		Node: nodeData,
		Nodes: []NodeInfo{{
			Name:   node.Name,
			Labels: node.Labels,
			Status: node.Status,
		}},
	}
	return data, nil
}

// ManagedPathsUnder returns the set of file paths declared by any ConfigSet
// that matches the local node, filtered to those under dirPath.
func (r *LocalResolver) ManagedPathsUnder(_ context.Context, _ string, node commonv1.ManagedNode, dirPath string) map[string]struct{} {
	managed := make(map[string]struct{})
	prefix := dirPath
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for _, cs := range r.configSets {
		if !matchAllLabels(node.Labels, cs.Labels) {
			continue
		}
		for _, f := range cs.Spec.Files {
			if f.Path == dirPath || strings.HasPrefix(f.Path, prefix) {
				managed[f.Path] = struct{}{}
			}
		}
	}
	return managed
}

// matchAllLabels returns true when every key/value pair in matchers is present
// in labels. An empty matchers map returns false so unlabelled ConfigSets do
// not match every node.
func matchAllLabels(labels, matchers map[string]string) bool {
	if len(matchers) == 0 {
		return false
	}
	for k, v := range matchers {
		if labels[k] != v {
			return false
		}
	}
	return true
}
