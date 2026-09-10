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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/rabbitmq"
)

var _ = Describe("RabbitMQCluster Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-rabbitmq"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind RabbitMQCluster")
			rabbitmqcluster := &paasv1alpha1.RabbitMQCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, rabbitmqcluster)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.RabbitMQCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.RabbitMQClusterSpec{
						Replicas: 3,
						Storage: paasv1alpha1.StorageSpec{
							Size: testStorageSize,
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance RabbitMQCluster")
			resource := &paasv1alpha1.RabbitMQCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &RabbitMQClusterReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  rabbitmq.Adapter{},
					Name:     RabbitMQClusterControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			target := &unstructured.Unstructured{}
			target.SetGroupVersionKind(rabbitmq.GVK)
			target.SetName(resourceName)
			target.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, target)
		})

		It("should create a matching RabbitmqCluster and report a Ready=False status", func() {
			controllerReconciler := &RabbitMQClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  rabbitmq.Adapter{},
				Name:     RabbitMQClusterControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.RabbitMQCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.example.com/cleanup"))

			By("reconciling again to apply the RabbitmqCluster and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated RabbitmqCluster matches the paas RabbitMQCluster spec")
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(rabbitmq.GVK)
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			replicas, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas")
			Expect(replicas).To(Equal(int64(3)))

			size, _, _ := unstructured.NestedString(got.Object, "spec", "persistence", "storage")
			Expect(size).To(Equal(testStorageSize))

			By("verifying the paas RabbitMQCluster status was patched")
			var withStatus paasv1alpha1.RabbitMQCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real RabbitMQ Cluster Operator controller runs in this
			// envtest, so the RabbitmqCluster never actually becomes ready —
			// this asserts the *mapping* (not-ready status correctly
			// mirrored), not the RabbitMQ Cluster Operator's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should create and prune an Ingress as .spec.ingress is set and unset", func() {
			controllerReconciler := &RabbitMQClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  rabbitmq.Adapter{},
				Name:     RabbitMQClusterControllerName,
			}

			By("reconciling twice with no ingress requested")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			ingressName := types.NamespacedName{Name: resourceName + "-rabbitmq-ingress", Namespace: resourceNamespace}
			Expect(errors.IsNotFound(k8sClient.Get(ctx, ingressName, &networkingv1.Ingress{}))).To(BeTrue())

			By("setting .spec.ingress and reconciling")
			var resource paasv1alpha1.RabbitMQCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			resource.Spec.Ingress = &paasv1alpha1.IngressSpec{Host: "rabbitmq.example.com"}
			Expect(k8sClient.Update(ctx, &resource)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var ing networkingv1.Ingress
			Expect(k8sClient.Get(ctx, ingressName, &ing)).To(Succeed())
			Expect(ing.Spec.Rules).To(HaveLen(1))
			Expect(ing.Spec.Rules[0].Host).To(Equal("rabbitmq.example.com"))
			Expect(ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name).To(Equal(resourceName))
			Expect(ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Number).To(Equal(int32(15672)))

			By("unsetting .spec.ingress and reconciling again")
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			resource.Spec.Ingress = nil
			Expect(k8sClient.Update(ctx, &resource)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(errors.IsNotFound(k8sClient.Get(ctx, ingressName, &networkingv1.Ingress{}))).To(BeTrue())
		})
	})
})
