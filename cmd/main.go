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
	"net/http"
	"net/http/pprof"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"k8s.io/client-go/kubernetes"
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
	"github.com/zachfi/nodemanager/internal/controller/freebsd"
	"github.com/zachfi/nodemanager/internal/notification"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/locker"
	freebsdnode "github.com/zachfi/nodemanager/pkg/nodes/freebsd"
	"github.com/zachfi/nodemanager/pkg/system"

	// "github.com/zachfi/nodemanager/pkg/nodes/freebsd"

	//+kubebuilder:scaffold:imports
	"github.com/zachfi/zkit/pkg/tracing"
)

var (
	version   = "dev" // semantic version, populated via -ldflags
	goos      = "unknown"
	goarch    = "unknown"
	gitCommit = "$Format:%H$" // sha1 from git, output of $(git rev-parse HEAD)

	buildDate = "1970-01-01T00:00:00Z" // build date in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
)

// buildInfo contains all the information related to the CLI version
type buildInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoOs      string `json:"goOs"`
	GoArch    string `json:"goArch"`
}

// versionString returns the CLI version
func versionString() string {
	return fmt.Sprintf("Version: %#v", buildInfo{
		version,
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
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "bootstrap":
			runBootstrap(os.Args[2:])
			return
		case "token":
			runToken(os.Args[2:])
			return
		case "rbac":
			runRBAC(os.Args[2:])
			return
		case "version", "-version", "--version":
			fmt.Println(versionString())
			return
		}
	}

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

	level, err := parseLevel(cfg.LogLevel)
	if err != nil {
		setupLog.Error(err, "unable to parse log-level")
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
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

	gracePeriod := 30 * time.Second
	syncPeriod := 2 * time.Minute
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Cache: cache.Options{
			SyncPeriod: &syncPeriod,
			DefaultNamespaces: map[string]cache.Config{
				cfg.ControllerConfig.Namespace: {},
			},
		},
		Metrics: metricsserver.Options{
			BindAddress:   cfg.ControllerConfig.MetricsAddr,
			SecureServing: cfg.ControllerConfig.SecureMetrics,
			TLSOpts:       tlsOpts,
			ExtraHandlers: map[string]http.Handler{
				"/debug/pprof/":        http.HandlerFunc(pprof.Index),
				"/debug/pprof/cmdline": http.HandlerFunc(pprof.Cmdline),
				"/debug/pprof/profile": http.HandlerFunc(pprof.Profile),
				"/debug/pprof/symbol":  http.HandlerFunc(pprof.Symbol),
				"/debug/pprof/trace":   http.HandlerFunc(pprof.Trace),
			},
		},
		WebhookServer:           webhookServer,
		HealthProbeBindAddress:  cfg.ControllerConfig.ProbeAddr,
		LeaderElection:          cfg.ControllerConfig.EnableLeaderElection,
		LeaderElectionID:        "0c551175.nodemanager",
		GracefulShutdownTimeout: &gracePeriod,
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
		config = mgr.GetConfig()
	)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		setupLog.Error(err, "failed to create clientset", "err", err)
		os.Exit(1)
	}

	sys, id, err := system.New(ctx, logger)
	if err != nil {
		setupLog.Error(err, "unable to create system handler", "err", err)
		os.Exit(1)
	}

	hostname, err := sys.Node().Hostname()
	if err != nil {
		setupLog.Error(err, "failed to get hostname")
		os.Exit(1)
	}

	locker := locker.NewLeaseLocker(ctx, logger, cfg.ControllerConfig.Locker, clientset, cfg.ControllerConfig.Namespace, hostname)

	controller.SetBuildInfo(version, gitCommit, buildDate, goarch, goos)

	// Set up notification server if enabled; the Notifier interface is passed
	// to reconcilers so they can gate upgrades and send backup events.
	var notifier notification.Notifier
	if cfg.ControllerConfig.Notification.Enabled {
		notifServer := notification.NewServer(logger, cfg.ControllerConfig.Notification)
		if err := mgr.Add(notifServer); err != nil {
			setupLog.Error(err, "unable to add notification server")
			os.Exit(1)
		}
		notifier = notifServer
	}

	managedNodeReconciler := controller.NewManagedNodeReconciler(client, scheme, logger, cfg.ControllerConfig.ManagedNode, sys, locker, clientset, version, notifier)
	if err = (managedNodeReconciler).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ManagedNode")
		os.Exit(1)
	}

	cfg.ControllerConfig.ConfigSet.Namespace = cfg.ControllerConfig.Namespace
	configSetReconciler := controller.NewConfigSetReconciler(client, scheme, logger, cfg.ControllerConfig.ConfigSet, sys, locker)

	if err = (configSetReconciler).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigSet")
		os.Exit(1)
	}

	switch id {
	case system.FreeBSD:
		// Skip host-only controllers when running inside a jail unless the operator
		// has explicitly opted in via freebsd.allow-in-jail.  Jail and Poudriere
		// controllers require ZFS access and the ability to create nested jails —
		// neither is available in a standard jail.  A privileged jail with ZFS
		// dataset delegation and allow.mount.zfs can opt in to run poudriere.
		if freebsdnode.IsJailed(ctx, sys.Exec()) && !cfg.ControllerConfig.FreeBSD.AllowInJail {
			setupLog.Info("running inside a FreeBSD jail; jail and poudriere controllers disabled (set freebsd.allow-in-jail=true to enable in a privileged jail)")
			break
		}

		poudriereReconciler := freebsd.NewPoudriereReconciler(client, scheme, logger, cfg.ControllerConfig.FreeBSD.Poudriere, sys)
		if err = poudriereReconciler.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Poudriere")
			os.Exit(1)
		}

		jailReconciler, jailErr := freebsd.NewJailReconciler(ctx, client, scheme, logger, cfg.ControllerConfig.FreeBSD.Jail, sys, locker)
		if jailErr != nil {
			setupLog.Error(jailErr, "unable to create controller", "controller", "Jail")
			os.Exit(1)
		}

		if err = jailReconciler.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Jail")
			os.Exit(1)
		}
	}

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

	setupLog.Info("starting manager", "hostname", hostname, "version", version)
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
