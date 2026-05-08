package cmdrunner

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBulkDispatchInput_RoundTrip locks in the JSON shape an executor
// program will see on stdin.  Field names, ordering of nested objects,
// and discriminator values are part of the contract — a regression
// here is a contract break that would surface as silent script
// failures in production.
func TestBulkDispatchInput_RoundTrip(t *testing.T) {
	in := BulkDispatchInput{
		APIVersion: ContractAPIVersion,
		Kind:       KindBulkDispatchInput,
		Metadata: BulkDispatchMetadata{
			Name:       "personal-amd64",
			Namespace:  "nodemanager",
			UID:        "9f27aabb-4f8c-4d4c-8e3a-12345abcdef0",
			Generation: 4,
		},
		Spec: BulkDispatchSpec{
			Jail:  "14amd64",
			Tree:  "personal",
			Ports: []string{"net/curl", "shells/zsh"},
		},
	}

	got, err := json.Marshal(in)
	require.NoError(t, err)

	const want = `{"apiVersion":"freebsd.nodemanager/v1","kind":"BulkDispatchInput",` +
		`"metadata":{"name":"personal-amd64","namespace":"nodemanager",` +
		`"uid":"9f27aabb-4f8c-4d4c-8e3a-12345abcdef0","generation":4},` +
		`"spec":{"jail":"14amd64","tree":"personal","ports":["net/curl","shells/zsh"]}}`
	require.JSONEq(t, want, string(got))

	// Round-trip back through the type to confirm decoding is lossless.
	var back BulkDispatchInput
	require.NoError(t, json.Unmarshal(got, &back))
	require.Equal(t, in, back)
}

// TestBulkDispatchInput_IgnoresUnknownFields documents the
// forward-compat rule from the contract: an executor authored against
// v1 must keep working when v2 (or any additive change) sends extra
// fields.  Go's json package ignores unknown fields by default; this
// test asserts that property holds.
func TestBulkDispatchInput_IgnoresUnknownFields(t *testing.T) {
	const future = `{
		"apiVersion": "freebsd.nodemanager/v1",
		"kind": "BulkDispatchInput",
		"metadata": {"name":"x","namespace":"y","uid":"z","generation":1},
		"spec": {"jail":"j","tree":"t","ports":["p"]},
		"futureFieldNobodyWroteYet": {"with": ["nested", "values"]}
	}`

	var in BulkDispatchInput
	require.NoError(t, json.Unmarshal([]byte(future), &in))
	require.Equal(t, "x", in.Metadata.Name)
	require.Equal(t, []string{"p"}, in.Spec.Ports)
}

func TestBulkRunStatus_RoundTrip(t *testing.T) {
	in := BulkRunStatus{
		APIVersion: ContractAPIVersion,
		Kind:       KindBulkRunStatus,
		State:      BulkRunStateRunning,
		URL:        "https://code.znet/zachfi/build-infra/actions/runs/427",
	}

	got, err := json.Marshal(in)
	require.NoError(t, err)

	const want = `{"apiVersion":"freebsd.nodemanager/v1","kind":"BulkRunStatus",` +
		`"state":"running","url":"https://code.znet/zachfi/build-infra/actions/runs/427"}`
	require.JSONEq(t, want, string(got))

	var back BulkRunStatus
	require.NoError(t, json.Unmarshal(got, &back))
	require.Equal(t, in, back)
}

// TestBulkRunStatus_OmitsEmpty asserts that optional fields
// (url, error) are omitted from JSON output when empty.  Executors
// that schema-validate strictly (e.g. CHECK type) would complain about
// "" string values — we want clean documents.
func TestBulkRunStatus_OmitsEmpty(t *testing.T) {
	in := BulkRunStatus{
		APIVersion: ContractAPIVersion,
		Kind:       KindBulkRunStatus,
		State:      BulkRunStateSuccess,
	}

	got, err := json.Marshal(in)
	require.NoError(t, err)
	require.NotContains(t, string(got), "url")
	require.NotContains(t, string(got), "error")
}

func TestBulkRunState_IsTerminal(t *testing.T) {
	cases := []struct {
		state    BulkRunState
		terminal bool
	}{
		{BulkRunStateRunning, false},
		{BulkRunStateSuccess, true},
		{BulkRunStateFailed, true},
		{BulkRunStateUnknown, false},
		{BulkRunState("typo"), false},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			require.Equal(t, tc.terminal, tc.state.IsTerminal())
		})
	}
}

// TestContractDiscriminators is a guard against accidental rename of
// the apiVersion / kind constants.  Renaming any of these is a
// contract break that requires either a v2 bump or a coordinated
// rollout with every executor in the field.  The test exists to make
// such a change loud.
func TestContractDiscriminators(t *testing.T) {
	require.Equal(t, "freebsd.nodemanager/v1", ContractAPIVersion,
		"ContractAPIVersion is part of the on-the-wire contract; do not rename without a v2 bump")
	require.Equal(t, "BulkDispatchInput", KindBulkDispatchInput,
		"KindBulkDispatchInput is part of the on-the-wire contract")
	require.Equal(t, "BulkRunStatus", KindBulkRunStatus,
		"KindBulkRunStatus is part of the on-the-wire contract")
}
