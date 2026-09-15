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

package ingress

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
)

func TestBuild(t *testing.T) {
	spec := &paasv1alpha1.IngressSpec{
		Host:             "svc.example.com",
		IngressClassName: "nginx",
		TLSSecretName:    "svc-tls",
		Annotations:      map[string]string{"a": "b"},
	}

	u := Build(spec, "svc-ingress", "default", "owner", "field-manager", "svc", 8080)

	if u.GetName() != "svc-ingress" || u.GetNamespace() != "default" {
		t.Fatalf("unexpected name/namespace: %s/%s", u.GetNamespace(), u.GetName())
	}
	if u.GetLabels()["paas.cncp.nl/owner"] != "owner" {
		t.Fatalf("expected owner label to be set")
	}
	if u.GetAnnotations()["a"] != "b" {
		t.Fatalf("expected annotations to be copied through")
	}

	class, _, _ := unstructured.NestedString(u.Object, "spec", "ingressClassName")
	if class != "nginx" {
		t.Fatalf("expected ingressClassName nginx, got %q", class)
	}

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	rule, _ := rules[0].(map[string]any)
	host, _, _ := unstructured.NestedString(rule, "host")
	if host != "svc.example.com" {
		t.Fatalf("expected host svc.example.com, got %q", host)
	}

	paths, _, _ := unstructured.NestedSlice(rule, "http", "paths")
	path, _ := paths[0].(map[string]any)
	name, _, _ := unstructured.NestedString(path, "backend", "service", "name")
	port, _, _ := unstructured.NestedInt64(path, "backend", "service", "port", "number")
	if name != "svc" || port != 8080 {
		t.Fatalf("expected backend svc:8080, got %s:%d", name, port)
	}

	tls, _, _ := unstructured.NestedSlice(u.Object, "spec", "tls")
	if len(tls) != 1 {
		t.Fatalf("expected exactly one tls entry, got %d", len(tls))
	}
	tlsEntry, _ := tls[0].(map[string]any)
	secretName, _, _ := unstructured.NestedString(tlsEntry, "secretName")
	if secretName != "svc-tls" {
		t.Fatalf("expected tls secretName svc-tls, got %q", secretName)
	}
}
