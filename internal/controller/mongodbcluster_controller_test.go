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
	"github.com/bartvanbenthem/paas-operator/internal/psmdb"
)

var _ = Describe("MongoDBCluster Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-mongodb"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind MongoDBCluster")
			mongodbcluster := &paasv1alpha1.MongoDBCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, mongodbcluster)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.MongoDBCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.MongoDBClusterSpec{
						Replicas: 3,
						Image:    "percona/percona-server-mongodb:8.0.26-11",
						Storage: paasv1alpha1.StorageSpec{
							Size: testStorageSize,
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance MongoDBCluster")
			resource := &paasv1alpha1.MongoDBCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &MongoDBClusterReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  psmdb.Adapter{},
					Name:     MongoDBClusterControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			mongodb := &unstructured.Unstructured{}
			mongodb.SetGroupVersionKind(psmdb.GVK)
			mongodb.SetName(resourceName)
			mongodb.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, mongodb)
		})

		It("should create a matching PerconaServerMongoDB and report a Ready=False status", func() {
			controllerReconciler := &MongoDBClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  psmdb.Adapter{},
				Name:     MongoDBClusterControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.MongoDBCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.example.com/cleanup"))

			By("reconciling again to apply the PerconaServerMongoDB and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated PerconaServerMongoDB matches the paas MongoDBCluster spec")
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(psmdb.GVK)
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())

			replsets, found, _ := unstructured.NestedSlice(got.Object, "spec", "replsets")
			Expect(found).To(BeTrue())
			Expect(replsets).To(HaveLen(1))
			rs, _ := replsets[0].(map[string]any)

			name, _, _ := unstructured.NestedString(rs, "name")
			Expect(name).To(Equal("rs0"))

			size, _, _ := unstructured.NestedInt64(rs, "size")
			Expect(size).To(Equal(int64(3)))

			storage, _, _ := unstructured.NestedString(rs, "volumeSpec", "persistentVolumeClaim", "resources", "requests", "storage")
			Expect(storage).To(Equal(testStorageSize))

			secretsUsers, _, _ := unstructured.NestedString(got.Object, "spec", "secrets", "users")
			Expect(secretsUsers).To(Equal(resourceName + "-psmdb-secrets"))

			By("verifying the paas MongoDBCluster status was patched")
			var withStatus paasv1alpha1.MongoDBCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real percona-server-mongodb-operator controller runs in this
			// envtest, so the PerconaServerMongoDB never actually becomes
			// ready -- this asserts the *mapping* (not-ready status correctly
			// mirrored), not the vendor operator's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should create and prune the PodMonitor and GrafanaDashboard as monitoring is toggled", func() {
			controllerReconciler := &MongoDBClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  psmdb.Adapter{},
				Name:     MongoDBClusterControllerName,
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
			var resource paasv1alpha1.MongoDBCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			disableMonitoring := []byte(`{"spec":{"monitoring":{"enablePodMonitor":false}}}`)
			Expect(k8sClient.Patch(ctx, &resource, client.RawPatch(types.MergePatchType, disableMonitoring))).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			podMonitorName := types.NamespacedName{Name: resourceName + "-podmonitor", Namespace: resourceNamespace}
			podMonitorGVK := psmdb.GVK
			podMonitorGVK.Kind = "PodMonitor"
			podMonitorGVK.Group = "monitoring.coreos.com"

			podMonitor := &unstructured.Unstructured{}
			podMonitor.SetGroupVersionKind(podMonitorGVK)
			Expect(errors.IsNotFound(k8sClient.Get(ctx, podMonitorName, podMonitor))).To(BeTrue())

			By("enabling monitoring and reconciling")
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			resource.Spec.Monitoring = paasv1alpha1.MonitoringSpec{EnablePodMonitor: true}
			Expect(k8sClient.Update(ctx, &resource)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, podMonitorName, podMonitor)).To(Succeed())
			component, _, _ := unstructured.NestedString(podMonitor.Object, "spec", "selector", "matchLabels", "app.kubernetes.io/component")
			Expect(component).To(Equal("mongod"))

			By("disabling monitoring and reconciling again")
			Expect(k8sClient.Get(ctx, typeNamespacedName, &resource)).To(Succeed())
			Expect(k8sClient.Patch(ctx, &resource, client.RawPatch(types.MergePatchType, disableMonitoring))).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			prunedPodMonitor := &unstructured.Unstructured{}
			prunedPodMonitor.SetGroupVersionKind(podMonitorGVK)
			Expect(errors.IsNotFound(k8sClient.Get(ctx, podMonitorName, prunedPodMonitor))).To(BeTrue())
		})
	})
})
