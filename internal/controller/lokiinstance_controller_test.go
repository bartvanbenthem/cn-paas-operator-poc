/*
Copyright 2026.

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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/loki"
)

var _ = Describe("LokiInstance Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-loki"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind LokiInstance")
			lokiinstance := &paasv1alpha1.LokiInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, lokiinstance)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.LokiInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.LokiInstanceSpec{
						Size:             "1x.demo",
						StorageClassName: "standard",
						ObjectStorage:    paasv1alpha1.LokiObjectStorageSpec{SecretName: "loki-s3"},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance LokiInstance")
			resource := &paasv1alpha1.LokiInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &LokiInstanceReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  loki.Adapter{},
					Name:     LokiInstanceControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			lokistack := &unstructured.Unstructured{}
			lokistack.SetGroupVersionKind(loki.GVK)
			lokistack.SetName(resourceName)
			lokistack.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, lokistack)
		})

		It("should create a matching LokiStack and report a Ready=False status", func() {
			controllerReconciler := &LokiInstanceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  loki.Adapter{},
				Name:     LokiInstanceControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.LokiInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.example.com/cleanup"))

			By("reconciling again to apply the LokiStack and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated LokiStack matches the paas LokiInstance spec")
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(loki.GVK)
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			size, _, _ := unstructured.NestedString(got.Object, "spec", "size")
			Expect(size).To(Equal("1x.demo"))

			secretName, _, _ := unstructured.NestedString(got.Object, "spec", "storage", "secret", "name")
			Expect(secretName).To(Equal("loki-s3"))

			By("verifying the paas LokiInstance status was patched")
			var withStatus paasv1alpha1.LokiInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real Loki Operator controller runs in this envtest, so the
			// LokiStack never actually becomes ready -- this asserts the
			// *mapping* (not-ready status correctly mirrored), not the
			// vendor operator's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})
	})
})
