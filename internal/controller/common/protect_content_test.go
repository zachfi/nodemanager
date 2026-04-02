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

package common

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace/noop"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	commonv1 "github.com/zachfi/nodemanager/api/common/v1"
	"github.com/zachfi/nodemanager/pkg/locker"
)

var _ = Describe("fileContentHash", func() {
	It("returns a stable hex string for the same input", func() {
		h1 := fileContentHash([]byte("hello"))
		h2 := fileContentHash([]byte("hello"))
		Expect(h1).To(Equal(h2))
		Expect(h1).To(HaveLen(64)) // SHA256 → 32 bytes → 64 hex chars
	})

	It("returns different hashes for different inputs", func() {
		Expect(fileContentHash([]byte("a"))).NotTo(Equal(fileContentHash([]byte("b"))))
	})

	It("returns a stable hash for empty input", func() {
		h := fileContentHash([]byte{})
		Expect(h).To(HaveLen(64))
	})
})

var _ = Describe("shouldSkipProtectedFile", func() {
	var (
		r    *ConfigSetReconciler
		dir  string
		path string
	)

	BeforeEach(func() {
		r = &ConfigSetReconciler{}
		var err error
		dir, err = os.MkdirTemp("", "protect-content-test-*")
		Expect(err).NotTo(HaveOccurred())
		path = filepath.Join(dir, "testfile")
	})

	AfterEach(func() {
		Expect(os.RemoveAll(dir)).To(Succeed())
	})

	It("returns false when the file does not exist", func() {
		skip, err := r.shouldSkipProtectedFile(path, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(skip).To(BeFalse())
	})

	It("returns false when the file exists but has never been tracked", func() {
		Expect(os.WriteFile(path, []byte("some content"), 0o644)).To(Succeed())

		skip, err := r.shouldSkipProtectedFile(path, map[string]string{})
		Expect(err).NotTo(HaveOccurred())
		Expect(skip).To(BeFalse())
	})

	It("returns false when the file content matches the stored hash (no out-of-band change)", func() {
		content := []byte("managed content")
		Expect(os.WriteFile(path, content, 0o644)).To(Succeed())

		hashes := map[string]string{path: fileContentHash(content)}
		skip, err := r.shouldSkipProtectedFile(path, hashes)
		Expect(err).NotTo(HaveOccurred())
		Expect(skip).To(BeFalse())
	})

	It("returns true when the file was modified out-of-band", func() {
		original := []byte("original content")
		Expect(os.WriteFile(path, []byte("modified content"), 0o644)).To(Succeed())

		// Stored hash reflects original; disk now has different content.
		hashes := map[string]string{path: fileContentHash(original)}
		skip, err := r.shouldSkipProtectedFile(path, hashes)
		Expect(err).NotTo(HaveOccurred())
		Expect(skip).To(BeTrue())
	})
})

var _ = Describe("ProtectContent reconcile integration", func() {
	const csName = "protect-content-cs"
	ctx := context.Background()

	var (
		dir  string
		path string
	)

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "protect-content-integ-*")
		Expect(err).NotTo(HaveOccurred())
		path = filepath.Join(dir, "managed.conf")

		cs := &commonv1.ConfigSet{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, cs)
		if err != nil && k8serrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      csName,
					Namespace: "default",
				},
				Spec: commonv1.ConfigSetSpec{
					Files: []commonv1.File{
						{
							Path:           path,
							Ensure:         "file",
							Content:        "managed content",
							ProtectContent: true,
						},
					},
				},
			})).To(Succeed())
		}
	})

	AfterEach(func() {
		Expect(os.RemoveAll(dir)).To(Succeed())

		cs := &commonv1.ConfigSet{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, cs); err == nil {
			Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
		}
	})

	newReconciler := func() (*ConfigSetReconciler, *mockFileHandler) {
		sys := &mockSystemHandler{}
		lkr := locker.NewLeaseLocker(ctx, logger, locker.Config{}, clientset, "default", hostname)
		r := &ConfigSetReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			tracer: noop.NewTracerProvider().Tracer("test"),
			logger: logger,
			system: sys,
			locker: lkr,
		}
		return r, sys.File().(*mockFileHandler)
	}

	It("writes the file and stores the hash on the first reconcile", func() {
		r, fh := newReconciler()
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: csName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("verifying the file write was attempted")
		Expect(fh.fileWriteCalls).To(HaveKey(path))

		By("verifying the hash is stored in ManagedNode status")
		osHostname, _ := os.Hostname()
		mn := &commonv1.ManagedNode{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: osHostname, Namespace: "default"}, mn)).To(Succeed())
		Expect(mn.Status.FileHashes).To(HaveKey(path))
		Expect(mn.Status.FileHashes[path]).To(Equal(fileContentHash([]byte("managed content"))))
	})

	It("skips the write when the file was modified out-of-band", func() {
		By("pre-seeding the node status with a hash of the original content")
		osHostname, _ := os.Hostname()
		mn := &commonv1.ManagedNode{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: osHostname, Namespace: "default"}, mn)).To(Succeed())
		if mn.Status.FileHashes == nil {
			mn.Status.FileHashes = make(map[string]string)
		}
		mn.Status.FileHashes[path] = fileContentHash([]byte("managed content"))
		Expect(k8sClient.Status().Update(ctx, mn)).To(Succeed())

		By("writing a different file to disk to simulate an out-of-band modification")
		Expect(os.WriteFile(path, []byte("user-modified content"), 0o644)).To(Succeed())

		r, fh := newReconciler()
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: csName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("verifying the file write was NOT attempted")
		Expect(fh.fileWriteCalls).NotTo(HaveKey(path))

		By("verifying the on-disk content is still the user-modified version")
		onDisk, readErr := os.ReadFile(path)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(string(onDisk)).To(Equal("user-modified content"))
	})

	It("writes the file when the on-disk content matches the last-written hash", func() {
		By("pre-seeding the node status so the file is tracked")
		osHostname, _ := os.Hostname()
		mn := &commonv1.ManagedNode{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: osHostname, Namespace: "default"}, mn)).To(Succeed())
		if mn.Status.FileHashes == nil {
			mn.Status.FileHashes = make(map[string]string)
		}
		mn.Status.FileHashes[path] = fileContentHash([]byte("managed content"))
		Expect(k8sClient.Status().Update(ctx, mn)).To(Succeed())

		By("writing the exact managed content to disk (no out-of-band change)")
		Expect(os.WriteFile(path, []byte("managed content"), 0o644)).To(Succeed())

		r, fh := newReconciler()
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: csName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("verifying the write was attempted (content may need updating)")
		Expect(fh.fileWriteCalls).To(HaveKey(path))
	})
})
