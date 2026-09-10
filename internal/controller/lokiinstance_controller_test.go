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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

			// testLokiImage/testLokiContainerName are the container image and
			// name used by every simulated Loki Operator StatefulSet/Pod
			// fixture below -- irrelevant to what's under test (no real
			// image is ever pulled in envtest), just need to be present.
			testLokiImage         = "docker.io/grafana/loki:3.7.3"
			testLokiContainerName = "loki"
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

		It("should patch fsGroup onto the Loki Operator's own StatefulSets once they exist, and leave them alone on deletion", func() {
			controllerReconciler := &LokiInstanceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  loki.Adapter{},
				Name:     LokiInstanceControllerName,
			}

			By("reconciling once to attach the finalizer, once more to apply the LokiStack")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("simulating the Loki Operator having created the ingester StatefulSet, with no fsGroup set")
			stsName := resourceName + "-ingester"
			sts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: resourceNamespace},
				Spec: appsv1.StatefulSetSpec{
					ServiceName: stsName,
					Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{testDBName: stsName}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{testDBName: stsName}},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: testLokiContainerName, Image: testLokiImage}},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, sts)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, sts)
			})

			By("reconciling again so ExtraResources patches fsGroup onto it")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var patched appsv1.StatefulSet
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: stsName, Namespace: resourceNamespace}, &patched)).To(Succeed())
			Expect(patched.Spec.Template.Spec.SecurityContext).NotTo(BeNil())
			Expect(patched.Spec.Template.Spec.SecurityContext.FSGroup).NotTo(BeNil())
			Expect(*patched.Spec.Template.Spec.SecurityContext.FSGroup).To(Equal(int64(10001)))

			By("deleting the LokiInstance and reconciling its deletion path")
			var toDelete paasv1alpha1.LokiInstance
			Expect(k8sClient.Get(ctx, typeNamespacedName, &toDelete)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &toDelete)).To(Succeed())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the StatefulSet itself was left alone -- it isn't owned by this reconciler")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: stsName, Namespace: resourceNamespace}, &appsv1.StatefulSet{})).To(Succeed())
		})

		It("should delete a stale, unhealthy pod stuck behind the fsGroup patch so the StatefulSet controller can recreate it", func() {
			controllerReconciler := &LokiInstanceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  loki.Adapter{},
				Name:     LokiInstanceControllerName,
			}

			By("reconciling once to attach the finalizer, once more to apply the LokiStack")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("simulating the Loki Operator having created the compactor StatefulSet, with no fsGroup set, at an old revision")
			stsName := resourceName + "-compactor"
			sts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: resourceNamespace},
				Spec: appsv1.StatefulSetSpec{
					ServiceName: stsName,
					Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{testDBName: stsName}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{testDBName: stsName}},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: testLokiContainerName, Image: testLokiImage}},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, sts)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, sts)
			})

			By("simulating the pod the Loki Operator created from that (unpatched) revision, crash-looping and not Ready")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stsName + "-0",
					Namespace: resourceNamespace,
					Labels:    map[string]string{testDBName: stsName, "controller-revision-hash": "old-rev"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: testLokiContainerName, Image: testLokiImage}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			By("simulating the StatefulSet controller having computed a newer revision than the pod is running")
			var withStatus appsv1.StatefulSet
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: stsName, Namespace: resourceNamespace}, &withStatus)).To(Succeed())
			withStatus.Status.CurrentRevision = "old-rev"
			withStatus.Status.UpdateRevision = "new-rev"
			Expect(k8sClient.Status().Update(ctx, &withStatus)).To(Succeed())

			By("reconciling again so ExtraResources patches fsGroup and unsticks the stale, unhealthy pod")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var patched appsv1.StatefulSet
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: stsName, Namespace: resourceNamespace}, &patched)).To(Succeed())
			Expect(patched.Spec.Template.Spec.SecurityContext).NotTo(BeNil())
			Expect(*patched.Spec.Template.Spec.SecurityContext.FSGroup).To(Equal(int64(10001)))

			By("verifying the stale, unhealthy pod was deleted so it can be recreated from the patched template")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: resourceNamespace}, &corev1.Pod{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})
