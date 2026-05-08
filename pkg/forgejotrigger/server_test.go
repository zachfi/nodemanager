package forgejotrigger

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
)

// testSecret is the HMAC secret used in tests.  Real deployments
// generate random secrets; this fixture is fine for the test path.
var testSecret = []byte("test-secret-hunter2")

// signedRequest builds a signed POST to /webhook for the given event +
// body.  Returns the request and the hex-encoded signature so tests
// can mutate either to exercise rejection paths.
func signedRequest(t *testing.T, event string, body []byte) *http.Request {
	t.Helper()
	mac := hmac.New(sha256.New, testSecret)
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gitea-Event", event)
	req.Header.Set("X-Gitea-Delivery", "test-delivery-uuid-0001")
	req.Header.Set("X-Gitea-Signature", sig)
	return req
}

func newTestServer(t *testing.T, bulks ...*freebsdv1.PoudriereBulk) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, freebsdv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	objs := make([]runtime.Object, 0, len(bulks))
	for _, b := range bulks {
		objs = append(objs, b)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(Config{
		Namespace:  "nodemanager",
		HMACSecret: testSecret,
		Logger:     logger,
	}, c)
	require.NoError(t, err)
	return srv
}

// pushPayload builds a minimal valid Forgejo push JSON for testing.
func pushPayload(repo, sha, ref string) []byte {
	p := PushPayload{
		Ref:   ref,
		After: sha,
		Repository: PushRepository{
			FullName: repo,
			HTMLURL:  "https://forgejo.example.com/" + repo,
		},
	}
	out, _ := json.Marshal(p)
	return out
}

func TestNew_RejectsMissingNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	_, err := New(Config{HMACSecret: []byte("x")}, c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Namespace")
}

func TestNew_RejectsMissingSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	_, err := New(Config{Namespace: "ns"}, c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "HMACSecret")
}

func TestHandleWebhook_PingReturns200(t *testing.T) {
	srv := newTestServer(t)
	body := []byte(`{"hook_id":1}`)
	req := signedRequest(t, "ping", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestHandleWebhook_NonPushEventReturns202(t *testing.T) {
	srv := newTestServer(t)
	body := []byte(`{"action":"opened"}`)
	req := signedRequest(t, "pull_request", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code)
}

func TestHandleWebhook_MissingSignatureReturns401(t *testing.T) {
	srv := newTestServer(t)
	body := pushPayload("zachfi/personal-ports", "deadbeef", "refs/heads/main")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Event", "push")
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleWebhook_InvalidSignatureReturns401(t *testing.T) {
	srv := newTestServer(t)
	body := pushPayload("zachfi/personal-ports", "deadbeef", "refs/heads/main")
	req := signedRequest(t, "push", body)
	// Tamper with the signature.
	req.Header.Set("X-Gitea-Signature",
		hex.EncodeToString([]byte("not-the-real-mac")))
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleWebhook_TamperedBodyReturns401(t *testing.T) {
	// Sign one body, send another — the original threat the HMAC
	// countermeasure exists for.
	srv := newTestServer(t)
	signedBody := pushPayload("zachfi/personal-ports", "deadbeef", "refs/heads/main")
	req := signedRequest(t, "push", signedBody)
	// Replace body after signing.
	tamperedBody := pushPayload("evil/repo", "ffffffff", "refs/heads/main")
	req.Body = io.NopCloser(bytes.NewReader(tamperedBody))
	req.ContentLength = int64(len(tamperedBody))
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleWebhook_GitHubStyleSignaturePrefixAccepted(t *testing.T) {
	// Forgejo currently sends bare hex; tolerate the GitHub-style
	// "sha256=" prefix proactively.
	srv := newTestServer(t)
	body := pushPayload("zachfi/personal-ports", "abc123", "refs/heads/main")

	mac := hmac.New(sha256.New, testSecret)
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Event", "push")
	req.Header.Set("X-Gitea-Signature", "sha256="+sig)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code,
		"sha256= prefix should be tolerated since some clients use the GitHub form")
}

func TestHandleWebhook_NonPostReturns405(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleWebhook_MalformedJSONReturns400(t *testing.T) {
	srv := newTestServer(t)
	body := []byte(`{"this is not": "valid push json"`) // unterminated
	req := signedRequest(t, "push", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleWebhook_EmptyAfterIsAccepted(t *testing.T) {
	// Branch-delete pushes have an empty After SHA; nothing to trigger.
	srv := newTestServer(t)
	body := pushPayload("zachfi/personal-ports", "", "refs/heads/main")
	req := signedRequest(t, "push", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code)
}

func TestHandleWebhook_MissingRepoNameReturns400(t *testing.T) {
	srv := newTestServer(t)
	// Hand-build a payload without repository.full_name.
	body := []byte(`{"ref":"refs/heads/main","after":"abc","repository":{"full_name":""}}`)
	req := signedRequest(t, "push", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleWebhook_NoMatchingBulksReturnsZeroMatched(t *testing.T) {
	// Bulk exists in the namespace but its annotation references a
	// different repo.
	bulk := &freebsdv1.PoudriereBulk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "personal",
			Namespace: "nodemanager",
			Annotations: map[string]string{
				freebsdv1.ForgejoRepoAnnotation: "other/repo",
			},
		},
	}
	srv := newTestServer(t, bulk)

	body := pushPayload("zachfi/personal-ports", "abc123", "refs/heads/main")
	req := signedRequest(t, "push", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.EqualValues(t, 0, resp["matched"])

	// Verify the unrelated bulk was NOT bumped.
	var got freebsdv1.PoudriereBulk
	require.NoError(t, srv.client.Get(context.Background(),
		types.NamespacedName{Name: "personal", Namespace: "nodemanager"}, &got))
	require.Empty(t, got.Annotations[freebsdv1.TriggerAnnotation])
}

func TestHandleWebhook_PatchesMatchingBulks(t *testing.T) {
	matchingA := &freebsdv1.PoudriereBulk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ports-amd64",
			Namespace: "nodemanager",
			Annotations: map[string]string{
				freebsdv1.ForgejoRepoAnnotation: "zachfi/personal-ports",
			},
		},
	}
	matchingB := &freebsdv1.PoudriereBulk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ports-arm64",
			Namespace: "nodemanager",
			Annotations: map[string]string{
				freebsdv1.ForgejoRepoAnnotation: "zachfi/personal-ports",
			},
		},
	}
	unrelated := &freebsdv1.PoudriereBulk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other",
			Namespace: "nodemanager",
			Annotations: map[string]string{
				freebsdv1.ForgejoRepoAnnotation: "someone-else/their-ports",
			},
		},
	}
	srv := newTestServer(t, matchingA, matchingB, unrelated)

	body := pushPayload("zachfi/personal-ports", "deadbeefdeadbeef", "refs/heads/main")
	req := signedRequest(t, "push", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.EqualValues(t, 2, resp["matched"])
	require.Equal(t, "deadbeefdeadbeef", resp["sha"])
	require.Equal(t, "zachfi/personal-ports", resp["repo"])

	// Both matching bulks should have the trigger annotation set;
	// the unrelated one must NOT.
	for _, name := range []string{"ports-amd64", "ports-arm64"} {
		var got freebsdv1.PoudriereBulk
		require.NoError(t, srv.client.Get(context.Background(),
			types.NamespacedName{Name: name, Namespace: "nodemanager"}, &got))
		require.Equal(t, "deadbeefdeadbeef", got.Annotations[freebsdv1.TriggerAnnotation],
			"matching bulk %q must have trigger annotation set", name)
	}
	var u freebsdv1.PoudriereBulk
	require.NoError(t, srv.client.Get(context.Background(),
		types.NamespacedName{Name: "other", Namespace: "nodemanager"}, &u))
	require.Empty(t, u.Annotations[freebsdv1.TriggerAnnotation],
		"non-matching bulk must NOT have trigger annotation set")
}

func TestHandleWebhook_BumpUpdatesExistingTriggerValue(t *testing.T) {
	// Existing trigger value gets replaced, not concatenated, when a
	// new push arrives with a different SHA.
	bulk := &freebsdv1.PoudriereBulk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "personal",
			Namespace: "nodemanager",
			Annotations: map[string]string{
				freebsdv1.ForgejoRepoAnnotation: "zachfi/personal-ports",
				freebsdv1.TriggerAnnotation:     "previous-sha",
			},
		},
	}
	srv := newTestServer(t, bulk)

	body := pushPayload("zachfi/personal-ports", "new-sha", "refs/heads/main")
	req := signedRequest(t, "push", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var got freebsdv1.PoudriereBulk
	require.NoError(t, srv.client.Get(context.Background(),
		types.NamespacedName{Name: "personal", Namespace: "nodemanager"}, &got))
	require.Equal(t, "new-sha", got.Annotations[freebsdv1.TriggerAnnotation])
	// Existing forgejo-repo annotation must survive the merge patch.
	require.Equal(t, "zachfi/personal-ports", got.Annotations[freebsdv1.ForgejoRepoAnnotation])
}

func TestHandleWebhook_BodyTooLargeIsTruncated(t *testing.T) {
	// A body well past MaxBodyBytes should hit the LimitReader cap.
	// At that point JSON parsing fails (truncated) and we return 400.
	srv := newTestServer(t)
	huge := bytes.Repeat([]byte("x"), MaxBodyBytes*2)

	mac := hmac.New(sha256.New, testSecret)
	mac.Write(huge[:MaxBodyBytes]) // sign only what server will read
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(huge))
	req.Header.Set("X-Gitea-Event", "push")
	req.Header.Set("X-Gitea-Signature", sig)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	// Truncated body is not valid JSON → 400.  The headline property
	// is that the server didn't OOM and didn't accept the full payload.
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleHealth_Returns200(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "ok", strings.TrimSpace(rr.Body.String()))
}

// TestHandleWebhook_PatchPreservesOtherAnnotations is the
// lightweight regression for the merge-patch shape: a bulk with
// pre-existing annotations not touched by the trigger continues to
// hold them after a patch.  k8s strategic merge would handle this
// naturally; we use raw merge patch so it's worth asserting.
func TestHandleWebhook_PatchPreservesOtherAnnotations(t *testing.T) {
	bulk := &freebsdv1.PoudriereBulk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "personal",
			Namespace: "nodemanager",
			Annotations: map[string]string{
				freebsdv1.ForgejoRepoAnnotation: "zachfi/personal-ports",
				"my.unrelated/annotation":       "important",
				"another/key":                   "also-important",
			},
		},
	}
	srv := newTestServer(t, bulk)

	body := pushPayload("zachfi/personal-ports", "fresh-sha", "refs/heads/main")
	req := signedRequest(t, "push", body)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var got freebsdv1.PoudriereBulk
	require.NoError(t, srv.client.Get(context.Background(),
		types.NamespacedName{Name: "personal", Namespace: "nodemanager"}, &got))
	require.Equal(t, "fresh-sha", got.Annotations[freebsdv1.TriggerAnnotation])
	require.Equal(t, "important", got.Annotations["my.unrelated/annotation"])
	require.Equal(t, "also-important", got.Annotations["another/key"])
}
