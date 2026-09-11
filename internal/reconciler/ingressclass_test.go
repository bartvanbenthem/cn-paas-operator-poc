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

package reconciler

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// testIngressAdapter is a minimal Adapter exercising only
// IngressClassDefaultingAdapter -- it uses *corev1.ConfigMap as a stand-in
// CR (a real, already-registered type) purely so defaultIngressClass/
// resolveDefaultIngressClass can be tested directly against a fake client,
// without needing the full BuildManifest/Server-Side-Apply path a real
// vendor GVK would require. Data["ingressRequested"]=="true" stands in for
// "spec.ingress is set"; Data["ingressClassName"] stands in for
// spec.ingress.ingressClassName.
type testIngressAdapter struct{}

func (testIngressAdapter) GVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
}
func (testIngressAdapter) TargetName(crName string) string { return crName }
func (testIngressAdapter) ObjectKind() string              { return "Test" }
func (testIngressAdapter) FieldManager() string            { return "test" }
func (testIngressAdapter) BuildManifest(_ *corev1.ConfigMap, _, _, _ string) *unstructured.Unstructured {
	return &unstructured.Unstructured{}
}
func (testIngressAdapter) ExtractStatus(_ *unstructured.Unstructured) TargetStatus {
	return TargetStatus{}
}
func (testIngressAdapter) ApplyStatus(_ *corev1.ConfigMap, _ string, _ TargetStatus) string {
	return ""
}

func (testIngressAdapter) RequestedIngressClassName(cr *corev1.ConfigMap) (string, bool) {
	if cr.Data["ingressRequested"] != "true" {
		return "", false
	}
	return cr.Data["ingressClassName"], true
}

func (testIngressAdapter) SetIngressClassName(cr *corev1.ConfigMap, className string) {
	if cr.Data == nil {
		cr.Data = map[string]string{}
	}
	cr.Data["ingressClassName"] = className
}

func newFakeReconciler(t *testing.T, objs ...client.Object) *GenericReconciler[corev1.ConfigMap, *corev1.ConfigMap] {
	t.Helper()
	scheme := clientgoscheme.Scheme
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	return &GenericReconciler[corev1.ConfigMap, *corev1.ConfigMap]{
		Client:  builder.Build(),
		Adapter: testIngressAdapter{},
	}
}

func defaultIngressClassObj(name string, isDefault bool) *networkingv1.IngressClass {
	ic := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if isDefault {
		ic.Annotations = map[string]string{defaultIngressClassAnnotation: "true"}
	}
	return ic
}

func TestDefaultIngressClass(t *testing.T) {
	ctx := context.Background()

	t.Run("no Ingress requested leaves the CR untouched", func(t *testing.T) {
		r := newFakeReconciler(t, defaultIngressClassObj("haproxy", true))
		cr := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "default"}}

		if err := r.defaultIngressClass(ctx, cr); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cr.Data["ingressClassName"] != "" {
			t.Fatalf("expected no ingressClassName set, got %q", cr.Data["ingressClassName"])
		}
	})

	t.Run("Ingress requested with an explicit class is left alone", func(t *testing.T) {
		r := newFakeReconciler(t, defaultIngressClassObj("haproxy", true))
		cr := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "default"},
			Data:       map[string]string{"ingressRequested": "true", "ingressClassName": "nginx"},
		}

		if err := r.defaultIngressClass(ctx, cr); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cr.Data["ingressClassName"] != "nginx" {
			t.Fatalf("expected explicit class nginx to be left alone, got %q", cr.Data["ingressClassName"])
		}
	})

	t.Run("Ingress requested with an unset class resolves the cluster's default", func(t *testing.T) {
		r := newFakeReconciler(t, defaultIngressClassObj("haproxy", true))
		cr := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "default"},
			Data:       map[string]string{"ingressRequested": "true"},
		}

		if err := r.defaultIngressClass(ctx, cr); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cr.Data["ingressClassName"] != "haproxy" {
			t.Fatalf("expected resolved class haproxy, got %q", cr.Data["ingressClassName"])
		}
	})

	t.Run("no default IngressClass in the cluster leaves the class unset", func(t *testing.T) {
		r := newFakeReconciler(t, defaultIngressClassObj("haproxy", false))
		cr := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "default"},
			Data:       map[string]string{"ingressRequested": "true"},
		}

		if err := r.defaultIngressClass(ctx, cr); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := cr.Data["ingressClassName"]; ok {
			t.Fatalf("expected ingressClassName to stay unset, got %q", cr.Data["ingressClassName"])
		}
	})

	t.Run("more than one default IngressClass is ambiguous and resolves nothing", func(t *testing.T) {
		r := newFakeReconciler(t, defaultIngressClassObj("haproxy", true), defaultIngressClassObj("nginx", true))
		cr := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "default"},
			Data:       map[string]string{"ingressRequested": "true"},
		}

		if err := r.defaultIngressClass(ctx, cr); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := cr.Data["ingressClassName"]; ok {
			t.Fatalf("expected ambiguous default to resolve nothing, got %q", cr.Data["ingressClassName"])
		}
	})
}
