/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package watchdog

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
)

// stubReader implements client.Reader for probe tests; only Get is exercised.
type stubReader struct {
	err error
}

func (s stubReader) Get(_ context.Context, _ types.NamespacedName, _ client.Object, _ ...client.GetOption) error {
	return s.err
}

func (s stubReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}

func TestManagedNodeProbe_SuccessWhenGetSucceeds(t *testing.T) {
	probe := ManagedNodeProbe(stubReader{err: nil}, "node1", "nm")
	if err := probe(context.Background()); err != nil {
		t.Fatalf("expected nil error on successful Get, got %v", err)
	}
}

func TestManagedNodeProbe_NotFoundCountsAsSuccess(t *testing.T) {
	notFound := apierrors.NewNotFound(schema.GroupResource{
		Group:    commonv1.GroupVersion.Group,
		Resource: "managednodes",
	}, "node1")
	probe := ManagedNodeProbe(stubReader{err: notFound}, "node1", "nm")
	if err := probe(context.Background()); err != nil {
		t.Fatalf("NotFound must count as success (API answered), got %v", err)
	}
}

func TestManagedNodeProbe_RealErrorIsFailure(t *testing.T) {
	probe := ManagedNodeProbe(stubReader{err: errors.New("connection refused")}, "node1", "nm")
	if err := probe(context.Background()); err == nil {
		t.Fatal("a non-NotFound error must propagate as probe failure")
	}
}

// ensure the stub satisfies the interface the helper depends on.
var _ client.Reader = stubReader{}
