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

package alloy

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

func TestBuildManifest(t *testing.T) {
	cr := &paasv1alpha1.AlloyInstance{
		Spec: paasv1alpha1.AlloyInstanceSpec{LokiInstanceRef: "loki"},
	}

	u := Adapter{}.BuildManifest(cr, "test", "tenant-ns", "test")

	controllerType, _, _ := unstructured.NestedString(u.Object, "spec", "controller", "type")
	if controllerType != "deployment" {
		t.Fatalf("expected controller.type %q (not the chart's DaemonSet default), got %q", "deployment", controllerType)
	}

	// Replicas defaults to 1 when unset.
	replicas, _, _ := unstructured.NestedInt64(u.Object, "spec", "controller", "replicas")
	if replicas != 1 {
		t.Fatalf("expected default controller.replicas 1, got %d", replicas)
	}

	namespaces, _, _ := unstructured.NestedSlice(u.Object, "spec", "rbac", "namespaces")
	if len(namespaces) != 1 || namespaces[0] != "tenant-ns" {
		t.Fatalf("expected rbac.namespaces [tenant-ns] (namespace-scoped Role, not a ClusterRole), got %v", namespaces)
	}

	config, _, _ := unstructured.NestedString(u.Object, "spec", "alloy", "configMap", "content")
	wantURL := "http://loki-distributor-http.tenant-ns.svc.cluster.local:3100/loki/api/v1/push"
	if !strings.Contains(config, wantURL) {
		t.Fatalf("expected rendered config to contain push URL %q, got:\n%s", wantURL, config)
	}
	if !strings.Contains(config, `own_namespace = true`) {
		t.Fatalf("expected rendered config to scope discovery to Alloy's own namespace, got:\n%s", config)
	}
}

func TestBuildManifestReplicas(t *testing.T) {
	cr := &paasv1alpha1.AlloyInstance{
		Spec: paasv1alpha1.AlloyInstanceSpec{LokiInstanceRef: "loki", Replicas: 3},
	}

	u := Adapter{}.BuildManifest(cr, "test", "tenant-ns", "test")

	replicas, _, _ := unstructured.NestedInt64(u.Object, "spec", "controller", "replicas")
	if replicas != 3 {
		t.Fatalf("expected controller.replicas 3, got %d", replicas)
	}
}

// statusObject builds an unstructured Alloy with just the status.conditions
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

const deployedReason = "InstallSuccessful"

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

	t.Run("deployed condition true", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("Deployed", "True", deployedReason))
		if !s.Ready {
			t.Fatalf("expected ready")
		}
		if s.Phase != deployedReason {
			t.Fatalf("expected phase %q, got %q", deployedReason, s.Phase)
		}
	})

	t.Run("initialized only, no Deployed entry yet", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("Initialized", "True", "Init"))
		if s.Ready {
			t.Fatalf("expected not ready")
		}
		if s.Phase != unknownPhase {
			t.Fatalf("expected phase %q, got %q", unknownPhase, s.Phase)
		}
	})

	t.Run("deployed condition false", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("Deployed", "False", "ReleaseFailed"))
		if s.Ready {
			t.Fatalf("expected not ready")
		}
		if s.Phase != "ReleaseFailed" {
			t.Fatalf("expected phase %q, got %q", "ReleaseFailed", s.Phase)
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

func TestApplyStatus(t *testing.T) {
	t.Run("not ready", func(t *testing.T) {
		cr := &paasv1alpha1.AlloyInstance{}
		msg := Adapter{}.ApplyStatus(cr, "test", reconciler.TargetStatus{Phase: unknownPhase})
		if cr.Status.Ready {
			t.Fatalf("expected not ready")
		}
		if msg == "" {
			t.Fatalf("expected a non-empty message")
		}
	})

	t.Run("ready", func(t *testing.T) {
		cr := &paasv1alpha1.AlloyInstance{}
		Adapter{}.ApplyStatus(cr, "test", reconciler.TargetStatus{Ready: true, Phase: deployedReason})
		if !cr.Status.Ready {
			t.Fatalf("expected ready")
		}
		if cr.Status.Phase != deployedReason {
			t.Fatalf("expected phase %q, got %q", deployedReason, cr.Status.Phase)
		}
	})
}
