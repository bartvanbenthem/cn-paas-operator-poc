// Package prometheus contains everything that talks to the Prometheus
// Operator's monitoring.coreos.com/v1 Prometheus
// (github.com/prometheus-operator/prometheus-operator).
//
// As with internal/cnpg, internal/valkey, internal/grafana, and
// internal/mariadb, we deliberately do not vendor the Prometheus Operator's
// own Go API types or its CRD schema. Prometheus is addressed purely
// through controller-runtime's dynamic client (unstructured.Unstructured),
// and the desired object is built as a plain map applied via Server-Side
// Apply. This keeps the operator decoupled from any specific Prometheus
// Operator version. See crd-prometheus-operator-v0.93.1.yaml at the repo
// root for the schema this was built against.
//
// The generated Prometheus always selects ServiceMonitors/PodMonitors from
// its own namespace only: serviceMonitorNamespaceSelector and
// podMonitorNamespaceSelector are deliberately left unset in the built
// manifest, which the Prometheus Operator treats as "current namespace
// only" rather than cluster-wide (a null namespace selector matches only
// the Prometheus's own namespace; only an explicit, even empty, selector
// widens that). serviceMonitorSelector/podMonitorSelector are set to an
// empty selector so every ServiceMonitor/PodMonitor within that single
// namespace is picked up -- matching the namespace-scoped PodMonitor/
// ServiceMonitor CNPG and mariadb-operator create for MonitoringSpec.
//
// Adapter implements internal/reconciler's Adapter interface, so the actual
// reconciliation loop (finalizers, SSA, status-mirroring) lives once in
// internal/reconciler and is shared with every other vendor integration.
package prometheus

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/ingress"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

const (
	Group        = "monitoring.coreos.com"
	Version      = "v1"
	Kind         = "Prometheus"
	FieldManager = "prometheusinstance-operator"

	// WebPort is the Prometheus web UI/API port, exposed by the Service this
	// operator creates (see ServiceName) fronting Prometheus.
	WebPort = 9090

	// WebServiceSuffix names the ClusterIP Service this operator creates
	// fronting a Prometheus's web UI/API (the Prometheus Operator creates no
	// Service of its own). Exported so other packages -- internal/grafana,
	// wiring a GrafanaDatasource at it -- can address it without duplicating
	// the naming convention.
	WebServiceSuffix = "-web"

	// podSelectorLabel is the Prometheus Operator's own documented label
	// (see pkg/prometheus/server/operator.go's PrometheusNameLabelName in
	// github.com/prometheus-operator/prometheus-operator) identifying every
	// pod belonging to one Prometheus resource. The Prometheus Operator
	// creates no Service of its own for Prometheus -- this label is the
	// public contract meant for exactly this purpose, so building a Service
	// around it (rather than guessing at other pod labels) is safe across
	// versions.
	podSelectorLabel = "operator.prometheus.io/name"
)

// serviceGVK is the GroupVersionKind of the Service this operator creates to
// front a Prometheus, since the Prometheus Operator creates none itself.
var serviceGVK = schema.GroupVersionKind{Version: "v1", Kind: "Service"}

// GVK is the GroupVersionKind of the Prometheus Operator Prometheus this
// operator manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// Adapter drives a paas PrometheusInstance onto a same-named Prometheus
// Operator Prometheus. It implements
// reconciler.Adapter[paasv1alpha1.PrometheusInstance, *paasv1alpha1.PrometheusInstance].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the Prometheus generated for crName. One
// paas PrometheusInstance maps to exactly one same-named upstream
// Prometheus.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "Prometheus" }
func (Adapter) FieldManager() string { return FieldManager }

// ServiceName returns the name of the web Service generated for a
// PrometheusInstance named crName.
func ServiceName(crName string) string { return Adapter{}.TargetName(crName) + WebServiceSuffix }

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

// BuildManifest builds the desired monitoring.coreos.com/v1 Prometheus
// object for cr, ready to be applied via Server-Side Apply.
func (Adapter) BuildManifest(cr *paasv1alpha1.PrometheusInstance, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	promSpec := map[string]any{
		"replicas": int64(spec.Replicas),
		// Empty selectors + unset namespace selectors: select every
		// ServiceMonitor/PodMonitor, but only within this Prometheus's own
		// namespace. See the package doc for why the namespace selectors
		// are never set.
		"serviceMonitorSelector": map[string]any{},
		"podMonitorSelector":     map[string]any{},
		// Unlike kube-prometheus-stack's Helm chart, the bare Prometheus
		// Operator does not default a pod securityContext -- leaving it
		// unset means the pod runs with no fsGroup, so a freshly
		// provisioned PVC (owned by root) never gets chowned and
		// Prometheus panics trying to create /prometheus/queries.active.
		// These are the same values kube-prometheus-stack has defaulted
		// to for years.
		"securityContext": map[string]any{
			"fsGroup":      int64(2000),
			"runAsGroup":   int64(2000),
			"runAsNonRoot": true,
			"runAsUser":    int64(1000),
		},
	}

	if spec.Version != "" {
		promSpec["version"] = spec.Version
	}
	if spec.Retention != "" {
		promSpec["retention"] = spec.Retention
	}

	if spec.Storage != nil {
		pvcSpec := map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources": map[string]any{
				"requests": map[string]any{
					"storage": spec.Storage.Size,
				},
			},
		}
		if spec.Storage.StorageClass != "" {
			pvcSpec["storageClassName"] = spec.Storage.StorageClass
		}
		promSpec["storage"] = map[string]any{
			"volumeClaimTemplate": map[string]any{
				"spec": pvcSpec,
			},
		}
	}

	resources := map[string]any{}
	if requests := resourceListJSON(spec.Resources.Requests); requests != nil {
		resources["requests"] = requests
	}
	if limits := resourceListJSON(spec.Resources.Limits); limits != nil {
		resources["limits"] = limits
	}
	if len(resources) > 0 {
		promSpec["resources"] = resources
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.example.com/owner":       ownerName,
	})
	u.Object["spec"] = promSpec

	return u
}

// ExtraResources builds the ClusterIP Service fronting the Prometheus web
// UI/API -- always, regardless of Ingress, since other in-cluster consumers
// (a GrafanaDatasource, in particular) need a stable in-cluster address even
// when Prometheus is never exposed externally -- plus an Ingress routed to
// that Service, when requested. Implements
// reconciler.ExtraResourcesAdapter[paasv1alpha1.PrometheusInstance, *paasv1alpha1.PrometheusInstance].
func (Adapter) ExtraResources(cr *paasv1alpha1.PrometheusInstance, targetName, namespace, owner string) []reconciler.ExtraResource {
	serviceName := ServiceName(targetName)
	ingressName := targetName + "-ingress"

	service := &unstructured.Unstructured{}
	service.SetGroupVersionKind(serviceGVK)
	service.SetName(serviceName)
	service.SetNamespace(namespace)
	service.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.example.com/owner":       owner,
	})
	service.Object["spec"] = map[string]any{
		"type":     "ClusterIP",
		"selector": map[string]any{podSelectorLabel: targetName},
		"ports": []any{
			map[string]any{"name": "web", "port": int64(WebPort), "targetPort": "web"},
		},
	}

	if cr.Spec.Ingress == nil {
		return []reconciler.ExtraResource{
			{GVK: serviceGVK, Name: serviceName, Desired: service},
			{GVK: ingress.GVK, Name: ingressName, Desired: nil},
		}
	}

	desiredIngress := ingress.Build(cr.Spec.Ingress, ingressName, namespace, owner, FieldManager, serviceName, WebPort)

	return []reconciler.ExtraResource{
		{GVK: serviceGVK, Name: serviceName, Desired: service},
		{GVK: ingress.GVK, Name: ingressName, Desired: desiredIngress},
	}
}

// ExtractStatus pulls replicas/availableReplicas out of a Prometheus's
// .status. ready is derived from replica counts rather than an opaque
// phase string, since the Prometheus Operator (like CNPG) doesn't report a
// single well-known phase for Prometheus itself.
func (Adapter) ExtractStatus(u *unstructured.Unstructured) reconciler.TargetStatus {
	if u == nil {
		return reconciler.TargetStatus{Phase: "Unknown"}
	}

	replicas, _, _ := unstructured.NestedInt64(u.Object, "status", "replicas")
	available, _, _ := unstructured.NestedInt64(u.Object, "status", "availableReplicas")
	observed := int32(replicas)
	ready := int32(available)

	phase := "Unknown"
	if paused, found, _ := unstructured.NestedBool(u.Object, "status", "paused"); found && paused {
		phase = "Paused"
	} else if observed > 0 {
		phase = "Progressing"
	}

	return reconciler.TargetStatus{
		Phase:         phase,
		Ready:         observed > 0 && ready >= observed,
		ObservedCount: observed,
		ReadyCount:    ready,
	}
}

// ApplyStatus mirrors s onto cr's own .status (replicas/readyReplicas + a
// standard Ready condition).
func (Adapter) ApplyStatus(cr *paasv1alpha1.PrometheusInstance, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("Prometheus %q is ready (%d/%d replicas)", targetName, s.ReadyCount, s.ObservedCount)
	} else {
		message = fmt.Sprintf("Waiting for Prometheus %q (phase: %s)", targetName, s.Phase)
	}

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "PrometheusNotReady",
		Message:            message,
		ObservedGeneration: cr.Generation,
	}
	if s.Ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "PrometheusReady"
	}
	meta.SetStatusCondition(&cr.Status.Conditions, condition)

	cr.Status.Ready = s.Ready
	cr.Status.Phase = s.Phase
	cr.Status.Replicas = s.ObservedCount
	cr.Status.ReadyReplicas = s.ReadyCount
	cr.Status.Message = message

	return message
}
