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

package mariadb

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
)

// baseSpec returns a minimal, valid MariaDBClusterSpec shared across this
// file's tests as a starting point.
func baseSpec() paasv1alpha1.MariaDBClusterSpec {
	return paasv1alpha1.MariaDBClusterSpec{
		Replicas: 1,
		Storage:  paasv1alpha1.StorageSpec{Size: "1Gi"},
		Database: paasv1alpha1.DatabaseSpec{Name: "app", Owner: "app"},
	}
}

func TestBuildManifestMonitoring(t *testing.T) {
	base := baseSpec()

	t.Run("disabled leaves spec.metrics unset", func(t *testing.T) {
		cr := &paasv1alpha1.MariaDBCluster{Spec: base}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "metrics"); found {
			t.Fatalf("expected no spec.metrics when EnablePodMonitor is unset")
		}
	})

	t.Run("enabled sets namespace-scoped ServiceMonitor", func(t *testing.T) {
		spec := base
		spec.Monitoring = paasv1alpha1.MonitoringSpec{EnablePodMonitor: true}
		cr := &paasv1alpha1.MariaDBCluster{Spec: spec}

		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		enabled, found, err := unstructured.NestedBool(u.Object, "spec", "metrics", "enabled")
		if err != nil {
			t.Fatalf("NestedBool: %v", err)
		}
		if !found || !enabled {
			t.Fatalf("expected spec.metrics.enabled=true, got found=%v enabled=%v", found, enabled)
		}

		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "metrics", "serviceMonitor"); !found {
			t.Fatalf("expected spec.metrics.serviceMonitor to be set")
		}

		// mariadb-operator always creates the ServiceMonitor in the same
		// namespace as the MariaDB itself, so the generated MariaDB's own
		// namespace is what makes this monitoring namespace-scoped.
		if ns := u.GetNamespace(); ns != "default" {
			t.Fatalf("expected MariaDB namespace %q, got %q", "default", ns)
		}
	})
}

func TestBuildManifestExpose(t *testing.T) {
	base := baseSpec()

	t.Run("unset leaves spec.service unset", func(t *testing.T) {
		cr := &paasv1alpha1.MariaDBCluster{Spec: base}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "service"); found {
			t.Fatalf("expected no spec.service when Expose is unset")
		}
	})

	t.Run("set changes the primary Service's type", func(t *testing.T) {
		spec := base
		spec.Expose = &paasv1alpha1.ServiceExposeSpec{
			Type:        "LoadBalancer",
			Annotations: map[string]string{"cloud.example.com/lb": "internal"},
		}
		cr := &paasv1alpha1.MariaDBCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		svcType, _, _ := unstructured.NestedString(u.Object, "spec", "service", "type")
		if svcType != "LoadBalancer" {
			t.Fatalf("expected spec.service.type LoadBalancer, got %q", svcType)
		}
		ann, _, _ := unstructured.NestedString(u.Object, "spec", "service", "metadata", "annotations", "cloud.example.com/lb")
		if ann != "internal" {
			t.Fatalf("expected annotation to be copied through, got %q", ann)
		}
	})
}

func TestExtraResourcesDashboard(t *testing.T) {
	t.Run("absent when monitoring is disabled", func(t *testing.T) {
		cr := &paasv1alpha1.MariaDBCluster{Spec: baseSpec()}

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
		cr := &paasv1alpha1.MariaDBCluster{Spec: spec}

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

func TestBuildManifestSecretNamesAreKindScoped(t *testing.T) {
	cr := &paasv1alpha1.MariaDBCluster{Spec: baseSpec()}
	u := Adapter{}.BuildManifest(cr, "test", "default", "test")

	appSecret, _, _ := unstructured.NestedString(u.Object, "spec", "passwordSecretKeyRef", "name")
	if want := "test-mariadb-app"; appSecret != want {
		t.Fatalf("expected passwordSecretKeyRef.name %q, got %q", want, appSecret)
	}

	rootSecret, _, _ := unstructured.NestedString(u.Object, "spec", "rootPasswordSecretKeyRef", "name")
	if want := "test-mariadb-root"; rootSecret != want {
		t.Fatalf("expected rootPasswordSecretKeyRef.name %q, got %q", want, rootSecret)
	}
}
