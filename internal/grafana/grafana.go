// Package grafana contains everything that talks to grafana-operator's
// grafana.integreatly.org/v1beta1 Grafana (github.com/grafana/grafana-operator).
//
// As with internal/cnpg and internal/valkey, we deliberately do not vendor
// grafana-operator's own Go API types or its CRD schema. Grafana is
// addressed purely through controller-runtime's dynamic client
// (unstructured.Unstructured), and the desired object is built as a plain
// map applied via Server-Side Apply. This keeps the operator decoupled from
// any specific grafana-operator version. See crd-grafana-v5.25.0.yaml at the
// repo root for the schema this was built against.
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
// creates exactly one, wiring the generated Grafana to its paired
// PrometheusInstance (see GrafanaInstanceSpec.PrometheusRef), because that
// pairing is inherent to standing up a usable Grafana and every building
// block's own GrafanaDashboard (internal/cnpg, ...) depends on it existing.
// See crd-grafana-datasource-v5.25.0.yaml at the repo root for the schema
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
	ScopeLabel = "dashboards.paas.example.com/scope"

	// defaultPrometheusRef is the PrometheusInstance name assumed when
	// GrafanaInstanceSpec.PrometheusRef is left unset.
	defaultPrometheusRef = "prometheus"

	// DatasourceUID is the fixed UID given to the GrafanaDatasource this
	// operator creates for a GrafanaInstance. Every GrafanaDashboard's
	// spec.datasources[].datasourceName should reference this same
	// constant, so dashboard JSON keeps working regardless of the paired
	// PrometheusInstance's own name.
	DatasourceUID = "prometheus"
)

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
				map[string]any{"host": ingress.Host},
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
		"paas.example.com/owner":       ownerName,
		ScopeLabel:                     namespace,
	})
	u.Object["spec"] = grafanaSpec

	return u
}

// ExtraResources builds the GrafanaDatasource wiring this Grafana to its
// paired PrometheusInstance (see GrafanaInstanceSpec.PrometheusRef).
// Implements reconciler.ExtraResourcesAdapter[paasv1alpha1.GrafanaInstance,
// *paasv1alpha1.GrafanaInstance].
func (Adapter) ExtraResources(cr *paasv1alpha1.GrafanaInstance, targetName, namespace, owner string) []reconciler.ExtraResource {
	prometheusRef := cr.Spec.PrometheusRef
	if prometheusRef == "" {
		prometheusRef = defaultPrometheusRef
	}
	prometheusURL := fmt.Sprintf("http://%s.%s.svc:%d", prometheus.ServiceName(prometheusRef), namespace, prometheus.WebPort)

	datasourceName := targetName + "-prometheus"

	datasource := &unstructured.Unstructured{}
	datasource.SetGroupVersionKind(datasourceGVK)
	datasource.SetName(datasourceName)
	datasource.SetNamespace(namespace)
	datasource.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.example.com/owner":       owner,
	})
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

	return []reconciler.ExtraResource{
		{GVK: datasourceGVK, Name: datasourceName, Desired: datasource},
	}
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
