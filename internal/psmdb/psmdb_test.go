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

package psmdb

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
)

// baseSpec returns a minimal, valid MongoDBClusterSpec shared across this
// file's tests as a starting point.
func baseSpec() paasv1alpha1.MongoDBClusterSpec {
	return paasv1alpha1.MongoDBClusterSpec{
		Replicas: 1,
		Image:    "percona/percona-server-mongodb:8.0.26-11",
		Storage:  paasv1alpha1.StorageSpec{Size: "1Gi"},
	}
}

func TestBuildManifestReplsetMapping(t *testing.T) {
	t.Run("basic replset fields", func(t *testing.T) {
		cr := &paasv1alpha1.MongoDBCluster{Spec: baseSpec()}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		replsets, found, _ := unstructured.NestedSlice(u.Object, "spec", "replsets")
		if !found || len(replsets) != 1 {
			t.Fatalf("expected exactly one replset, got found=%v len=%d", found, len(replsets))
		}
		rs, _ := replsets[0].(map[string]any)

		if name, _, _ := unstructured.NestedString(rs, "name"); name != "rs0" {
			t.Fatalf("expected replset name %q, got %q", "rs0", name)
		}
		size, _, _ := unstructured.NestedInt64(rs, "size")
		if size != 1 {
			t.Fatalf("expected replset size 1, got %d", size)
		}
		storage, _, _ := unstructured.NestedString(rs, "volumeSpec", "persistentVolumeClaim", "resources", "requests", "storage")
		if storage != "1Gi" {
			t.Fatalf("expected storage 1Gi, got %q", storage)
		}
		if _, found, _ := unstructured.NestedString(rs, "volumeSpec", "persistentVolumeClaim", "storageClassName"); found {
			t.Fatalf("expected no storageClassName when unset")
		}

		image, _, _ := unstructured.NestedString(u.Object, "spec", "image")
		if image != "percona/percona-server-mongodb:8.0.26-11" {
			t.Fatalf("expected spec.image to be set, got %q", image)
		}
	})

	t.Run("storage class set", func(t *testing.T) {
		spec := baseSpec()
		spec.Storage.StorageClass = "fast"
		cr := &paasv1alpha1.MongoDBCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		replsets, _, _ := unstructured.NestedSlice(u.Object, "spec", "replsets")
		rs, _ := replsets[0].(map[string]any)
		sc, _, _ := unstructured.NestedString(rs, "volumeSpec", "persistentVolumeClaim", "storageClassName")
		if sc != "fast" {
			t.Fatalf("expected storageClassName %q, got %q", "fast", sc)
		}
	})
}

func TestBuildManifestEnablesVolumeScaling(t *testing.T) {
	cr := &paasv1alpha1.MongoDBCluster{Spec: baseSpec()}
	u := Adapter{}.BuildManifest(cr, "test", "default", "test")

	enabled, found, _ := unstructured.NestedBool(u.Object, "spec", "storageScaling", "enableVolumeScaling")
	if !found || !enabled {
		t.Fatalf("expected spec.storageScaling.enableVolumeScaling=true, got found=%v value=%v", found, enabled)
	}
}

func TestBuildManifestSecretNameIsKindScoped(t *testing.T) {
	cr := &paasv1alpha1.MongoDBCluster{Spec: baseSpec()}
	u := Adapter{}.BuildManifest(cr, "test", "default", "test")

	secret, _, _ := unstructured.NestedString(u.Object, "spec", "secrets", "users")
	if want := "test-psmdb-secrets"; secret != want {
		t.Fatalf("expected spec.secrets.users %q, got %q", want, secret)
	}
}

func TestBuildManifestMonitoringSidecar(t *testing.T) {
	base := baseSpec()

	t.Run("disabled leaves sidecars unset", func(t *testing.T) {
		cr := &paasv1alpha1.MongoDBCluster{Spec: base}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		replsets, _, _ := unstructured.NestedSlice(u.Object, "spec", "replsets")
		rs, _ := replsets[0].(map[string]any)
		if _, found, _ := unstructured.NestedSlice(rs, "sidecars"); found {
			t.Fatalf("expected no sidecars when EnablePodMonitor is unset")
		}
	})

	t.Run("enabled injects the mongodb_exporter sidecar", func(t *testing.T) {
		spec := base
		spec.Monitoring = paasv1alpha1.MonitoringSpec{EnablePodMonitor: true}
		cr := &paasv1alpha1.MongoDBCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		replsets, _, _ := unstructured.NestedSlice(u.Object, "spec", "replsets")
		rs, _ := replsets[0].(map[string]any)
		sidecars, found, _ := unstructured.NestedSlice(rs, "sidecars")
		if !found || len(sidecars) != 1 {
			t.Fatalf("expected exactly one sidecar, got found=%v len=%d", found, len(sidecars))
		}
		sidecar, _ := sidecars[0].(map[string]any)

		if name, _, _ := unstructured.NestedString(sidecar, "name"); name != "mongodb-exporter" {
			t.Fatalf("expected sidecar name %q, got %q", "mongodb-exporter", name)
		}

		env, _, _ := unstructured.NestedSlice(sidecar, "env")
		if len(env) != 2 {
			t.Fatalf("expected 2 env entries, got %d", len(env))
		}
		wantEnvNames := map[string]string{
			"MONGODB_USER":     "MONGODB_CLUSTER_MONITOR_USER",
			"MONGODB_PASSWORD": "MONGODB_CLUSTER_MONITOR_PASSWORD",
		}
		for _, e := range env {
			entry, _ := e.(map[string]any)
			envName, _, _ := unstructured.NestedString(entry, "name")
			wantKey, ok := wantEnvNames[envName]
			if !ok {
				t.Fatalf("unexpected env var name %q", envName)
			}
			secretName, _, _ := unstructured.NestedString(entry, "valueFrom", "secretKeyRef", "name")
			if secretName != "test-psmdb-secrets" {
				t.Fatalf("expected env secretKeyRef.name %q, got %q", "test-psmdb-secrets", secretName)
			}
			secretKey, _, _ := unstructured.NestedString(entry, "valueFrom", "secretKeyRef", "key")
			if secretKey != wantKey {
				t.Fatalf("expected env %q to reference secret key %q, got %q", envName, wantKey, secretKey)
			}
		}

		// No shell wrapper: percona/mongodb_exporter's image has no shell
		// (FROM scratch, static ENTRYPOINT), so BuildManifest must not set
		// "command" and credentials must not be embedded in --mongodb.uri.
		if _, found, _ := unstructured.NestedStringSlice(sidecar, "command"); found {
			t.Fatalf("expected no command override for the mongodb_exporter sidecar")
		}
		args, _, _ := unstructured.NestedStringSlice(sidecar, "args")
		if len(args) == 0 {
			t.Fatalf("expected non-empty mongodb_exporter args")
		}
		for _, a := range args {
			if strings.Contains(a, "$(") {
				t.Fatalf("expected no unexpanded $(VAR) reference in args, got %q", a)
			}
		}
	})
}

func TestBuildManifestExpose(t *testing.T) {
	base := baseSpec()

	t.Run("unset leaves expose unset", func(t *testing.T) {
		cr := &paasv1alpha1.MongoDBCluster{Spec: base}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		replsets, _, _ := unstructured.NestedSlice(u.Object, "spec", "replsets")
		rs, _ := replsets[0].(map[string]any)
		if _, found, _ := unstructured.NestedMap(rs, "expose"); found {
			t.Fatalf("expected no expose when Expose is unset")
		}
	})

	t.Run("set enables external exposure", func(t *testing.T) {
		spec := base
		spec.Expose = &paasv1alpha1.ServiceExposeSpec{
			Type:        "LoadBalancer",
			Annotations: map[string]string{"cloud.example.com/lb": "internal"},
		}
		cr := &paasv1alpha1.MongoDBCluster{Spec: spec}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		replsets, _, _ := unstructured.NestedSlice(u.Object, "spec", "replsets")
		rs, _ := replsets[0].(map[string]any)

		enabled, _, _ := unstructured.NestedBool(rs, "expose", "enabled")
		if !enabled {
			t.Fatalf("expected expose.enabled=true")
		}
		exposeType, _, _ := unstructured.NestedString(rs, "expose", "type")
		if exposeType != "LoadBalancer" {
			t.Fatalf("expected expose.type LoadBalancer, got %q", exposeType)
		}
		ann, _, _ := unstructured.NestedString(rs, "expose", "serviceAnnotations", "cloud.example.com/lb")
		if ann != "internal" {
			t.Fatalf("expected annotation to be copied through, got %q", ann)
		}
	})
}

func TestExtraResourcesPodMonitorAndDashboard(t *testing.T) {
	t.Run("absent when monitoring is disabled", func(t *testing.T) {
		cr := &paasv1alpha1.MongoDBCluster{Spec: baseSpec()}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")
		if len(extras) != 2 {
			t.Fatalf("expected 2 extras, got %d", len(extras))
		}
		for _, extra := range extras {
			if extra.Desired != nil {
				t.Fatalf("expected %s to be absent when monitoring is disabled", extra.GVK.Kind)
			}
		}
	})

	t.Run("built when monitoring is enabled", func(t *testing.T) {
		spec := baseSpec()
		spec.Monitoring.EnablePodMonitor = true
		cr := &paasv1alpha1.MongoDBCluster{Spec: spec}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")
		if len(extras) != 2 {
			t.Fatalf("expected 2 extras, got %d", len(extras))
		}

		var podMonitor, dashboard *unstructured.Unstructured
		for _, extra := range extras {
			switch extra.GVK.Kind {
			case "PodMonitor":
				podMonitor = extra.Desired
			case "GrafanaDashboard":
				dashboard = extra.Desired
			}
		}
		if podMonitor == nil {
			t.Fatalf("expected PodMonitor to be desired")
		}
		if dashboard == nil {
			t.Fatalf("expected GrafanaDashboard to be desired")
		}

		instance, _, _ := unstructured.NestedString(podMonitor.Object, "spec", "selector", "matchLabels", "app.kubernetes.io/instance")
		if instance != "test" {
			t.Fatalf("expected PodMonitor selector instance %q, got %q", "test", instance)
		}
		component, _, _ := unstructured.NestedString(podMonitor.Object, "spec", "selector", "matchLabels", "app.kubernetes.io/component")
		if component != "mongod" {
			t.Fatalf("expected PodMonitor selector component %q, got %q", "mongod", component)
		}
		endpoints, _, _ := unstructured.NestedSlice(podMonitor.Object, "spec", "podMetricsEndpoints")
		if len(endpoints) != 1 {
			t.Fatalf("expected 1 podMetricsEndpoints entry, got %d", len(endpoints))
		}
		endpoint, _ := endpoints[0].(map[string]any)
		targetPort, _, _ := unstructured.NestedInt64(endpoint, "targetPort")
		if targetPort != 9216 {
			t.Fatalf("expected targetPort 9216, got %d", targetPort)
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

// statusObject builds an unstructured PerconaServerMongoDB with just the
// top-level status.{state,size,ready} fields ExtractStatus reads.
func statusObject(state string, size, ready int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"state": state,
			"size":  size,
			"ready": ready,
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

	t.Run("ready when ready count meets size", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("ready", 3, 3))
		if !s.Ready {
			t.Fatalf("expected ready")
		}
		if s.Phase != "ready" {
			t.Fatalf("expected phase %q, got %q", "ready", s.Phase)
		}
		if s.ObservedCount != 3 || s.ReadyCount != 3 {
			t.Fatalf("expected observed/ready 3/3, got %d/%d", s.ObservedCount, s.ReadyCount)
		}
	})

	t.Run("not ready when ready count is below size", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("initializing", 3, 1))
		if s.Ready {
			t.Fatalf("expected not ready")
		}
		if s.Phase != "initializing" {
			t.Fatalf("expected phase %q, got %q", "initializing", s.Phase)
		}
	})

	t.Run("missing state defaults to Unknown", func(t *testing.T) {
		u := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{}}}
		s := Adapter{}.ExtractStatus(u)
		if s.Phase != unknownPhase {
			t.Fatalf("expected phase %q, got %q", unknownPhase, s.Phase)
		}
	})
}
