// Package loki contains everything that talks to the Loki Operator's
// loki.grafana.com/v1 LokiStack (github.com/grafana/loki, operator/
// subtree -- the project formerly known as the standalone
// github.com/grafana/loki-operator repository).
//
// As with every other vendor integration in this repo, we deliberately do
// not vendor the Loki Operator's own Go API types or its CRD schema --
// LokiStack is addressed purely through controller-runtime's dynamic client
// (unstructured.Unstructured), and the desired object is built as a plain
// map applied via Server-Side Apply. See
// external-crds/crd-loki-operator-v0.11.0.yaml for the schema this was
// built against (field paths below were verified directly against that
// schema, and against github.com/grafana/loki's own
// operator/internal/handlers/internal/storage package for the exact object
// storage Secret key layout).
//
// Unlike every other building block here, a LokiStack has no ephemeral or
// local-PVC storage mode: spec.storage.secret (an S3-compatible bucket) and
// spec.storageClassName are both required, and spec.size (a t-shirt size,
// not a replica count) governs how many replicas of each internal
// component (distributor, ingester, querier, ...) get created. See
// LokiInstanceSpec's own doc comment for what's exposed and why only
// S3-compatible storage is supported.
//
// spec.storage.schemas is required by the LokiStack CRD (at least one
// entry) but not exposed on LokiInstanceSpec: BuildManifest always writes a
// single v13 (the current recommended index/schema version) entry with a
// fixed, safely-in-the-past effectiveDate, since there's no reason for a
// freshly created LokiStack to ever need more than one schema version from
// day one.
//
// spec.tenants is deliberately left unset: configuring it requires either
// OpenShift's own OAuth integration or a self-managed OIDC provider, both
// out of scope for a minimal front like this one. Without it, the Loki
// Operator does not stand up its multi-tenant gateway component -- callers
// (Promtail/Alloy pushing logs, Grafana querying) talk to the distributor's
// and query-frontend's own Services directly instead (see
// WriteServiceName/QueryServiceName below), the same single-tenant shape
// Loki itself defaults to when run standalone.
//
// Adapter implements internal/reconciler's Adapter interface, so the actual
// reconciliation loop (finalizers, SSA, status-mirroring) lives once in
// internal/reconciler and is shared with every other vendor integration.
package loki

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

const (
	Group        = "loki.grafana.com"
	Version      = "v1"
	Kind         = "LokiStack"
	FieldManager = "lokiinstance-operator"

	// schemaVersion and schemaEffectiveDate are the single storage schema
	// entry BuildManifest always writes -- see the package doc for why this
	// isn't exposed on LokiInstanceSpec. v13 is the current recommended
	// schema version (TSDB index, native OTLP/structured-metadata support);
	// the effective date just needs to be safely in the past so it's active
	// immediately rather than pending a future rollover.
	schemaVersion       = "v13"
	schemaEffectiveDate = "2024-01-01"

	// objectStorageSecretType is the only spec.storage.secret.type this
	// package builds -- see LokiInstanceSpec's doc comment for why only
	// S3-compatible storage is supported.
	objectStorageSecretType = "s3"

	// QueryPort is the HTTP port every LokiStack component Service listens
	// on -- fixed by the Loki Operator itself (its own httpPort constant),
	// not configurable via the LokiStack spec.
	QueryPort = 3100

	// queryFrontendServiceSuffix and distributorServiceSuffix name the Loki
	// Operator's own generated Services for the query-frontend (read path --
	// what Grafana's Loki datasource should point at) and distributor
	// (write path -- what log shippers like Promtail/Alloy should point at).
	// Confirmed against the Loki Operator's own naming helpers
	// (serviceNameQueryFrontendHTTP/serviceNameDistributorHTTP); exported so
	// internal/grafana can address the query-frontend Service without
	// duplicating that convention.
	queryFrontendServiceSuffix = "-query-frontend-http"
	distributorServiceSuffix   = "-distributor-http"

	// unknownPhase is the Phase reported when the underlying LokiStack has no
	// "Ready" condition yet.
	unknownPhase = "Unknown"

	// lokiStackNameLabel and lokiStackManagedByValue are two of the three
	// labels the Loki Operator applies to every object it generates for a
	// LokiStack, PVCs included -- confirmed directly against a live
	// LokiStack's PVCs (`kubectl get pvc -o jsonpath='{.metadata.labels}'`),
	// since this is the vendor controller's own runtime behavior and isn't
	// part of the CRD's OpenAPI schema. Paired with
	// "app.kubernetes.io/instance"=targetName (the third), this selector
	// uniquely identifies every PVC backing one LokiStack's
	// ingester/compactor/index-gateway StatefulSets -- see PVCLabelSelector.
	lokiStackNameLabel      = "lokistack"
	lokiStackManagedByValue = "lokistack-controller"

	// lokiFSGroup is the fixed non-root UID/GID (10001) baked into the Loki
	// Operator's own grafana/loki container image (its Dockerfile sets
	// "USER 10001"). The Loki Operator's generated StatefulSets never set
	// spec.template.spec.securityContext.fsGroup themselves -- confirmed
	// against its own operator/internal/manifests/securitycontext.go: even
	// with the lokiStackWebhook/restrictedPodSecurityStandard feature gate
	// on, that path only ever adds RunAsNonRoot/seccompProfile/dropped
	// capabilities, never fsGroup. On storage backends that provision
	// volumes owned by root (Cinder CSI, for one -- the common case outside
	// OpenShift's own SCC-managed volume ownership), the ingester,
	// compactor, and index-gateway containers then can't write to their own
	// PersistentVolumeClaims ("mkdir /tmp/loki/...: permission denied",
	// CrashLoopBackOff). ExtraResources below patches fsGroup directly onto
	// those three StatefulSets to work around it.
	//
	// This is safe against the Loki Operator's own reconcile loop
	// clobbering it back out: its mutatePodSpec (internal/manifests/
	// mutate.go) only ever overwrites Affinity/Containers/InitContainers/
	// NodeSelector/Tolerations/TopologySpreadConstraints/Volumes on the
	// existing object -- it never touches SecurityContext, so a
	// Server-Side-Apply field claim on fsGroup, once made, survives every
	// future reconcile from the vendor controller.
	//
	// The patch alone doesn't self-heal a pod that's already crash-looping
	// on the permission error, though -- and on a fresh LokiStack, the Loki
	// Operator routinely creates the StatefulSet (and its first crashing
	// pod) before this PatchOnly extra gets a chance to land, since it
	// skips applying until the target exists. With the StatefulSets'
	// default OrderedReady podManagementPolicy, Kubernetes then won't roll
	// the (now-correct) template out to that pod until the pod is Ready --
	// which it never becomes, since it's crashing on the defect the patch
	// just fixed. See reconciler.unstickStatefulSetRollout, which the
	// generic reconciler runs after every PatchOnly StatefulSet patch to
	// break exactly that deadlock.
	lokiFSGroup = 10001
)

// GVK is the GroupVersionKind of the Loki Operator LokiStack this operator
// manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// statefulSetGVK is the GroupVersionKind of the Loki Operator's own
// generated per-component StatefulSets -- see ExtraResources.
var statefulSetGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}

// pvcBackedComponents are the LokiStack component name suffixes whose
// StatefulSets the Loki Operator backs with their own PersistentVolumeClaim
// -- and so the ones that need lokiFSGroup patched onto them. Ruler is
// PVC-backed too, but this project never sets spec.rules, so the Loki
// Operator never creates one.
var pvcBackedComponents = []string{"ingester", "compactor", "index-gateway"}

// Adapter drives a paas LokiInstance onto a same-named Loki Operator
// LokiStack. It implements
// reconciler.Adapter[paasv1alpha1.LokiInstance, *paasv1alpha1.LokiInstance].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the LokiStack generated for crName. One
// paas LokiInstance maps to exactly one same-named upstream LokiStack.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "LokiStack" }
func (Adapter) FieldManager() string { return FieldManager }

// QueryServiceName returns the name of the Loki Operator's own generated
// query-frontend Service for a LokiInstance named crName -- the address
// Grafana's Loki datasource (or any other reader) should query.
func QueryServiceName(crName string) string { return crName + queryFrontendServiceSuffix }

// WriteServiceName returns the name of the Loki Operator's own generated
// distributor Service for a LokiInstance named crName -- the address log
// shippers should push to.
func WriteServiceName(crName string) string { return crName + distributorServiceSuffix }

// ExtraResources patches spec.template.spec.securityContext.fsGroup =
// lokiFSGroup onto each PVC-backed component's StatefulSet -- see
// lokiFSGroup's doc comment for why. Every entry is PatchOnly: these
// StatefulSets are created and owned by the Loki Operator itself, not by
// this reconciler, and won't exist yet on the reconcile where the LokiStack
// is first created -- GenericReconciler retries on later reconciles until
// the Loki Operator has created them. Implements
// reconciler.ExtraResourcesAdapter[paasv1alpha1.LokiInstance, *paasv1alpha1.LokiInstance].
func (Adapter) ExtraResources(_ *paasv1alpha1.LokiInstance, targetName, namespace, _ string) []reconciler.ExtraResource {
	extras := make([]reconciler.ExtraResource, 0, len(pvcBackedComponents))
	for _, component := range pvcBackedComponents {
		name := targetName + "-" + component

		patch := &unstructured.Unstructured{}
		patch.SetGroupVersionKind(statefulSetGVK)
		patch.SetName(name)
		patch.SetNamespace(namespace)
		patch.Object["spec"] = map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"securityContext": map[string]any{
						"fsGroup": int64(lokiFSGroup),
					},
				},
			},
		}

		extras = append(extras, reconciler.ExtraResource{
			GVK:       statefulSetGVK,
			Name:      name,
			Desired:   patch,
			PatchOnly: true,
		})
	}
	return extras
}

// PVCLabelSelector implements reconciler.PVCCleanupAdapter. Unlike
// CNPG/mariadb-operator, which manage their own PVCs directly and already
// delete them when their Cluster/MariaDB CR is removed, the Loki Operator's
// ingester/compactor/index-gateway StatefulSets use plain
// volumeClaimTemplates -- PVCs Kubernetes never garbage-collects on their
// StatefulSet's deletion. This selector lets GenericReconciler delete them
// itself on the LokiInstance's own deletion, for the same
// "deleting the CR deletes its storage too" behavior those other resources
// already get for free from their own vendor operator.
func (Adapter) PVCLabelSelector(_ *paasv1alpha1.LokiInstance, targetName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       lokiStackNameLabel,
		"app.kubernetes.io/instance":   targetName,
		"app.kubernetes.io/managed-by": lokiStackManagedByValue,
	}
}

// BuildManifest builds the desired loki.grafana.com/v1 LokiStack object for
// cr, ready to be applied via Server-Side Apply.
func (Adapter) BuildManifest(cr *paasv1alpha1.LokiInstance, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	lokiSpec := map[string]any{
		"size":             spec.Size,
		"storageClassName": spec.StorageClassName,
		"storage": map[string]any{
			"schemas": []any{
				map[string]any{
					"version":       schemaVersion,
					"effectiveDate": schemaEffectiveDate,
				},
			},
			"secret": map[string]any{
				"name": spec.ObjectStorage.SecretName,
				"type": objectStorageSecretType,
			},
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
	u.Object["spec"] = lokiSpec

	return u
}

// ExtractStatus reads the underlying LokiStack's "Ready" condition. Like
// Strimzi's Kafka, LokiStack's own .status has no replica/ready-count field
// at all (status.components reports per-pod status per component, not a
// single roll-up this operator could meaningfully mirror), so
// ObservedCount/ReadyCount are left zero-valued -- the generic reconciler
// engine only branches on TargetStatus.Ready, never the counts.
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
func (Adapter) ApplyStatus(cr *paasv1alpha1.LokiInstance, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("LokiStack %q is ready", targetName)
	} else {
		message = fmt.Sprintf("Waiting for LokiStack %q (phase: %s)", targetName, s.Phase)
	}

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "LokiStackNotReady",
		Message:            message,
		ObservedGeneration: cr.Generation,
	}
	if s.Ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "LokiStackReady"
	}
	meta.SetStatusCondition(&cr.Status.Conditions, condition)

	cr.Status.Ready = s.Ready
	cr.Status.Phase = s.Phase
	cr.Status.Message = message

	return message
}
