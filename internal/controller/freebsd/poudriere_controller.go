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

package freebsd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/internal/controller/limits"
	"github.com/zachfi/nodemanager/pkg/cmdrunner"
	"github.com/zachfi/nodemanager/pkg/common/labels"
	"github.com/zachfi/nodemanager/pkg/handler"
	"github.com/zachfi/nodemanager/pkg/poudriere"
)

// Default timeouts for the Command executor's dispatch and status programs.
// Operators can override per-bulk via Spec.Executor.Command.{Dispatch,Status}Timeout.
const (
	defaultDispatchTimeout = 60 * time.Second
	defaultStatusTimeout   = 30 * time.Second
	// statusPollInterval is the requeue delay between status polls when a
	// run is in flight.  Short enough that a 5-minute build feels responsive,
	// long enough that a 4-hour build doesn't generate hundreds of API calls.
	statusPollInterval = 30 * time.Second
	// statusRetryInterval is the requeue delay after a transient status
	// failure (program returned malformed output, exited non-zero, etc.)
	// — distinct from statusPollInterval so we can tune the two
	// independently if Forgejo or Woodpecker outages turn out to need
	// different patience.
	statusRetryInterval = 60 * time.Second
)

// triggerAnnotation is the metadata.annotation key that downstream
// triggers (Forgejo push webhooks, manual `kubectl annotate`) bump to
// force a rebuild.  Including its value in the input hash means an
// annotation patch alone is sufficient to invalidate the skip-if-
// unchanged check on the next reconcile.
const triggerAnnotation = "freebsd.nodemanager/trigger"

// PoudriereReconciler reconciles a Poudriere object
type PoudriereReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	tracer trace.Tracer
	logger *slog.Logger
	system handler.System
	cfg    PoudriereConfig
	// runner executes Command-mode dispatch/status programs.  Carries no
	// per-invocation state; safe to share across reconciles.
	runner *cmdrunner.Runner
}

func NewPoudriereReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg PoudriereConfig, system handler.System) *PoudriereReconciler {
	return &PoudriereReconciler{
		Client: client,
		Scheme: scheme,
		tracer: otel.Tracer("controller.freebsd.poudriere"),
		logger: logger.With("controller", "poudriere"),
		system: system,
		cfg:    cfg,
		runner: cmdrunner.New(client, logger.With("component", "cmdrunner")),
	}
}

//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=freebsd.nodemanager,resources=poudrieres/finalizers,verbs=update

// Reconcile drives a single PoudriereBulk through whichever executor
// its spec selects:
//
//   - InProcess (default): nodemanager runs `poudriere bulk` directly
//     on the controller's host.  Requires the trees and jails declared
//     by sibling CRDs to exist on this host; ensures them via
//     `poudriere ports/jail -c` before invoking bulk.
//
//   - Command: nodemanager invokes an external dispatch program
//     (typically a small bridge to a CI runner) and observes the
//     resulting run via an optional Status program.  The host running
//     the controller does NOT need a working poudriere installation —
//     the build happens wherever the bridge points.
//
// Either path applies the input-hash skip-if-unchanged optimisation:
// when Status.Hash matches the freshly computed hash AND the last
// build succeeded AND there's no in-flight Command run, the heavy
// work is skipped and the reconciler just requeues per
// Spec.ReconcilePeriod.
func (r *PoudriereReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	_ = log.FromContext(ctx)

	// Top-level span for the reconcile.  Sub-spans for executor
	// branches and bridge invocations hang off this one so an operator
	// can follow a single Bulk's reconcile end-to-end in Tempo.
	ctx, span := r.tracer.Start(ctx, "PoudriereReconciler.Reconcile",
		trace.WithAttributes(
			attribute.String("bulk.name", req.Name),
			attribute.String("bulk.namespace", req.Namespace),
		))
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.SetAttributes(
			attribute.Float64("result.requeue_after_seconds", result.RequeueAfter.Seconds()),
		)
		span.End()
	}()

	hostname, err := r.system.Node().Hostname()
	if err != nil {
		return ctrl.Result{}, err
	}
	span.SetAttributes(attribute.String("host.hostname", hostname))

	var node commonv1.ManagedNode
	if err := r.Get(ctx, types.NamespacedName{Name: hostname, Namespace: req.Namespace}, &node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Bail if this node has the poudriere label explicitly set to "disabled".
	// labels.Or returns true on the first matching key+value, which is exactly
	// "this label is set to disabled."  The previous use of labels.NoneMatch
	// was inverted: NoneMatch returns true only when the value does NOT match,
	// so the reconciler bailed on every node whose poudriere label was anything
	// other than "disabled" — meaning the reconciler effectively never ran on
	// a correctly-configured build host.
	if labels.LabelGate(labels.Or, node.Labels, map[string]string{labels.PoudriereBuild: "disabled"}) {
		span.AddEvent("skip: poudriere label disabled")
		return ctrl.Result{}, nil
	}

	// Require the poudriere label to be present at all (any non-"disabled"
	// value opts the node in).
	if !labels.LabelGate(labels.AnyKey, node.Labels, map[string]string{labels.PoudriereBuild: ""}) {
		span.AddEvent("skip: poudriere label absent")
		return ctrl.Result{}, nil
	}

	// Fetch the specific Bulk being reconciled.  If it's gone (deletion),
	// return without doing anything else — there's nothing to record
	// status against.
	var bulk freebsdv1.PoudriereBulk
	if err := r.Get(ctx, req.NamespacedName, &bulk); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	span.SetAttributes(
		attribute.String("bulk.spec.jail", bulk.Spec.Jail),
		attribute.String("bulk.spec.tree", bulk.Spec.Tree),
		attribute.Int("bulk.spec.ports", len(bulk.Spec.Ports)),
		attribute.String("bulk.spec.executor", string(r.executorType(&bulk))),
		attribute.Int64("bulk.metadata.generation", bulk.Generation),
	)

	switch r.executorType(&bulk) {
	case freebsdv1.BulkExecutorCommand:
		return r.reconcileCommand(ctx, hostname, &bulk)
	case freebsdv1.BulkExecutorInProcess:
		return r.reconcileInProcess(ctx, hostname, &bulk)
	default:
		// Unknown type defensively: kubebuilder validation should have
		// rejected the CR at admission, but if a future client somehow
		// stores an unknown value, surface it as Degraded rather than
		// silently no-op.
		etype := r.executorType(&bulk)
		err := fmt.Errorf("unsupported executor type %q", etype)
		r.recordBuildFailure(ctx, req.NamespacedName, hostname, "UnknownExecutor", err)
		return ctrl.Result{}, nil
	}
}

// executorType resolves the effective executor for a bulk, defaulting
// to InProcess when Spec.Executor is unset (backward-compat with v0.14.x
// CRs that don't know about the field).
func (r *PoudriereReconciler) executorType(bulk *freebsdv1.PoudriereBulk) freebsdv1.BulkExecutorType {
	if bulk.Spec.Executor == nil || bulk.Spec.Executor.Type == "" {
		return freebsdv1.BulkExecutorInProcess
	}
	return bulk.Spec.Executor.Type
}

// computeInputHash is the skip-if-unchanged signal.  Hashes the
// build's intent (jail, tree, sorted ports) plus any external trigger
// annotation.  Operational fields (ReconcilePeriod, Executor) are
// deliberately excluded — changing the schedule or swapping executor
// type should NOT force a rebuild of identical inputs.
func (r *PoudriereReconciler) computeInputHash(bulk *freebsdv1.PoudriereBulk) string {
	h := sha256.New()
	fmt.Fprintf(h, "jail=%s\n", bulk.Spec.Jail)
	fmt.Fprintf(h, "tree=%s\n", bulk.Spec.Tree)
	sortedPorts := append([]string(nil), bulk.Spec.Ports...)
	sort.Strings(sortedPorts)
	for _, p := range sortedPorts {
		fmt.Fprintf(h, "port=%s\n", p)
	}
	if v := bulk.Annotations[triggerAnnotation]; v != "" {
		fmt.Fprintf(h, "trigger=%s\n", v)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// reconcileInProcess is the v0.14.x build path: ensure trees and
// jails exist on this host, run portshaker + `poudriere bulk`, write
// status.  Now wrapped in skip-if-unchanged.
func (r *PoudriereReconciler) reconcileInProcess(ctx context.Context, hostname string, bulk *freebsdv1.PoudriereBulk) (result ctrl.Result, retErr error) {
	key := types.NamespacedName{Name: bulk.Name, Namespace: bulk.Namespace}

	ctx, span := r.tracer.Start(ctx, "reconcileInProcess")
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	// Skip-if-unchanged: identical inputs and last build succeeded → no work.
	newHash := r.computeInputHash(bulk)
	span.SetAttributes(attribute.String("bulk.input_hash", newHash))
	if newHash == bulk.Status.Hash && bulk.Status.LastBuildResult == "Succeeded" {
		span.AddEvent("skip: inputs unchanged")
		poudriereBulkRunsTotal.WithLabelValues(hostname, bulk.Name, "skipped").Inc()
		r.logger.Debug("skipping bulk: inputs unchanged since last successful build", "bulk", bulk.Name, "hash", newHash)
		return r.requeueAfterReconcilePeriod(bulk), nil
	}

	exec := r.system.Exec()

	p, err := poudriere.NewPorts(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureAllTrees(ctx, p); err != nil {
		r.recordBuildFailure(ctx, key, hostname, "TreesEnsureFailed", err)
		return ctrl.Result{}, err
	}

	j, err := poudriere.NewJail(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureAllJails(ctx, j); err != nil {
		r.recordBuildFailure(ctx, key, hostname, "JailsEnsureFailed", err)
		return ctrl.Result{}, err
	}

	b, err := poudriere.NewBulk(r.logger, exec)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Mark the bulk as Progressing before kicking off the build.
	_ = r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
		r.setBulkCondition(fresh, condProgressing, metav1.ConditionTrue, "Building",
			"syncing port tree and running poudriere bulk")
	})

	start := time.Now()
	buildErr := r.runBuild(ctx, b, bulk)
	duration := time.Since(start)

	resultLabel := "success"
	if buildErr != nil {
		resultLabel = "error"
	}
	poudriereBulkRunsTotal.WithLabelValues(hostname, bulk.Name, resultLabel).Inc()
	poudriereBulkDuration.WithLabelValues(hostname, bulk.Name).Observe(duration.Seconds())
	poudriereLastBulkTimestamp.WithLabelValues(hostname, bulk.Name).Set(float64(time.Now().Unix()))

	_ = r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
		now := metav1.Now()
		fresh.Status.LastBuildTime = &now
		if buildErr != nil {
			fresh.Status.LastBuildResult = "Failed"
			fresh.Status.LastError = buildErr.Error()
			r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, "BuildFailed", "build failed")
			r.setBulkCondition(fresh, condDegraded, metav1.ConditionTrue, "BuildFailed", buildErr.Error())
			r.setBulkCondition(fresh, condAvailable, metav1.ConditionFalse, "BuildFailed",
				"no usable build available")
		} else {
			fresh.Status.LastBuildResult = "Succeeded"
			fresh.Status.LastError = ""
			fresh.Status.Hash = newHash // record so future identical reconciles skip
			r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, "Built", "build completed")
			r.setBulkCondition(fresh, condDegraded, metav1.ConditionFalse, "Built", "")
			r.setBulkCondition(fresh, condAvailable, metav1.ConditionTrue, "Built",
				"build completed successfully")
		}
	})

	if buildErr != nil {
		return ctrl.Result{}, buildErr
	}
	return r.requeueAfterReconcilePeriod(bulk), nil
}

// reconcileCommand runs the Command-executor state machine:
//
//   - if a previous dispatch is in flight (LastDispatchedRunID set,
//     LastBuildResult cleared by dispatchCommand), poll the Status
//     program;
//   - else, if inputs are unchanged since last success, skip;
//   - else, dispatch a new run via the Dispatch program.
//
// The Command executor does NOT call ensureAllTrees / ensureAllJails:
// the host running this reconciler is the bridge to a build system,
// not the build host itself.  Tree/jail provisioning is the build
// system's concern.
func (r *PoudriereReconciler) reconcileCommand(ctx context.Context, hostname string, bulk *freebsdv1.PoudriereBulk) (result ctrl.Result, retErr error) {
	key := types.NamespacedName{Name: bulk.Name, Namespace: bulk.Namespace}

	ctx, span := r.tracer.Start(ctx, "reconcileCommand")
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	// Validation: Type=Command requires Spec.Executor.Command; otherwise
	// we have nothing to invoke.
	if bulk.Spec.Executor == nil || bulk.Spec.Executor.Command == nil {
		err := fmt.Errorf("executor type Command requires spec.executor.command")
		span.AddEvent("validation: spec.executor.command missing")
		r.recordBuildFailure(ctx, key, hostname, "InvalidExecutor", err)
		return ctrl.Result{}, nil
	}
	cmdSpec := bulk.Spec.Executor.Command

	// In-flight phase: a prior reconcile dispatched, but no terminal
	// status has been recorded yet.  Poll the Status program.
	if r.hasInFlightRun(bulk) {
		span.SetAttributes(
			attribute.String("phase", "poll"),
			attribute.String("bulk.run_id", bulk.Status.LastDispatchedRunID),
		)
		return r.pollCommandStatus(ctx, hostname, bulk, cmdSpec)
	}

	// Skip-if-unchanged.
	newHash := r.computeInputHash(bulk)
	span.SetAttributes(attribute.String("bulk.input_hash", newHash))
	if newHash == bulk.Status.Hash && bulk.Status.LastBuildResult == "Succeeded" {
		span.AddEvent("skip: inputs unchanged")
		poudriereBulkRunsTotal.WithLabelValues(hostname, bulk.Name, "skipped").Inc()
		r.logger.Debug("skipping dispatch: inputs unchanged since last successful run", "bulk", bulk.Name, "hash", newHash)
		return r.requeueAfterReconcilePeriod(bulk), nil
	}

	// Dispatch a new run.
	span.SetAttributes(attribute.String("phase", "dispatch"))
	return r.dispatchCommand(ctx, hostname, bulk, cmdSpec, newHash)
}

// hasInFlightRun reports whether the bulk has a pending dispatched run
// that we should poll rather than re-dispatching.  An in-flight run is
// one where LastDispatchedRunID is non-empty AND LastBuildResult has
// not yet been written by a successful poll.
func (r *PoudriereReconciler) hasInFlightRun(bulk *freebsdv1.PoudriereBulk) bool {
	return bulk.Status.LastDispatchedRunID != "" && bulk.Status.LastBuildResult == ""
}

// dispatchCommand invokes the Dispatch program with the bulk's spec on
// stdin, captures the run identifier from stdout, and writes the
// dispatched-but-not-finished state to status.  If a Status program is
// configured, the next reconcile (requeued at statusPollInterval) will
// poll it; otherwise the run is fire-and-forget and the next periodic
// reconcile dispatches another.
func (r *PoudriereReconciler) dispatchCommand(ctx context.Context, hostname string, bulk *freebsdv1.PoudriereBulk, cmdSpec *freebsdv1.CommandExecutor, newHash string) (result ctrl.Result, retErr error) {
	key := types.NamespacedName{Name: bulk.Name, Namespace: bulk.Namespace}

	ctx, span := r.tracer.Start(ctx, "dispatchCommand",
		trace.WithAttributes(
			attribute.String("dispatch.program", firstOrEmpty(cmdSpec.Dispatch)),
			attribute.Bool("dispatch.has_status_program", len(cmdSpec.Status) > 0),
		))
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	// Build the input document declared by the contract.  See
	// pkg/cmdrunner/types.go.
	input := cmdrunner.BulkDispatchInput{
		APIVersion: cmdrunner.ContractAPIVersion,
		Kind:       cmdrunner.KindBulkDispatchInput,
		Metadata: cmdrunner.BulkDispatchMetadata{
			Name:       bulk.Name,
			Namespace:  bulk.Namespace,
			UID:        string(bulk.UID),
			Generation: bulk.Generation,
		},
		Spec: cmdrunner.BulkDispatchSpec{
			Jail:  bulk.Spec.Jail,
			Tree:  bulk.Spec.Tree,
			Ports: bulk.Spec.Ports,
		},
	}
	stdin, err := json.Marshal(input)
	if err != nil {
		// json.Marshal of a hand-rolled struct shouldn't realistically
		// fail, but surface defensively.
		r.recordBuildFailure(ctx, key, hostname, "DispatchInputMarshal", err)
		return ctrl.Result{}, err
	}

	// Mark Progressing before invoking the dispatch program so a slow
	// dispatch is visible in `kubectl describe`.
	_ = r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
		r.setBulkCondition(fresh, condProgressing, metav1.ConditionTrue, "Dispatching",
			"invoking external dispatch program")
	})

	timeout := defaultDispatchTimeout
	if d, perr := parseDurationOrZero(cmdSpec.DispatchTimeout); perr == nil && d > 0 {
		timeout = d
	}

	start := time.Now()
	res, runErr := r.runner.Run(ctx, cmdrunner.RunSpec{
		Argv:      cmdSpec.Dispatch,
		Stdin:     stdin,
		Env:       cmdSpec.Env,
		SecretEnv: cmdSpec.SecretEnv,
		Namespace: bulk.Namespace,
		Timeout:   timeout,
	})
	duration := time.Since(start)
	poudriereBulkDuration.WithLabelValues(hostname, bulk.Name).Observe(duration.Seconds())

	// Infrastructural failure (timeout, secret resolution, exec error).
	if runErr != nil {
		poudriereBulkRunsTotal.WithLabelValues(hostname, bulk.Name, "error").Inc()
		r.recordBuildFailure(ctx, key, hostname, "DispatchRunError", runErr)
		return ctrl.Result{}, runErr
	}
	// Program exited non-zero — treat as build failure with stderr as
	// the diagnostic.  Truncate to keep status manageable.
	if res.ExitCode != 0 {
		poudriereBulkRunsTotal.WithLabelValues(hostname, bulk.Name, "error").Inc()
		failErr := fmt.Errorf("dispatch program exit %d: %s", res.ExitCode, truncate(string(res.Stderr), 4096))
		r.recordBuildFailure(ctx, key, hostname, "DispatchExitNonZero", failErr)
		return ctrl.Result{}, nil
	}

	runID := cmdrunner.ExtractRunID(res.Stdout)
	poudriereBulkRunsTotal.WithLabelValues(hostname, bulk.Name, "dispatched").Inc()

	_ = r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
		now := metav1.Now()
		fresh.Status.LastDispatchTime = &now
		fresh.Status.LastDispatchedRunID = runID
		fresh.Status.LastDispatchedRunURL = ""
		// LastBuildResult cleared so the next reconcile sees the run as
		// in-flight and polls (or, with no Status program, treats as
		// fire-and-forget on the next requeue).
		fresh.Status.LastBuildResult = ""
		fresh.Status.LastError = ""
		// Persist the input hash now: a subsequent reconcile that finds
		// matching inputs and a Succeeded result will skip; an in-flight
		// run is detected separately and polls regardless of hash.
		fresh.Status.Hash = newHash
		r.setBulkCondition(fresh, condProgressing, metav1.ConditionTrue, "Dispatched",
			fmt.Sprintf("dispatched run %q, awaiting completion", runID))
	})

	// If a Status program is configured, requeue soon to start polling.
	if len(cmdSpec.Status) > 0 {
		return ctrl.Result{RequeueAfter: statusPollInterval}, nil
	}

	// Fire-and-forget: no Status program.  Treat the dispatch itself as
	// success and let ReconcilePeriod drive the next dispatch.
	_ = r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
		now := metav1.Now()
		fresh.Status.LastBuildTime = &now
		fresh.Status.LastBuildResult = "Succeeded"
		fresh.Status.LastDispatchedRunID = "" // not in-flight (we won't poll)
		r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, "Dispatched",
			"dispatch succeeded; no status program configured (fire-and-forget)")
		r.setBulkCondition(fresh, condAvailable, metav1.ConditionTrue, "Dispatched",
			"dispatch succeeded")
		r.setBulkCondition(fresh, condDegraded, metav1.ConditionFalse, "Dispatched", "")
	})
	poudriereLastBulkTimestamp.WithLabelValues(hostname, bulk.Name).Set(float64(time.Now().Unix()))
	return r.requeueAfterReconcilePeriod(bulk), nil
}

// pollCommandStatus invokes the Status program with the in-flight run
// ID on stdin, parses the BulkRunStatus reply, and writes terminal
// state to the bulk's Status when the run finishes.  Transient
// failures (program exit non-zero, malformed output) are logged and
// retried; they don't blow up the reconcile.
func (r *PoudriereReconciler) pollCommandStatus(ctx context.Context, hostname string, bulk *freebsdv1.PoudriereBulk, cmdSpec *freebsdv1.CommandExecutor) (result ctrl.Result, retErr error) {
	key := types.NamespacedName{Name: bulk.Name, Namespace: bulk.Namespace}
	runID := bulk.Status.LastDispatchedRunID

	ctx, span := r.tracer.Start(ctx, "pollCommandStatus",
		trace.WithAttributes(
			attribute.String("status.program", firstOrEmpty(cmdSpec.Status)),
			attribute.String("bulk.run_id", runID),
		))
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	if len(cmdSpec.Status) == 0 {
		// In-flight ID set but no Status program — operator likely
		// removed Status.  Clear the in-flight marker so future
		// reconciles dispatch fresh.
		_ = r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
			fresh.Status.LastDispatchedRunID = ""
		})
		return r.requeueAfterReconcilePeriod(bulk), nil
	}

	timeout := defaultStatusTimeout
	if d, perr := parseDurationOrZero(cmdSpec.StatusTimeout); perr == nil && d > 0 {
		timeout = d
	}

	res, runErr := r.runner.Run(ctx, cmdrunner.RunSpec{
		Argv:      cmdSpec.Status,
		Stdin:     []byte(runID + "\n"),
		Env:       cmdSpec.Env,
		SecretEnv: cmdSpec.SecretEnv,
		Namespace: bulk.Namespace,
		Timeout:   timeout,
	})
	if runErr != nil {
		r.logger.Warn("status program errored, will retry", "bulk", bulk.Name, "err", runErr)
		return ctrl.Result{RequeueAfter: statusRetryInterval}, nil
	}
	if res.ExitCode != 0 {
		r.logger.Warn("status program exited non-zero, will retry",
			"bulk", bulk.Name, "exit", res.ExitCode,
			"stderr", truncate(string(res.Stderr), 512))
		return ctrl.Result{RequeueAfter: statusRetryInterval}, nil
	}

	status, parseErr := cmdrunner.ParseRunStatus(res.Stdout)
	if parseErr != nil {
		r.logger.Warn("status program produced malformed output, will retry",
			"bulk", bulk.Name, "err", parseErr)
		return ctrl.Result{RequeueAfter: statusRetryInterval}, nil
	}
	span.SetAttributes(
		attribute.String("status.state", string(status.State)),
		attribute.Bool("status.terminal", status.State.IsTerminal()),
	)

	if !status.State.IsTerminal() {
		// Still running — opportunistically update URL when the
		// executor surfaces it (Forgejo's run page link, etc.).
		if status.URL != "" && status.URL != bulk.Status.LastDispatchedRunURL {
			_ = r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
				fresh.Status.LastDispatchedRunURL = status.URL
			})
		}
		return ctrl.Result{RequeueAfter: statusPollInterval}, nil
	}

	// Terminal: success or failed.  Promote the dispatched run to a
	// completed build.
	resultLabel := "success"
	if status.State == cmdrunner.BulkRunStateFailed {
		resultLabel = "error"
	}
	poudriereBulkRunsTotal.WithLabelValues(hostname, bulk.Name, resultLabel).Inc()
	poudriereLastBulkTimestamp.WithLabelValues(hostname, bulk.Name).Set(float64(time.Now().Unix()))

	_ = r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
		now := metav1.Now()
		fresh.Status.LastBuildTime = &now
		fresh.Status.LastDispatchedRunURL = status.URL
		// Clear the in-flight marker — we have a terminal result now.
		fresh.Status.LastDispatchedRunID = ""
		switch status.State {
		case cmdrunner.BulkRunStateSuccess:
			fresh.Status.LastBuildResult = "Succeeded"
			fresh.Status.LastError = ""
			r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, "Built", "build completed")
			r.setBulkCondition(fresh, condDegraded, metav1.ConditionFalse, "Built", "")
			r.setBulkCondition(fresh, condAvailable, metav1.ConditionTrue, "Built",
				"build completed successfully")
		default: // failed (unknown is non-terminal so unreachable here)
			fresh.Status.LastBuildResult = "Failed"
			fresh.Status.LastError = status.Error
			r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, "BuildFailed", "build failed")
			r.setBulkCondition(fresh, condDegraded, metav1.ConditionTrue, "BuildFailed", status.Error)
			r.setBulkCondition(fresh, condAvailable, metav1.ConditionFalse, "BuildFailed",
				"no usable build available")
		}
	})

	return r.requeueAfterReconcilePeriod(bulk), nil
}

// requeueAfterReconcilePeriod returns ctrl.Result with a RequeueAfter
// derived from Spec.ReconcilePeriod.  Empty/zero/invalid period means
// no requeue (event-driven only).
func (r *PoudriereReconciler) requeueAfterReconcilePeriod(bulk *freebsdv1.PoudriereBulk) ctrl.Result {
	if bulk.Spec.ReconcilePeriod == "" {
		return ctrl.Result{}
	}
	period, err := time.ParseDuration(bulk.Spec.ReconcilePeriod)
	if err != nil {
		r.logger.Warn("invalid reconcilePeriod, ignoring",
			"bulk", bulk.Name, "value", bulk.Spec.ReconcilePeriod, "err", err)
		return ctrl.Result{}
	}
	if period <= 0 {
		return ctrl.Result{}
	}
	return ctrl.Result{RequeueAfter: period}
}

// parseDurationOrZero parses s as a duration; empty or invalid returns
// 0 (caller substitutes its default).
func parseDurationOrZero(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

// firstOrEmpty returns argv[0] or "" — used in span attributes to
// surface "which program got invoked" without dragging the full argv
// (which can include user-controlled values) into the span.
func firstOrEmpty(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[0]
}

// truncate returns s clipped to at most max bytes, with an ellipsis
// suffix when clipping happened.  Used to bound stderr/error strings
// stored on a CR's status — Status fields written to the API server
// shouldn't grow without bound.
func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	const ellipsis = "…(truncated)"
	if maxBytes <= len(ellipsis) {
		return s[:maxBytes]
	}
	return s[:maxBytes-len(ellipsis)] + ellipsis
}

// runBuild executes portshaker sync followed by `poudriere bulk` for the
// single PoudriereBulk being reconciled.  Errors are wrapped with the failing
// stage so status messages identify which step broke.
func (r *PoudriereReconciler) runBuild(ctx context.Context, b *poudriere.PoudriereBulk, bulk *freebsdv1.PoudriereBulk) error {
	if err := b.Sync(ctx); err != nil {
		return fmt.Errorf("portshaker sync failed: %w", err)
	}
	if err := b.Build(ctx, bulk.Spec.Jail, bulk.Spec.Tree, bulk.Spec.Ports); err != nil {
		return fmt.Errorf("poudriere bulk failed: %w", err)
	}
	return nil
}

// setBulkCondition upserts a named condition on the bulk's status.
func (r *PoudriereReconciler) setBulkCondition(b *freebsdv1.PoudriereBulk, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&b.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: b.Generation,
	})
}

// updateBulkStatus re-fetches the bulk and applies the mutation under
// RetryOnConflict so concurrent status writes from rapid reconciles don't
// produce "object has been modified" errors.
func (r *PoudriereReconciler) updateBulkStatus(ctx context.Context, key types.NamespacedName, mutate func(*freebsdv1.PoudriereBulk)) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var fresh freebsdv1.PoudriereBulk
		if err := r.Get(ctx, key, &fresh); err != nil {
			return err
		}
		mutate(&fresh)
		return r.Status().Update(ctx, &fresh)
	})
}

// recordBuildFailure writes a Degraded condition + LastError when an
// ensure-step fails before the bulk run can start.  It is best-effort: we
// already have a real error from the failing step, so a second error from
// the status write is logged and dropped.
func (r *PoudriereReconciler) recordBuildFailure(ctx context.Context, key types.NamespacedName, hostname, reason string, err error) {
	poudriereBulkRunsTotal.WithLabelValues(hostname, key.Name, "error").Inc()
	if uerr := r.updateBulkStatus(ctx, key, func(fresh *freebsdv1.PoudriereBulk) {
		now := metav1.Now()
		fresh.Status.LastBuildTime = &now
		fresh.Status.LastBuildResult = "Failed"
		fresh.Status.LastError = err.Error()
		r.setBulkCondition(fresh, condDegraded, metav1.ConditionTrue, reason, err.Error())
		r.setBulkCondition(fresh, condProgressing, metav1.ConditionFalse, reason, "build prerequisites failed")
	}); uerr != nil {
		r.logger.Error("failed to record build failure status", "bulk", key.Name, "err", uerr)
	}
}

// SetupWithManager sets up the controller with the Manager.  Uses the
// LongRunning profile because a successful reconcile can run portshaker
// plus `poudriere bulk`, which routinely takes minutes to hours; the
// 30-second token bucket prevents a status-update loop or a flapping
// resource from kicking off heavy builds back-to-back.
func (r *PoudriereReconciler) SetupWithManager(mgr ctrl.Manager) error {
	opts := controller.Options{}
	limits.LongRunning.ApplyTo(&opts)
	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.PoudriereBulk{}).
		Named("Poudriere").
		Watches(&freebsdv1.PoudriereJail{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.bulksOnChange)).
		Watches(&freebsdv1.PoudrierePorts{}, ctrlhandler.EnqueueRequestsFromMapFunc(r.bulksOnChange)).
		WithOptions(opts).
		Complete(r)
}

// bulksOnChange enqueues all PoudriereBulk objects in the same namespace as
// the changed object. This ensures that creating or updating a PoudriereJail
// or PoudrierePorts triggers the reconciler, which processes all of them.
func (r *PoudriereReconciler) bulksOnChange(ctx context.Context, obj client.Object) []reconcile.Request {
	var list freebsdv1.PoudriereBulkList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, len(list.Items))
	for i, b := range list.Items {
		reqs[i] = reconcile.Request{NamespacedName: types.NamespacedName{Name: b.Name, Namespace: b.Namespace}}
	}
	return reqs
}

func (r *PoudriereReconciler) ensureAllTrees(ctx context.Context, p *poudriere.PoudrierePorts) error {
	trees := &freebsdv1.PoudrierePortsList{}
	err := r.List(ctx, trees)
	if err != nil {
		return err
	}

	existing, err := p.List(ctx)
	if err != nil {
		return err
	}

	for _, t := range trees.Items {
		if v, ok := exists(t.Name, existing); ok {
			err = p.Update(ctx, *v)
			if err != nil {
				return err
			}
		} else {
			err = p.Create(ctx, poudriere.PortsTree{
				Name:        t.Name,
				FetchMethod: t.Spec.FetchMethod,
				Branch:      t.Spec.Branch,
			})
			if err != nil {
				return err
			}
		}

		for _, e := range existing {
			if e.Name == t.Name {
				err = p.Update(ctx, *e)
				if err != nil {
					return err
				}
				continue
			}
		}
	}

	return nil
}

func (r *PoudriereReconciler) ensureAllJails(ctx context.Context, p *poudriere.PoudriereJail) error {
	jails := &freebsdv1.PoudriereJailList{}
	err := r.List(ctx, jails)
	if err != nil {
		return err
	}

	existing, err := p.List(ctx)
	if err != nil {
		return err
	}

	for _, t := range jails.Items {
		if v, ok := exists(t.Name, existing); ok {
			err = p.Update(ctx, *v)
			if err != nil {
				return err
			}
		} else {
			err = p.Create(ctx, poudriere.BuildJail{
				Name:        t.Name,
				FetchMethod: "http",
				Version:     t.Spec.Version,
			})
			if err != nil {
				return err
			}
		}

		for _, e := range existing {
			if e.Name == t.Name {
				err = p.Update(ctx, *e)
				if err != nil {
					return err
				}
				continue
			}
		}
	}

	return nil
}

type nameable interface {
	GetName() string
}

func exists[N nameable](s string, l []N) (N, bool) {
	for _, p := range l {
		if p.GetName() == s {
			return p, true
		}
	}

	x := new(N)
	return *x, false
}
