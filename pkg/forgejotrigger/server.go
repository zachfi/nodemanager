package forgejotrigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

// MaxBodyBytes caps the webhook request body.  Forgejo push payloads
// are typically a few KB; a 1 MB cap is generous for huge commit
// messages while preventing a flood from exhausting memory.
const MaxBodyBytes = 1 << 20

// signatureHeader is Forgejo's HMAC header name.  The value is the
// hex-encoded SHA-256 HMAC of the request body keyed by the
// configured webhook secret.
const signatureHeader = "X-Gitea-Signature"

// eventHeader carries the event name on a Forgejo webhook delivery.
// Push events are processed; "ping" responds 200; everything else
// is acknowledged with 202 and ignored.
const eventHeader = "X-Gitea-Event"

// deliveryHeader is the per-delivery UUID Forgejo sends.  Logged for
// operator correlation; not used for deduplication.
const deliveryHeader = "X-Gitea-Delivery"

// Config bundles the runtime configuration of a Server.
type Config struct {
	// Namespace scopes which PoudriereBulks the webhook will mutate.
	// Required.
	Namespace string

	// HMACSecret is the shared secret Forgejo signs each delivery
	// with.  Read once at startup; rotation requires a process
	// restart.  Required.
	HMACSecret []byte

	// Logger receives structured log lines per delivery.  Defaults
	// to slog.Default() when nil.
	Logger *slog.Logger
}

// Server is the HTTP handler for Forgejo push webhooks.  It listens
// at /webhook for delivery events, verifies HMAC signatures, and
// patches the freebsd.nodemanager/trigger annotation on each
// PoudriereBulk in cfg.Namespace whose forgejo.nodemanager/repo
// annotation matches the pushed repository.
//
// One Server instance handles all incoming traffic; it carries no
// per-request state.
type Server struct {
	cfg    Config
	client client.Client
	tracer trace.Tracer
}

// New constructs a Server.  Returns an error if cfg is invalid.
func New(cfg Config, c client.Client) (*Server, error) {
	if cfg.Namespace == "" {
		return nil, errors.New("forgejotrigger: Namespace is required")
	}
	if len(cfg.HMACSecret) == 0 {
		return nil, errors.New("forgejotrigger: HMACSecret is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{
		cfg:    cfg,
		client: c,
		tracer: otel.Tracer("pkg/forgejotrigger"),
	}, nil
}

// Handler returns an http.Handler routing the webhook + health
// endpoints.  Use it directly with http.ListenAndServe or wire it
// into a larger mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/healthz", s.handleHealth)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleWebhook processes one Forgejo webhook delivery.
//
// Response codes:
//
//	200 OK            — push handled, body is `{"matched":N,"sha":"…"}`
//	200 OK            — ping event acknowledged
//	202 Accepted      — non-push event ignored
//	400 Bad Request   — body unreadable or malformed JSON
//	401 Unauthorized  — missing or invalid HMAC signature
//	405 Method Not Allowed — non-POST
//	500 Internal Server Error — k8s client error during list/patch
//
// The 202 path is intentional: Forgejo retries on 4xx/5xx but accepts
// 2xx as "delivered".  We don't want unrelated event noise causing
// retry storms.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.tracer.Start(r.Context(), "forgejotrigger.handleWebhook",
		trace.WithAttributes(
			attribute.String("forgejo.event", r.Header.Get(eventHeader)),
			attribute.String("forgejo.delivery", r.Header.Get(deliveryHeader)),
		))
	defer span.End()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		span.SetStatus(codes.Error, "method not allowed")
		return
	}

	// Read body with a hard cap so a malicious sender can't OOM us.
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}

	// Verify HMAC FIRST — before parsing or routing — so a malformed
	// or unsigned request gets a 401 without us doing any other work.
	if !s.verifyHMAC(r.Header.Get(signatureHeader), body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		s.cfg.Logger.Warn("rejected webhook: invalid signature",
			"delivery", r.Header.Get(deliveryHeader),
			"event", r.Header.Get(eventHeader))
		span.SetStatus(codes.Error, "invalid signature")
		return
	}

	event := r.Header.Get(eventHeader)
	span.SetAttributes(attribute.String("forgejo.event_normalised", event))

	switch event {
	case "ping":
		// Forgejo sends a ping when the webhook is created.  Just
		// acknowledge it.
		w.WriteHeader(http.StatusOK)
		s.cfg.Logger.Info("acknowledged ping",
			"delivery", r.Header.Get(deliveryHeader))
		return
	case "push":
		// Handled below.
	default:
		// Unknown event: 202 Accepted, no body.  Don't 4xx, that
		// would make Forgejo retry; 5xx would too.  202 says "got
		// it, intentionally ignored."
		w.WriteHeader(http.StatusAccepted)
		s.cfg.Logger.Debug("ignored non-push event",
			"event", event,
			"delivery", r.Header.Get(deliveryHeader))
		return
	}

	var p PushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "parse payload: "+err.Error(), http.StatusBadRequest)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}

	if p.After == "" {
		// Branch deletion: After is all zeros or empty depending on
		// the push.  Nothing to bump to.
		w.WriteHeader(http.StatusAccepted)
		s.cfg.Logger.Debug("ignored push with empty after-sha (likely branch delete)",
			"ref", p.Ref,
			"repo", p.Repository.FullName)
		return
	}
	if p.Repository.FullName == "" {
		http.Error(w, "payload missing repository.full_name", http.StatusBadRequest)
		span.SetStatus(codes.Error, "missing repository.full_name")
		return
	}
	span.SetAttributes(
		attribute.String("forgejo.repo", p.Repository.FullName),
		attribute.String("forgejo.ref", p.Ref),
		attribute.String("forgejo.sha", p.After),
	)

	matched, err := s.bumpMatchingBulks(ctx, p)
	if err != nil {
		http.Error(w, "list/patch bulks: "+err.Error(), http.StatusInternalServerError)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}
	span.SetAttributes(attribute.Int("forgejo.matched_bulks", matched))

	s.cfg.Logger.Info("processed push",
		"repo", p.Repository.FullName,
		"ref", p.Ref,
		"sha", p.After,
		"matched", matched,
		"delivery", r.Header.Get(deliveryHeader))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"matched": matched,
		"sha":     p.After,
		"repo":    p.Repository.FullName,
	})
}

// verifyHMAC is constant-time HMAC-SHA-256 comparison of the
// hex-encoded sigHeader against the local computation over body.
// Returns false on missing, malformed, or non-matching signatures.
func (s *Server) verifyHMAC(sigHeader string, body []byte) bool {
	if sigHeader == "" {
		return false
	}
	// Forgejo's signature is bare hex (no "sha256=" prefix as GitHub uses);
	// be tolerant of a "sha256=" prefix in case a future Forgejo release
	// adopts the GitHub form.
	sigHeader = strings.TrimPrefix(sigHeader, "sha256=")
	sig, err := hex.DecodeString(sigHeader)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, s.cfg.HMACSecret)
	mac.Write(body)
	expected := mac.Sum(nil)

	return hmac.Equal(sig, expected)
}

// bumpMatchingBulks lists every PoudriereBulk in the configured
// namespace, finds the ones whose forgejo.nodemanager/repo annotation
// equals the push's repository.full_name, and patches their
// freebsd.nodemanager/trigger annotation with the after-sha.
//
// Returns the count of bulks patched (0 is fine — no match isn't
// an error).  Any patch error short-circuits and is returned to the
// caller, which surfaces as a 500 to Forgejo so it retries.
func (s *Server) bumpMatchingBulks(ctx context.Context, p PushPayload) (int, error) {
	ctx, span := s.tracer.Start(ctx, "forgejotrigger.bumpMatchingBulks",
		trace.WithAttributes(
			attribute.String("forgejo.repo", p.Repository.FullName),
			attribute.String("forgejo.sha", p.After),
		))
	defer span.End()

	var bulks freebsdv1.PoudriereBulkList
	if err := s.client.List(ctx, &bulks, client.InNamespace(s.cfg.Namespace)); err != nil {
		return 0, fmt.Errorf("list poudrierebulks: %w", err)
	}

	matched := 0
	for i := range bulks.Items {
		b := &bulks.Items[i]
		if b.Annotations[freebsdv1.ForgejoRepoAnnotation] != p.Repository.FullName {
			continue
		}
		if err := s.bumpTrigger(ctx, b, p.After); err != nil {
			return matched, fmt.Errorf("patch %s/%s: %w", b.Namespace, b.Name, err)
		}
		matched++
	}
	return matched, nil
}

// bumpTrigger sets the trigger annotation on a single bulk to sha.
// Idempotent: re-patching with the same value is a no-op write at
// the API server (no resourceVersion change, no watch event), so a
// duplicate webhook delivery for the same SHA does not double-trigger.
func (s *Server) bumpTrigger(ctx context.Context, bulk *freebsdv1.PoudriereBulk, sha string) error {
	patch := fmt.Appendf(nil,
		`{"metadata":{"annotations":{%q:%q}}}`,
		freebsdv1.TriggerAnnotation,
		sha,
	)
	return s.client.Patch(ctx, bulk, client.RawPatch(types.MergePatchType, patch))
}
