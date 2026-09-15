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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/valkey"
)

var _ = Describe("ValkeyCluster Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ValkeyCluster")
			valkeycluster := &paasv1alpha1.ValkeyCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, valkeycluster)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.ValkeyCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.ValkeyClusterSpec{
						Shards:   3,
						Replicas: 1,
						Persistence: paasv1alpha1.PersistenceSpec{
							Size: testStorageSize,
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance ValkeyCluster")
			resource := &paasv1alpha1.ValkeyCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &ValkeyClusterReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  valkey.Adapter{},
					Name:     ValkeyClusterControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			valkeyCluster := &unstructured.Unstructured{}
			valkeyCluster.SetGroupVersionKind(valkey.GVK)
			valkeyCluster.SetName(resourceName)
			valkeyCluster.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, valkeyCluster)
		})

		It("should create a matching ValkeyCluster and report a Ready=False status", func() {
			controllerReconciler := &ValkeyClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  valkey.Adapter{},
				Name:     ValkeyClusterControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.ValkeyCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.cncp.nl/cleanup"))

			By("reconciling again to apply the ValkeyCluster and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated ValkeyCluster matches the paas ValkeyCluster spec")
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(valkey.GVK)
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			shards, _, _ := unstructured.NestedInt64(got.Object, "spec", "shards")
			Expect(shards).To(Equal(int64(3)))

			replicas, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas")
			Expect(replicas).To(Equal(int64(1)))

			size, _, _ := unstructured.NestedString(got.Object, "spec", "persistence", "size")
			Expect(size).To(Equal(testStorageSize))

			By("verifying the paas ValkeyCluster status was patched")
			var withStatus paasv1alpha1.ValkeyCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real valkey-operator controller runs in this envtest, so the
			// ValkeyCluster never actually becomes ready — this asserts the
			// *mapping* (not-ready status correctly mirrored), not the
			// valkey-operator's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should create and prune an external Service as .spec.expose is set and unset", func() {
			controllerReconciler := &ValkeyClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  valkey.Adapter{},
				Name:     ValkeyClusterControllerName,
			}

			By("reconciling twice with no exposure requested")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			serviceName := types.NamespacedName{Name: resourceName + "-external", Namespace: resourceNamespace}
			Expect(errors.IsNotFound(k8sClient.Get(ctx, serviceName, &corev1.Service{}))).To(BeTrue())

			By("setting .spec.expose and reconciling")
			var resource paasv1alpha1.ValkeyCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			resource.Spec.Expose = &paasv1alpha1.ServiceExposeSpec{Type: corev1.ServiceTypeLoadBalancer}
			Expect(k8sClient.Update(ctx, &resource)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var svc corev1.Service
			Expect(k8sClient.Get(ctx, serviceName, &svc)).To(Succeed())
			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
			Expect(svc.Spec.Selector).To(HaveKeyWithValue("valkey.io/cluster", resourceName))

			By("unsetting .spec.expose and reconciling again")
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			resource.Spec.Expose = nil
			Expect(k8sClient.Update(ctx, &resource)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(errors.IsNotFound(k8sClient.Get(ctx, serviceName, &corev1.Service{}))).To(BeTrue())
		})
	})
})
