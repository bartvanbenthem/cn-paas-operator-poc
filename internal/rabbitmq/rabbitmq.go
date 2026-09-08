// Package rabbitmq contains everything that talks to the RabbitMQ Cluster
// Operator's rabbitmq.com/v1beta1 RabbitmqCluster
// (github.com/rabbitmq/cluster-operator).
//
// As with internal/cnpg, internal/valkey, internal/grafana, and
// internal/mariadb, we deliberately do not vendor the RabbitMQ Cluster
// Operator's own Go API types or its CRD schema. RabbitmqCluster is
// addressed purely through controller-runtime's dynamic client
// (unstructured.Unstructured), and the desired object is built as a plain
// map applied via Server-Side Apply. This keeps the operator decoupled from
// any specific RabbitMQ Cluster Operator version. See
// crd-rabbitmq-cluster-operator-v2.22.5.yaml in external-crds/ for the schema
// this was built against.
//
// Adapter implements internal/reconciler's Adapter interface, so the actual
// reconciliation loop (finalizers, SSA, status-mirroring) lives once in
// internal/reconciler and is shared with every other vendor integration.
//
// ExtraResources additionally creates a Prometheus Operator ServiceMonitor
// and a grafana-operator GrafanaDashboard alongside a monitored
// RabbitmqCluster (gated on spec.monitoring.enablePodMonitor), so the
// namespace-scoped Prometheus and Grafana pick them up automatically -- see
// internal/prometheus's package doc for the namespace-scoping convention
// and internal/grafana's for the instanceSelector/datasource one. See
// crd-prometheus-servicemonitor-v0.93.1.yaml and
// crd-grafana-dashboard-v5.25.0.yaml in external-crds/ for the schemas this
// was built against.
package rabbitmq

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
	"github.com/bartvanbenthem/paas-operator/internal/ingress"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

const (
	Group        = "rabbitmq.com"
	Version      = "v1beta1"
	Kind         = "RabbitmqCluster"
	FieldManager = "rabbitmqcluster-operator"

	// conditionTypeAllReplicasReady is the RabbitMQ Cluster Operator's own
	// condition type that goes True once every node in the StatefulSet is
	// ready. See internal/status/all_replicas_ready.go in
	// github.com/rabbitmq/cluster-operator.
	conditionTypeAllReplicasReady = "AllReplicasReady"

	// managementPort is the RabbitmqCluster's management UI port. The
	// RabbitMQ Cluster Operator always creates a Service named exactly like
	// the RabbitmqCluster itself, exposing this port -- a documented public
	// contract (https://www.rabbitmq.com/kubernetes/operator/using-operator),
	// not something this operator guesses at.
	managementPort = 15672

	// metricsPort is the RabbitmqCluster's Prometheus metrics port, exposed
	// by the same auto-generated Service as managementPort.
	// rabbitmq_prometheus is one of the RabbitMQ Cluster Operator's
	// always-on essential plugins (alongside rabbitmq_management and
	// rabbitmq_peer_discovery_k8s), documented at the same URL as
	// managementPort -- no opt-in on the RabbitmqCluster spec is needed to
	// make this port live, only to scrape it.
	metricsPort = 15692
)

// GVK is the GroupVersionKind of the RabbitMQ Cluster Operator's
// RabbitmqCluster this operator manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// serviceMonitorGVK is the GroupVersionKind of the Prometheus Operator
// ServiceMonitor this adapter creates alongside a monitored RabbitmqCluster.
// The RabbitMQ Cluster Operator creates no ServiceMonitor of its own (unlike
// CNPG/mariadb-operator), so this operator builds one directly, targeting
// the RabbitmqCluster's own auto-generated, same-named Service.
var serviceMonitorGVK = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "ServiceMonitor"}

// dashboardGVK is the GroupVersionKind of the grafana-operator
// GrafanaDashboard this adapter creates alongside a monitored
// RabbitmqCluster.
var dashboardGVK = schema.GroupVersionKind{Group: grafana.Group, Version: grafana.Version, Kind: "GrafanaDashboard"}

// dashboardJSON is Team RabbitMQ's official "RabbitMQ-Overview" Grafana
// dashboard (https://grafana.com/grafana/dashboards/10991, revision 15),
// vendored verbatim -- linked from rabbitmq.com's own Prometheus/Grafana
// monitoring guide (https://www.rabbitmq.com/docs/prometheus). Its panels
// reference the Prometheus datasource via the templated input
// "${DS_PROMETHEUS}", resolved by GrafanaDashboard's own spec.datasources --
// see ExtraResources below.
//
//go:embed dashboards/overview.json
var dashboardJSON string

// Adapter drives a paas RabbitMQCluster onto a same-named RabbitMQ Cluster
// Operator RabbitmqCluster. It implements
// reconciler.Adapter[paasv1alpha1.RabbitMQCluster, *paasv1alpha1.RabbitMQCluster].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the RabbitmqCluster generated for crName.
// One paas RabbitMQCluster maps to exactly one same-named upstream
// RabbitmqCluster.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "RabbitmqCluster" }
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

// BuildManifest builds the desired rabbitmq.com/v1beta1 RabbitmqCluster
// object for cr, ready to be applied via Server-Side Apply.
func (Adapter) BuildManifest(cr *paasv1alpha1.RabbitMQCluster, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	persistence := map[string]any{"storage": spec.Storage.Size}
	if spec.Storage.StorageClass != "" {
		persistence["storageClassName"] = spec.Storage.StorageClass
	}

	clusterSpec := map[string]any{
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

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(commonLabels(ownerName))
	u.Object["spec"] = clusterSpec

	return u
}

// ExtraResources builds the Ingress fronting the RabbitmqCluster's
// management UI (when requested), plus the ServiceMonitor and
// GrafanaDashboard driven by spec.monitoring.enablePodMonitor. Implements
// reconciler.ExtraResourcesAdapter[paasv1alpha1.RabbitMQCluster, *paasv1alpha1.RabbitMQCluster].
func (Adapter) ExtraResources(cr *paasv1alpha1.RabbitMQCluster, targetName, namespace, owner string) []reconciler.ExtraResource {
	ingressName := targetName + "-ingress"
	serviceMonitorName := targetName + "-servicemonitor"
	dashboardName := targetName + "-dashboard"

	var desiredIngress *unstructured.Unstructured
	if cr.Spec.Ingress != nil {
		desiredIngress = ingress.Build(cr.Spec.Ingress, ingressName, namespace, owner, FieldManager, targetName, managementPort)
	}

	extras := []reconciler.ExtraResource{
		{GVK: ingress.GVK, Name: ingressName, Desired: desiredIngress},
	}

	if !cr.Spec.Monitoring.EnablePodMonitor {
		return append(extras,
			reconciler.ExtraResource{GVK: serviceMonitorGVK, Name: serviceMonitorName, Desired: nil},
			reconciler.ExtraResource{GVK: dashboardGVK, Name: dashboardName, Desired: nil},
		)
	}

	serviceMonitor := &unstructured.Unstructured{}
	serviceMonitor.SetGroupVersionKind(serviceMonitorGVK)
	serviceMonitor.SetName(serviceMonitorName)
	serviceMonitor.SetNamespace(namespace)
	serviceMonitor.SetLabels(commonLabels(owner))
	serviceMonitor.Object["spec"] = map[string]any{
		// app.kubernetes.io/name=<RabbitmqCluster name> is a documented
		// label the RabbitMQ Cluster Operator always applies to its own
		// generated Service (https://www.rabbitmq.com/kubernetes/operator/using-operator#labels).
		"selector": map[string]any{
			"matchLabels": map[string]any{"app.kubernetes.io/name": targetName},
		},
		"endpoints": []any{
			map[string]any{"targetPort": int64(metricsPort)},
		},
	}

	dashboard := &unstructured.Unstructured{}
	dashboard.SetGroupVersionKind(dashboardGVK)
	dashboard.SetName(dashboardName)
	dashboard.SetNamespace(namespace)
	dashboard.SetLabels(commonLabels(owner))
	dashboard.Object["spec"] = map[string]any{
		"instanceSelector": grafana.InstanceSelector(namespace),
		"folder":           "RabbitMQ",
		"datasources": []any{
			map[string]any{"inputName": "DS_PROMETHEUS", "datasourceName": grafana.DatasourceUID},
		},
		"json": dashboardJSON,
	}

	return append(extras,
		reconciler.ExtraResource{GVK: serviceMonitorGVK, Name: serviceMonitorName, Desired: serviceMonitor},
		reconciler.ExtraResource{GVK: dashboardGVK, Name: dashboardName, Desired: dashboard},
	)
}

// ExtractStatus pulls replicas and the "AllReplicasReady" condition out of a
// RabbitmqCluster's .status/.spec. The RabbitMQ Cluster Operator reports
// readiness solely via a set of standard metav1.Conditions -- there's no
// separate phase or ready-count field on its status -- so Phase is the
// "AllReplicasReady" condition's Reason and readyCount is derived: replicas
// (read back from the applied .spec, since status carries no copy of its
// own) when Ready is True, 0 otherwise.
func (Adapter) ExtractStatus(u *unstructured.Unstructured) reconciler.TargetStatus {
	if u == nil {
		return reconciler.TargetStatus{Phase: "Unknown"}
	}

	replicas, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	observed := int32(replicas)

	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	phase := "Unknown"
	ready := false
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t != conditionTypeAllReplicasReady {
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
func (Adapter) ApplyStatus(cr *paasv1alpha1.RabbitMQCluster, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("RabbitmqCluster %q is ready (%d/%d replicas)", targetName, s.ReadyCount, s.ObservedCount)
	} else {
		message = fmt.Sprintf("Waiting for RabbitmqCluster %q (phase: %s)", targetName, s.Phase)
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
