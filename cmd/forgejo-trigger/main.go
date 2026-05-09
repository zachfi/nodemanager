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

// Command forgejo-trigger receives Forgejo push webhooks and patches
// the freebsd.nodemanager/trigger annotation on matching
// PoudriereBulk CRs so a push invalidates the controller's input-hash
// skip-if-unchanged check and a fresh build dispatches.
//
// See docs/poudriere/forgejo-trigger.md for the operator-facing
// setup guide and pkg/forgejotrigger for the HTTP handler logic.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/forgejotrigger"
)

// Build metadata.  These are package-level vars so the Go linker can
// override them at build time via -ldflags "-X main.version=…", which
// is exactly what `make docker-ci-forgejo-trigger` does.  Defaults
// here are what `go build` from a working tree produces.
var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := run(); err != nil {
		slog.Default().Error("forgejo-trigger exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr            = flag.String("addr", ":9090", "HTTP listen address.")
		namespace       = flag.String("namespace", "nodemanager", "Kubernetes namespace to scan for matching PoudriereBulks.")
		secretFile      = flag.String("secret-file", "", "Path to a file containing the Forgejo webhook HMAC secret.  Required.")
		readTimeout     = flag.Duration("read-timeout", 10*time.Second, "HTTP server read timeout.")
		writeTimeout    = flag.Duration("write-timeout", 10*time.Second, "HTTP server write timeout.")
		shutdownTimeout = flag.Duration("shutdown-timeout", 10*time.Second, "How long to wait for in-flight requests to finish on SIGTERM.")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
	logger.Info("starting forgejo-trigger",
		"addr", *addr,
		"namespace", *namespace,
		"version", version,
		"git_commit", gitCommit,
		"build_date", buildDate,
	)

	if *secretFile == "" {
		return errors.New("--secret-file is required (path to a file containing the Forgejo webhook HMAC secret)")
	}
	hmacSecret, err := os.ReadFile(*secretFile)
	if err != nil {
		return fmt.Errorf("read --secret-file: %w", err)
	}
	// Trim a trailing newline so common `echo "secret" > file`
	// usage produces the right key.  Don't trim leading whitespace —
	// that would silently change a deliberate-but-weird secret.
	if n := len(hmacSecret); n > 0 && hmacSecret[n-1] == '\n' {
		hmacSecret = hmacSecret[:n-1]
	}
	if len(hmacSecret) == 0 {
		return errors.New("--secret-file is empty after trimming the trailing newline")
	}

	// k8s client.  In-cluster (ServiceAccount) when KUBECONFIG isn't
	// set; otherwise honour --kubeconfig per controller-runtime
	// convention.  Same flag as cmd/main.go.
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("kubeconfig: %w", err)
	}

	scheme := clientgoscheme.Scheme
	if err := freebsdv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register freebsd v1 scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("k8s client: %w", err)
	}

	srv, err := forgejotrigger.New(forgejotrigger.Config{
		Namespace:  *namespace,
		HMACSecret: hmacSecret,
		Logger:     logger,
	}, c)
	if err != nil {
		return fmt.Errorf("forgejotrigger.New: %w", err)
	}

	httpServer := &http.Server{
		Addr:         *addr,
		Handler:      srv.Handler(),
		ReadTimeout:  *readTimeout,
		WriteTimeout: *writeTimeout,
	}

	// Graceful shutdown: SIGTERM/SIGINT triggers Server.Shutdown so
	// in-flight webhook handlers can finish their k8s patch before
	// the process exits.  Useful when Forgejo retries a delivery and
	// catches us mid-rolling-update.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening for webhooks", "addr", *addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining in-flight requests",
			"timeout", *shutdownTimeout)
		shutCtx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
	}

	logger.Info("forgejo-trigger stopped cleanly")
	return nil
}
