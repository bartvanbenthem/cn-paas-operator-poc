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

package loki

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

// baseSpec returns a minimal, valid LokiInstanceSpec shared across this
// file's tests as a starting point.
func baseSpec() paasv1alpha1.LokiInstanceSpec {
	return paasv1alpha1.LokiInstanceSpec{
		Size:             "1x.demo",
		StorageClassName: "standard",
		ObjectStorage:    paasv1alpha1.LokiObjectStorageSpec{SecretName: "loki-s3"},
	}
}

func TestBuildManifest(t *testing.T) {
	cr := &paasv1alpha1.LokiInstance{Spec: baseSpec()}
	u := Adapter{}.BuildManifest(cr, "test", "default", "test")

	size, _, _ := unstructured.NestedString(u.Object, "spec", "size")
	if size != "1x.demo" {
		t.Fatalf("expected size %q, got %q", "1x.demo", size)
	}

	sc, _, _ := unstructured.NestedString(u.Object, "spec", "storageClassName")
	if sc != "standard" {
		t.Fatalf("expected storageClassName %q, got %q", "standard", sc)
	}

	secretName, _, _ := unstructured.NestedString(u.Object, "spec", "storage", "secret", "name")
	if secretName != "loki-s3" {
		t.Fatalf("expected storage.secret.name %q, got %q", "loki-s3", secretName)
	}
	secretType, _, _ := unstructured.NestedString(u.Object, "spec", "storage", "secret", "type")
	if secretType != "s3" {
		t.Fatalf("expected storage.secret.type %q, got %q", "s3", secretType)
	}

	schemas, found, _ := unstructured.NestedSlice(u.Object, "spec", "storage", "schemas")
	if !found || len(schemas) != 1 {
		t.Fatalf("expected exactly one storage schema, found=%v len=%d", found, len(schemas))
	}
	schema, _ := schemas[0].(map[string]any)
	if v, _, _ := unstructured.NestedString(schema, "version"); v != schemaVersion {
		t.Fatalf("expected schema version %q, got %q", schemaVersion, v)
	}
	if d, _, _ := unstructured.NestedString(schema, "effectiveDate"); d != schemaEffectiveDate {
		t.Fatalf("expected schema effectiveDate %q, got %q", schemaEffectiveDate, d)
	}
}

func TestServiceNames(t *testing.T) {
	if got, want := QueryServiceName("mystack"), "mystack-query-frontend-http"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	if got, want := WriteServiceName("mystack"), "mystack-distributor-http"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// statusObject builds an unstructured LokiStack with just the
// status.conditions field ExtractStatus reads.
func statusObject(conditionType, status, reason string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{ //nolint:goconst // "status" is an unrelated JSON key/identifier in each of its occurrences
			"conditions": []any{
				map[string]any{"type": conditionType, "status": status, "reason": reason}, //nolint:goconst // "type" is an unrelated JSON key in each of its occurrences
			},
		},
	}}
}

const readyComponentsReason = "ReadyComponents"

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
		s := Adapter{}.ExtractStatus(statusObject("Ready", "True", readyComponentsReason))
		if !s.Ready {
			t.Fatalf("expected ready")
		}
		if s.Phase != readyComponentsReason {
			t.Fatalf("expected phase %q, got %q", readyComponentsReason, s.Phase)
		}
	})

	t.Run("pending condition, no Ready entry", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("Pending", "True", "PendingComponents"))
		if s.Ready {
			t.Fatalf("expected not ready")
		}
		if s.Phase != unknownPhase {
			t.Fatalf("expected phase %q, got %q", unknownPhase, s.Phase)
		}
	})

	t.Run("ready condition false", func(t *testing.T) {
		s := Adapter{}.ExtractStatus(statusObject("Ready", "False", "FailedComponents"))
		if s.Ready {
			t.Fatalf("expected not ready")
		}
		if s.Phase != "FailedComponents" {
			t.Fatalf("expected phase %q, got %q", "FailedComponents", s.Phase)
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
		cr := &paasv1alpha1.LokiInstance{}
		msg := Adapter{}.ApplyStatus(cr, "test", reconciler.TargetStatus{Phase: "PendingComponents"})
		if cr.Status.Ready {
			t.Fatalf("expected not ready")
		}
		if msg == "" {
			t.Fatalf("expected a non-empty message")
		}
	})

	t.Run("ready", func(t *testing.T) {
		cr := &paasv1alpha1.LokiInstance{}
		Adapter{}.ApplyStatus(cr, "test", reconciler.TargetStatus{Ready: true, Phase: readyComponentsReason})
		if !cr.Status.Ready {
			t.Fatalf("expected ready")
		}
		if cr.Status.Phase != readyComponentsReason {
			t.Fatalf("expected phase %q, got %q", readyComponentsReason, cr.Status.Phase)
		}
	})
}
