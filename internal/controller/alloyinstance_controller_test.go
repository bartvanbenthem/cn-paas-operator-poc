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
	"github.com/bartvanbenthem/paas-operator/internal/alloy"
)

var _ = Describe("AlloyInstance Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-alloy"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind AlloyInstance")
			alloyinstance := &paasv1alpha1.AlloyInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, alloyinstance)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.AlloyInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.AlloyInstanceSpec{
						LokiInstanceRef: "test-loki",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance AlloyInstance")
			resource := &paasv1alpha1.AlloyInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &AlloyInstanceReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  alloy.Adapter{},
					Name:     AlloyInstanceControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			target := &unstructured.Unstructured{}
			target.SetGroupVersionKind(alloy.GVK)
			target.SetName(resourceName)
			target.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, target)
		})

		It("should create a matching Alloy wired to the referenced LokiInstance and report a Ready=False status", func() {
			controllerReconciler := &AlloyInstanceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  alloy.Adapter{},
				Name:     AlloyInstanceControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.AlloyInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.cncp.nl/cleanup"))

			By("reconciling again to apply the Alloy and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated Alloy points at the referenced LokiInstance's distributor, scoped to its own namespace")
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(alloy.GVK)
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			controllerType, _, _ := unstructured.NestedString(got.Object, "spec", "controller", "type")
			Expect(controllerType).To(Equal("deployment"))

			config, _, _ := unstructured.NestedString(got.Object, "spec", "alloy", "configMap", "content")
			Expect(config).To(ContainSubstring("http://test-loki-distributor-http." + resourceNamespace + ".svc.cluster.local:3100/loki/api/v1/push"))

			namespaces, _, _ := unstructured.NestedSlice(got.Object, "spec", "rbac", "namespaces")
			Expect(namespaces).To(ConsistOf(resourceNamespace))

			By("verifying the paas AlloyInstance status was patched")
			var withStatus paasv1alpha1.AlloyInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real Alloy Operator controller runs in this envtest, so the
			// Alloy never actually becomes ready -- this asserts the
			// *mapping* (not-ready status correctly mirrored), not the
			// vendor operator's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})
	})
})
