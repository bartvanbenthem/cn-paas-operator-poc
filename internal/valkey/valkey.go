// Package valkey contains everything that talks to the valkey-operator's
// valkey.io/v1alpha1 ValkeyCluster (github.com/valkey-io/valkey-operator).
//
// As with internal/cnpg, we deliberately do not vendor valkey-operator's own
// Go API types or its CRD schema. ValkeyCluster is addressed purely through
// controller-runtime's dynamic client (unstructured.Unstructured), and the
// desired object is built as a plain map applied via Server-Side Apply.
// This keeps the operator decoupled from any specific valkey-operator
// version. See crd-valkey-v0.6.0.yaml at the repo root for the schema this
// was built against.
//
// ValkeyCluster's own child ValkeyNode CRD is not targeted here: it is an
// internal object of the valkey-operator ("users should not create
// ValkeyNodes directly" per its own CRD description), so it plays the same
// role CNPG's internal-only resources do -- out of scope for this operator.
//
// Adapter implements internal/reconciler's Adapter interface, so the actual
// reconciliation loop (finalizers, SSA, status-mirroring) lives once in
// internal/reconciler and is shared with every other vendor integration.
//
// ExtraResources additionally creates a Prometheus Operator PodMonitor and a
// grafana-operator GrafanaDashboard alongside a monitored ValkeyCluster
// (gated on spec.monitoring.enablePodMonitor), so the namespace-scoped
// Prometheus and Grafana pick them up automatically -- see
// internal/prometheus's package doc for the namespace-scoping convention
// and internal/grafana's for the instanceSelector/datasource one. See
// crd-prometheus-podmonitor-v0.93.1.yaml and
// crd-grafana-dashboard-v5.25.0.yaml at the repo root for the schemas this
// was built against.
package valkey

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
	Group        = "valkey.io"
	Version      = "v1alpha1"
	Kind         = "ValkeyCluster"
	FieldManager = "valkeycluster-operator"

	// dataPort is Valkey's data-plane (RESP) port.
	dataPort = 6379

	// exporterPort is the metrics-exporter sidecar's port, exposing
	// redis_exporter-compatible metrics -- documented in valkey-operator's
	// own docs/valkeycluster.md ("exposing Prometheus metrics on port
	// 9121"), not something this operator guesses at. The sidecar itself is
	// enabled/disabled by BuildManifest below, driven by
	// spec.monitoring.enablePodMonitor.
	exporterPort = 9121

	// clusterSelectorLabel is the label valkey-operator itself applies to
	// every pod (and its own headless Service's selector) belonging to one
	// ValkeyCluster. Unlike CNPG/mariadb-operator/the Prometheus Operator,
	// valkey-operator has no documented public contract for this -- it was
	// confirmed by reading internal/controller/valkeycluster_controller.go
	// in github.com/valkey-io/valkey-operator (LabelCluster = "valkey.io/cluster"),
	// not from any published API guarantee. It may need re-checking after a
	// valkey-operator upgrade.
	clusterSelectorLabel = "valkey.io/cluster"
)

// externalServiceGVK is the GroupVersionKind of the Service this operator
// creates to expose a ValkeyCluster outside the cluster: valkey-operator's
// own generated Service is headless (ClusterIP: None), which can't be a
// LoadBalancer/NodePort, so a separate Service is required.
var externalServiceGVK = schema.GroupVersionKind{Version: "v1", Kind: "Service"}

// podMonitorGVK is the GroupVersionKind of the Prometheus Operator
// PodMonitor this adapter creates alongside a monitored ValkeyCluster.
// valkey-operator creates no PodMonitor of its own (unlike CNPG/
// mariadb-operator), so this operator builds one directly, targeting the
// metrics-exporter sidecar via pod labels rather than a Service.
var podMonitorGVK = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PodMonitor"}

// dashboardGVK is the GroupVersionKind of the grafana-operator
// GrafanaDashboard this adapter creates alongside a monitored ValkeyCluster.
var dashboardGVK = schema.GroupVersionKind{Group: grafana.Group, Version: grafana.Version, Kind: "GrafanaDashboard"}

// GVK is the GroupVersionKind of the valkey-operator ValkeyCluster this
// operator manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// dashboardJSON is the community "Redis Dashboard for Prometheus Redis
// Exporter 1.x" dashboard (https://grafana.com/grafana/dashboards/763,
// revision 6), vendored verbatim -- compatible with Valkey's
// metrics-exporter sidecar since it emits the same redis_exporter metric
// format. Its panels reference the Prometheus datasource via the templated
// input "${DS_PROM}" (this dashboard's own input name, unlike the
// "${DS_PROMETHEUS}" used elsewhere), resolved by GrafanaDashboard's own
// spec.datasources -- see ExtraResources below.
//
//go:embed dashboards/cluster.json
var dashboardJSON string

// Adapter drives a paas ValkeyCluster onto a same-named valkey-operator
// ValkeyCluster. It implements
// reconciler.Adapter[paasv1alpha1.ValkeyCluster, *paasv1alpha1.ValkeyCluster].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the ValkeyCluster generated for crName. One
// paas ValkeyCluster maps to exactly one same-named upstream ValkeyCluster.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "Valkey ValkeyCluster" }
func (Adapter) FieldManager() string { return FieldManager }

// commonLabels returns the app.kubernetes.io/managed-by + paas.example.com/owner
// pair every object this adapter creates carries.
func commonLabels(owner string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.example.com/owner":       owner,
	}
}

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

// BuildManifest builds the desired valkey.io/v1alpha1 ValkeyCluster object
// for cr, ready to be applied via Server-Side Apply.
func (Adapter) BuildManifest(cr *paasv1alpha1.ValkeyCluster, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	persistence := map[string]any{"size": spec.Persistence.Size}
	if spec.Persistence.StorageClass != "" {
		persistence["storageClassName"] = spec.Persistence.StorageClass
	}

	clusterSpec := map[string]any{
		"shards":      int64(spec.Shards),
		"replicas":    int64(spec.Replicas),
		"persistence": persistence,
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

	// valkey-operator's exporter defaults to enabled regardless of what this
	// operator's own caller wants -- set it explicitly either way so
	// spec.monitoring.enablePodMonitor is the single source of truth (and
	// the sidecar's resource cost isn't paid when monitoring is off).
	clusterSpec["exporter"] = map[string]any{"enabled": spec.Monitoring.EnablePodMonitor}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(commonLabels(ownerName))
	u.Object["spec"] = clusterSpec

	return u
}

// ExtraResources builds the LoadBalancer/NodePort Service exposing the
// cluster's data-plane port outside the cluster (when requested), plus the
// PodMonitor and GrafanaDashboard driven by spec.monitoring.enablePodMonitor
// (see BuildManifest for the paired metrics-exporter sidecar toggle).
// Implements reconciler.ExtraResourcesAdapter[paasv1alpha1.ValkeyCluster,
// *paasv1alpha1.ValkeyCluster].
func (Adapter) ExtraResources(cr *paasv1alpha1.ValkeyCluster, targetName, namespace, owner string) []reconciler.ExtraResource {
	name := targetName + "-external"
	podMonitorName := targetName + "-podmonitor"
	dashboardName := targetName + "-dashboard"

	extras := make([]reconciler.ExtraResource, 0, 3)

	expose := cr.Spec.Expose
	if expose == nil {
		extras = append(extras, reconciler.ExtraResource{GVK: externalServiceGVK, Name: name, Desired: nil})
	} else {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(externalServiceGVK)
		u.SetName(name)
		u.SetNamespace(namespace)
		u.SetLabels(commonLabels(owner))
		if len(expose.Annotations) > 0 {
			u.SetAnnotations(expose.Annotations)
		}
		u.Object["spec"] = map[string]any{
			"type":     string(expose.Type),
			"selector": map[string]any{clusterSelectorLabel: targetName},
			"ports": []any{
				map[string]any{"name": "valkey", "port": int64(dataPort), "targetPort": int64(dataPort)},
			},
		}
		extras = append(extras, reconciler.ExtraResource{GVK: externalServiceGVK, Name: name, Desired: u})
	}

	if !cr.Spec.Monitoring.EnablePodMonitor {
		extras = append(extras,
			reconciler.ExtraResource{GVK: podMonitorGVK, Name: podMonitorName, Desired: nil},
			reconciler.ExtraResource{GVK: dashboardGVK, Name: dashboardName, Desired: nil},
		)
		return extras
	}

	podMonitor := &unstructured.Unstructured{}
	podMonitor.SetGroupVersionKind(podMonitorGVK)
	podMonitor.SetName(podMonitorName)
	podMonitor.SetNamespace(namespace)
	podMonitor.SetLabels(commonLabels(owner))
	podMonitor.Object["spec"] = map[string]any{
		"selector": map[string]any{
			"matchLabels": map[string]any{clusterSelectorLabel: targetName},
		},
		"podMetricsEndpoints": []any{
			map[string]any{"targetPort": int64(exporterPort)},
		},
	}

	dashboard := &unstructured.Unstructured{}
	dashboard.SetGroupVersionKind(dashboardGVK)
	dashboard.SetName(dashboardName)
	dashboard.SetNamespace(namespace)
	dashboard.SetLabels(commonLabels(owner))
	dashboard.Object["spec"] = map[string]any{
		"instanceSelector": grafana.InstanceSelector(namespace),
		"folder":           "Valkey",
		"datasources": []any{
			map[string]any{"inputName": "DS_PROM", "datasourceName": grafana.DatasourceUID},
		},
		"json": dashboardJSON,
	}

	return append(extras,
		reconciler.ExtraResource{GVK: podMonitorGVK, Name: podMonitorName, Desired: podMonitor},
		reconciler.ExtraResource{GVK: dashboardGVK, Name: dashboardName, Desired: dashboard},
	)
}

// ExtractStatus pulls state/shards/readyShards out of a ValkeyCluster's
// .status. ready is derived from shard counts rather than the free-form
// state string, since the exact wording of state is not a stable API
// contract across valkey-operator versions.
func (Adapter) ExtractStatus(u *unstructured.Unstructured) reconciler.TargetStatus {
	if u == nil {
		return reconciler.TargetStatus{Phase: "Unknown"}
	}

	state, found, _ := unstructured.NestedString(u.Object, "status", "state")
	if !found || state == "" {
		state = "Unknown"
	}
	shards, _, _ := unstructured.NestedInt64(u.Object, "status", "shards")
	readyShards, _, _ := unstructured.NestedInt64(u.Object, "status", "readyShards")
	observed := int32(shards)
	ready := int32(readyShards)

	return reconciler.TargetStatus{
		Phase:         state,
		Ready:         observed > 0 && ready >= observed,
		ObservedCount: observed,
		ReadyCount:    ready,
	}
}

// ApplyStatus mirrors s onto cr's own .status (shards/readyShards + a
// standard Ready condition).
func (Adapter) ApplyStatus(cr *paasv1alpha1.ValkeyCluster, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("ValkeyCluster %q is ready (%d/%d shards)", targetName, s.ReadyCount, s.ObservedCount)
	} else {
		message = fmt.Sprintf("Waiting for ValkeyCluster %q (state: %s)", targetName, s.Phase)
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
	cr.Status.Shards = s.ObservedCount
	cr.Status.ReadyShards = s.ReadyCount
	cr.Status.Message = message

	return message
}
