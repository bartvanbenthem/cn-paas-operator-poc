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

package prometheus

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
)

func TestBuildManifestNamespaceScoped(t *testing.T) {
	cr := &paasv1alpha1.PrometheusInstance{
		Spec: paasv1alpha1.PrometheusInstanceSpec{Replicas: 1},
	}

	u := Adapter{}.BuildManifest(cr, "test", "monitoring-ns", "test")

	if ns := u.GetNamespace(); ns != "monitoring-ns" {
		t.Fatalf("expected Prometheus namespace %q, got %q", "monitoring-ns", ns)
	}

	// A null/unset namespace selector is how the Prometheus Operator scopes
	// monitor discovery to the Prometheus's own namespace -- these keys must
	// never be set, or discovery would widen beyond that namespace.
	if _, found, _ := unstructured.NestedMap(u.Object, "spec", "serviceMonitorNamespaceSelector"); found {
		t.Fatalf("expected spec.serviceMonitorNamespaceSelector to be unset (namespace-scoped)")
	}
	if _, found, _ := unstructured.NestedMap(u.Object, "spec", "podMonitorNamespaceSelector"); found {
		t.Fatalf("expected spec.podMonitorNamespaceSelector to be unset (namespace-scoped)")
	}

	// The (namespace-scoped) selectors themselves must still be non-nil and
	// empty so every monitor in that single namespace is actually picked up.
	if _, found, _ := unstructured.NestedMap(u.Object, "spec", "serviceMonitorSelector"); !found {
		t.Fatalf("expected spec.serviceMonitorSelector to be set (empty selector)")
	}
	if _, found, _ := unstructured.NestedMap(u.Object, "spec", "podMonitorSelector"); !found {
		t.Fatalf("expected spec.podMonitorSelector to be set (empty selector)")
	}
}

func TestBuildManifestStorage(t *testing.T) {
	cr := &paasv1alpha1.PrometheusInstance{
		Spec: paasv1alpha1.PrometheusInstanceSpec{
			Replicas: 1,
			Storage:  &paasv1alpha1.StorageSpec{Size: "5Gi", StorageClass: "fast"},
		},
	}

	u := Adapter{}.BuildManifest(cr, "test", "default", "test")

	size, _, _ := unstructured.NestedString(u.Object, "spec", "storage", "volumeClaimTemplate", "spec", "resources", "requests", "storage")
	if size != "5Gi" {
		t.Fatalf("expected storage size 5Gi, got %q", size)
	}
	class, _, _ := unstructured.NestedString(u.Object, "spec", "storage", "volumeClaimTemplate", "spec", "storageClassName")
	if class != "fast" {
		t.Fatalf("expected storageClassName fast, got %q", class)
	}
}

func TestExtraResourcesIngress(t *testing.T) {
	t.Run("unset still builds the Service (needed in-cluster regardless of Ingress) but leaves the Ingress absent", func(t *testing.T) {
		cr := &paasv1alpha1.PrometheusInstance{Spec: paasv1alpha1.PrometheusInstanceSpec{Replicas: 1}}

		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")

		if len(extras) != 2 {
			t.Fatalf("expected 2 extras, got %d", len(extras))
		}
		if extras[0].Name != "test-web" || extras[1].Name != "test-ingress" {
			t.Fatalf("unexpected extra names: %q, %q", extras[0].Name, extras[1].Name)
		}
		if extras[0].Desired == nil {
			t.Fatalf("expected the Service to be desired even when Ingress is unset")
		}
		if extras[1].Desired != nil {
			t.Fatalf("expected the Ingress to be absent when Ingress is unset")
		}
	})

	t.Run("set builds a Service and an Ingress routed to it", func(t *testing.T) {
		cr := &paasv1alpha1.PrometheusInstance{
			Spec: paasv1alpha1.PrometheusInstanceSpec{
				Replicas: 1,
				Ingress:  &paasv1alpha1.IngressSpec{Host: "prometheus.example.com"},
			},
		}

		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")
		if len(extras) != 2 {
			t.Fatalf("expected 2 extras, got %d", len(extras))
		}

		svc := extras[0]
		if svc.Desired == nil {
			t.Fatalf("expected the Service to be desired")
		}
		selector, _, _ := unstructured.NestedString(svc.Desired.Object, "spec", "selector", "operator.prometheus.io/name")
		if selector != "test" {
			t.Fatalf("expected selector operator.prometheus.io/name=test, got %q", selector)
		}

		ing := extras[1]
		if ing.Desired == nil {
			t.Fatalf("expected the Ingress to be desired")
		}
		rules, _, _ := unstructured.NestedSlice(ing.Desired.Object, "spec", "rules")
		if len(rules) != 1 {
			t.Fatalf("expected exactly one Ingress rule, got %d", len(rules))
		}
		rule, _ := rules[0].(map[string]any)
		host, _, _ := unstructured.NestedString(rule, "host")
		if host != "prometheus.example.com" {
			t.Fatalf("expected host prometheus.example.com, got %q", host)
		}

		paths, _, _ := unstructured.NestedSlice(rule, "http", "paths")
		path, _ := paths[0].(map[string]any)
		backendName, _, _ := unstructured.NestedString(path, "backend", "service", "name")
		if backendName != "test-web" {
			t.Fatalf("expected Ingress backend service name test-web, got %q", backendName)
		}
	})
}
