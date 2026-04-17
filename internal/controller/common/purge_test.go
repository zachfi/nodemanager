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
	"log/slog"
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

var _ = Describe("Directory purge integration", func() {
	const ownerCS = "purge-owner"
	const otherCS = "purge-other"
	ctx := context.Background()

	var dir string

	BeforeEach(func() {
		ensureLocalNodeLabel(ctx, "nodemanager.test/enabled", "true")

		var err error
		dir, err = os.MkdirTemp("", "purge-dir-*")
		Expect(err).NotTo(HaveOccurred())

		// ownerCS declares two managed files in the temp directory.
		cs := &commonv1.ConfigSet{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: ownerCS, Namespace: "default"}, cs)
		if err != nil && k8serrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ownerCS,
					Namespace: "default",
					Labels:    map[string]string{"nodemanager.test/enabled": "true"},
				},
				Spec: commonv1.ConfigSetSpec{
					Files: []commonv1.File{
						{
							Path:   dir,
							Ensure: "directory",
							Purge:  true,
						},
						{
							Path:    filepath.Join(dir, "managed-a.conf"),
							Ensure:  "file",
							Content: "a",
						},
					},
				},
			})).To(Succeed())
		}

		// otherCS declares a second managed file in the same directory but is a
		// separate ConfigSet (simulating split ownership).
		cs2 := &commonv1.ConfigSet{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: otherCS, Namespace: "default"}, cs2)
		if err != nil && k8serrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      otherCS,
					Namespace: "default",
					Labels:    map[string]string{"nodemanager.test/enabled": "true"},
				},
				Spec: commonv1.ConfigSetSpec{
					Files: []commonv1.File{
						{
							Path:    filepath.Join(dir, "managed-b.conf"),
							Ensure:  "file",
							Content: "b",
						},
					},
				},
			})).To(Succeed())
		}
	})

	AfterEach(func() {
		Expect(os.RemoveAll(dir)).To(Succeed())

		for _, name := range []string{ownerCS, otherCS} {
			cs := &commonv1.ConfigSet{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, cs); err == nil {
				Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
			}
		}
	})

	newReconciler := func() *ConfigSetReconciler {
		lkr := locker.NewLeaseLocker(ctx, logger, locker.Config{}, clientset, "default", hostname)
		return &ConfigSetReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			tracer: noop.NewTracerProvider().Tracer("test"),
			logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})),
			system: &mockSystemHandler{},
			locker: lkr,
			cfg:    ConfigSetConfig{Namespace: "default"},
		}
	}

	It("removes unmanaged files but keeps files declared by any matching ConfigSet", func() {
		// Plant a file that is declared in otherCS — must be kept.
		managedB := filepath.Join(dir, "managed-b.conf")
		Expect(os.WriteFile(managedB, []byte("b"), 0o644)).To(Succeed())

		// Plant an unmanaged file — must be removed.
		unmanaged := filepath.Join(dir, "unmanaged.conf")
		Expect(os.WriteFile(unmanaged, []byte("stray"), 0o644)).To(Succeed())

		_, err := newReconciler().Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: ownerCS, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("keeping the file owned by the other matching ConfigSet")
		_, err = os.Stat(managedB)
		Expect(err).NotTo(HaveOccurred())

		By("removing the unmanaged file")
		_, err = os.Stat(unmanaged)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("does not remove subdirectories", func() {
		subdir := filepath.Join(dir, "subdir")
		Expect(os.Mkdir(subdir, 0o755)).To(Succeed())

		_, err := newReconciler().Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: ownerCS, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("leaving the subdirectory untouched")
		info, err := os.Stat(subdir)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.IsDir()).To(BeTrue())
	})

	It("does not purge files from non-matching ConfigSets", func() {
		// Plant a file declared by a ConfigSet that does NOT match this node
		// (different label). It should be treated as unmanaged and removed.
		Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "purge-nomatch",
				Namespace: "default",
				Labels:    map[string]string{"kubernetes.io/hostname": "nonexistent-node"},
			},
			Spec: commonv1.ConfigSetSpec{
				Files: []commonv1.File{
					{Path: filepath.Join(dir, "nomatch.conf"), Ensure: "file", Content: "x"},
				},
			},
		})).To(Succeed())
		DeferCleanup(func() {
			cs := &commonv1.ConfigSet{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "purge-nomatch", Namespace: "default"}, cs); err == nil {
				Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
			}
		})

		noMatchFile := filepath.Join(dir, "nomatch.conf")
		Expect(os.WriteFile(noMatchFile, []byte("x"), 0o644)).To(Succeed())

		_, err := newReconciler().Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: ownerCS, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("removing the file declared only by a non-matching ConfigSet")
		_, err = os.Stat(noMatchFile)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
})
