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

package strimzi

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
)

// baseSpec returns a minimal, valid KafkaClusterSpec shared across this
// file's tests as a starting point.
func baseSpec() paasv1alpha1.KafkaClusterSpec {
	return paasv1alpha1.KafkaClusterSpec{
		Replicas: 1,
		Storage:  paasv1alpha1.StorageSpec{Size: "10Gi"},
	}
}

func TestBuildManifestListeners(t *testing.T) {
	t.Run("only the internal plain listener by default", func(t *testing.T) {
		cr := &paasv1alpha1.KafkaCluster{Spec: baseSpec()}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		listeners, found, _ := unstructured.NestedSlice(u.Object, "spec", "kafka", "listeners")
		if !found || len(listeners) != 1 {
			t.Fatalf("expected exactly one listener, got found=%v len=%d", found, len(listeners))
		}
		l, _ := listeners[0].(map[string]any)
		if name, _, _ := unstructured.NestedString(l, "name"); name != "plain" {
			t.Fatalf("expected listener name %q, got %q", "plain", name)
		}
		if lt, _, _ := unstructured.NestedString(l, "type"); lt != "internal" {
			t.Fatalf("expected listener type %q, got %q", "internal", lt)
		}
		tls, _, _ := unstructured.NestedBool(l, "tls")
		if tls {
			t.Fatalf("expected tls=false on the plain listener")
		}
	})

	t.Run("expose adds a second external listener", func(t *testing.T) {
		spec := baseSpec()
		spec.Expose = &paasv1alpha1.ServiceExposeSpec{Type: "LoadBalancer"}
		cr := &paasv1alpha1.KafkaCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		listeners, _, _ := unstructured.NestedSlice(u.Object, "spec", "kafka", "listeners")
		if len(listeners) != 2 {
			t.Fatalf("expected 2 listeners, got %d", len(listeners))
		}
		l, _ := listeners[1].(map[string]any)
		if name, _, _ := unstructured.NestedString(l, "name"); name != "external" {
			t.Fatalf("expected second listener name %q, got %q", "external", name)
		}
		if lt, _, _ := unstructured.NestedString(l, "type"); lt != "loadbalancer" {
			t.Fatalf("expected listener type %q, got %q", "loadbalancer", lt)
		}
		tls, _, _ := unstructured.NestedBool(l, "tls")
		if !tls {
			t.Fatalf("expected tls=true on the external listener")
		}
	})
}

func TestBuildManifestVersionAndMetrics(t *testing.T) {
	t.Run("version unset leaves spec.kafka.version unset", func(t *testing.T) {
		cr := &paasv1alpha1.KafkaCluster{Spec: baseSpec()}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")
		if _, found, _ := unstructured.NestedString(u.Object, "spec", "kafka", "version"); found {
			t.Fatalf("expected no spec.kafka.version when Version is unset")
		}
	})

	t.Run("version set is copied through", func(t *testing.T) {
		spec := baseSpec()
		spec.Version = "4.3.1"
		cr := &paasv1alpha1.KafkaCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")
		v, _, _ := unstructured.NestedString(u.Object, "spec", "kafka", "version")
		if v != "4.3.1" {
			t.Fatalf("expected version %q, got %q", "4.3.1", v)
		}
	})

	t.Run("monitoring disabled leaves metricsConfig unset", func(t *testing.T) {
		cr := &paasv1alpha1.KafkaCluster{Spec: baseSpec()}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")
		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "kafka", "metricsConfig"); found {
			t.Fatalf("expected no metricsConfig when EnablePodMonitor is unset")
		}
	})

	t.Run("monitoring enabled sets the Strimzi Metrics Reporter", func(t *testing.T) {
		spec := baseSpec()
		spec.Monitoring = paasv1alpha1.MonitoringSpec{EnablePodMonitor: true}
		cr := &paasv1alpha1.KafkaCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		mType, found, _ := unstructured.NestedString(u.Object, "spec", "kafka", "metricsConfig", "type")
		if !found || mType != "strimziMetricsReporter" {
			t.Fatalf("expected metricsConfig.type %q, got found=%v value=%q", "strimziMetricsReporter", found, mType)
		}
	})
}

func TestExtraResourcesNodePool(t *testing.T) {
	t.Run("always built regardless of monitoring", func(t *testing.T) {
		cr := &paasv1alpha1.KafkaCluster{Spec: baseSpec()}
		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")

		var nodePool *unstructured.Unstructured
		for _, e := range extras {
			if e.GVK == nodePoolGVK {
				nodePool = e.Desired
			}
		}
		if nodePool == nil {
			t.Fatalf("expected a KafkaNodePool to always be desired")
		}

		if got := nodePool.GetLabels()["strimzi.io/cluster"]; got != "test" {
			t.Fatalf("expected strimzi.io/cluster label %q, got %q", "test", got)
		}

		replicas, _, _ := unstructured.NestedInt64(nodePool.Object, "spec", "replicas")
		if replicas != 1 {
			t.Fatalf("expected replicas 1, got %d", replicas)
		}

		roles, _, _ := unstructured.NestedSlice(nodePool.Object, "spec", "roles")
		if len(roles) != 2 || roles[0] != "controller" || roles[1] != "broker" {
			t.Fatalf("expected roles [controller broker], got %v", roles)
		}

		volumes, _, _ := unstructured.NestedSlice(nodePool.Object, "spec", "storage", "volumes")
		if len(volumes) != 1 {
			t.Fatalf("expected 1 storage volume, got %d", len(volumes))
		}
		vol, _ := volumes[0].(map[string]any)
		size, _, _ := unstructured.NestedString(vol, "size")
		if size != "10Gi" {
			t.Fatalf("expected volume size %q, got %q", "10Gi", size)
		}
		if _, found, _ := unstructured.NestedString(vol, "class"); found {
			t.Fatalf("expected no storage class when unset")
		}
		kraftMetadata, _, _ := unstructured.NestedString(vol, "kraftMetadata")
		if kraftMetadata != "shared" {
			t.Fatalf("expected kraftMetadata %q, got %q", "shared", kraftMetadata)
		}
	})

	t.Run("storage class set is copied through", func(t *testing.T) {
		spec := baseSpec()
		spec.Storage.StorageClass = "fast"
		cr := &paasv1alpha1.KafkaCluster{Spec: spec}
		extras := Adapter{}.ExtraResources(cr, "test", "default", "test")

		var nodePool *unstructured.Unstructured
		for _, e := range extras {
			if e.GVK == nodePoolGVK {
				nodePool = e.Desired
			}
		}
		volumes, _, _ := unstructured.NestedSlice(nodePool.Object, "spec", "storage", "volumes")
		vol, _ := volumes[0].(map[string]any)
		class, _, _ := unstructured.NestedString(vol, "class")
		if class != "fast" {
			t.Fatalf("expected class %q, got %q", "fast", class)
		}
	})
}

func TestExtraResourcesPodMonitorAndDashboard(t *testing.T) {
	t.Run("absent when monitoring is disabled", func(t *testing.T) {
		cr := &paasv1alpha1.KafkaCluster{Spec: baseSpec()}
		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")

		if len(extras) != 3 {
			t.Fatalf("expected 3 extras (node pool + podmonitor + dashboard), got %d", len(extras))
		}
		for _, e := range extras {
			if e.GVK != nodePoolGVK && e.Desired != nil {
				t.Fatalf("expected %s to be absent when monitoring is disabled", e.GVK.Kind)
			}
		}
	})

	t.Run("built when monitoring is enabled", func(t *testing.T) {
		spec := baseSpec()
		spec.Monitoring.EnablePodMonitor = true
		cr := &paasv1alpha1.KafkaCluster{Spec: spec}
		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")

		var podMonitor, dashboard *unstructured.Unstructured
		for _, e := range extras {
			switch e.GVK.Kind {
			case "PodMonitor":
				podMonitor = e.Desired
			case "GrafanaDashboard":
				dashboard = e.Desired
			}
		}
		if podMonitor == nil {
			t.Fatalf("expected PodMonitor to be desired")
		}
		if dashboard == nil {
			t.Fatalf("expected GrafanaDashboard to be desired")
		}

		cluster, _, _ := unstructured.NestedString(podMonitor.Object, "spec", "selector", "matchLabels", "strimzi.io/cluster")
		if cluster != "test" {
			t.Fatalf("expected PodMonitor selector strimzi.io/cluster %q, got %q", "test", cluster)
		}
		endpoints, _, _ := unstructured.NestedSlice(podMonitor.Object, "spec", "podMetricsEndpoints")
		if len(endpoints) != 1 {
			t.Fatalf("expected 1 podMetricsEndpoints entry, got %d", len(endpoints))
		}
		endpoint, _ := endpoints[0].(map[string]any)
		targetPort, _, _ := unstructured.NestedInt64(endpoint, "targetPort")
		if targetPort != 9404 {
			t.Fatalf("expected targetPort 9404, got %d", targetPort)
		}

		scope, _, _ := unstructured.NestedString(dashboard.Object, "spec", "instanceSelector", "matchLabels", grafana.ScopeLabel)
		if scope != "team-a" {
			t.Fatalf("expected instanceSelector to match scope label team-a, got %q", scope)
		}
		json, found, _ := unstructured.NestedString(dashboard.Object, "spec", "json")
		if !found || json == "" {
			t.Fatalf("expected spec.json to be populated with the embedded dashboard")
		}
		datasources, _, _ := unstructured.NestedSlice(dashboard.Object, "spec", "datasources")
		if len(datasources) != 1 {
			t.Fatalf("expected exactly one datasource mapping, got %d", len(datasources))
		}
		entry, _ := datasources[0].(map[string]any)
		if name, _, _ := unstructured.NestedString(entry, "datasourceName"); name != grafana.DatasourceUID {
			t.Fatalf("expected datasourceName %q, got %q", grafana.DatasourceUID, name)
		}
	})
}

// statusObject builds an unstructured Kafka with just the status.conditions
// field ExtractStatus reads.
func statusObject(conditionType, status, reason string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{ //nolint:goconst // "status" is an unrelated JSON key/identifier in each of its occurrences
			"conditions": []any{
				map[string]any{"type": conditionType, "status": status, "reason": reason}, //nolint:goconst // "type" is an unrelated JSON key in each of its occurrences
			},
		},
	}}
}

func TestExtractStatus(t *testing.T) {
	t.Run("nil object", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(nil)
		if s.Phase != unknownPhase {
			t.Fatalf("expected phase %q, got %q", unknownPhase, s.Phase)
		}
		if s.Ready {
			t.Fatalf("expected not ready")
		}
	})

	t.Run("ready condition true", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("Ready", "True", "ClusterReady"))
		if !s.Ready {
			t.Fatalf("expected ready")
		}
		if s.Phase != "ClusterReady" {
			t.Fatalf("expected phase %q, got %q", "ClusterReady", s.Phase)
		}
	})

	t.Run("ready condition false", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("Ready", "False", "Reconciling"))
		if s.Ready {
			t.Fatalf("expected not ready")
		}
		if s.Phase != "Reconciling" {
			t.Fatalf("expected phase %q, got %q", "Reconciling", s.Phase)
		}
	})

	t.Run("no Ready condition present", func(t *testing.T) {
		u := &unstructured.Unstructured{Object: map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Warning", "status": "True", "reason": "SomethingElse"},
				},
			},
		}}
		s := Adapter{}.ExtractStatus(u)
		if s.Phase != unknownPhase {
			t.Fatalf("expected phase %q, got %q", unknownPhase, s.Phase)
		}
		if s.Ready {
			t.Fatalf("expected not ready")
		}
	})

	t.Run("missing conditions entirely", func(t *testing.T) {
		u := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{}}}
		s := Adapter{}.ExtractStatus(u)
		if s.Phase != unknownPhase {
			t.Fatalf("expected phase %q, got %q", unknownPhase, s.Phase)
		}
	})
}
