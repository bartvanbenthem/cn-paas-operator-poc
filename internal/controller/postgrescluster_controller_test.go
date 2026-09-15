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
	"github.com/bartvanbenthem/paas-operator/internal/cnpg"
)

var _ = Describe("PostgresCluster Controller", func() {
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
			By("creating the custom resource for the Kind PostgresCluster")
			postgrescluster := &paasv1alpha1.PostgresCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, postgrescluster)
			if err != nil && errors.IsNotFound(err) {
				resource := &paasv1alpha1.PostgresCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: paasv1alpha1.PostgresClusterSpec{
						Instances: 3,
						Storage: paasv1alpha1.StorageSpec{
							Size: testStorageSize,
						},
						Database: paasv1alpha1.DatabaseSpec{
							Name:  testDBName,
							Owner: testDBName,
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance PostgresCluster")
			resource := &paasv1alpha1.PostgresCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				// Reconcile the deletion path so the finalizer is removed and
				// the CR actually disappears, instead of being left stuck
				// terminating for the next test.
				controllerReconciler := &PostgresClusterReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: events.NewFakeRecorder(10),
					Adapter:  cnpg.Adapter{},
					Name:     PostgresClusterControllerName,
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			}

			cnpgCluster := &unstructured.Unstructured{}
			cnpgCluster.SetGroupVersionKind(cnpg.GVK)
			cnpgCluster.SetName(resourceName)
			cnpgCluster.SetNamespace(resourceNamespace)
			_ = k8sClient.Delete(ctx, cnpgCluster)
		})

		It("should create a matching CNPG Cluster and report a Ready=False status", func() {
			controllerReconciler := &PostgresClusterReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
				Adapter:  cnpg.Adapter{},
				Name:     PostgresClusterControllerName,
			}

			By("reconciling once to attach the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			var withFinalizer paasv1alpha1.PostgresCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withFinalizer)).To(Succeed())
			Expect(withFinalizer.Finalizers).To(ContainElement("paas.cncp.nl/cleanup"))

			By("reconciling again to apply the CNPG Cluster and patch status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated CNPG Cluster matches the PostgresCluster spec")
			got, err := cnpg.GetCluster(ctx, k8sClient, resourceNamespace, resourceName)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).NotTo(BeNil())

			instances, _, _ := unstructured.NestedInt64(got.Object, "spec", "instances")
			Expect(instances).To(Equal(int64(3)))

			size, _, _ := unstructured.NestedString(got.Object, "spec", "storage", "size")
			Expect(size).To(Equal(testStorageSize))

			dbName, _, _ := unstructured.NestedString(got.Object, "spec", "bootstrap", "initdb", "database")
			Expect(dbName).To(Equal(testDBName))

			By("verifying the PostgresCluster status was patched")
			var withStatus paasv1alpha1.PostgresCluster
			Expect(k8sClient.Get(ctx, typeNamespacedName, &withStatus)).To(Succeed())
			// No real CNPG controller runs in this envtest, so the Cluster
			// never actually becomes ready — this asserts the *mapping*
			// (not-ready status correctly mirrored), not CNPG's own behavior.
			Expect(withStatus.Status.Ready).To(BeFalse())
			cond := meta.FindStatusCondition(withStatus.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})
	})
})
