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
	"encoding/json"
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
	"github.com/zachfi/nodemanager/pkg/files"
	"github.com/zachfi/nodemanager/pkg/locker"
)

var _ = Describe("FileBucket reconcile integration", func() {
	const csName = "filebucket-cs"
	ctx := context.Background()

	var (
		bucketDir string
		fileDir   string
		path      string
	)

	BeforeEach(func() {
		ensureLocalNodeLabel(ctx, "nodemanager.test/enabled", "true")

		var err error
		bucketDir, err = os.MkdirTemp("", "filebucket-bucket-*")
		Expect(err).NotTo(HaveOccurred())
		fileDir, err = os.MkdirTemp("", "filebucket-files-*")
		Expect(err).NotTo(HaveOccurred())
		path = filepath.Join(fileDir, "managed.conf")

		cs := &commonv1.ConfigSet{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, cs)
		if err != nil && k8serrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, &commonv1.ConfigSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      csName,
					Namespace: "default",
					Labels:    map[string]string{"nodemanager.test/enabled": "true"},
				},
				Spec: commonv1.ConfigSetSpec{
					Files: []commonv1.File{
						{
							Path:    path,
							Ensure:  "file",
							Content: "new managed content",
						},
					},
				},
			})).To(Succeed())
		}
	})

	AfterEach(func() {
		Expect(os.RemoveAll(bucketDir)).To(Succeed())
		Expect(os.RemoveAll(fileDir)).To(Succeed())

		cs := &commonv1.ConfigSet{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, cs); err == nil {
			Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
		}
	})

	newBucketReconciler := func(bucketPath string, maxBytes int64) *ConfigSetReconciler {
		sys := &mockSystemHandler{}
		lkr := locker.NewLeaseLocker(ctx, logger, locker.Config{}, clientset, "default", hostname)
		return &ConfigSetReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			tracer: noop.NewTracerProvider().Tracer("test"),
			logger: logger,
			system: sys,
			locker: lkr,
			cfg: ConfigSetConfig{
				Namespace: "default",
				FileBucket: FileBucketConfig{
					Enabled:          true,
					Path:             bucketPath,
					MaxFileSizeBytes: maxBytes,
				},
			},
		}
	}

	It("does not create a backup when the file does not yet exist", func() {
		// path does not exist on disk — nothing to back up
		r := newBucketReconciler(bucketDir, 102400)
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: csName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("verifying no backup hash is stored in ManagedNode status")
		osHostname, _ := os.Hostname()
		mn := &commonv1.ManagedNode{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: osHostname, Namespace: "default"}, mn)).To(Succeed())
		Expect(mn.Status.FileBackups).NotTo(HaveKey(path))
	})

	It("backs up the existing file and records the hash in ManagedNode status", func() {
		oldContent := []byte("original content")
		Expect(os.WriteFile(path, oldContent, 0o644)).To(Succeed())

		r := newBucketReconciler(bucketDir, 102400)
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: csName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("verifying a backup hash is stored in ManagedNode status")
		osHostname, _ := os.Hostname()
		mn := &commonv1.ManagedNode{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: osHostname, Namespace: "default"}, mn)).To(Succeed())
		Expect(mn.Status.FileBackups).To(HaveKey(path))

		hash := mn.Status.FileBackups[path]
		Expect(hash).To(HaveLen(64))

		By("verifying the blob content matches the original file")
		blobPath := filepath.Join(bucketDir, hash[0:2], hash[2:4], hash[4:])
		blob, readErr := os.ReadFile(blobPath)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(blob).To(Equal(oldContent))

		By("verifying the meta sidecar records the correct path and mode")
		metaBytes, metaErr := os.ReadFile(blobPath + ".meta")
		Expect(metaErr).NotTo(HaveOccurred())
		var meta files.FileBucketMeta
		Expect(json.Unmarshal(metaBytes, &meta)).To(Succeed())
		Expect(meta.Path).To(Equal(path))
		Expect(meta.Mode).NotTo(BeEmpty())
	})

	It("skips backup when the existing file exceeds MaxFileSizeBytes", func() {
		// Write a 2-byte file and set the limit to 1 byte so the size guard fires.
		Expect(os.WriteFile(path, []byte("ab"), 0o644)).To(Succeed())

		r := newBucketReconciler(bucketDir, 1)
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: csName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		By("verifying no backup hash is stored (file was too large)")
		osHostname, _ := os.Hostname()
		mn := &commonv1.ManagedNode{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: osHostname, Namespace: "default"}, mn)).To(Succeed())
		Expect(mn.Status.FileBackups).NotTo(HaveKey(path))
	})
})
