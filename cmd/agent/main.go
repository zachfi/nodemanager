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

// runSubscribe is the default mode: connect to the daemon, stream events,
// and relay them as desktop notifications via D-Bus.
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

	logger.Info("starting agent", "version", version)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	runTray(ctx, cancel, logger, subscribeConfig{
		socketPath: socketPath,
		user:       user,
	})
}

func subscribe(ctx context.Context, logger *slog.Logger, socketPath, user string, onStatus func(bool), onActivity func()) error {
	// Connect to D-Bus for desktop notifications.
	desk, err := newDesktop(logger)
	if err != nil {
		return fmt.Errorf("desktop notifications: %w", err)
	}
	defer desk.close()
	logger.Info("connected to session D-Bus")

	// Connect to the nodemanager daemon.
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

	if onStatus != nil {
		onStatus(true)
	}

	for {
		event, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				logger.Info("shutting down")
				return nil
			}
			return fmt.Errorf("recv: %w", err)
		}

		if onActivity != nil {
			onActivity()
		}
		handleEvent(ctx, logger, desk, client, user, event)
	}
}

func handleEvent(ctx context.Context, logger *slog.Logger, desk desktopNotifier, client notificationv1.NodeNotificationServiceClient, user string, event *notificationv1.Event) {
	switch p := event.GetPayload().(type) {
	case *notificationv1.Event_Notification:
		n := p.Notification
		icon := severityIcon(n.GetSeverity())
		if _, err := desk.notify(n.GetTitle(), n.GetBody(), icon); err != nil {
			logger.Error("failed to show notification", "err", err)
		}
		logger.Info("notification shown", "title", n.GetTitle(), "severity", n.GetSeverity())

	case *notificationv1.Event_UpgradeApprovalRequest:
		req := p.UpgradeApprovalRequest
		handleUpgradeApproval(ctx, logger, desk, client, user, event.GetId(), req)

	case *notificationv1.Event_UpgradeStarted:
		if _, err := desk.notify("Upgrade Started", p.UpgradeStarted.GetDescription(), "system-software-update"); err != nil {
			logger.Error("failed to show upgrade started notification", "err", err)
		}
		logger.Info("upgrade started", "description", p.UpgradeStarted.GetDescription())

	case *notificationv1.Event_UpgradeCompleted:
		summary := "Upgrade Completed"
		body := "System upgrade finished successfully."
		icon := "emblem-default"
		if !p.UpgradeCompleted.GetSuccess() {
			summary = "Upgrade Failed"
			body = p.UpgradeCompleted.GetError()
			icon = "dialog-error"
		} else if p.UpgradeCompleted.GetRebootPending() {
			body = "System upgrade finished. A reboot is pending."
			icon = "system-reboot"
		}
		if _, err := desk.notify(summary, body, icon); err != nil {
			logger.Error("failed to show upgrade completed notification", "err", err)
		}
		logger.Info("upgrade completed",
			"success", p.UpgradeCompleted.GetSuccess(),
			"reboot_pending", p.UpgradeCompleted.GetRebootPending(),
		)

	default:
		logger.Warn("unknown event type", "event_id", event.GetId())
	}
}

func handleUpgradeApproval(ctx context.Context, logger *slog.Logger, desk desktopNotifier, client notificationv1.NodeNotificationServiceClient, user, eventID string, req *notificationv1.UpgradeApprovalRequest) {
	deadline := req.GetDeadline().AsTime()
	remaining := time.Until(deadline)
	body := fmt.Sprintf("%s\nAuto-approves in %s", req.GetDescription(), remaining.Round(time.Second))

	logger.Info("showing upgrade approval request",
		"event", eventID,
		"description", req.GetDescription(),
		"deadline", deadline,
	)

	// Show notification with Approve/Deny actions.
	_, err := desk.notifyWithActions(
		"Upgrade Approval Required",
		body,
		"system-software-update",
		[]string{"approve", "Approve", "deny", "Deny"},
		int32(remaining.Milliseconds()),
		func(actionKey string) {
			var action notificationv1.ApprovalAction
			switch actionKey {
			case "approve":
				action = notificationv1.ApprovalAction_APPROVAL_ACTION_APPROVE
				logger.Info("user approved upgrade", "event", eventID)
			case "deny":
				action = notificationv1.ApprovalAction_APPROVAL_ACTION_DENY
				logger.Info("user denied upgrade", "event", eventID)
			default:
				logger.Warn("unexpected action key", "key", actionKey, "event", eventID)
				return
			}

			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			ack, sendErr := client.RespondToApproval(rctx, &notificationv1.ApprovalResponse{
				EventId: eventID,
				Action:  action,
				User:    user,
			})
			if sendErr != nil {
				logger.Error("failed to send approval response", "err", sendErr, "event", eventID)
				return
			}
			if !ack.GetAccepted() {
				logger.Warn("approval response not accepted", "reason", ack.GetReason(), "event", eventID)
			}
		},
	)
	if err != nil {
		logger.Error("failed to show upgrade approval notification", "err", err)
	}
}

func severityIcon(sev notificationv1.Severity) string {
	switch sev {
	case notificationv1.Severity_SEVERITY_WARNING:
		return "dialog-warning"
	case notificationv1.Severity_SEVERITY_ERROR:
		return "dialog-error"
	default:
		return "dialog-information"
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
