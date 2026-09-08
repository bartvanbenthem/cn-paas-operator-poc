// Package cnpg contains everything that talks to CloudNativePG's
// postgresql.cnpg.io/v1 Cluster.
//
// We deliberately do not vendor CNPG's own Go API types or its CRD schema.
// Cluster is addressed purely through controller-runtime's dynamic client
// (unstructured.Unstructured), and the desired object is built as a plain
// map applied via Server-Side Apply. This keeps the operator decoupled from
// any specific CNPG version — as long as the postgresql.cnpg.io/v1 Cluster
// shape stays backward compatible, this operator keeps working without a
// rebuild.
//
// Adapter implements internal/reconciler's Adapter interface, so the actual
// reconciliation loop (finalizers, SSA, status-mirroring) lives once in
// internal/reconciler and is shared with every other vendor integration.
//
// BuildManifest never sets bootstrap.initdb.secret, so CNPG auto-generates
// the app user's credentials Secret itself, always named "<cluster
// name>-app" -- a name this operator cannot change without instead
// pre-creating that Secret itself (bootstrap.initdb.secret requires an
// existing kubernetes.io/basic-auth Secret, making this operator responsible
// for generating and durably keeping its password stable across reconciles,
// which nothing here currently does). Because that name is unscoped by
// resource kind, a PostgresCluster and any other building block whose CR
// shares its name in the same namespace can collide on it if that other
// kind also defaults to "<name>-app" -- see internal/mariadb's
// appSecretSuffix, which is deliberately kind-scoped for exactly this
// reason.
//
// ExtraResources additionally creates a grafana-operator GrafanaDashboard
// alongside a monitored Cluster (gated on spec.monitoring.enablePodMonitor),
// so the Grafana in the same namespace picks it up automatically -- see
// internal/grafana's package doc for the instanceSelector/datasource
// convention this relies on. See crd-grafana-dashboard-v5.25.0.yaml in
// external-crds/ for the GrafanaDashboard schema this was built against.
package cnpg

import (
	"context"
	_ "embed"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

const (
	Group        = "postgresql.cnpg.io"
	Version      = "v1"
	Kind         = "Cluster"
	FieldManager = "postgrescluster-operator"
)

// GVK is the GroupVersionKind of the CNPG Cluster this operator manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// dashboardGVK is the GroupVersionKind of the grafana-operator
// GrafanaDashboard this adapter creates alongside a monitored PostgresCluster.
var dashboardGVK = schema.GroupVersionKind{Group: grafana.Group, Version: grafana.Version, Kind: "GrafanaDashboard"}

// dashboardJSON is CloudNativePG's official Grafana dashboard
// (https://github.com/cloudnative-pg/grafana-dashboards, Apache-2.0),
// vendored verbatim. Its panels reference the Prometheus datasource via the
// templated input "${DS_PROMETHEUS}", resolved by GrafanaDashboard's own
// spec.datasources -- see ExtraResources below.
//
//go:embed dashboards/cluster.json
var dashboardJSON string

// Adapter drives a PostgresCluster onto a same-named CNPG Cluster. It
// implements reconciler.Adapter[paasv1alpha1.PostgresCluster,
// *paasv1alpha1.PostgresCluster].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the CNPG Cluster generated for crName. One
// PostgresCluster maps to exactly one same-named Cluster — there's no need
// for a separate naming scheme.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "CNPG Cluster" }
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

// BuildManifest builds the desired postgresql.cnpg.io/v1 Cluster object for
// cr, ready to be applied via Server-Side Apply.
func (Adapter) BuildManifest(cr *paasv1alpha1.PostgresCluster, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	storage := map[string]any{"size": spec.Storage.Size}
	if spec.Storage.StorageClass != "" {
		storage["storageClass"] = spec.Storage.StorageClass
	}

	clusterSpec := map[string]any{
		"instances": int64(spec.Instances),
		"storage":   storage,
		"bootstrap": map[string]any{
			"initdb": map[string]any{
				"database": spec.Database.Name,
				"owner":    spec.Database.Owner,
			},
		},
	}

	if spec.Image != "" {
		clusterSpec["imageName"] = spec.Image
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
		serviceMetadata := map[string]any{"name": name + "-external"}
		if len(spec.Expose.Annotations) > 0 {
			annotations := make(map[string]any, len(spec.Expose.Annotations))
			for k, v := range spec.Expose.Annotations {
				annotations[k] = v
			}
			serviceMetadata["annotations"] = annotations
		}
		clusterSpec["managed"] = map[string]any{
			"services": map[string]any{
				"additional": []any{
					map[string]any{
						"selectorType": "rw",
						"serviceTemplate": map[string]any{
							"metadata": serviceMetadata,
							"spec": map[string]any{
								"type": string(spec.Expose.Type),
							},
						},
					},
				},
			},
		}
	}

	if spec.Monitoring.EnablePodMonitor {
		// CNPG always creates the PodMonitor in the same namespace as the
		// Cluster, so this is inherently namespace-scoped monitoring.
		clusterSpec["monitoring"] = map[string]any{
			"enablePodMonitor": true,
		}
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": "postgrescluster-operator",
		"paas.example.com/owner":       ownerName,
	})
	u.Object["spec"] = clusterSpec

	return u
}

// ExtraResources builds the GrafanaDashboard for cr's CNPG Cluster, gated on
// spec.monitoring.enablePodMonitor -- a dashboard with nothing scraping the
// Cluster is pointless, so the same toggle that requests the PodMonitor also
// requests the dashboard. Implements
// reconciler.ExtraResourcesAdapter[paasv1alpha1.PostgresCluster, *paasv1alpha1.PostgresCluster].
func (Adapter) ExtraResources(cr *paasv1alpha1.PostgresCluster, targetName, namespace, owner string) []reconciler.ExtraResource {
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
		"paas.example.com/owner":       owner,
	})
	dashboard.Object["spec"] = map[string]any{
		"instanceSelector": grafana.InstanceSelector(namespace),
		"folder":           "CloudNativePG",
		"datasources": []any{
			map[string]any{"inputName": "DS_PROMETHEUS", "datasourceName": grafana.DatasourceUID},
		},
		"json": dashboardJSON,
	}

	return []reconciler.ExtraResource{
		{GVK: dashboardGVK, Name: dashboardName, Desired: dashboard},
	}
}

// ExtractStatus pulls phase/instances/readyInstances out of a CNPG
// Cluster's .status. ready is derived from instance counts rather than
// CNPG's free-form phase string, since the exact wording of phase is not a
// stable API contract across CNPG versions.
func (Adapter) ExtractStatus(u *unstructured.Unstructured) reconciler.TargetStatus {
	if u == nil {
		return reconciler.TargetStatus{Phase: "Unknown"}
	}

	phase, found, _ := unstructured.NestedString(u.Object, "status", "phase")
	if !found || phase == "" {
		phase = "Unknown"
	}
	i, _, _ := unstructured.NestedInt64(u.Object, "status", "instances")
	ri, _, _ := unstructured.NestedInt64(u.Object, "status", "readyInstances")
	instances := int32(i)
	readyInstances := int32(ri)
	ready := instances > 0 && readyInstances >= instances

	return reconciler.TargetStatus{
		Phase:         phase,
		Ready:         ready,
		ObservedCount: instances,
		ReadyCount:    readyInstances,
	}
}

// ApplyStatus mirrors s onto cr's own .status (instances/readyInstances +
// a standard Ready condition).
func (Adapter) ApplyStatus(cr *paasv1alpha1.PostgresCluster, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("CNPG Cluster %q is ready (%d/%d instances)", targetName, s.ReadyCount, s.ObservedCount)
	} else {
		message = fmt.Sprintf("Waiting for CNPG Cluster %q (phase: %s)", targetName, s.Phase)
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
	cr.Status.Instances = s.ObservedCount
	cr.Status.ReadyInstances = s.ReadyCount
	cr.Status.Message = message

	return message
}

// GetCluster fetches the current CNPG Cluster, returning (nil, nil) if it
// does not exist. A thin convenience wrapper over reconciler.GetTarget, kept
// here so callers (mainly tests) don't need to know the target's GVK.
func GetCluster(ctx context.Context, c client.Client, namespace, name string) (*unstructured.Unstructured, error) {
	return reconciler.GetTarget(ctx, c, GVK, namespace, name)
}
