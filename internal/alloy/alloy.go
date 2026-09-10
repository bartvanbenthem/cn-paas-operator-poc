// Package alloy contains everything that talks to the Alloy Operator's
// collectors.grafana.com/v1alpha1 Alloy (github.com/grafana/alloy-operator).
//
// As with every other vendor integration in this repo, we deliberately do
// not vendor the Alloy Operator's own Go API types or its CRD schema --
// Alloy is addressed purely through controller-runtime's dynamic client
// (unstructured.Unstructured), and the desired object is built as a plain
// map applied via Server-Side Apply. See
// external-crds/crd-alloy-operator-v0.7.1.yaml for the schema this was
// built against.
//
// Unlike every other vendor CRD here, Alloy's own .spec/.status are both
// "x-kubernetes-preserve-unknown-fields: true" -- wide open, no structured
// OpenAPI schema at all. That's because the Alloy Operator is built with
// the Operator SDK's Helm plugin (see its own operator/watches.yaml): it
// doesn't reconcile Alloy directly, it installs/upgrades the real
// grafana/alloy Helm chart with cr.spec merged in as that chart's values,
// so spec's shape is exactly that chart's values.yaml schema (confirmed
// against https://github.com/grafana/alloy's
// operations/helm/charts/alloy/values.yaml at chart version 1.12.1, the
// version this Alloy Operator release embeds) -- and status is whatever the
// generic helm-operator-plugins reconciler
// (github.com/operator-framework/helm-operator-plugins) writes, the same
// four/five well-known condition types (Initialized/Deployed/
// ReleaseFailed/Irreconcilable/Paused) regardless of which Helm-based
// operator is asking, not anything Alloy-specific.
//
// This adapter only ever sets three things: spec.alloy.configMap.content
// (the generated Alloy config -- see alloyConfigTemplate), spec.controller
// (deployment, sized by spec.replicas -- not the chart's default
// "daemonset": the generated config reads pod logs through the Kubernetes
// API via loki.source.kubernetes, not by tailing local node log files, so
// one or a few replicas cover a namespace regardless of node count), and
// spec.rbac.namespaces (restricting the ServiceAccount's Role -- the chart
// creates ClusterRoles by default -- to this AlloyInstance's own namespace:
// this operator pairs one AlloyInstance/LokiInstance per tenant namespace,
// so a namespace-scoped Alloy that can only ever read its own namespace's
// pods keeps one tenant from ever being able to see another's logs, even by
// mistake). Everything else -- the ServiceAccount, the ConfigMap, the
// Role/RoleBinding, the Deployment itself, and every RBAC rule needed for
// discovery.kubernetes/loki.source.kubernetes (see the chart's own
// rbac.rules default) -- is left at the chart's own defaults, the same way
// every other adapter here leaves the rest of its vendor object alone.
//
// Adapter implements internal/reconciler's Adapter interface, so the actual
// reconciliation loop (finalizers, SSA, status-mirroring) lives once in
// internal/reconciler and is shared with every other vendor integration.
package alloy

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/loki"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

const (
	Group        = "collectors.grafana.com"
	Version      = "v1alpha1"
	Kind         = "Alloy"
	FieldManager = "alloyinstance-operator"

	// controllerType is the chart's spec.controller.type this adapter always
	// requests -- see the package doc for why (loki.source.kubernetes reads
	// pod logs through the Kubernetes API, not local node log files, so this
	// never needs to be the chart's own default of "daemonset").
	controllerType = "deployment"

	// unknownPhase is the Phase reported when the underlying Alloy has no
	// "Deployed" condition yet.
	unknownPhase = "Unknown"

	// alloyConfigTemplate is the Alloy-syntax config every AlloyInstance
	// runs, minus the one thing that varies per instance (the Loki push
	// URL, filled in by fmt.Sprintf below). It discovers every pod in
	// Alloy's own namespace, attaches namespace/pod/container/node labels
	// via discovery.relabel, reads each pod's logs through the Kubernetes
	// API (loki.source.kubernetes), and forwards them to the target
	// LokiInstance's distributor.
	alloyConfigTemplate = `discovery.kubernetes "pods" {
  role = "pod"

  namespaces {
    own_namespace = true
  }
}

discovery.relabel "pod_logs" {
  targets = discovery.kubernetes.pods.targets

  rule {
    source_labels = ["__meta_kubernetes_namespace"]
    target_label  = "namespace"
  }
  rule {
    source_labels = ["__meta_kubernetes_pod_name"]
    target_label  = "pod"
  }
  rule {
    source_labels = ["__meta_kubernetes_pod_container_name"]
    target_label  = "container"
  }
  rule {
    source_labels = ["__meta_kubernetes_pod_node_name"]
    target_label  = "node"
  }
}

loki.source.kubernetes "pod_logs" {
  targets    = discovery.relabel.pod_logs.output
  forward_to = [loki.write.default.receiver]
}

loki.write "default" {
  endpoint {
    url = %q
  }
}
`
)

// GVK is the GroupVersionKind of the Alloy Operator Alloy this operator
// manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// Adapter drives a paas AlloyInstance onto a same-named Alloy Operator
// Alloy. It implements
// reconciler.Adapter[paasv1alpha1.AlloyInstance, *paasv1alpha1.AlloyInstance].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the Alloy generated for crName. One paas
// AlloyInstance maps to exactly one same-named upstream Alloy.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "Alloy" }
func (Adapter) FieldManager() string { return FieldManager }

// BuildManifest builds the desired collectors.grafana.com/v1alpha1 Alloy
// object for cr, ready to be applied via Server-Side Apply.
func (Adapter) BuildManifest(cr *paasv1alpha1.AlloyInstance, name, namespace, ownerName string) *unstructured.Unstructured {
	replicas := cr.Spec.Replicas
	if replicas == 0 {
		replicas = 1
	}

	writeURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/loki/api/v1/push",
		loki.WriteServiceName(cr.Spec.LokiInstanceRef), namespace, loki.QueryPort)

	alloySpec := map[string]any{
		"alloy": map[string]any{
			"configMap": map[string]any{
				"content": fmt.Sprintf(alloyConfigTemplate, writeURL),
			},
		},
		"controller": map[string]any{
			"type":     controllerType,
			"replicas": int64(replicas),
		},
		// Restricts the chart's own RBAC to a namespace-scoped Role/
		// RoleBinding instead of its default ClusterRole/ClusterRoleBinding
		// -- see the package doc for why.
		"rbac": map[string]any{
			"namespaces": []any{namespace},
		},
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": FieldManager,
		"paas.example.com/owner":       ownerName,
	})
	u.Object["spec"] = alloySpec

	return u
}

// ExtractStatus reads the underlying Alloy's "Deployed" condition -- the
// well-known condition every helm-operator-plugins-based operator writes
// once it's installed/upgraded the Helm release the CR's spec maps to (see
// the package doc). Like LokiStack, Alloy's own .status has no replica/
// ready-count field of its own (helm-operator-plugins reports Helm release
// state, not the underlying Deployment's rollout), so ObservedCount/
// ReadyCount are left zero-valued -- the generic reconciler engine only
// branches on TargetStatus.Ready, never the counts.
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
		if t, _, _ := unstructured.NestedString(cond, "type"); t != "Deployed" {
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
func (Adapter) ApplyStatus(cr *paasv1alpha1.AlloyInstance, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("Alloy %q is deployed", targetName)
	} else {
		message = fmt.Sprintf("Waiting for Alloy %q (phase: %s)", targetName, s.Phase)
	}

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "AlloyNotReady",
		Message:            message,
		ObservedGeneration: cr.Generation,
	}
	if s.Ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "AlloyReady"
	}
	meta.SetStatusCondition(&cr.Status.Conditions, condition)

	cr.Status.Ready = s.Ready
	cr.Status.Phase = s.Phase
	cr.Status.Message = message

	return message
}
