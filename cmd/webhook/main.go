package main

import (
	"flag"
	"log/slog"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/internal/webhook"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(commonv1.AddToScheme(scheme))
	utilruntime.Must(freebsdv1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr string
		probeAddr   string
		certDir     string
		port        int
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "address the metrics endpoint binds to")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "address the health probe endpoint binds to")
	flag.StringVar(&certDir, "cert-dir", "", "directory containing tls.crt and tls.key (default: controller-runtime default)")
	flag.IntVar(&port, "port", 9443, "webhook server port")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrllog.SetLogger(ctrl.Log)

	webhookOpts := ctrlwebhook.Options{Port: port}
	if certDir != "" {
		webhookOpts.CertDir = certDir
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		WebhookServer:          ctrlwebhook.NewServer(webhookOpts),
		HealthProbeBindAddress: probeAddr,
	})
	if err != nil {
		logger.Error("unable to create manager", "err", err)
		os.Exit(1)
	}

	decoder := admission.NewDecoder(scheme)
	mgr.GetWebhookServer().Register(
		"/validate-nodemanager",
		&ctrlwebhook.Admission{Handler: webhook.NewNodeValidator(decoder)},
	)

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error("unable to set up health check", "err", err)
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error("unable to set up ready check", "err", err)
		os.Exit(1)
	}

	logger.Info("starting webhook server", "port", port)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error("webhook server exited", "err", err)
		os.Exit(1)
	}
}
