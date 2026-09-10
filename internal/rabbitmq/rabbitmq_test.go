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

package rabbitmq

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
	"github.com/bartvanbenthem/paas-operator/internal/ingress"
)

func TestExtraResourcesIngress(t *testing.T) {
	baseSpec := paasv1alpha1.RabbitMQClusterSpec{
		Replicas: 1,
		Storage:  paasv1alpha1.StorageSpec{Size: "1Gi"},
	}

	t.Run("unset returns an absent entry", func(t *testing.T) {
		cr := &paasv1alpha1.RabbitMQCluster{Spec: baseSpec}

		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")
		if len(extras) != 3 {
			t.Fatalf("expected 3 extras, got %d", len(extras))
		}
		if extras[0].Desired != nil {
			t.Fatalf("expected Desired to be nil when Ingress is unset")
		}
		if extras[0].GVK != ingress.GVK {
			t.Fatalf("expected GVK %v, got %v", ingress.GVK, extras[0].GVK)
		}
		if extras[0].Name != "test-rabbitmq-ingress" {
			t.Fatalf("expected name test-rabbitmq-ingress, got %q", extras[0].Name)
		}
	})

	t.Run("set routes to the RabbitmqCluster's own Service on the management port", func(t *testing.T) {
		spec := baseSpec
		spec.Ingress = &paasv1alpha1.IngressSpec{Host: "rabbitmq.example.com"}
		cr := &paasv1alpha1.RabbitMQCluster{Spec: spec}

		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")
		if len(extras) != 3 || extras[0].Desired == nil {
			t.Fatalf("expected the Ingress extra to be desired, got %+v", extras)
		}

		rules, _, _ := unstructured.NestedSlice(extras[0].Desired.Object, "spec", "rules")
		rule, _ := rules[0].(map[string]any)
		paths, _, _ := unstructured.NestedSlice(rule, "http", "paths")
		path, _ := paths[0].(map[string]any)

		backendName, _, _ := unstructured.NestedString(path, "backend", "service", "name")
		backendPort, _, _ := unstructured.NestedInt64(path, "backend", "service", "port", "number")
		if backendName != "test" {
			t.Fatalf("expected Ingress backend service name test (the RabbitmqCluster's own Service), got %q", backendName)
		}
		if backendPort != managementPort {
			t.Fatalf("expected Ingress backend port %d, got %d", managementPort, backendPort)
		}
	})
}

func TestRequestedIngressClassName(t *testing.T) {
	baseSpec := paasv1alpha1.RabbitMQClusterSpec{
		Replicas: 1,
		Storage:  paasv1alpha1.StorageSpec{Size: "1Gi"},
	}

	t.Run("unset Ingress reports not requested", func(t *testing.T) {
		cr := &paasv1alpha1.RabbitMQCluster{Spec: baseSpec}
		class, requested := Adapter{}.RequestedIngressClassName(cr)
		if requested || class != "" {
			t.Fatalf("expected (\"\", false), got (%q, %v)", class, requested)
		}
	})

	t.Run("Ingress set with an unset class reports requested with an empty class", func(t *testing.T) {
		spec := baseSpec
		spec.Ingress = &paasv1alpha1.IngressSpec{Host: "rabbitmq.example.com"}
		cr := &paasv1alpha1.RabbitMQCluster{Spec: spec}
		class, requested := Adapter{}.RequestedIngressClassName(cr)
		if !requested || class != "" {
			t.Fatalf("expected (\"\", true), got (%q, %v)", class, requested)
		}
	})

	t.Run("SetIngressClassName sets it in place for ExtraResources to pick up", func(t *testing.T) {
		spec := baseSpec
		spec.Ingress = &paasv1alpha1.IngressSpec{Host: "rabbitmq.example.com"}
		cr := &paasv1alpha1.RabbitMQCluster{Spec: spec}
		Adapter{}.SetIngressClassName(cr, "haproxy")

		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")
		class, _, _ := unstructured.NestedString(extras[0].Desired.Object, "spec", "ingressClassName")
		if class != "haproxy" {
			t.Fatalf("expected ingressClassName haproxy, got %q", class)
		}
	})
}

func TestBuildManifestExpose(t *testing.T) {
	baseSpec := paasv1alpha1.RabbitMQClusterSpec{
		Replicas: 1,
		Storage:  paasv1alpha1.StorageSpec{Size: "1Gi"},
	}

	t.Run("unset leaves spec.service unset", func(t *testing.T) {
		cr := &paasv1alpha1.RabbitMQCluster{Spec: baseSpec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "service"); found {
			t.Fatalf("expected no spec.service when Expose is unset")
		}
	})

	t.Run("set fills the underlying RabbitmqCluster's own spec.service", func(t *testing.T) {
		spec := baseSpec
		spec.Expose = &paasv1alpha1.ServiceExposeSpec{
			Type:        "LoadBalancer",
			Annotations: map[string]string{"service.beta.kubernetes.io/foo": "bar"},
		}
		cr := &paasv1alpha1.RabbitMQCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		svcType, _, _ := unstructured.NestedString(u.Object, "spec", "service", "type")
		if svcType != "LoadBalancer" {
			t.Fatalf("expected spec.service.type LoadBalancer, got %q", svcType)
		}

		ann, _, _ := unstructured.NestedString(u.Object, "spec", "service", "annotations", "service.beta.kubernetes.io/foo")
		if ann != "bar" {
			t.Fatalf("expected annotation to be copied through, got %q", ann)
		}
	})
}

func TestExtraResourcesMonitoring(t *testing.T) {
	baseSpec := paasv1alpha1.RabbitMQClusterSpec{
		Replicas: 1,
		Storage:  paasv1alpha1.StorageSpec{Size: "1Gi"},
	}

	t.Run("absent when monitoring is disabled", func(t *testing.T) {
		cr := &paasv1alpha1.RabbitMQCluster{Spec: baseSpec}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")
		for _, extra := range extras[1:] {
			if extra.Desired != nil {
				t.Fatalf("expected %q to be absent when monitoring is disabled", extra.Name)
			}
		}
	})

	t.Run("built when monitoring is enabled", func(t *testing.T) {
		spec := baseSpec
		spec.Monitoring.EnablePodMonitor = true
		cr := &paasv1alpha1.RabbitMQCluster{Spec: spec}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")

		sm := extras[1]
		if sm.GVK != serviceMonitorGVK {
			t.Fatalf("expected GVK %v, got %v", serviceMonitorGVK, sm.GVK)
		}
		if sm.Desired == nil {
			t.Fatalf("expected the ServiceMonitor to be desired")
		}
		selector, _, _ := unstructured.NestedString(sm.Desired.Object, "spec", "selector", "matchLabels", "app.kubernetes.io/name")
		if selector != "test" {
			t.Fatalf("expected selector app.kubernetes.io/name=test, got %q", selector)
		}
		endpoints, _, _ := unstructured.NestedSlice(sm.Desired.Object, "spec", "endpoints")
		endpoint, _ := endpoints[0].(map[string]any)
		port, _, _ := unstructured.NestedInt64(endpoint, "targetPort")
		if port != metricsPort {
			t.Fatalf("expected targetPort %d, got %d", metricsPort, port)
		}

		dash := extras[2]
		if dash.GVK != dashboardGVK {
			t.Fatalf("expected GVK %v, got %v", dashboardGVK, dash.GVK)
		}
		if dash.Desired == nil {
			t.Fatalf("expected the GrafanaDashboard to be desired")
		}
		scope, _, _ := unstructured.NestedString(dash.Desired.Object, "spec", "instanceSelector", "matchLabels", grafana.ScopeLabel)
		if scope != "team-a" {
			t.Fatalf("expected instanceSelector to match scope label team-a, got %q", scope)
		}
	})
}
