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

package grafana

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
)

func TestBuildManifestIngress(t *testing.T) {
	t.Run("unset leaves spec.ingress unset", func(t *testing.T) {
		cr := &paasv1alpha1.GrafanaInstance{Spec: paasv1alpha1.GrafanaInstanceSpec{Replicas: 1}}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "ingress"); found {
			t.Fatalf("expected no spec.ingress when Ingress is unset")
		}
	})

	t.Run("set fills the underlying Grafana's own spec.ingress", func(t *testing.T) {
		cr := &paasv1alpha1.GrafanaInstance{
			Spec: paasv1alpha1.GrafanaInstanceSpec{
				Replicas: 1,
				Ingress: &paasv1alpha1.IngressSpec{
					Host:             "grafana.example.com",
					IngressClassName: "nginx",
					TLSSecretName:    "grafana-tls",
					Annotations:      map[string]string{"cert-manager.io/cluster-issuer": "letsencrypt"},
				},
			},
		}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		rules, found, _ := unstructured.NestedSlice(u.Object, "spec", "ingress", "spec", "rules")
		if !found || len(rules) != 1 {
			t.Fatalf("expected exactly one ingress rule, found=%v len=%d", found, len(rules))
		}
		rule, _ := rules[0].(map[string]any)
		if host, _, _ := unstructured.NestedString(rule, "host"); host != "grafana.example.com" {
			t.Fatalf("expected host grafana.example.com, got %q", host)
		}

		class, _, _ := unstructured.NestedString(u.Object, "spec", "ingress", "spec", "ingressClassName")
		if class != "nginx" {
			t.Fatalf("expected ingressClassName nginx, got %q", class)
		}

		tls, _, _ := unstructured.NestedSlice(u.Object, "spec", "ingress", "spec", "tls")
		if len(tls) != 1 {
			t.Fatalf("expected exactly one tls entry, got %d", len(tls))
		}
		tlsEntry, _ := tls[0].(map[string]any)
		secretName, _, _ := unstructured.NestedString(tlsEntry, "secretName")
		if secretName != "grafana-tls" {
			t.Fatalf("expected tls secretName grafana-tls, got %q", secretName)
		}

		ann, _, _ := unstructured.NestedString(u.Object, "spec", "ingress", "metadata", "annotations", "cert-manager.io/cluster-issuer")
		if ann != "letsencrypt" {
			t.Fatalf("expected annotation to be copied through, got %q", ann)
		}
	})
}

func TestBuildManifestExpose(t *testing.T) {
	t.Run("unset leaves spec.service unset", func(t *testing.T) {
		cr := &paasv1alpha1.GrafanaInstance{Spec: paasv1alpha1.GrafanaInstanceSpec{Replicas: 1}}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		if _, found, _ := unstructured.NestedMap(u.Object, "spec", "service"); found {
			t.Fatalf("expected no spec.service when Expose is unset")
		}
	})

	t.Run("set fills the underlying Grafana's own spec.service", func(t *testing.T) {
		cr := &paasv1alpha1.GrafanaInstance{
			Spec: paasv1alpha1.GrafanaInstanceSpec{
				Replicas: 1,
				Expose: &paasv1alpha1.ServiceExposeSpec{
					Type:        "LoadBalancer",
					Annotations: map[string]string{"service.beta.kubernetes.io/foo": "bar"},
				},
			},
		}
		u := Adapter{}.BuildManifest(cr, "test", "default", "test")

		svcType, _, _ := unstructured.NestedString(u.Object, "spec", "service", "spec", "type")
		if svcType != "LoadBalancer" {
			t.Fatalf("expected spec.service.spec.type LoadBalancer, got %q", svcType)
		}

		ann, _, _ := unstructured.NestedString(u.Object, "spec", "service", "metadata", "annotations", "service.beta.kubernetes.io/foo")
		if ann != "bar" {
			t.Fatalf("expected annotation to be copied through, got %q", ann)
		}
	})
}

func TestBuildManifestScopeLabel(t *testing.T) {
	cr := &paasv1alpha1.GrafanaInstance{Spec: paasv1alpha1.GrafanaInstanceSpec{Replicas: 1}}
	u := Adapter{}.BuildManifest(cr, "test", "team-a", "test")

	if got := u.GetLabels()[ScopeLabel]; got != "team-a" {
		t.Fatalf("expected label %q=%q, got %q", ScopeLabel, "team-a", got)
	}
}

func TestExtraResourcesDatasource(t *testing.T) {
	t.Run("wires the datasource to the explicit PrometheusRef", func(t *testing.T) {
		cr := &paasv1alpha1.GrafanaInstance{
			Spec: paasv1alpha1.GrafanaInstanceSpec{Replicas: 1, PrometheusRef: "custom-prom"},
		}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")
		if len(extras) != 2 {
			t.Fatalf("expected 2 extras (prometheus datasource + loki datasource placeholder), got %d", len(extras))
		}

		ds := extras[0]
		if ds.Desired == nil {
			t.Fatalf("expected the GrafanaDatasource to be desired")
		}
		if ds.GVK.Kind != "GrafanaDatasource" {
			t.Fatalf("expected Kind GrafanaDatasource, got %q", ds.GVK.Kind)
		}

		url, _, _ := unstructured.NestedString(ds.Desired.Object, "spec", "datasource", "url")
		if want := "http://custom-prom-web.team-a.svc:9090"; want != url {
			t.Fatalf("expected datasource url %q, got %q", want, url)
		}

		scope, _, _ := unstructured.NestedString(ds.Desired.Object, "spec", "instanceSelector", "matchLabels", ScopeLabel)
		if scope != "team-a" {
			t.Fatalf("expected instanceSelector to match scope label team-a, got %q", scope)
		}

		uid, _, _ := unstructured.NestedString(ds.Desired.Object, "spec", "uid")
		if uid != DatasourceUID {
			t.Fatalf("expected uid %q, got %q", DatasourceUID, uid)
		}

		if extras[1].Desired != nil {
			t.Fatalf("expected the loki datasource to be absent when LokiRef is unset")
		}
	})

	t.Run("wires a second datasource to LokiRef when set", func(t *testing.T) {
		cr := &paasv1alpha1.GrafanaInstance{
			Spec: paasv1alpha1.GrafanaInstanceSpec{Replicas: 1, PrometheusRef: "prom", LokiRef: "custom-loki"},
		}

		extras := Adapter{}.ExtraResources(cr, "test", "team-a", "test")
		if len(extras) != 2 {
			t.Fatalf("expected 2 extras, got %d", len(extras))
		}

		lokiDS := extras[1]
		if lokiDS.Desired == nil {
			t.Fatalf("expected the loki GrafanaDatasource to be desired when LokiRef is set")
		}

		url, _, _ := unstructured.NestedString(lokiDS.Desired.Object, "spec", "datasource", "url")
		if want := "http://custom-loki-query-frontend-http.team-a.svc:3100"; want != url {
			t.Fatalf("expected datasource url %q, got %q", want, url)
		}

		dsType, _, _ := unstructured.NestedString(lokiDS.Desired.Object, "spec", "datasource", "type")
		if dsType != "loki" {
			t.Fatalf("expected datasource type %q, got %q", "loki", dsType)
		}

		uid, _, _ := unstructured.NestedString(lokiDS.Desired.Object, "spec", "uid")
		if uid != LokiDatasourceUID {
			t.Fatalf("expected uid %q, got %q", LokiDatasourceUID, uid)
		}

		headerName, _, _ := unstructured.NestedString(lokiDS.Desired.Object, "spec", "datasource", "jsonData", "httpHeaderName1")
		if headerName != "X-Scope-OrgID" {
			t.Fatalf("expected httpHeaderName1 %q, got %q", "X-Scope-OrgID", headerName)
		}

		headerValue, _, _ := unstructured.NestedString(lokiDS.Desired.Object, "spec", "datasource", "secureJsonData", "httpHeaderValue1")
		if headerValue != lokiOrgID {
			t.Fatalf("expected httpHeaderValue1 %q, got %q", lokiOrgID, headerValue)
		}
	})
}
