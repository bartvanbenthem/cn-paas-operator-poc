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

package cnpg

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
)

// baseSpec returns a minimal, valid PostgresClusterSpec shared across this
// file's tests as a starting point.
func baseSpec() paasv1alpha1.PostgresClusterSpec {
	return paasv1alpha1.PostgresClusterSpec{
		Instances: 1,
		Storage:   paasv1alpha1.StorageSpec{Size: "1Gi"},
		Database:  paasv1alpha1.DatabaseSpec{Name: "app", Owner: "app"},
	}
}

func TestBuildManifestMonitoring(t *testing.T) {
	spec := baseSpec()

	t.Run("disabled by default", func(t *testing.T) {
		cr := &paasv1alpha1.PostgresCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "monitoring"); found {
			t.Fatalf("expected no spec.monitoring when EnablePodMonitor is unset")
		}
	})

	t.Run("enabled sets namespace-scoped PodMonitor", func(t *testing.T) {
		spec := spec
		spec.Monitoring = paasv1alpha1.MonitoringSpec{EnablePodMonitor: true}
		cr := &paasv1alpha1.PostgresCluster{Spec: spec}

		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		enabled, found, err := unstructured.NestedBool(u.Object, "spec", "monitoring", "enablePodMonitor")
		if err != nil {
			t.Fatalf("NestedBool: %v", err)
		}
		if !found || !enabled {
			t.Fatalf("expected spec.monitoring.enablePodMonitor=true, got found=%v enabled=%v", found, enabled)
		}

		// CNPG always creates the PodMonitor in the same namespace as the
		// Cluster itself, so the generated Cluster's own namespace is what
		// makes this monitoring namespace-scoped.
		if ns := u.GetNamespace(); ns != "default" {
			t.Fatalf("expected Cluster namespace %q, got %q", "default", ns)
		}
	})
}

func TestBuildManifestExpose(t *testing.T) {
	spec := baseSpec()

	t.Run("unset leaves spec.managed unset", func(t *testing.T) {
		cr := &paasv1alpha1.PostgresCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "managed"); found {
			t.Fatalf("expected no spec.managed when Expose is unset")
		}
	})

	t.Run("set adds an rw additional service", func(t *testing.T) {
		spec := spec
		spec.Expose = &paasv1alpha1.ServiceExposeSpec{Type: "LoadBalancer"}
		cr := &paasv1alpha1.PostgresCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		additional, found, err := unstructured.NestedSlice(u.Object, "spec", "managed", "services", "additional")
		if err != nil || !found || len(additional) != 1 {
			t.Fatalf("expected exactly one spec.managed.services.additional entry, found=%v err=%v len=%d", found, err, len(additional))
		}
		entry, ok := additional[0].(map[string]any)
		if !ok {
			t.Fatalf("expected additional[0] to be a map, got %T", additional[0])
		}
		if selectorType, _, _ := unstructured.NestedString(entry, "selectorType"); selectorType != "rw" {
			t.Fatalf("expected selectorType rw, got %q", selectorType)
		}
		name, _, _ := unstructured.NestedString(entry, "serviceTemplate", "metadata", "name")
		if name != "test-external" {
			t.Fatalf("expected serviceTemplate name test-external, got %q", name)
		}
		svcType, _, _ := unstructured.NestedString(entry, "serviceTemplate", "spec", "type")
		if svcType != "LoadBalancer" {
			t.Fatalf("expected serviceTemplate.spec.type LoadBalancer, got %q", svcType)
		}
	})
}

func TestExtraResourcesDashboard(t *testing.T) {
	t.Run("absent when monitoring is disabled", func(t *testing.T) {
		cr := &paasv1alpha1.PostgresCluster{Spec: baseSpec()}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")
		if len(extras) != 1 {
			t.Fatalf("expected 1 extra, got %d", len(extras))
		}
		if extras[0].Desired != nil {
			t.Fatalf("expected the GrafanaDashboard to be absent when monitoring is disabled")
		}
	})

	t.Run("built when monitoring is enabled", func(t *testing.T) {
		spec := baseSpec()
		spec.Monitoring.EnablePodMonitor = true
		cr := &paasv1alpha1.PostgresCluster{Spec: spec}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")
		dash := extras[0]
		if dash.Desired == nil {
			t.Fatalf("expected the GrafanaDashboard to be desired")
		}
		if dash.GVK.Kind != "GrafanaDashboard" {
			t.Fatalf("expected Kind GrafanaDashboard, got %q", dash.GVK.Kind)
		}

		scope, _, _ := unstructured.NestedString(dash.Desired.Object, "spec", "instanceSelector", "matchLabels", grafana.ScopeLabel)
		if scope != "team-a" {
			t.Fatalf("expected instanceSelector to match scope label team-a, got %q", scope)
		}

		json, found, _ := unstructured.NestedString(dash.Desired.Object, "spec", "json")
		if !found || json == "" {
			t.Fatalf("expected spec.json to be populated with the embedded dashboard")
		}

		datasources, _, _ := unstructured.NestedSlice(dash.Desired.Object, "spec", "datasources")
		if len(datasources) != 1 {
			t.Fatalf("expected exactly one datasource mapping, got %d", len(datasources))
		}
		entry, _ := datasources[0].(map[string]any)
		if name, _, _ := unstructured.NestedString(entry, "datasourceName"); name != grafana.DatasourceUID {
			t.Fatalf("expected datasourceName %q, got %q", grafana.DatasourceUID, name)
		}
	})
}
