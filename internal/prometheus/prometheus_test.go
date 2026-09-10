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

// testName is the CR/target name used throughout this file's tests.
const testName = "test"

func TestBuildManifestNamespaceScoped(t *testing.T) {
	cr := &paasv1alpha1.PrometheusInstance{
		Spec: paasv1alpha1.PrometheusInstanceSpec{Replicas: 1},
	}

	u := Adapter{}.BuildManifest(cr, testName, "monitoring-ns", testName)

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

	u := Adapter{}.BuildManifest(cr, testName, "default", testName)

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

		extras := Adapter{}.ExtraResources(cr, testName, "default", testName)

		if len(extras) != 5 {
			t.Fatalf("expected 5 extras, got %d", len(extras))
		}
		if extras[0].Name != "test-web" || extras[1].Name != "test-prometheus-ingress" {
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

		extras := Adapter{}.ExtraResources(cr, testName, "default", testName)
		if len(extras) != 5 {
			t.Fatalf("expected 5 extras, got %d", len(extras))
		}

		svc := extras[0]
		if svc.Desired == nil {
			t.Fatalf("expected the Service to be desired")
		}
		selector, _, _ := unstructured.NestedString(svc.Desired.Object, "spec", "selector", "operator.prometheus.io/name")
		if selector != testName {
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

func TestRequestedIngressClassName(t *testing.T) {
	t.Run("unset Ingress reports not requested", func(t *testing.T) {
		cr := &paasv1alpha1.PrometheusInstance{}
		class, requested := Adapter{}.RequestedIngressClassName(cr)
		if requested || class != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", class, requested)
		}
	})

	t.Run("Ingress set with an unset class reports requested with an empty class", func(t *testing.T) {
		cr := &paasv1alpha1.PrometheusInstance{Spec: paasv1alpha1.PrometheusInstanceSpec{
			Ingress: &paasv1alpha1.IngressSpec{Host: "prometheus.example.com"},
		}}
		class, requested := Adapter{}.RequestedIngressClassName(cr)
		if !requested || class != "" {
			t.Fatalf("expected (\"\", true), got (%q, %v)", class, requested)
		}
	})

	t.Run("SetIngressClassName sets it in place for ExtraResources to pick up", func(t *testing.T) {
		cr := &paasv1alpha1.PrometheusInstance{Spec: paasv1alpha1.PrometheusInstanceSpec{
			Replicas: 1,
			Ingress:  &paasv1alpha1.IngressSpec{Host: "prometheus.example.com"},
		}}
		Adapter{}.SetIngressClassName(cr, "haproxy")

		extras := Adapter{}.ExtraResources(cr, testName, "default", testName)
		class, _, _ := unstructured.NestedString(extras[1].Desired.Object, "spec", "ingressClassName")
		if class != "haproxy" {
			t.Fatalf("expected ingressClassName haproxy, got %q", class)
		}
	})
}

func TestExtraResourcesExpose(t *testing.T) {
	t.Run("unset leaves the Service type ClusterIP", func(t *testing.T) {
		cr := &paasv1alpha1.PrometheusInstance{Spec: paasv1alpha1.PrometheusInstanceSpec{Replicas: 1}}

		extras := Adapter{}.ExtraResources(cr, testName, "default", testName)

		svcType, _, _ := unstructured.NestedString(extras[0].Desired.Object, "spec", "type")
		if svcType != "ClusterIP" {
			t.Fatalf("expected Service type ClusterIP, got %q", svcType)
		}
	})

	t.Run("set overrides the Service type and copies annotations", func(t *testing.T) {
		cr := &paasv1alpha1.PrometheusInstance{
			Spec: paasv1alpha1.PrometheusInstanceSpec{
				Replicas: 1,
				Expose: &paasv1alpha1.ServiceExposeSpec{
					Type:        "LoadBalancer",
					Annotations: map[string]string{"service.beta.kubernetes.io/foo": "bar"},
				},
			},
		}

		extras := Adapter{}.ExtraResources(cr, testName, "default", testName)

		svc := extras[0]
		svcType, _, _ := unstructured.NestedString(svc.Desired.Object, "spec", "type")
		if svcType != "LoadBalancer" {
			t.Fatalf("expected Service type LoadBalancer, got %q", svcType)
		}
		if ann := svc.Desired.GetAnnotations()["service.beta.kubernetes.io/foo"]; ann != "bar" {
			t.Fatalf("expected annotation to be copied through, got %q", ann)
		}
	})
}

func TestBuildManifestServiceAccountName(t *testing.T) {
	cr := &paasv1alpha1.PrometheusInstance{Spec: paasv1alpha1.PrometheusInstanceSpec{Replicas: 1}}
	u := Adapter{}.BuildManifest(cr, testName, "default", testName)

	sa, _, _ := unstructured.NestedString(u.Object, "spec", "serviceAccountName")
	if sa != testName {
		t.Fatalf("expected spec.serviceAccountName %q, got %q", testName, sa)
	}
}

func TestExtraResourcesScrapeRBAC(t *testing.T) {
	cr := &paasv1alpha1.PrometheusInstance{Spec: paasv1alpha1.PrometheusInstanceSpec{Replicas: 1}}

	extras := Adapter{}.ExtraResources(cr, testName, "default", testName)
	if len(extras) != 5 {
		t.Fatalf("expected 5 extras, got %d", len(extras))
	}

	sa, role, roleBinding := extras[2], extras[3], extras[4]

	if sa.GVK != serviceAccountGVK || sa.Name != testName || sa.Desired == nil {
		t.Fatalf("unexpected ServiceAccount extra: %+v", sa)
	}

	if role.GVK != roleGVK || role.Name != testName || role.Desired == nil {
		t.Fatalf("unexpected Role extra: %+v", role)
	}
	rules, _, _ := unstructured.NestedSlice(role.Desired.Object, "rules")
	if len(rules) != 1 {
		t.Fatalf("expected exactly one rule, got %d", len(rules))
	}
	rule, _ := rules[0].(map[string]any)
	resources, _, _ := unstructured.NestedStringSlice(rule, "resources")
	wantResources := []string{"pods", "services", "endpoints"}
	if len(resources) != len(wantResources) {
		t.Fatalf("expected resources %v, got %v", wantResources, resources)
	}
	for i, r := range wantResources {
		if resources[i] != r {
			t.Fatalf("expected resources %v, got %v", wantResources, resources)
		}
	}
	verbs, _, _ := unstructured.NestedStringSlice(rule, "verbs")
	wantVerbs := []string{"get", "list", "watch"}
	if len(verbs) != len(wantVerbs) {
		t.Fatalf("expected verbs %v, got %v", wantVerbs, verbs)
	}

	if roleBinding.GVK != roleBindingGVK || roleBinding.Name != testName || roleBinding.Desired == nil {
		t.Fatalf("unexpected RoleBinding extra: %+v", roleBinding)
	}
	roleRefName, _, _ := unstructured.NestedString(roleBinding.Desired.Object, "roleRef", "name")
	if roleRefName != testName {
		t.Fatalf("expected roleRef.name %q, got %q", testName, roleRefName)
	}
	subjects, _, _ := unstructured.NestedSlice(roleBinding.Desired.Object, "subjects")
	if len(subjects) != 1 {
		t.Fatalf("expected exactly one subject, got %d", len(subjects))
	}
	subject, _ := subjects[0].(map[string]any)
	subjectName, _, _ := unstructured.NestedString(subject, "name")
	subjectNamespace, _, _ := unstructured.NestedString(subject, "namespace")
	if subjectName != testName || subjectNamespace != "default" {
		t.Fatalf("expected subject test/default, got %s/%s", subjectNamespace, subjectName)
	}
}
