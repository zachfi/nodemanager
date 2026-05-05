package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/apply"
	"github.com/zachfi/nodemanager/pkg/system"
	"sigs.k8s.io/yaml"
)

func runApply(args []string) {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	manifestDir := fs.String("manifest-dir", "", "directory containing ConfigSet (and optionally ManagedNode) YAML files (required)")
	nodeName := fs.String("node-name", "", "node name to use for label matching; defaults to local hostname")
	gomplatePath := fs.String("gomplate-path", "gomplate", "path to gomplate binary")
	logLevel := fs.String("log-level", "info", "log level (debug, info, warn, error)")
	_ = fs.Parse(args)

	if *manifestDir == "" {
		fmt.Fprintln(os.Stderr, "apply: --manifest-dir is required")
		fs.Usage()
		os.Exit(1)
	}

	level, err := parseLevel(*logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apply: invalid log level: %v\n", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	absDir, err := filepath.Abs(*manifestDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apply: resolving manifest-dir: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	sys, _, err := system.New(ctx, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apply: detecting system: %v\n", err)
		os.Exit(1)
	}

	hostname := *nodeName
	if hostname == "" {
		hostname, err = sys.Node().Hostname()
		if err != nil {
			fmt.Fprintf(os.Stderr, "apply: getting hostname: %v\n", err)
			os.Exit(1)
		}
	}

	configSets, localNode, err := loadManifests(absDir, hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apply: loading manifests: %v\n", err)
		os.Exit(1)
	}

	logger.Info("loaded manifests", "configsets", len(configSets), "node", localNode.Name)

	applier := apply.New(sys, logger, apply.Config{
		GomplatePath: *gomplatePath,
		ManifestRoot: absDir,
	})
	resolver := apply.NewLocalResolver(logger, localNode, configSets)

	var applyErr error
	for _, cs := range configSets {
		if !nodeMatches(localNode, cs.Labels) {
			logger.Debug("configset labels do not match node, skipping", "configset", cs.Name)
			continue
		}
		logger.Info("applying configset", "configset", cs.Name,
			"files", len(cs.Spec.Files),
			"packages", len(cs.Spec.Packages),
			"services", len(cs.Spec.Services),
			"executions", len(cs.Spec.Executions))

		pkgErr := applier.Packages(ctx, cs.Spec.Packages)
		if pkgErr != nil {
			logger.Error("packages failed", "configset", cs.Name, "err", pkgErr)
			applyErr = pkgErr
		}

		changedFiles, _, fileErr := applier.Files(ctx, hostname, cs.Name, "", cs.Spec.Files, localNode, resolver)
		if fileErr != nil {
			logger.Error("files failed", "configset", cs.Name, "err", fileErr)
			applyErr = fileErr
		}

		svcErr := applier.Services(ctx, cs.Spec.Services, cs.Spec.Files, changedFiles)
		if svcErr != nil {
			logger.Error("services failed", "configset", cs.Name, "err", svcErr)
			applyErr = svcErr
		}

		execErr := applier.Execs(ctx, cs.Spec.Executions, changedFiles)
		if execErr != nil {
			logger.Error("executions failed", "configset", cs.Name, "err", execErr)
			applyErr = execErr
		}
	}

	if applyErr != nil {
		os.Exit(1)
	}
}

// loadManifests reads all *.yaml and *.yml files from dir, decoding ConfigSet
// and ManagedNode objects. Returns the ConfigSets found and a ManagedNode
// representing the local host (synthesised from hostname if none was found).
func loadManifests(dir, hostname string) ([]commonv1.ConfigSet, commonv1.ManagedNode, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, commonv1.ManagedNode{}, fmt.Errorf("read directory: %w", err)
	}

	var configSets []commonv1.ConfigSet
	var localNode *commonv1.ManagedNode

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml" {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			return nil, commonv1.ManagedNode{}, fmt.Errorf("read %s: %w", name, readErr)
		}

		// Peek at kind to route to the correct type.
		var meta struct {
			Kind string `yaml:"kind"`
		}
		if peekErr := yaml.Unmarshal(data, &meta); peekErr != nil {
			return nil, commonv1.ManagedNode{}, fmt.Errorf("parse %s: %w", name, peekErr)
		}

		switch meta.Kind {
		case "ConfigSet":
			var cs commonv1.ConfigSet
			if decErr := yaml.Unmarshal(data, &cs); decErr != nil {
				return nil, commonv1.ManagedNode{}, fmt.Errorf("decode ConfigSet from %s: %w", name, decErr)
			}
			configSets = append(configSets, cs)

		case "ManagedNode":
			var mn commonv1.ManagedNode
			if decErr := yaml.Unmarshal(data, &mn); decErr != nil {
				return nil, commonv1.ManagedNode{}, fmt.Errorf("decode ManagedNode from %s: %w", name, decErr)
			}
			if mn.Name == hostname {
				localNode = &mn
			}
		}
	}

	// If no matching ManagedNode was found, synthesise one with just the hostname label.
	if localNode == nil {
		mn := commonv1.ManagedNode{}
		mn.Name = hostname
		mn.Labels = map[string]string{
			"kubernetes.io/hostname": hostname,
		}
		localNode = &mn
	}

	return configSets, *localNode, nil
}

// nodeMatches reports whether the node's labels satisfy all label matchers on
// the ConfigSet. An empty matcher map means the ConfigSet matches no node.
func nodeMatches(node commonv1.ManagedNode, matchers map[string]string) bool {
	if len(matchers) == 0 {
		return false
	}
	for k, v := range matchers {
		if node.Labels[k] != v {
			return false
		}
	}
	return true
}
