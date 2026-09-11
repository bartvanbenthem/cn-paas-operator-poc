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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
)

var _ = Describe("PrometheusInstance Webhook", func() {
	var namespace *corev1.Namespace

	newPrometheusInstance := func(name string) *paasv1alpha1.PrometheusInstance {
		return &paasv1alpha1.PrometheusInstance{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace.Name},
		}
	}

	BeforeEach(func() {
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus-webhook-" + rand.String(8)},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	})

	Context("When creating PrometheusInstance under the singleton-per-namespace webhook", func() {
		It("should admit the first PrometheusInstance in a namespace", func() {
			Expect(k8sClient.Create(ctx, newPrometheusInstance("primary"))).To(Succeed())
		})

		It("should reject a second PrometheusInstance in the same namespace", func() {
			Expect(k8sClient.Create(ctx, newPrometheusInstance("primary"))).To(Succeed())

			err := k8sClient.Create(ctx, newPrometheusInstance("secondary"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one PrometheusInstance is allowed per namespace"))
		})

		It("should admit a PrometheusInstance in a different namespace", func() {
			Expect(k8sClient.Create(ctx, newPrometheusInstance("primary"))).To(Succeed())

			other := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "prometheus-webhook-" + rand.String(8)},
			}
			Expect(k8sClient.Create(ctx, other)).To(Succeed())

			otherInstance := newPrometheusInstance("primary")
			otherInstance.Namespace = other.Name
			Expect(k8sClient.Create(ctx, otherInstance)).To(Succeed())
		})
	})
})
