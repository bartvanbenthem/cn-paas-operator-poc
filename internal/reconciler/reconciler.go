/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package reconciler provides the reconciliation engine shared by every
// vendor integration in this operator (cnpg, valkey, ...). Each of our own
// paas CRDs maps 1:1 onto a same-named object of some foreign, vendor-owned
// GVK; an Adapter describes that mapping for one vendor, and
// GenericReconciler drives the shared finalizer-gated create/update/delete
// and status-mirroring loop against it. This is what lets each type be
// added/owned/versioned independently (its own CRD, its own adapter
// package) while the actual CRUD/reconciliation logic is written once.
package reconciler

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// FinalizerName is added to every paas CR reconciled through this package,
// gating removal of the CR on cleanup of its foreign target object.
const FinalizerName = "paas.example.com/cleanup"

// TargetStatus is the subset of a foreign target object's status that every
// Adapter extracts and every paas CR mirrors back onto itself.
type TargetStatus struct {
	// Phase is a verbatim copy of the target's own status.phase (or
	// equivalent), for display. "Unknown" if the target has none yet.
	Phase string
	// Ready is true once the target reports ObservedCount == ReadyCount
	// and ObservedCount > 0.
	Ready bool
	// ObservedCount and ReadyCount are the target's own notion of "how many
	// units make this up" and "how many of those are ready" -- CNPG's
	// instances/readyInstances, Valkey's shards/readyShards, etc.
	ObservedCount int32
	ReadyCount    int32
}

// ObjectPtr constrains PT to "pointer to T that implements client.Object" --
// the usual way to write a generic reconciler over concrete Kubernetes API
// types without reflection: T is the CR struct (e.g.
// paasv1alpha1.PostgresCluster), PT is *T.
type ObjectPtr[T any] interface {
	client.Object
	*T
}

// Adapter is everything specific to one vendor integration: the foreign
// target GVK a paas CR of type PT maps to, how to build and read that
// target, and how to mirror its status back onto the CR.
type Adapter[T any, PT ObjectPtr[T]] interface {
	// GVK is the GroupVersionKind of the foreign target object this
	// adapter's CR maps to.
	GVK() schema.GroupVersionKind
	// TargetName returns the name of the target object for a CR named
	// crName.
	TargetName(crName string) string
	// ObjectKind is a human-readable label for the target, used in log
	// messages and events (e.g. "CNPG Cluster").
	ObjectKind() string
	// FieldManager is used for the Server-Side Apply of the target object.
	FieldManager() string
	// BuildManifest builds the desired target object for cr.
	BuildManifest(cr PT, name, namespace, owner string) *unstructured.Unstructured
	// ExtractStatus reads the target object's observed status.
	ExtractStatus(u *unstructured.Unstructured) TargetStatus
	// ApplyStatus mirrors s onto cr's own .status (conditions included) and
	// returns a human-readable summary message.
	ApplyStatus(cr PT, targetName string, s TargetStatus) (message string)
}

// ExtraResource is one auxiliary object (Ingress, Service, ...) an Adapter
// wants applied alongside its primary target. GVK/Name identify it even
// when Desired is nil, so a previously-applied extra can be deleted once the
// CR spec stops requesting it (e.g. .spec.ingress removed).
type ExtraResource struct {
	GVK  schema.GroupVersionKind
	Name string
	// Desired is the object to Server-Side-Apply, or nil to ensure the
	// object (still identified by GVK/Name above) is absent.
	Desired *unstructured.Unstructured
	// PatchOnly marks an extra whose object is created and owned by a
	// foreign controller (the primary target's own vendor operator), not by
	// this reconciler -- Desired is Server-Side-Applied only to claim
	// ownership of the specific fields it sets on that object (e.g. one
	// field deep inside a Deployment/StatefulSet the vendor operator
	// otherwise fully manages), never to create the object itself.
	// GenericReconciler skips applying it until the target already exists
	// (the vendor controller creates it, typically on a later reconcile),
	// and never deletes it on the CR's own deletion -- the vendor
	// controller's own owner reference on the object handles that once the
	// primary target goes away.
	//
	// When GVK is a StatefulSet, PatchOnly also gets a self-heal step for
	// free: see unstickStatefulSetRollout.
	PatchOnly bool
}

// ExtraResourcesAdapter is an optional Adapter extension for vendor
// integrations whose CR also drives objects that aren't the primary target
// -- currently an Ingress and/or Service used to expose a resource
// externally, for vendors with no native equivalent on the primary target's
// own spec (see internal/rabbitmq, internal/prometheus, internal/valkey).
// Every possible extra must be listed on every call, even ones not
// currently desired (Desired: nil), so GenericReconciler can prune one a
// spec change turned off.
type ExtraResourcesAdapter[T any, PT ObjectPtr[T]] interface {
	ExtraResources(cr PT, targetName, namespace, owner string) []ExtraResource
}

// PVCCleanupAdapter is an optional Adapter extension for vendor integrations
// whose target backs its pods with PersistentVolumeClaims via plain
// StatefulSet volumeClaimTemplates -- PVCs Kubernetes deliberately never
// garbage-collects on the owning StatefulSet's (or its owner's) deletion,
// unless persistentVolumeClaimRetentionPolicy.whenDeleted=Delete is set,
// which most vendor operators don't set. This is unlike CNPG/mariadb-operator,
// which manage their own PVCs directly and already delete them when their
// Cluster/MariaDB CR is removed -- so their paas CRs need no equivalent
// here. When an Adapter implements this, GenericReconciler deletes every
// PersistentVolumeClaim matching the returned label selector, in the CR's
// namespace, right after deleting the primary target on the CR's own
// deletion -- giving the same "deleting the CR deletes its storage too"
// behavior CNPG/mariadb-operator already provide.
type PVCCleanupAdapter[T any, PT ObjectPtr[T]] interface {
	// PVCLabelSelector returns the labels identifying every PVC backing
	// targetName's StatefulSets, or nil/empty to delete none.
	PVCLabelSelector(cr PT, targetName string) map[string]string
}

// IngressClassDefaultingAdapter is an optional Adapter extension for vendor
// integrations built from the shared IngressSpec (see internal/grafana,
// internal/prometheus, internal/rabbitmq). Kubernetes' own
// DefaultIngressClass admission plugin is supposed to fill in an unset
// ingressClassName from the cluster's default IngressClass, but it only
// does so on an object's initial Create, never on a later Update -- fine
// for Prometheus/RabbitMQ, whose Ingress this operator creates once,
// directly, but not for Grafana's: grafana-operator creates and then
// immediately updates its own generated Ingress every reconcile, and that
// update (built from Grafana.spec.ingress.spec, which never had a class
// admission could fill in) wipes out anything the admission plugin set on
// the original create -- the class silently ends up empty forever, even
// with a default IngressClass configured correctly.
//
// When an Adapter implements this, GenericReconciler resolves the
// cluster's own default IngressClass itself and calls SetIngressClassName
// before BuildManifest runs, whenever RequestedIngressClassName reports an
// Ingress is wanted but left unset -- replicating what the admission
// plugin does, but redone by this reconciler on every reconcile so it
// self-heals regardless of vendor create/update timing. The mutation is
// applied to the in-memory cr only, never persisted back to the CR's own
// spec.
type IngressClassDefaultingAdapter[T any, PT ObjectPtr[T]] interface {
	// RequestedIngressClassName reports the CR's own explicit
	// ingressClassName (which may be "") and whether an Ingress was
	// requested at all -- (_, false) when spec.ingress itself is unset, so
	// GenericReconciler knows not to bother resolving a default.
	RequestedIngressClassName(cr PT) (className string, requested bool)
	// SetIngressClassName sets the resolved class name in place on cr, for
	// BuildManifest to pick up in this same reconcile.
	SetIngressClassName(cr PT, className string)
}

// defaultIngressClassAnnotation marks the cluster's default IngressClass --
// the same annotation Kubernetes' own DefaultIngressClass admission plugin
// looks for.
const defaultIngressClassAnnotation = "ingressclass.kubernetes.io/is-default-class"

// GenericReconciler drives any paas CR type PT towards a matching,
// same-named foreign target object (as described by Adapter) and mirrors
// that object's status back onto the CR. This is the reconciliation logic
// shared by every vendor integration: finalizer-gated cleanup, idempotent
// Server-Side Apply, status-mirroring, and event recording.
//
// No secondary watch is wired on the foreign target: it's typically a
// foreign, unstructured type not registered with the manager's scheme, so
// an Owns()-style watch isn't available the way it is for typed children.
// Status convergence is picked up by polling instead of a push trigger --
// fast while not ready, slow once settled.
type GenericReconciler[T any, PT ObjectPtr[T]] struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	Adapter  Adapter[T, PT]
	// Name is the controller name passed to ctrl.Builder.Named().
	Name string
}

// Reconcile implements reconcile.Reconciler.
func (r *GenericReconciler[T, PT]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var obj T
	cr := PT(&obj)
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	targetName := r.Adapter.TargetName(cr.GetName())
	kind := r.Adapter.ObjectKind()
	log = log.WithValues("target", targetName)

	// -------------------------------------------------------------------
	// Deletion path
	// -------------------------------------------------------------------
	if !cr.GetDeletionTimestamp().IsZero() {
		if controllerutil.ContainsFinalizer(cr, FinalizerName) {
			log.Info(fmt.Sprintf("deletion timestamp set — deleting %s", kind))

			deleted, err := r.deleteTarget(ctx, cr.GetNamespace(), targetName)
			if err != nil {
				log.Error(err, fmt.Sprintf("failed to delete %s", kind))
				return ctrl.Result{}, err
			}
			if deleted {
				log.Info(fmt.Sprintf("deleted %s", kind))
			} else {
				log.Info(fmt.Sprintf("%s was already gone", kind))
			}

			if err := r.deleteExtraResources(ctx, cr, targetName); err != nil {
				log.Error(err, "failed to delete extra resources")
				return ctrl.Result{}, err
			}

			if err := r.deletePVCs(ctx, cr, targetName); err != nil {
				log.Error(err, "failed to delete PVCs")
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(cr, FinalizerName)
			if err := r.Update(ctx, cr); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("finalizer removed — deletion complete")
		}
		return ctrl.Result{}, nil
	}

	// -------------------------------------------------------------------
	// Normal reconcile path
	// -------------------------------------------------------------------

	// 1. Ensure finalizer is present so we get a chance to clean up the
	//    target before the CR itself is removed.
	if !controllerutil.ContainsFinalizer(cr, FinalizerName) {
		controllerutil.AddFinalizer(cr, FinalizerName)
		if err := r.Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 2. Detect create-vs-update up front, purely for event recording -- the
	//    apply below is idempotent either way.
	existing, err := r.getTarget(ctx, cr.GetNamespace(), targetName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 2b. Resolve an unset ingressClassName to the cluster's own default,
	//     for Adapters that need it (see IngressClassDefaultingAdapter) --
	//     redone every reconcile so it self-heals regardless of a vendor
	//     controller's own create/update timing.
	if err := r.defaultIngressClass(ctx, cr); err != nil {
		log.Error(err, "failed to resolve default IngressClass")
		return ctrl.Result{}, err
	}

	// 3. Apply the desired target object via Server-Side Apply.
	desired := r.Adapter.BuildManifest(cr, targetName, cr.GetNamespace(), cr.GetName())
	if err := r.applyTarget(ctx, desired); err != nil {
		log.Error(err, fmt.Sprintf("failed to apply %s", kind))
		return ctrl.Result{}, err
	}
	log.Info(fmt.Sprintf("applied %s", kind))

	if existing == nil {
		r.Recorder.Eventf(cr, nil, corev1.EventTypeNormal, "TargetCreated", "Sync",
			"%s %q created in namespace %q", kind, targetName, cr.GetNamespace())
	}

	// 3b. Apply/prune any auxiliary objects (Ingress, Service, ...) the
	//     Adapter wants alongside the primary target.
	if err := r.applyExtraResources(ctx, cr, targetName); err != nil {
		log.Error(err, "failed to apply extra resources")
		return ctrl.Result{}, err
	}

	// 4. Mirror the target's status onto our own CR.
	status := r.Adapter.ExtractStatus(desired)
	r.Adapter.ApplyStatus(cr, targetName, status)

	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconcile complete", "ready", status.Ready)

	requeueAfter := 15 * time.Second
	if status.Ready {
		requeueAfter = 5 * time.Minute
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GenericReconciler[T, PT]) SetupWithManager(mgr ctrl.Manager) error {
	var obj T
	return ctrl.NewControllerManagedBy(mgr).
		For(PT(&obj)).
		Named(r.Name).
		Complete(r)
}

func (r *GenericReconciler[T, PT]) getTarget(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	return GetTarget(ctx, r.Client, r.Adapter.GVK(), namespace, name)
}

// applyTarget applies the desired target object via Server-Side Apply.
// desired is updated in place with the object as merged by the API server
// (including .status, if the vendor controller has already populated one).
func (r *GenericReconciler[T, PT]) applyTarget(ctx context.Context, desired *unstructured.Unstructured) error {
	// client.Client.Apply (the newer typed SSA method) takes a
	// runtime.ApplyConfiguration, which unstructured.Unstructured does not
	// implement -- there is no generated apply-configuration for a foreign
	// CRD we don't own. The Patch(client.Apply) form remains the correct
	// way to Server-Side-Apply an unstructured object.
	return r.Client.Patch(ctx, desired, client.Apply, client.FieldOwner(r.Adapter.FieldManager()), client.ForceOwnership) //nolint:staticcheck
}

func (r *GenericReconciler[T, PT]) deleteTarget(ctx context.Context, namespace, name string) (bool, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(r.Adapter.GVK())
	u.SetNamespace(namespace)
	u.SetName(name)
	err := r.Delete(ctx, u)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// applyExtraResources applies/prunes the Adapter's auxiliary objects (if it
// implements ExtraResourcesAdapter), a no-op otherwise.
func (r *GenericReconciler[T, PT]) applyExtraResources(ctx context.Context, cr PT, targetName string) error {
	er, ok := r.Adapter.(ExtraResourcesAdapter[T, PT])
	if !ok {
		return nil
	}
	for _, extra := range er.ExtraResources(cr, targetName, cr.GetNamespace(), cr.GetName()) {
		if extra.Desired != nil {
			if extra.PatchOnly {
				existing, err := GetTarget(ctx, r.Client, extra.GVK, cr.GetNamespace(), extra.Name)
				if err != nil {
					return err
				}
				if existing == nil {
					// The vendor controller hasn't created its object yet --
					// nothing to claim fields on until a later reconcile.
					continue
				}
			}
			if err := r.applyTarget(ctx, extra.Desired); err != nil {
				return err
			}
			if extra.PatchOnly && extra.GVK == statefulSetGVK {
				if err := r.unstickStatefulSetRollout(ctx, cr.GetNamespace(), extra.Name); err != nil {
					return err
				}
			}
			continue
		}
		if err := r.deleteExtra(ctx, extra.GVK, cr.GetNamespace(), extra.Name); err != nil {
			return err
		}
	}
	return nil
}

// deleteExtraResources unconditionally deletes every non-PatchOnly auxiliary
// object the Adapter's ExtraResources reports for cr (if it implements
// ExtraResourcesAdapter), a no-op otherwise. Called on the CR's own deletion
// path, mirroring deleteTarget's cleanup of the primary target. PatchOnly
// extras are skipped -- this reconciler never owned the object itself, only
// specific fields on it, and the vendor controller's own owner reference
// handles its cleanup once the primary target is gone.
func (r *GenericReconciler[T, PT]) deleteExtraResources(ctx context.Context, cr PT, targetName string) error {
	er, ok := r.Adapter.(ExtraResourcesAdapter[T, PT])
	if !ok {
		return nil
	}
	for _, extra := range er.ExtraResources(cr, targetName, cr.GetNamespace(), cr.GetName()) {
		if extra.PatchOnly {
			continue
		}
		if err := r.deleteExtra(ctx, extra.GVK, cr.GetNamespace(), extra.Name); err != nil {
			return err
		}
	}
	return nil
}

// deletePVCs deletes every PersistentVolumeClaim matching the Adapter's
// PVCLabelSelector (if it implements PVCCleanupAdapter), a no-op otherwise
// or when the selector is empty. Called on the CR's own deletion path,
// right after deleteTarget/deleteExtraResources -- see PVCCleanupAdapter's
// doc comment for why this is needed for some vendors and not others.
func (r *GenericReconciler[T, PT]) deletePVCs(ctx context.Context, cr PT, targetName string) error {
	pvcAdapter, ok := r.Adapter.(PVCCleanupAdapter[T, PT])
	if !ok {
		return nil
	}
	labels := pvcAdapter.PVCLabelSelector(cr, targetName)
	if len(labels) == 0 {
		return nil
	}
	return r.DeleteAllOf(ctx, &corev1.PersistentVolumeClaim{}, client.InNamespace(cr.GetNamespace()), client.MatchingLabels(labels))
}

// defaultIngressClass resolves cr's own ingressClassName to the cluster's
// default IngressClass, in place, for Adapters implementing
// IngressClassDefaultingAdapter -- a no-op otherwise, when no Ingress was
// requested, when a class is already set, or when the cluster has no (or
// more than one) IngressClass annotated as default, exactly mirroring
// Kubernetes' own DefaultIngressClass admission plugin's no-op cases. See
// IngressClassDefaultingAdapter's doc comment for why this is needed at all
// instead of just relying on that admission plugin.
func (r *GenericReconciler[T, PT]) defaultIngressClass(ctx context.Context, cr PT) error {
	ica, ok := r.Adapter.(IngressClassDefaultingAdapter[T, PT])
	if !ok {
		return nil
	}
	className, requested := ica.RequestedIngressClassName(cr)
	if !requested || className != "" {
		return nil
	}
	resolved, err := r.resolveDefaultIngressClass(ctx)
	if err != nil || resolved == "" {
		return err
	}
	ica.SetIngressClassName(cr, resolved)
	return nil
}

// resolveDefaultIngressClass returns the name of the cluster's single
// IngressClass annotated ingressclass.kubernetes.io/is-default-class=true,
// or "" if there is none or more than one (ambiguous -- Kubernetes' own
// admission plugin also declines to guess in that case).
func (r *GenericReconciler[T, PT]) resolveDefaultIngressClass(ctx context.Context) (string, error) {
	var classes networkingv1.IngressClassList
	if err := r.List(ctx, &classes); err != nil {
		return "", err
	}
	resolved := ""
	for _, ic := range classes.Items {
		if ic.Annotations[defaultIngressClassAnnotation] != "true" {
			continue
		}
		if resolved != "" {
			return "", nil
		}
		resolved = ic.Name
	}
	return resolved, nil
}

// deleteExtra deletes one auxiliary object by GVK/name, ignoring not-found.
func (r *GenericReconciler[T, PT]) deleteExtra(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetNamespace(namespace)
	u.SetName(name)
	err := r.Delete(ctx, u)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// statefulSetGVK identifies the built-in apps/v1 StatefulSet kind, checked
// against in applyExtraResources to trigger unstickStatefulSetRollout.
var statefulSetGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}

// unstickStatefulSetRollout breaks a structural deadlock in Kubernetes'
// own StatefulSet controller that a PatchOnly field claim can otherwise run
// straight into: with the (default) OrderedReady podManagementPolicy, that
// controller refuses to roll a template change out to a pod until the pod
// it's replacing first becomes Ready. If the pod is crash-looping for
// exactly the reason the template change just fixed (e.g. our fsGroup
// patch in internal/loki), it can never become Ready, so the fix sits in
// the template forever without reaching the pod -- no amount of
// Server-Side-Apply retries changes that, since the object itself is
// already correct and generates no further events.
//
// Called right after a PatchOnly patch lands on a StatefulSet, this deletes
// any of its pods that are both on a stale controller-revision-hash and not
// Ready, letting the StatefulSet controller recreate them -- this time from
// the already-patched template. Pods that are Ready are left alone even if
// stale, so an in-progress, healthy rolling update is never interrupted.
func (r *GenericReconciler[T, PT]) unstickStatefulSetRollout(ctx context.Context, namespace, name string) error {
	sts, err := GetTarget(ctx, r.Client, statefulSetGVK, namespace, name)
	if err != nil || sts == nil {
		return err
	}
	updateRevision, _, _ := unstructured.NestedString(sts.Object, "status", "updateRevision")
	currentRevision, _, _ := unstructured.NestedString(sts.Object, "status", "currentRevision")
	if updateRevision == "" || updateRevision == currentRevision {
		return nil
	}
	selector, _, _ := unstructured.NestedStringMap(sts.Object, "spec", "selector", "matchLabels")
	if len(selector) == 0 {
		return nil
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(namespace), client.MatchingLabels(selector)); err != nil {
		return err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Labels["controller-revision-hash"] == updateRevision || isPodReady(pod) {
			continue
		}
		if err := r.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// isPodReady reports whether pod's Ready condition is True.
func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// GetTarget fetches the current foreign target object identified by gvk,
// returning (nil, nil) if it does not exist. Exposed (in addition to being
// used internally) so tests can assert on the generated manifest without
// driving a full Reconcile.
func GetTarget(ctx context.Context, c client.Client, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, u)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}
