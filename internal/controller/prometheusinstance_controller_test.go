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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/prometheus"
)

var _ = Describe("PrometheusInstance Controller", func() {
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
			By("creating the custom resource for the Kind PrometheusInstance")
			prometheusinstance := &paasv1alpha1.PrometheusInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, prometheusinstance)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.PrometheusInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.PrometheusInstanceSpec{
						Replicas: 2,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance PrometheusInstance")
			resource := &paasv1alpha1.PrometheusInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &PrometheusInstanceReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  prometheus.Adapter{},
					Name:     PrometheusInstanceControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			prometheusInstance := &unstructured.Unstructured{}
			prometheusInstance.SetGroupVersionKind(prometheus.GVK)
			prometheusInstance.SetName(resourceName)
			prometheusInstance.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, prometheusInstance)
		})

		It("should create a matching, namespace-scoped Prometheus and report a Ready=False status", func() {
			controllerReconciler := &PrometheusInstanceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  prometheus.Adapter{},
				Name:     PrometheusInstanceControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.PrometheusInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.example.com/cleanup"))

			By("reconciling again to apply the Prometheus and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated Prometheus matches the paas PrometheusInstance spec")
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(prometheus.GVK)
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			replicas, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas")
			Expect(replicas).To(Equal(int64(2)))

			By("verifying the Prometheus only selects monitors in its own namespace")
			_, nsSelectorFound, _ := unstructured.NestedMap(got.Object, "spec", "serviceMonitorNamespaceSelector")
			Expect(nsSelectorFound).To(BeFalse())
			_, podNsSelectorFound, _ := unstructured.NestedMap(got.Object, "spec", "podMonitorNamespaceSelector")
			Expect(podNsSelectorFound).To(BeFalse())

			By("verifying the paas PrometheusInstance status was patched")
			var withStatus paasv1alpha1.PrometheusInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real Prometheus Operator controller runs in this envtest, so
			// the Prometheus never actually becomes ready — this asserts the
			// *mapping* (not-ready status correctly mirrored), not the
			// Prometheus Operator's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should always create the Service, and create/prune the Ingress as .spec.ingress is set and unset", func() {
			controllerReconciler := &PrometheusInstanceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  prometheus.Adapter{},
				Name:     PrometheusInstanceControllerName,
			}

			By("reconciling twice with no ingress requested")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			serviceName := types.NamespacedName{Name: resourceName + "-web", Namespace: resourceNamespace}
			ingressName := types.NamespacedName{Name: resourceName + "-ingress", Namespace: resourceNamespace}
			// The Service is created regardless of Ingress -- in-cluster
			// consumers (a GrafanaDatasource, in particular) need a stable
			// address even when Prometheus is never exposed externally.
			Expect(k8sClient.Get(ctx, serviceName, &corev1.Service{})).To(Succeed())
			Expect(errors.IsNotFound(k8sClient.Get(ctx, ingressName, &networkingv1.Ingress{}))).To(BeTrue())

			By("setting .spec.ingress and reconciling")
			var resource paasv1alpha1.PrometheusInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			resource.Spec.Ingress = &paasv1alpha1.IngressSpec{Host: "prometheus.example.com"}
			Expect(k8sClient.Update(ctx, &resource)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var svc corev1.Service
			Expect(k8sClient.Get(ctx, serviceName, &svc)).To(Succeed())
			Expect(svc.Spec.Selector).To(HaveKeyWithValue("operator.prometheus.io/name", resourceName))

			var ing networkingv1.Ingress
			Expect(k8sClient.Get(ctx, ingressName, &ing)).To(Succeed())
			Expect(ing.Spec.Rules).To(HaveLen(1))
			Expect(ing.Spec.Rules[0].Host).To(Equal("prometheus.example.com"))
			Expect(ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name).To(Equal(serviceName.Name))

			By("unsetting .spec.ingress and reconciling again")
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			resource.Spec.Ingress = nil
			Expect(k8sClient.Update(ctx, &resource)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, serviceName, &corev1.Service{})).To(Succeed())
			Expect(errors.IsNotFound(k8sClient.Get(ctx, ingressName, &networkingv1.Ingress{}))).To(BeTrue())
		})
	})
})
