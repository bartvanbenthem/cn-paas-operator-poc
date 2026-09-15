// Package grafana contains everything that talks to grafana-operator's
// grafana.integreatly.org/v1beta1 Grafana (github.com/grafana/grafana-operator).
//
// As with internal/cnpg and internal/valkey, we deliberately do not vendor
// grafana-operator's own Go API types or its CRD schema. Grafana is
// addressed purely through controller-runtime's dynamic client
// (unstructured.Unstructured), and the desired object is built as a plain
// map applied via Server-Side Apply. This keeps the operator decoupled from
// any specific grafana-operator version. See crd-grafana-v5.25.0.yaml at the
// external-crds/ folder for the schema this was built against.
//
// grafana-operator also ships a large family of child CRDs (GrafanaDashboard,
// GrafanaDatasource, GrafanaFolder, GrafanaAlertRuleGroup, ...) that
// configure a running Grafana. Most of those are not targeted here: they
// aren't "an instance" the way Grafana itself, CNPG's Cluster, or Valkey's
// ValkeyCluster are -- they're child config objects, the same role CNPG's
// Pooler/Backup or Valkey's internal-only ValkeyNode play. Out of scope for
// this operator.
//
// The one exception is GrafanaDatasource: this package's ExtraResources
// always creates one wiring the generated Grafana to its paired
// PrometheusInstance (see GrafanaInstanceSpec.PrometheusRef), because that
// pairing is inherent to standing up a usable Grafana and every building
// block's own GrafanaDashboard (internal/cnpg, ...) depends on it existing.
// A second, optional one wires it to a paired LokiInstance instead (see
// GrafanaInstanceSpec.LokiRef), pruned via Desired: nil when unset. See
// crd-grafana-datasource-v5.25.0.yaml in external-crds/ for the schema
// this was built against.
//
// Adapter implements internal/reconciler's Adapter interface, so the actual
// reconciliation loop (finalizers, SSA, status-mirroring) lives once in
// internal/reconciler and is shared with every other vendor integration.
package grafana

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/loki"
	"github.com/bartvanbenthem/paas-operator/internal/prometheus"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

const (
	Group        = "grafana.integreatly.org"
	Version      = "v1beta1"
	Kind         = "Grafana"
	FieldManager = "grafanainstance-operator"

	// specKey is the "spec" map key shared by every nested
	// {metadata,spec}-shaped block (deployment, ingress,
	// persistentVolumeClaim) BuildManifest assembles below.
	specKey = "spec"

	// ScopeLabel is applied to every Grafana this operator creates, and
	// matched by the instanceSelector of every GrafanaDatasource/
	// GrafanaDashboard created in the same namespace -- by this package's
	// own ExtraResources below, and by every building-block adapter
	// (internal/cnpg, internal/mariadb, ...) wiring its own dashboard. One
	// namespace is expected to hold exactly one GrafanaInstance and one
	// PrometheusInstance (mirroring the namespace-scoped Prometheus in
	// internal/prometheus), so the namespace name alone is a sufficient
	// scope key: it lets every building block target "the Grafana in my
	// namespace" without a client lookup or an explicit cross-reference.
	ScopeLabel = "dashboards.paas.cncp.nl/scope"

	// DatasourceUID is the fixed UID given to the GrafanaDatasource this
	// operator creates for a GrafanaInstance. Every GrafanaDashboard's
	// spec.datasources[].datasourceName should reference this same
	// constant, so dashboard JSON keeps working regardless of the paired
	// PrometheusInstance's own name.
	DatasourceUID = "prometheus"

	// LokiDatasourceUID is the fixed UID given to the optional Loki
	// GrafanaDatasource this operator creates when GrafanaInstanceSpec.LokiRef
	// is set, mirroring DatasourceUID.
	LokiDatasourceUID = "loki"

	// lokiOrgID is the X-Scope-OrgID header value every request to a
	// LokiStack must carry. The Loki Operator's generated config always sets
	// auth_enabled: true, regardless of whether its gateway (the component
	// that would otherwise inject this header) is enabled -- and
	// internal/loki deliberately leaves that gateway disabled, since wiring
	// it up needs tenant/OIDC config out of scope for this project (see its
	// package doc). Without the header, Loki rejects every request outright
	// ("no org id"). "fake" is the conventional placeholder tenant used by
	// every other single-tenant Loki setup in this same boat (Grafana's own
	// Loki Helm chart docs recommend it for exactly this case) -- there's no
	// real multi-tenancy here, so the value itself is arbitrary as long as
	// every request uses the same one.
	lokiOrgID = "fake"

	// webServiceSuffix and WebPort name/describe grafana-operator's own
	// generated Service fronting Grafana's web UI -- a fixed convention
	// (confirmed directly against a live Grafana: a Service named
	// "<Grafana-name>-service", port 3000 named "grafana"), not documented
	// in the CRD schema itself since it's the vendor controller's own
	// runtime behavior. BuildManifest's own generated Ingress rule needs
	// this to route anywhere at all -- grafana-operator does not fill in
	// spec.ingress.spec.rules[].http.paths itself; it mirrors
	// Grafana.spec.ingress.spec onto the real Ingress verbatim, backend
	// included, exactly like internal/ingress.Build does for
	// RabbitMQCluster/PrometheusInstance.
	webServiceSuffix = "-service"
	WebPort          = 3000
)

// ServiceName returns the name of grafana-operator's own generated Service
// fronting the Grafana generated for crName.
func ServiceName(crName string) string { return Adapter{}.TargetName(crName) + webServiceSuffix }

// GVK is the GroupVersionKind of the grafana-operator Grafana this operator
// manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// datasourceGVK is the GroupVersionKind of the grafana-operator
// GrafanaDatasource this operator creates to wire a GrafanaInstance to its
// paired PrometheusInstance.
var datasourceGVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: "GrafanaDatasource"}

// InstanceSelector builds the instanceSelector every GrafanaDatasource/
// GrafanaDashboard in namespace must carry to be picked up by the Grafana
// this package creates there. Exported so building-block adapters can reuse
// it verbatim when building their own GrafanaDashboard extras.
func InstanceSelector(namespace string) map[string]any {
	return map[string]any{
		"matchLabels": map[string]any{ScopeLabel: namespace},
	}
}

// Adapter drives a paas GrafanaInstance onto a same-named grafana-operator
// Grafana. It implements
// reconciler.Adapter[paasv1alpha1.GrafanaInstance, *paasv1alpha1.GrafanaInstance].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the Grafana generated for crName. One paas
// GrafanaInstance maps to exactly one same-named upstream Grafana.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "Grafana" }
func (Adapter) FieldManager() string { return FieldManager }

// RequestedIngressClassName and SetIngressClassName implement
// reconciler.IngressClassDefaultingAdapter -- see its doc comment for why
// GrafanaInstance specifically needs this rather than relying on
// Kubernetes' own DefaultIngressClass admission plugin.
func (Adapter) RequestedIngressClassName(cr *paasv1alpha1.GrafanaInstance) (string, bool) {
	if cr.Spec.Ingress == nil {
		return "", false
	}
	return cr.Spec.Ingress.IngressClassName, true
}

func (Adapter) SetIngressClassName(cr *paasv1alpha1.GrafanaInstance, className string) {
	cr.Spec.Ingress.IngressClassName = className
}

// BuildManifest builds the desired grafana.integreatly.org/v1beta1 Grafana
// object for cr, ready to be applied via Server-Side Apply.
func (Adapter) BuildManifest(cr *paasv1alpha1.GrafanaInstance, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	deploymentSpec := map[string]any{
		"replicas": int64(spec.Replicas),
	}

	grafanaSpec := map[string]any{
		"deployment": map[string]any{
			specKey: deploymentSpec,
		},
	}

	if spec.Version != "" {
		grafanaSpec["version"] = spec.Version
	}

	if spec.Ingress != nil {
		ingress := spec.Ingress
		ingressSpec := map[string]any{
			"rules": []any{
				map[string]any{
					"host": ingress.Host,
					"http": map[string]any{
						"paths": []any{
							map[string]any{
								"path":     "/",
								"pathType": "Prefix",
								"backend": map[string]any{
									"service": map[string]any{
										"name": ServiceName(name),
										"port": map[string]any{"number": int64(WebPort)},
									},
								},
							},
						},
					},
				},
			},
		}
		if ingress.IngressClassName != "" {
			ingressSpec["ingressClassName"] = ingress.IngressClassName
		}
		if ingress.TLSSecretName != "" {
			ingressSpec["tls"] = []any{
				map[string]any{
					"hosts":      []any{ingress.Host},
					"secretName": ingress.TLSSecretName,
				},
			}
		}

		grafanaIngress := map[string]any{specKey: ingressSpec}
		if len(ingress.Annotations) > 0 {
			annotations := make(map[string]any, len(ingress.Annotations))
			for k, v := range ingress.Annotations {
				annotations[k] = v
			}
			grafanaIngress["metadata"] = map[string]any{"annotations": annotations}
		}
		grafanaSpec["ingress"] = grafanaIngress
	}

	if spec.Expose != nil {
		serviceSpec := map[string]any{}
		if spec.Expose.Type != "" {
			serviceSpec["type"] = string(spec.Expose.Type)
		}
		grafanaService := map[string]any{specKey: serviceSpec}
		if len(spec.Expose.Annotations) > 0 {
			annotations := make(map[string]any, len(spec.Expose.Annotations))
			for k, v := range spec.Expose.Annotations {
				annotations[k] = v
			}
			grafanaService["metadata"] = map[string]any{"annotations": annotations}
		}
		grafanaSpec["service"] = grafanaService
	}

	if spec.Persistence != nil {
		pvcSpec := map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources": map[string]any{
				"requests": map[string]any{
					"storage": spec.Persistence.Size,
				},
			},
		}
		if spec.Persistence.StorageClass != "" {
			pvcSpec["storageClassName"] = spec.Persistence.StorageClass
		}
		grafanaSpec["persistentVolumeClaim"] = map[string]any{
			specKey: pvcSpec,
		}
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.cncp.nl/owner":           ownerName,
		ScopeLabel:                     namespace,
	})
	u.Object["spec"] = grafanaSpec

	return u
}

// ExtraResources builds the GrafanaDatasource wiring this Grafana to its
// paired PrometheusInstance (see GrafanaInstanceSpec.PrometheusRef), plus a
// second one wiring it to a paired LokiInstance when
// GrafanaInstanceSpec.LokiRef is set (pruned via Desired: nil otherwise).
// Implements reconciler.ExtraResourcesAdapter[paasv1alpha1.GrafanaInstance,
// *paasv1alpha1.GrafanaInstance].
func (Adapter) ExtraResources(cr *paasv1alpha1.GrafanaInstance, targetName, namespace, owner string) []reconciler.ExtraResource {
	labels := map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.cncp.nl/owner":           owner,
	}

	prometheusURL := fmt.Sprintf("http://%s.%s.svc:%d", prometheus.ServiceName(cr.Spec.PrometheusRef), namespace, prometheus.WebPort)

	datasourceName := targetName + "-prometheus"

	datasource := &unstructured.Unstructured{}
	datasource.SetGroupVersionKind(datasourceGVK)
	datasource.SetName(datasourceName)
	datasource.SetNamespace(namespace)
	datasource.SetLabels(labels)
	datasource.Object["spec"] = map[string]any{
		"instanceSelector": InstanceSelector(namespace),
		"uid":              DatasourceUID,
		"datasource": map[string]any{
			"name":      "Prometheus",
			"type":      "prometheus",
			"access":    "proxy",
			"url":       prometheusURL,
			"isDefault": true,
		},
	}

	lokiDatasourceName := targetName + "-loki"
	extras := []reconciler.ExtraResource{
		{GVK: datasourceGVK, Name: datasourceName, Desired: datasource},
	}

	if cr.Spec.LokiRef == "" {
		return append(extras, reconciler.ExtraResource{GVK: datasourceGVK, Name: lokiDatasourceName, Desired: nil})
	}

	lokiURL := fmt.Sprintf("http://%s.%s.svc:%d", loki.QueryServiceName(cr.Spec.LokiRef), namespace, loki.QueryPort)

	lokiDatasource := &unstructured.Unstructured{}
	lokiDatasource.SetGroupVersionKind(datasourceGVK)
	lokiDatasource.SetName(lokiDatasourceName)
	lokiDatasource.SetNamespace(namespace)
	lokiDatasource.SetLabels(labels)
	lokiDatasource.Object["spec"] = map[string]any{
		"instanceSelector": InstanceSelector(namespace),
		"uid":              LokiDatasourceUID,
		"datasource": map[string]any{
			"name":   "Loki",
			"type":   "loki",
			"access": "proxy",
			"url":    lokiURL,
			"jsonData": map[string]any{
				"httpHeaderName1": "X-Scope-OrgID",
			},
			"secureJsonData": map[string]any{
				"httpHeaderValue1": lokiOrgID,
			},
		},
	}

	return append(extras, reconciler.ExtraResource{GVK: datasourceGVK, Name: lokiDatasourceName, Desired: lokiDatasource})
}

// ExtractStatus pulls stage/stageStatus/replicas out of a Grafana's
// .status. ready is derived from stage/stageStatus rather than treated as an
// opaque phase string, since grafana-operator uses this pair (not a single
// well-known phase) to report reconciliation progress.
func (Adapter) ExtractStatus(u *unstructured.Unstructured) reconciler.TargetStatus {
	if u == nil {
		return reconciler.TargetStatus{Phase: "Unknown"}
	}

	stage, found, _ := unstructured.NestedString(u.Object, "status", "stage")
	if !found || stage == "" {
		stage = "Unknown"
	}
	stageStatus, _, _ := unstructured.NestedString(u.Object, "status", "stageStatus")
	replicas, _, _ := unstructured.NestedInt64(u.Object, "status", "replicas")

	desired, found, _ := unstructured.NestedInt64(u.Object, "spec", "deployment", "spec", "replicas")
	if !found {
		desired = 1
	}

	return reconciler.TargetStatus{
		Phase:         fmt.Sprintf("%s/%s", stage, stageStatus),
		Ready:         stage == "complete" && stageStatus == "success",
		ObservedCount: int32(desired),
		ReadyCount:    int32(replicas),
	}
}

// ApplyStatus mirrors s onto cr's own .status (replicas + a standard Ready
// condition).
func (Adapter) ApplyStatus(cr *paasv1alpha1.GrafanaInstance, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("Grafana %q is ready (%d/%d replicas)", targetName, s.ReadyCount, s.ObservedCount)
	} else {
		message = fmt.Sprintf("Waiting for Grafana %q (stage: %s)", targetName, s.Phase)
	}

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "GrafanaNotReady",
		Message:            message,
		ObservedGeneration: cr.Generation,
	}
	if s.Ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "GrafanaReady"
	}
	meta.SetStatusCondition(&cr.Status.Conditions, condition)

	cr.Status.Ready = s.Ready
	cr.Status.Phase = s.Phase
	cr.Status.Replicas = s.ReadyCount
	cr.Status.Message = message

	return message
}
