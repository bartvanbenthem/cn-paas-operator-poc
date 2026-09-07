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

package valkey

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
)

// baseSpec returns a minimal, valid ValkeyClusterSpec shared across this
// file's tests as a starting point.
func baseSpec() paasv1alpha1.ValkeyClusterSpec {
	return paasv1alpha1.ValkeyClusterSpec{
		Shards:      1,
		Persistence: paasv1alpha1.PersistenceSpec{Size: "1Gi"},
	}
}

func TestExtraResourcesExpose(t *testing.T) {
	t.Run("unset returns an absent entry", func(t *testing.T) {
		cr := &paasv1alpha1.ValkeyCluster{Spec: baseSpec()}

		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")
		if len(extras) != 3 {
			t.Fatalf("expected 3 extras, got %d", len(extras))
		}
		if extras[0].Desired != nil {
			t.Fatalf("expected Desired to be nil when Expose is unset")
		}
		if extras[0].Name != "test-external" {
			t.Fatalf("expected name test-external, got %q", extras[0].Name)
		}
	})

	t.Run("set mirrors valkey-operator's own cluster-selector label", func(t *testing.T) {
		spec := baseSpec()
		spec.Expose = &paasv1alpha1.ServiceExposeSpec{Type: "LoadBalancer"}
		cr := &paasv1alpha1.ValkeyCluster{Spec: spec}

		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")
		if len(extras) != 3 || extras[0].Desired == nil {
			t.Fatalf("expected the Service extra to be desired, got %+v", extras)
		}

		svcType, _, _ := unstructured.NestedString(extras[0].Desired.Object, "spec", "type")
		if svcType != "LoadBalancer" {
			t.Fatalf("expected spec.type LoadBalancer, got %q", svcType)
		}
		selector, _, _ := unstructured.NestedString(extras[0].Desired.Object, "spec", "selector", "valkey.io/cluster")
		if selector != "test" {
			t.Fatalf("expected selector valkey.io/cluster=test, got %q", selector)
		}
	})
}

func TestBuildManifestExporter(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		cr := &paasv1alpha1.ValkeyCluster{Spec: baseSpec()}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		enabled, _, _ := unstructured.NestedBool(u.Object, "spec", "exporter", "enabled")
		if enabled {
			t.Fatalf("expected spec.exporter.enabled=false by default")
		}
	})

	t.Run("enabled when monitoring is requested", func(t *testing.T) {
		spec := baseSpec()
		spec.Monitoring.EnablePodMonitor = true
		cr := &paasv1alpha1.ValkeyCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		enabled, _, _ := unstructured.NestedBool(u.Object, "spec", "exporter", "enabled")
		if !enabled {
			t.Fatalf("expected spec.exporter.enabled=true when EnablePodMonitor is set")
		}
	})
}

func TestExtraResourcesMonitoring(t *testing.T) {
	t.Run("absent when monitoring is disabled", func(t *testing.T) {
		cr := &paasv1alpha1.ValkeyCluster{Spec: baseSpec()}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")
		for _, extra := range extras[1:] {
			if extra.Desired != nil {
				t.Fatalf("expected %q to be absent when monitoring is disabled", extra.Name)
			}
		}
	})

	t.Run("built when monitoring is enabled", func(t *testing.T) {
		spec := baseSpec()
		spec.Monitoring.EnablePodMonitor = true
		cr := &paasv1alpha1.ValkeyCluster{Spec: spec}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")

		pm := extras[1]
		if pm.GVK != podMonitorGVK {
			t.Fatalf("expected GVK %v, got %v", podMonitorGVK, pm.GVK)
		}
		if pm.Desired == nil {
			t.Fatalf("expected the PodMonitor to be desired")
		}
		selector, _, _ := unstructured.NestedString(pm.Desired.Object, "spec", "selector", "matchLabels", "valkey.io/cluster")
		if selector != "test" {
			t.Fatalf("expected selector valkey.io/cluster=test, got %q", selector)
		}
		endpoints, _, _ := unstructured.NestedSlice(pm.Desired.Object, "spec", "podMetricsEndpoints")
		endpoint, _ := endpoints[0].(map[string]any)
		port, _, _ := unstructured.NestedInt64(endpoint, "targetPort")
		if port != exporterPort {
			t.Fatalf("expected targetPort %d, got %d", exporterPort, port)
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
