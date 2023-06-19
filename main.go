/*
Copyright 2022.

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
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.opentelemetry.io/otel"

	commonv1 "github.com/zachfi/nodemanager/apis/common/v1"
	commoncontrollers "github.com/zachfi/nodemanager/controllers/common"

	freebsdv1 "github.com/zachfi/nodemanager/apis/freebsd/v1"
	freebsdcontrollers "github.com/zachfi/nodemanager/controllers/freebsd"

	//+kubebuilder:scaffold:imports

	"github.com/go-kit/log"
	"github.com/zachfi/zkit/pkg/tracing"
)

// var needs to be used instead of const as ldflags is used to fill this
// information in the release process
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
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var otelEndpoint string
	var orgID string
	var namespace string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&otelEndpoint, "otel-endpoint", "", "The URL to use when sending traces")
	flag.StringVar(&orgID, "org-id", "", "The X-Scope-OrgID header to set when sending traces")
	flag.StringVar(&namespace, "namespace", "nodemanager", "The namespace to operate within")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "39b18fec.nodemanager",
		Namespace:              namespace,
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

	if err = (&commoncontrollers.ConfigSetReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Tracer: otel.Tracer("ConfigSet"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigSet")
		os.Exit(1)
	}
	if err = (&commoncontrollers.ManagedNodeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Tracer: otel.Tracer("ManagedNode"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ManagedNode")
		os.Exit(1)
	}
	if err = (&freebsdcontrollers.PoudriereReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Poudriere")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if otelEndpoint != "" {
		shutdownTracer, err := tracing.InstallOpenTelemetryTracer(
			&tracing.Config{
				OtelEndpoint: otelEndpoint,
				OrgID:        orgID,
			},
			log.NewLogfmtLogger(os.Stderr),
			"nodemanager",
			versionString(),
		)
		if err != nil {
			setupLog.Error(err, "error initializing tracer")
			os.Exit(1)
		}
		defer shutdownTracer()
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
