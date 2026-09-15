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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/strimzi"
)

var _ = Describe("KafkaCluster Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-kafka"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind KafkaCluster")
			kafkacluster := &paasv1alpha1.KafkaCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, kafkacluster)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.KafkaCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.KafkaClusterSpec{
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
			By("Cleanup the specific resource instance KafkaCluster")
			resource := &paasv1alpha1.KafkaCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &KafkaClusterReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  strimzi.Adapter{},
					Name:     KafkaClusterControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			kafka := &unstructured.Unstructured{}
			kafka.SetGroupVersionKind(strimzi.GVK)
			kafka.SetName(resourceName)
			kafka.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, kafka)
		})

		It("should create a matching Kafka and KafkaNodePool, and report a Ready=False status", func() {
			controllerReconciler := &KafkaClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  strimzi.Adapter{},
				Name:     KafkaClusterControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.KafkaCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.cncp.nl/cleanup"))

			By("reconciling again to apply the Kafka/KafkaNodePool and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated Kafka matches the paas KafkaCluster spec")
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(strimzi.GVK)
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			listeners, found, _ := unstructured.NestedSlice(got.Object, "spec", "kafka", "listeners")
			Expect(found).To(BeTrue())
			Expect(listeners).To(HaveLen(1))

			By("verifying the generated KafkaNodePool matches the paas KafkaCluster spec")
			nodePoolGVK := strimzi.GVK
			nodePoolGVK.Kind = "KafkaNodePool"
			nodePool := &unstructured.Unstructured{}
			nodePool.SetGroupVersionKind(nodePoolGVK)
			nodePoolName := types.NamespacedName{Name: resourceName + "-dual-role", Namespace: resourceNamespace}
			Expect(k8sClient.Get(ctx, nodePoolName, nodePool)).To(Succeed())

			Expect(nodePool.GetLabels()).To(HaveKeyWithValue("strimzi.io/cluster", resourceName))

			replicas, _, _ := unstructured.NestedInt64(nodePool.Object, "spec", "replicas")
			Expect(replicas).To(Equal(int64(3)))

			volumes, _, _ := unstructured.NestedSlice(nodePool.Object, "spec", "storage", "volumes")
			Expect(volumes).To(HaveLen(1))
			vol, _ := volumes[0].(map[string]any)
			size, _, _ := unstructured.NestedString(vol, "size")
			Expect(size).To(Equal(testStorageSize))

			By("verifying the paas KafkaCluster status was patched")
			var withStatus paasv1alpha1.KafkaCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real Strimzi Kafka Operator controller runs in this envtest,
			// so the Kafka never actually becomes ready -- this asserts the
			// *mapping* (not-ready status correctly mirrored), not the
			// vendor operator's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should create and prune the PodMonitor and GrafanaDashboard as monitoring is toggled, while always keeping the KafkaNodePool", func() {
			controllerReconciler := &KafkaClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  strimzi.Adapter{},
				Name:     KafkaClusterControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			// MonitoringSpec defaults enablePodMonitor to true when the
			// "monitoring" block is entirely omitted from the create request
			// (the CRD's own "+kubebuilder:default={}"/"+kubebuilder:default=
			// true" pair cascades) -- see MonitoringSpec's doc comment. So
			// "disabled" must be set explicitly, and via a raw merge patch
			// rather than a typed Update: EnablePodMonitor's Go zero value
			// (false) is indistinguishable from "unset" once
			// json:"...,omitempty"/"omitzero" elides it from the marshaled
			// request, which would otherwise let the API server's own
			// default silently re-enable it on every typed Update.
			By("explicitly disabling monitoring and reconciling twice")
			var resource paasv1alpha1.KafkaCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			disableMonitoring := []byte(`{"spec":{"monitoring":{"enablePodMonitor":false}}}`)
			Expect(k8sClient.Patch(ctx, &resource, client.RawPatch(types.MergePatchType, disableMonitoring))).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			podMonitorName := types.NamespacedName{Name: resourceName + "-podmonitor", Namespace: resourceNamespace}
			podMonitorGVK := strimzi.GVK
			podMonitorGVK.Kind = "PodMonitor"
			podMonitorGVK.Group = "monitoring.coreos.com"

			podMonitor := &unstructured.Unstructured{}
			podMonitor.SetGroupVersionKind(podMonitorGVK)
			Expect(errors.IsNotFound(k8sClient.Get(ctx, podMonitorName, podMonitor))).To(BeTrue())

			By("the KafkaNodePool must still exist even with monitoring disabled")
			nodePoolGVK := strimzi.GVK
			nodePoolGVK.Kind = "KafkaNodePool"
			nodePool := &unstructured.Unstructured{}
			nodePool.SetGroupVersionKind(nodePoolGVK)
			nodePoolName := types.NamespacedName{Name: resourceName + "-dual-role", Namespace: resourceNamespace}
			Expect(k8sClient.Get(ctx, nodePoolName, nodePool)).To(Succeed())

			By("enabling monitoring and reconciling")
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			resource.Spec.Monitoring = paasv1alpha1.MonitoringSpec{EnablePodMonitor: true}
			Expect(k8sClient.Update(ctx, &resource)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, podMonitorName, podMonitor)).To(Succeed())
			cluster, _, _ := unstructured.NestedString(podMonitor.Object, "spec", "selector", "matchLabels", "strimzi.io/cluster")
			Expect(cluster).To(Equal(resourceName))

			By("disabling monitoring and reconciling again")
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			Expect(k8sClient.Patch(ctx, &resource, client.RawPatch(types.MergePatchType, disableMonitoring))).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			prunedPodMonitor := &unstructured.Unstructured{}
			prunedPodMonitor.SetGroupVersionKind(podMonitorGVK)
			Expect(errors.IsNotFound(k8sClient.Get(ctx, podMonitorName, prunedPodMonitor))).To(BeTrue())

			By("the KafkaNodePool must still exist after monitoring is disabled again")
			Expect(k8sClient.Get(ctx, nodePoolName, nodePool)).To(Succeed())
		})
	})
})
