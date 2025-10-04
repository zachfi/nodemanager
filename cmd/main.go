/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/go-logr/logr"
	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	controller "github.com/zachfi/nodemanager/internal/controller/common"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/common"
	"github.com/zachfi/nodemanager/pkg/system"

	// "github.com/zachfi/nodemanager/pkg/nodes/freebsd"

	//+kubebuilder:scaffold:imports
	"github.com/zachfi/zkit/pkg/tracing"
)

var (
	goos      = "unknown"
	goarch    = "unknown"
	gitCommit = "$Format:%H$" // sha1 from git, output of $(git rev-parse HEAD)

	buildDate = "1970-01-01T00:00:00Z" // build date in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
)

// version contains all the information related to the CLI version
type version struct {
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoOs      string `json:"goOs"`
	GoArch    string `json:"goArch"`
}

// versionString returns the CLI version
func versionString() string {
	return fmt.Sprintf("Version: %#v", version{
		gitCommit,
		buildDate,
		goos,
		goarch,
	})
}

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(commonv1.AddToScheme(scheme))
	utilruntime.Must(freebsdv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	cfg, configVerify, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed parsing config: %v\n", err)
		os.Exit(1)
	}

	isValid := configIsValid(cfg)

	// Exit if config.verify flag is true
	if configVerify {
		if !isValid {
			os.Exit(1)
		}
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !cfg.ControllerConfig.EnableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				cfg.ControllerConfig.Namespace: {},
			},
		},
		Metrics: metricsserver.Options{
			BindAddress:   cfg.ControllerConfig.MetricsAddr,
			SecureServing: cfg.ControllerConfig.SecureMetrics,
			TLSOpts:       tlsOpts,
		},
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: cfg.ControllerConfig.ProbeAddr,
		LeaderElection:         cfg.ControllerConfig.EnableLeaderElection,
		LeaderElectionID:       "0c551175.nodemanager",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	var (
		ctx    = ctrl.SetupSignalHandler()
		client = mgr.GetClient()
		scheme = mgr.GetScheme()
		locker = controller.NewKeyLocker(logger, cfg.ControllerConfig.Locker, client, common.AnnotationUpgradeLock)
	)

	system, err := system.New(ctx, logger)
	if err != nil {
		setupLog.Error(err, "unable to create system handler", "err", err)
		os.Exit(1)
	}

	managedNodeReconciler := controller.NewManagedNodeReconciler(client, scheme, logger, cfg.ControllerConfig.ManagedNode, system, locker)

	if err = (managedNodeReconciler).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ManagedNode")
		os.Exit(1)
	}

	configSetReconciler := controller.NewConfigSetReconciler(client, scheme, logger, cfg.ControllerConfig.ConfigSet, system, locker)

	if err = (configSetReconciler).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigSet")
		os.Exit(1)
	}

	// TODO: Switch on the system implementation
	// switch system.Node().(type) {
	// case *freebsd.FreeBSD:
	// 	poudriereReconciler := &freebsdcontroller.PoudriereReconciler{
	// 		Client: mgr.GetClient(),
	// 		Scheme: mgr.GetScheme(),
	// 	}
	// 	poudriereReconciler.WithTracer(otel.Tracer("ConfigSet"))
	// 	poudriereReconciler.WithLogger(logger.With("reconciler", "Poudriere"))
	//
	// 	if err = poudriereReconciler.SetupWithManager(mgr); err != nil {
	// 		setupLog.Error(err, "unable to create controller", "controller", "Poudriere")
	// 		os.Exit(1)
	// 	}
	// }

	//+kubebuilder:scaffold:builder

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	shutdownTracer, err := tracing.InstallOpenTelemetryTracer(
		&cfg.Tracing,
		logger,
		"nodemanager",
		versionString(),
	)
	if err != nil {
		setupLog.Error(err, "error initializing tracer")
		os.Exit(1)
	}
	defer shutdownTracer()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
