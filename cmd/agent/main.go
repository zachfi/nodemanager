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
	"time"

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

const defaultSocketPath = "/run/nodemanager/notify.sock"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "notify":
			runNotify(os.Args[2:])
			return
		case "version":
			fmt.Printf("nodemanager-agent %s (%s) built %s %s/%s\n", version, gitCommit, buildDate, goos, goarch)
			return
		}
	}

	runSubscribe()
}

// runSubscribe is the default mode: connect to the daemon and stream events.
func runSubscribe() {
	var (
		socketPath string
		user       string
		logLevel   string
		showVer    bool
	)

	flag.StringVar(&socketPath, "socket-path", defaultSocketPath, "Unix domain socket to connect to")
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

	if err := subscribe(ctx, logger, socketPath, user); err != nil {
		logger.Error("agent exited with error", "err", err)
		os.Exit(1)
	}
}

func subscribe(ctx context.Context, logger *slog.Logger, socketPath, user string) error {
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

// runNotify sends a one-shot generic notification through the daemon to all
// connected agents. Intended for use in scripts:
//
//	nodemanager-agent notify --title "Backup" --body "Completed successfully"
func runNotify(args []string) {
	fs := flag.NewFlagSet("notify", flag.ExitOnError)

	var (
		socketPath string
		title      string
		body       string
		severity   string
	)

	fs.StringVar(&socketPath, "socket-path", defaultSocketPath, "Unix domain socket to connect to")
	fs.StringVar(&title, "title", "", "Notification title (required)")
	fs.StringVar(&body, "body", "", "Notification body")
	fs.StringVar(&severity, "severity", "info", "Severity: info, warning, error")
	fs.Parse(args)

	if title == "" {
		fmt.Fprintln(os.Stderr, "error: --title is required")
		fs.Usage()
		os.Exit(1)
	}

	var sev notificationv1.Severity
	switch severity {
	case "info":
		sev = notificationv1.Severity_SEVERITY_INFO
	case "warning":
		sev = notificationv1.Severity_SEVERITY_WARNING
	case "error":
		sev = notificationv1.Severity_SEVERITY_ERROR
	default:
		fmt.Fprintf(os.Stderr, "error: unknown severity %q (use info, warning, error)\n", severity)
		os.Exit(1)
	}

	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: dial %s: %v\n", socketPath, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := notificationv1.NewNodeNotificationServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ack, err := client.SendNotification(ctx, &notificationv1.Notification{
		Title:    title,
		Body:     body,
		Severity: sev,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: send notification: %v\n", err)
		os.Exit(1)
	}

	if !ack.GetAccepted() {
		fmt.Fprintln(os.Stderr, "notification was not accepted")
		os.Exit(1)
	}
}

func logEvent(logger *slog.Logger, event *notificationv1.Event) {
	base := logger.With("event_id", event.GetId(), "timestamp", event.GetTimestamp().AsTime())

	switch p := event.GetPayload().(type) {
	case *notificationv1.Event_Notification:
		base.Info("notification",
			"title", p.Notification.GetTitle(),
			"body", p.Notification.GetBody(),
			"severity", p.Notification.GetSeverity(),
		)
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
