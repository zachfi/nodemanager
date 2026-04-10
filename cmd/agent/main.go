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
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	notificationv1 "github.com/zachfi/nodemanager/pkg/notification/v1"
)

var (
	version   = "dev"
	gitCommit = "$Format:%H$"
	buildDate = "1970-01-01T00:00:00Z"
	goos      = "unknown"
	goarch    = "unknown"
)

func main() {
	var (
		socketPath string
		user       string
		logLevel   string
		showVer    bool
	)

	flag.StringVar(&socketPath, "socket-path", "/run/nodemanager/notify.sock", "Unix domain socket to connect to")
	flag.StringVar(&user, "user", os.Getenv("USER"), "User name to identify this agent")
	flag.StringVar(&logLevel, "log-level", "INFO", "Log level (DEBUG, INFO, WARN, ERROR)")
	flag.BoolVar(&showVer, "version", false, "Print version and exit")
	flag.Parse()

	if showVer {
		fmt.Printf("nodemanager-agent %s (%s) built %s %s/%s\n", version, gitCommit, buildDate, goos, goarch)
		os.Exit(0)
	}

	level := new(slog.Level)
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		fmt.Fprintf(os.Stderr, "invalid log level %q: %v\n", logLevel, err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: *level}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, logger, socketPath, user); err != nil {
		logger.Error("agent exited with error", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, socketPath, user string) error {
	logger.Info("connecting to notification server", "socket", socketPath)

	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socketPath, err)
	}
	defer conn.Close()

	client := notificationv1.NewNodeNotificationServiceClient(conn)
	sessionID := uuid.NewString()

	stream, err := client.Subscribe(ctx, &notificationv1.SubscribeRequest{
		User:      user,
		SessionId: sessionID,
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	logger.Info("subscribed", "user", user, "session", sessionID)

	for {
		event, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				logger.Info("shutting down")
				return nil
			}
			return fmt.Errorf("recv: %w", err)
		}

		logEvent(logger, event)
	}
}

func logEvent(logger *slog.Logger, event *notificationv1.Event) {
	base := logger.With("event_id", event.GetId(), "timestamp", event.GetTimestamp().AsTime())

	switch p := event.GetPayload().(type) {
	case *notificationv1.Event_BackupStarted:
		base.Info("backup started", "configset", p.BackupStarted.GetConfigset(), "files", p.BackupStarted.GetFiles())
	case *notificationv1.Event_BackupCompleted:
		base.Info("backup completed", "configset", p.BackupCompleted.GetConfigset(), "results", len(p.BackupCompleted.GetResults()))
	case *notificationv1.Event_UpgradeApprovalRequest:
		base.Info("upgrade approval requested",
			"description", p.UpgradeApprovalRequest.GetDescription(),
			"deadline", p.UpgradeApprovalRequest.GetDeadline().AsTime(),
			"default_action", p.UpgradeApprovalRequest.GetDefaultAction(),
		)
	case *notificationv1.Event_UpgradeStarted:
		base.Info("upgrade started", "description", p.UpgradeStarted.GetDescription())
	case *notificationv1.Event_UpgradeCompleted:
		base.Info("upgrade completed",
			"success", p.UpgradeCompleted.GetSuccess(),
			"reboot_pending", p.UpgradeCompleted.GetRebootPending(),
		)
	default:
		base.Warn("unknown event type")
	}
}
