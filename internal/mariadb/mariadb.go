// Package mariadb contains everything that talks to mariadb-operator's
// k8s.mariadb.com/v1alpha1 MariaDB (github.com/mariadb-operator/mariadb-operator).
//
// As with internal/cnpg, internal/valkey, and internal/grafana, we
// deliberately do not vendor mariadb-operator's own Go API types or its CRD
// schema. MariaDB is addressed purely through controller-runtime's dynamic
// client (unstructured.Unstructured), and the desired object is built as a
// plain map applied via Server-Side Apply. This keeps the operator decoupled
// from any specific mariadb-operator version. See
// crd-mariadb-operator-v26.6.0.yaml in external-crds/ for the schema this was
// built against.
//
// Adapter implements internal/reconciler's Adapter interface, so the actual
// reconciliation loop (finalizers, SSA, status-mirroring) lives once in
// internal/reconciler and is shared with every other vendor integration.
//
// ExtraResources additionally creates a grafana-operator GrafanaDashboard
// alongside a monitored MariaDB (gated on spec.monitoring.enablePodMonitor),
// so the Grafana in the same namespace picks it up automatically -- see
// internal/grafana's package doc for the instanceSelector/datasource
// convention this relies on. See crd-grafana-dashboard-v5.25.0.yaml in
// external-crds/ for the GrafanaDashboard schema this was built against.
package mariadb

import (
	_ "embed"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

const (
	Group        = "k8s.mariadb.com"
	Version      = "v1alpha1"
	Kind         = "MariaDB"
	FieldManager = "mariadbcluster-operator"
)

// GVK is the GroupVersionKind of the mariadb-operator MariaDB this operator
// manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// dashboardGVK is the GroupVersionKind of the grafana-operator
// GrafanaDashboard this adapter creates alongside a monitored MariaDB.
var dashboardGVK = schema.GroupVersionKind{Group: grafana.Group, Version: grafana.Version, Kind: "GrafanaDashboard"}

// dashboardJSON is the community "Galera/MariaDB - Overview" dashboard
// (https://grafana.com/grafana/dashboards/13106, revision 3), vendored
// verbatim -- linked from mariadb-operator's own docs/metrics.md. Its panels
// reference the Prometheus datasource via the templated input
// "${DS_PROMETHEUS}", resolved by GrafanaDashboard's own spec.datasources --
// see ExtraResources below.
//
//go:embed dashboards/cluster.json
var dashboardJSON string

// Adapter drives a paas MariaDBCluster onto a same-named mariadb-operator
// MariaDB. It implements
// reconciler.Adapter[paasv1alpha1.MariaDBCluster, *paasv1alpha1.MariaDBCluster].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the MariaDB generated for crName. One paas
// MariaDBCluster maps to exactly one same-named upstream MariaDB.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "MariaDB" }
func (Adapter) FieldManager() string { return FieldManager }

func resourceListJSON(list corev1.ResourceList) map[string]any {
	if len(list) == 0 {
		return nil
	}
	out := map[string]any{}
	if cpu, ok := list[corev1.ResourceCPU]; ok {
		out["cpu"] = cpu.String()
	}
	if mem, ok := list[corev1.ResourceMemory]; ok {
		out["memory"] = mem.String()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// appSecretSuffix and rootSecretSuffix name the Secrets mariadb-operator
// generates for the initial user's and root's passwords (see BuildManifest).
// Deliberately kind-scoped ("-mariadb-app", not just "-app") -- CNPG
// defaults its own auto-generated credentials Secret to the unscoped
// "<name>-app", a name it does not let this operator override without
// taking over generating and owning that password ourselves (via
// bootstrap.initdb.secret, which requires a pre-existing Secret; see
// internal/cnpg's package doc). A PostgresCluster and a MariaDBCluster with
// the same CR name in the same namespace would otherwise silently fight
// over one Secret, each vendor operator expecting its own key shape.
const (
	appSecretSuffix  = "-mariadb-app"
	rootSecretSuffix = "-mariadb-root"
)

// BuildManifest builds the desired k8s.mariadb.com/v1alpha1 MariaDB object
// for cr, ready to be applied via Server-Side Apply.
//
// The initial user's and root's passwords are referenced via
// passwordSecretKeyRef/rootPasswordSecretKeyRef with generate: true, so
// mariadb-operator creates and manages the appSecretSuffix/rootSecretSuffix
// Secrets itself, since our DatabaseSpec (like CNPG's) has no password
// field.
func (Adapter) BuildManifest(cr *paasv1alpha1.MariaDBCluster, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	// pvcRetentionPolicy.whenDeleted defaults to Retain in the mariadb-operator
	// CRD; set it to Delete so the PVCs are cleaned up when the MariaDB (and
	// its underlying StatefulSet) is deleted, matching the finalizer-gated
	// deletion this operator already performs for every other child object.
	// whenScaled is deliberately left unset (defaults to Retain) so scaling
	// replicas down doesn't drop data.
	storage := map[string]any{
		"size":               spec.Storage.Size,
		"pvcRetentionPolicy": map[string]any{"whenDeleted": "Delete"},
	}
	if spec.Storage.StorageClass != "" {
		storage["storageClassName"] = spec.Storage.StorageClass
	}

	clusterSpec := map[string]any{
		"replicas": int64(spec.Replicas),
		"storage":  storage,
		"database": spec.Database.Name,
		"username": spec.Database.Owner,
		"passwordSecretKeyRef": map[string]any{
			"name":     name + appSecretSuffix,
			"key":      "password",
			"generate": true,
		},
		"rootPasswordSecretKeyRef": map[string]any{
			"name":     name + rootSecretSuffix,
			"key":      "password",
			"generate": true,
		},
	}

	// replicas > 1 enables Galera Cluster (synchronous multi-primary
	// replication); a single replica runs as a standalone instance.
	if spec.Replicas > 1 {
		clusterSpec["galera"] = map[string]any{"enabled": true}
	}

	if spec.Image != "" {
		clusterSpec["image"] = spec.Image
	}

	resources := map[string]any{}
	if requests := resourceListJSON(spec.Resources.Requests); requests != nil {
		resources["requests"] = requests
	}
	if limits := resourceListJSON(spec.Resources.Limits); limits != nil {
		resources["limits"] = limits
	}
	if len(resources) > 0 {
		clusterSpec["resources"] = resources
	}

	if spec.Expose != nil {
		service := map[string]any{"type": string(spec.Expose.Type)}
		if len(spec.Expose.Annotations) > 0 {
			annotations := make(map[string]any, len(spec.Expose.Annotations))
			for k, v := range spec.Expose.Annotations {
				annotations[k] = v
			}
			service["metadata"] = map[string]any{"annotations": annotations}
		}
		clusterSpec["service"] = service
	}

	if spec.Monitoring.EnablePodMonitor {
		// mariadb-operator creates the ServiceMonitor in the same namespace
		// as the MariaDB, so this is inherently namespace-scoped monitoring.
		clusterSpec["metrics"] = map[string]any{
			"enabled":        true,
			"serviceMonitor": map[string]any{},
		}
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.cncp.nl/owner":           ownerName,
	})
	u.Object["spec"] = clusterSpec

	return u
}

// ExtraResources builds the GrafanaDashboard for cr's MariaDB, gated on
// spec.monitoring.enablePodMonitor -- a dashboard with nothing scraping the
// MariaDB is pointless, so the same toggle that requests the ServiceMonitor
// also requests the dashboard. Implements
// reconciler.ExtraResourcesAdapter[paasv1alpha1.MariaDBCluster, *paasv1alpha1.MariaDBCluster].
func (Adapter) ExtraResources(cr *paasv1alpha1.MariaDBCluster, targetName, namespace, owner string) []reconciler.ExtraResource {
	dashboardName := targetName + "-dashboard"

	if !cr.Spec.Monitoring.EnablePodMonitor {
		return []reconciler.ExtraResource{
			{GVK: dashboardGVK, Name: dashboardName, Desired: nil},
		}
	}

	dashboard := &unstructured.Unstructured{}
	dashboard.SetGroupVersionKind(dashboardGVK)
	dashboard.SetName(dashboardName)
	dashboard.SetNamespace(namespace)
	dashboard.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.cncp.nl/owner":           owner,
	})
	dashboard.Object["spec"] = map[string]any{
		"instanceSelector": grafana.InstanceSelector(namespace),
		"folder":           "MariaDB",
		"datasources": []any{
			map[string]any{"inputName": "DS_PROMETHEUS", "datasourceName": grafana.DatasourceUID},
		},
		"json": dashboardJSON,
	}

	return []reconciler.ExtraResource{
		{GVK: dashboardGVK, Name: dashboardName, Desired: dashboard},
	}
}

// ExtractStatus pulls replicas and the "Ready" condition out of a MariaDB's
// .status. mariadb-operator reports readiness solely via a standard
// metav1.Condition of type "Ready" (no separate phase or ready-count field),
// so Phase is that condition's Reason and readyCount is derived: replicas
// when Ready is True, 0 otherwise.
func (Adapter) ExtractStatus(u *unstructured.Unstructured) reconciler.TargetStatus {
	if u == nil {
		return reconciler.TargetStatus{Phase: "Unknown"}
	}

	replicas, _, _ := unstructured.NestedInt64(u.Object, "status", "replicas")
	observed := int32(replicas)

	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	phase := "Unknown"
	ready := false
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t != "Ready" {
			continue
		}
		if s, _ := cond["status"].(string); s == "True" {
			ready = true
		}
		if r, _ := cond["reason"].(string); r != "" {
			phase = r
		}
		break
	}

	readyCount := int32(0)
	if ready {
		readyCount = observed
	}

	return reconciler.TargetStatus{
		Phase:         phase,
		Ready:         ready && observed > 0,
		ObservedCount: observed,
		ReadyCount:    readyCount,
	}
}

// ApplyStatus mirrors s onto cr's own .status (replicas/readyReplicas + a
// standard Ready condition).
func (Adapter) ApplyStatus(cr *paasv1alpha1.MariaDBCluster, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("MariaDB %q is ready (%d/%d replicas)", targetName, s.ReadyCount, s.ObservedCount)
	} else {
		message = fmt.Sprintf("Waiting for MariaDB %q (phase: %s)", targetName, s.Phase)
	}

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "ClusterNotReady",
		Message:            message,
		ObservedGeneration: cr.Generation,
	}
	if s.Ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "ClusterReady"
	}
	meta.SetStatusCondition(&cr.Status.Conditions, condition)

	cr.Status.Ready = s.Ready
	cr.Status.Phase = s.Phase
	cr.Status.Replicas = s.ObservedCount
	cr.Status.ReadyReplicas = s.ReadyCount
	cr.Status.Message = message

	return message
}
