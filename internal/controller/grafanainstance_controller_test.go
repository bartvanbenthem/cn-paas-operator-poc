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
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
)

var _ = Describe("GrafanaInstance Controller", func() {
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
			By("creating the custom resource for the Kind GrafanaInstance")
			grafanainstance := &paasv1alpha1.GrafanaInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, grafanainstance)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.GrafanaInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.GrafanaInstanceSpec{
						Replicas:      2,
						PrometheusRef: "prometheus",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance GrafanaInstance")
			resource := &paasv1alpha1.GrafanaInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &GrafanaInstanceReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  grafana.Adapter{},
					Name:     GrafanaInstanceControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			grafanaInstance := &unstructured.Unstructured{}
			grafanaInstance.SetGroupVersionKind(grafana.GVK)
			grafanaInstance.SetName(resourceName)
			grafanaInstance.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, grafanaInstance)
		})

		It("should create a matching Grafana and report a Ready=False status", func() {
			controllerReconciler := &GrafanaInstanceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  grafana.Adapter{},
				Name:     GrafanaInstanceControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.GrafanaInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.cncp.nl/cleanup"))

			By("reconciling again to apply the Grafana and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated Grafana matches the paas GrafanaInstance spec")
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(grafana.GVK)
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			replicas, _, _ := unstructured.NestedInt64(got.Object, "spec", "deployment", "spec", "replicas")
			Expect(replicas).To(Equal(int64(2)))

			By("verifying the paas GrafanaInstance status was patched")
			var withStatus paasv1alpha1.GrafanaInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real grafana-operator controller runs in this envtest, so the
			// Grafana never actually becomes ready — this asserts the
			// *mapping* (not-ready status correctly mirrored), not
			// grafana-operator's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should reject a GrafanaInstance with prometheusRef unset or empty", func() {
			// Regression test: prometheusRef used to silently default to the
			// literal name "prometheus", producing a GrafanaDatasource
			// pointing at a Service that doesn't exist whenever the actual
			// PrometheusInstance was named anything else -- a real cluster
			// hit this. The field is now required with minLength=1 so the
			// API server rejects the ambiguous case outright instead of
			// silently wiring nothing up.
			unset := &paasv1alpha1.GrafanaInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "no-prometheus-ref", Namespace: resourceNamespace},
				Spec:       paasv1alpha1.GrafanaInstanceSpec{Replicas: 1},
			}
			Expect(k8sClient.Create(ctx, unset)).NotTo(Succeed())

			empty := &paasv1alpha1.GrafanaInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "empty-prometheus-ref", Namespace: resourceNamespace},
				Spec:       paasv1alpha1.GrafanaInstanceSpec{Replicas: 1, PrometheusRef: ""},
			}
			Expect(k8sClient.Create(ctx, empty)).NotTo(Succeed())
		})
	})
})
