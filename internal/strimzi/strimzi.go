// Package strimzi contains everything that talks to the Strimzi Kafka
// Operator's kafka.strimzi.io/v1 Kafka (github.com/strimzi/strimzi-kafka-operator).
//
// As with internal/mariadb, internal/valkey, internal/cnpg, and
// internal/psmdb, we deliberately do not vendor the Strimzi operator's own
// Go API types or its CRD schema. Kafka is addressed purely through
// controller-runtime's dynamic client (unstructured.Unstructured), and the
// desired object is built as a plain map applied via Server-Side Apply. This
// keeps the operator decoupled from any specific strimzi-kafka-operator
// version. See external-crds/crd-strimzi-kafka-operator-v1.2.0.yaml for the
// schema this was built against (field paths below were verified directly
// against that schema).
//
// Strimzi is KRaft-only: ZooKeeper has been fully removed, and a Kafka
// requires at least one KafkaNodePool with the controller role and one with
// the broker role (the same pool can hold both). KafkaClusterSpec always
// uses a single combined-role pool sized by one Replicas field, matching
// Strimzi's own documented shape for small/dev clusters and keeping this
// spec to one replica count like every other building block here.
// ExtraResources creates that KafkaNodePool unconditionally (not gated on
// monitoring), the same way internal/grafana's ExtraResources unconditionally
// creates a GrafanaDatasource.
//
// Monitoring: Strimzi creates no PodMonitor/ServiceMonitor of its own.
// Rather than the legacy jmxPrometheusExporter (which needs a sidecar-style
// JMX-exporter-rules ConfigMap, the approach internal/valkey/internal/psmdb
// take), this package uses the modern Strimzi Metrics Reporter
// (spec.kafka.metricsConfig.type: strimziMetricsReporter) -- a native Kafka
// MetricsReporter plugin with no ConfigMap or sidecar needed, exposing
// Prometheus metrics directly on port 9404. This package's own
// ExtraResources still builds the PodMonitor and GrafanaDashboard directly,
// since Strimzi doesn't -- see internal/grafana's package doc for the
// instanceSelector/datasource convention it relies on.
package strimzi

import (
	_ "embed"
	"fmt"
	"strings"

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
	Group        = "kafka.strimzi.io"
	Version      = "v1"
	Kind         = "Kafka"
	FieldManager = "kafkacluster-operator"

	// nodePoolSuffix names the single, combined controller+broker
	// KafkaNodePool this operator manages -- KafkaClusterSpec models exactly
	// one node pool, so this name never needs to vary per-CR.
	nodePoolSuffix = "-dual-role"

	// clusterLabelKey is the label Strimzi uses to associate a KafkaNodePool
	// (and every pod it creates) with its parent Kafka -- confirmed from
	// Strimzi's own example manifests, not a spec field. It's also the label
	// every node-pool pod (controller and broker alike) carries, which is
	// what this package's PodMonitor selects on.
	clusterLabelKey = "strimzi.io/cluster"

	// metricsPort is the fixed port the Strimzi Metrics Reporter listens on.
	// Reserved by Strimzi itself -- its own listener port schema explicitly
	// excludes 9404 (and 9999, used for JMX) from the range Kafka listeners
	// may use, precisely because this port is already spoken for.
	metricsPort = 9404

	// plainListenerName and exposedListenerName name the internal
	// (always-present) and external (spec.expose-gated) Kafka listeners
	// this package builds. Strimzi listener names must be lowercase
	// alphanumeric and at most 11 characters.
	plainListenerName   = "plain"
	exposedListenerName = "external"

	// unknownPhase is the Phase reported when the underlying Kafka has no
	// "Ready" condition yet.
	unknownPhase = "Unknown"
)

// GVK is the GroupVersionKind of the Strimzi Kafka Operator Kafka this
// operator manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// nodePoolGVK is the GroupVersionKind of the KafkaNodePool this package's
// ExtraResources always creates alongside the Kafka -- see the package doc
// for why this, unlike every other adapter's primary/extras split, needs a
// second required object rather than one the vendor operator alone manages.
var nodePoolGVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: "KafkaNodePool"}

// podMonitorGVK is the GroupVersionKind of the PodMonitor this package's
// ExtraResources builds directly, since the Strimzi operator creates none of
// its own (see the package doc).
var podMonitorGVK = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PodMonitor"}

// dashboardGVK is the GroupVersionKind of the grafana-operator
// GrafanaDashboard this package's ExtraResources builds.
var dashboardGVK = schema.GroupVersionKind{Group: grafana.Group, Version: grafana.Version, Kind: "GrafanaDashboard"}

// dashboardJSON is Strimzi's own official "Strimzi Kafka" Grafana dashboard,
// purpose-built for the Strimzi Metrics Reporter's metric names --
// examples/metrics/strimzi-metrics-reporter/grafana-dashboards/strimzi-kafka.json
// in github.com/strimzi/strimzi-kafka-operator at tag 1.2.0. Its panels
// reference the Prometheus datasource via the templated input
// "${DS_PROMETHEUS}" -- see ExtraResources below. A companion
// "strimzi-kraft.json" dashboard (controller-quorum-specific panels) exists
// upstream but is left out of scope here, matching the one-dashboard-per-
// building-block convention every other adapter in this repo follows.
//
//go:embed dashboards/cluster.json
var dashboardJSON string

// Adapter drives a paas KafkaCluster onto a same-named Strimzi Kafka
// Operator Kafka (plus one KafkaNodePool, see ExtraResources). It implements
// reconciler.Adapter[paasv1alpha1.KafkaCluster, *paasv1alpha1.KafkaCluster].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the Kafka generated for crName. One paas
// KafkaCluster maps to exactly one same-named upstream Kafka.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "Kafka" }
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

// BuildManifest builds the desired kafka.strimzi.io/v1 Kafka object for cr,
// ready to be applied via Server-Side Apply. The matching KafkaNodePool that
// actually gives this Kafka its broker/controller nodes is built by
// ExtraResources, not here.
func (Adapter) BuildManifest(cr *paasv1alpha1.KafkaCluster, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	listeners := []any{
		map[string]any{"name": plainListenerName, "port": int64(9092), "type": "internal", "tls": false}, //nolint:goconst // "type" is an unrelated JSON key in each of its occurrences
	}
	if spec.Expose != nil {
		listeners = append(listeners, map[string]any{
			"name": exposedListenerName,
			"port": int64(9093),
			"type": strings.ToLower(string(spec.Expose.Type)),
			"tls":  true,
		})
	}

	kafkaSpec := map[string]any{
		"listeners": listeners,
	}
	if spec.Version != "" {
		kafkaSpec["version"] = spec.Version
	}
	if spec.Monitoring.EnablePodMonitor {
		kafkaSpec["metricsConfig"] = map[string]any{"type": "strimziMetricsReporter"}
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(commonLabels(ownerName))
	u.Object["spec"] = map[string]any{
		"kafka": kafkaSpec,
	}

	return u
}

// ExtraResources builds the KafkaNodePool this Kafka needs to actually run
// (always, regardless of monitoring -- see the package doc), plus a
// PodMonitor and GrafanaDashboard gated on spec.monitoring.enablePodMonitor
// (pruned via Desired: nil when disabled).
func (Adapter) ExtraResources(cr *paasv1alpha1.KafkaCluster, targetName, namespace, owner string) []reconciler.ExtraResource {
	spec := cr.Spec
	nodePoolName := targetName + nodePoolSuffix

	// deleteClaim defaults to false in the Strimzi CRD; set it to true so the
	// PVC is cleaned up when the KafkaNodePool (and its Kafka) are deleted,
	// matching the finalizer-gated deletion this operator already performs
	// for every other child object.
	volume := map[string]any{
		"id":            int64(0),
		"type":          "persistent-claim",
		"size":          spec.Storage.Size,
		"kraftMetadata": "shared",
		"deleteClaim":   true,
	}
	if spec.Storage.StorageClass != "" {
		volume["class"] = spec.Storage.StorageClass
	}

	nodePoolSpec := map[string]any{
		"replicas": int64(spec.Replicas),
		"roles":    []any{"controller", "broker"},
		"storage": map[string]any{
			"type":    "jbod",
			"volumes": []any{volume},
		},
	}

	resources := map[string]any{}
	if requests := resourceListJSON(spec.Resources.Requests); requests != nil {
		resources["requests"] = requests
	}
	if limits := resourceListJSON(spec.Resources.Limits); limits != nil {
		resources["limits"] = limits
	}
	if len(resources) > 0 {
		nodePoolSpec["resources"] = resources
	}

	nodePool := &unstructured.Unstructured{}
	nodePool.SetGroupVersionKind(nodePoolGVK)
	nodePool.SetName(nodePoolName)
	nodePool.SetNamespace(namespace)
	labels := commonLabels(owner)
	labels[clusterLabelKey] = targetName
	nodePool.SetLabels(labels)
	nodePool.Object["spec"] = nodePoolSpec

	podMonitorName := targetName + "-podmonitor"
	dashboardName := targetName + "-dashboard"

	if !spec.Monitoring.EnablePodMonitor {
		return []reconciler.ExtraResource{
			{GVK: nodePoolGVK, Name: nodePoolName, Desired: nodePool},
			{GVK: podMonitorGVK, Name: podMonitorName, Desired: nil},
			{GVK: dashboardGVK, Name: dashboardName, Desired: nil},
		}
	}

	podMonitor := &unstructured.Unstructured{}
	podMonitor.SetGroupVersionKind(podMonitorGVK)
	podMonitor.SetName(podMonitorName)
	podMonitor.SetNamespace(namespace)
	podMonitor.SetLabels(commonLabels(owner))
	podMonitor.Object["spec"] = map[string]any{
		"selector": map[string]any{
			"matchLabels": map[string]any{clusterLabelKey: targetName},
		},
		"podMetricsEndpoints": []any{
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
		"folder":           "Kafka",
		"datasources": []any{
			map[string]any{"inputName": "DS_PROMETHEUS", "datasourceName": grafana.DatasourceUID},
		},
		"json": dashboardJSON,
	}

	return []reconciler.ExtraResource{
		{GVK: nodePoolGVK, Name: nodePoolName, Desired: nodePool},
		{GVK: podMonitorGVK, Name: podMonitorName, Desired: podMonitor},
		{GVK: dashboardGVK, Name: dashboardName, Desired: dashboard},
	}
}

// ExtractStatus reads the underlying Kafka's "Ready" condition. Kafka's own
// .status has no replica/ready-count field at all (confirmed against the
// vendored CRD schema -- only status.conditions[], status.listeners[], and
// a few identity/version fields), so unlike every other adapter here,
// ObservedCount/ReadyCount are left zero-valued; the generic reconciler
// engine only branches on TargetStatus.Ready (see internal/reconciler's
// Reconcile), never the counts, so this is a safe, deliberate omission.
func (Adapter) ExtractStatus(u *unstructured.Unstructured) reconciler.TargetStatus {
	if u == nil {
		return reconciler.TargetStatus{Phase: unknownPhase}
	}

	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _, _ := unstructured.NestedString(cond, "type"); t != "Ready" {
			continue
		}
		status, _, _ := unstructured.NestedString(cond, "status")
		reason, _, _ := unstructured.NestedString(cond, "reason")
		if reason == "" {
			reason = unknownPhase
		}
		return reconciler.TargetStatus{
			Phase: reason,
			Ready: status == "True",
		}
	}

	return reconciler.TargetStatus{Phase: unknownPhase}
}

// ApplyStatus mirrors s onto cr's own .status (a standard Ready condition).
func (Adapter) ApplyStatus(cr *paasv1alpha1.KafkaCluster, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("Kafka %q is ready", targetName)
	} else {
		message = fmt.Sprintf("Waiting for Kafka %q (phase: %s)", targetName, s.Phase)
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
	cr.Status.Message = message

	return message
}
