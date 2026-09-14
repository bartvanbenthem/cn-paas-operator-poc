// Package psmdb contains everything that talks to the Percona Server for
// MongoDB Operator's psmdb.percona.com/v1 PerconaServerMongoDB
// (github.com/percona/percona-server-mongodb-operator).
//
// As with internal/mariadb, internal/valkey, and internal/cnpg, we
// deliberately do not vendor the Percona operator's own Go API types or its
// CRD schema. PerconaServerMongoDB is addressed purely through
// controller-runtime's dynamic client (unstructured.Unstructured), and the
// desired object is built as a plain map applied via Server-Side Apply. This
// keeps the operator decoupled from any specific percona-server-mongodb-
// operator version. See external-crds/crd-percona-server-mongodb-operator-
// v1.23.0.yaml for the schema this was built against (field paths below were
// verified directly against that schema, not merely against Percona's
// documentation).
//
// MongoDBClusterSpec models exactly one, non-sharded replica set (named
// "rs0") -- sharded topologies (multiple replsets, mongos, config servers)
// are out of scope, matching how internal/mariadb, internal/valkey, and
// internal/cnpg don't model their own vendor's sharding/multi-topology
// features either.
//
// Monitoring: the Percona operator has no native PodMonitor/ServiceMonitor
// toggle -- its own answer to monitoring is PMM (Percona Monitoring and
// Management), a separate stack this operator does not use, since every
// other building block here standardizes on the Prometheus Operator +
// Grafana instead. So, mirroring internal/valkey's workaround for
// valkey-operator (which has the same gap), enabling
// spec.monitoring.enablePodMonitor here injects a percona/mongodb_exporter
// sidecar into the replset pod (using the operator's own auto-generated
// "clusterMonitor" low-privilege credentials) and this package's own
// ExtraResources builds a PodMonitor targeting that sidecar directly, plus a
// GrafanaDashboard -- see internal/grafana's package doc for the
// instanceSelector/datasource convention it relies on.
package psmdb

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
	Group        = "psmdb.percona.com"
	Version      = "v1"
	Kind         = "PerconaServerMongoDB"
	FieldManager = "mongodbcluster-operator"

	// replsetName is the name of the single, non-sharded replica set this
	// operator manages -- MongoDBClusterSpec models exactly one replset, so
	// this name never needs to vary per-CR.
	replsetName = "rs0"

	// exporterPort is percona/mongodb_exporter's default metrics port.
	exporterPort = 9216

	// instanceLabelKey and componentLabel are the pod labels the Percona
	// operator applies to every mongod pod belonging to one
	// PerconaServerMongoDB's replset -- confirmed by reading
	// pkg/naming/labels.go in github.com/percona/percona-server-mongodb-operator
	// (LabelKubernetesInstance = "app.kubernetes.io/instance",
	// LabelKubernetesComponent = "app.kubernetes.io/component"), not from a
	// published API guarantee (mirroring internal/valkey's
	// clusterSelectorLabel doc comment). May need re-checking after a
	// percona-server-mongodb-operator upgrade. Other componentLabel values
	// (mongos, arbiter, cfg, ...) exist for sharded/other roles; not used
	// here since MongoDBCluster only ever manages a single, non-sharded
	// replset (component "mongod").
	instanceLabelKey = "app.kubernetes.io/instance"
	componentLabel   = "app.kubernetes.io/component"
	componentValue   = "mongod"

	// secretsUsersSuffix names the Secret the Percona operator generates (if
	// absent) holding auto-generated system-user credentials, referenced via
	// spec.secrets.users (a bare string field -- confirmed against the
	// vendored CRD schema). Deliberately kind-scoped ("-psmdb-secrets"), not
	// the unscoped "<name>-secrets" used by upstream's own sample manifest --
	// see internal/mariadb's appSecretSuffix doc comment and internal/cnpg's
	// package doc for why an unscoped name risks collision with another
	// building block CR sharing the same name in the same namespace.
	secretsUsersSuffix = "-psmdb-secrets"

	// clusterMonitorUserKey and clusterMonitorPasswordKey are the keys the
	// Percona operator writes into the secrets.users Secret for the
	// low-privilege "clusterMonitor" system user -- the correct credential
	// pair for a metrics exporter to use rather than an admin user.
	clusterMonitorUserKey     = "MONGODB_CLUSTER_MONITOR_USER"
	clusterMonitorPasswordKey = "MONGODB_CLUSTER_MONITOR_PASSWORD"

	// exporterContainerName names the metrics-exporter sidecar this adapter
	// injects into spec.replsets[0].sidecars when monitoring is enabled.
	exporterContainerName = "mongodb-exporter"

	// exporterImage is the percona/mongodb_exporter image used for the
	// metrics sidecar. Pinned to a specific tag rather than "latest".
	exporterImage = "percona/mongodb_exporter:0.53.0"

	// unknownPhase is the Phase reported when the underlying
	// PerconaServerMongoDB has no status yet, or reports an empty state.
	unknownPhase = "Unknown"
)

// GVK is the GroupVersionKind of the Percona Server for MongoDB Operator
// PerconaServerMongoDB this operator manages.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// podMonitorGVK is the GroupVersionKind of the PodMonitor this package's
// ExtraResources builds directly, since the Percona operator creates none of
// its own (see the package doc).
var podMonitorGVK = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PodMonitor"}

// dashboardGVK is the GroupVersionKind of the grafana-operator
// GrafanaDashboard this package's ExtraResources builds.
var dashboardGVK = schema.GroupVersionKind{Group: grafana.Group, Version: grafana.Version, Kind: "GrafanaDashboard"}

// dashboardJSON is grafana.com dashboard 14997 ("MongoDB"), revision 3 --
// https://grafana.com/grafana/dashboards/14997-mongodb/ -- a fork of the
// older community dashboard 2583 built specifically to work with
// github.com/percona/mongodb_exporter's metric format (the exporter this
// package's BuildManifest injects as a sidecar). Its panels reference the
// Prometheus datasource via the templated input "${DS_PROMETHEUS}" -- see
// ExtraResources below.
//
//go:embed dashboards/cluster.json
var dashboardJSON string

// Adapter drives a paas MongoDBCluster onto a same-named Percona Server for
// MongoDB Operator PerconaServerMongoDB. It implements
// reconciler.Adapter[paasv1alpha1.MongoDBCluster, *paasv1alpha1.MongoDBCluster].
type Adapter struct{}

func (Adapter) GVK() schema.GroupVersionKind { return GVK }

// TargetName returns the name of the PerconaServerMongoDB generated for
// crName. One paas MongoDBCluster maps to exactly one same-named upstream
// PerconaServerMongoDB.
func (Adapter) TargetName(crName string) string { return crName }

func (Adapter) ObjectKind() string   { return "PerconaServerMongoDB" }
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

// BuildManifest builds the desired psmdb.percona.com/v1 PerconaServerMongoDB
// object for cr, ready to be applied via Server-Side Apply.
func (Adapter) BuildManifest(cr *paasv1alpha1.MongoDBCluster, name, namespace, ownerName string) *unstructured.Unstructured {
	spec := cr.Spec

	pvcSpec := map[string]any{
		"accessModes": []any{"ReadWriteOnce"},
		"resources": map[string]any{
			"requests": map[string]any{"storage": spec.Storage.Size},
		},
	}
	if spec.Storage.StorageClass != "" {
		pvcSpec["storageClassName"] = spec.Storage.StorageClass
	}

	replset := map[string]any{
		"name": replsetName,          //nolint:goconst // "name" is an unrelated JSON key in each of its occurrences
		"size": int64(spec.Replicas), //nolint:goconst // "size" is an unrelated JSON key in each of its occurrences
		"volumeSpec": map[string]any{
			"persistentVolumeClaim": pvcSpec,
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
		replset["resources"] = resources
	}

	if spec.Expose != nil {
		expose := map[string]any{
			"enabled": true,
			"type":    string(spec.Expose.Type),
		}
		if len(spec.Expose.Annotations) > 0 {
			annotations := make(map[string]any, len(spec.Expose.Annotations))
			for k, v := range spec.Expose.Annotations {
				annotations[k] = v
			}
			expose["serviceAnnotations"] = annotations
		}
		replset["expose"] = expose
	}

	// secretsName is kind-scoped -- see secretsUsersSuffix's doc comment.
	secretsName := name + secretsUsersSuffix

	if spec.Monitoring.EnablePodMonitor {
		replset["sidecars"] = []any{
			map[string]any{
				// percona/mongodb_exporter's image is FROM scratch with a
				// single static ENTRYPOINT (["/mongodb_exporter"]) -- no
				// shell, so no "command: sh -c" wrapper. Credentials go
				// through the exporter's own MONGODB_USER/MONGODB_PASSWORD
				// env vars (which it combines with a credential-less
				// --mongodb.uri) rather than being embedded in the URI
				// string: that avoids leaking them via ps/top on the node,
				// and avoids a generated password containing URI-special
				// characters (@, /, #, ...) corrupting the URI.
				"name":  exporterContainerName,
				"image": exporterImage,
				"args": []any{
					"--mongodb.uri=mongodb://localhost:27017/admin?ssl=false",
					"--collect-all",
					"--compatible-mode",
				},
				"ports": []any{
					map[string]any{"name": "metrics", "containerPort": int64(exporterPort)},
				},
				"env": []any{
					map[string]any{
						"name": "MONGODB_USER",
						"valueFrom": map[string]any{
							"secretKeyRef": map[string]any{
								"name": secretsName,
								"key":  clusterMonitorUserKey,
							},
						},
					},
					map[string]any{
						"name": "MONGODB_PASSWORD",
						"valueFrom": map[string]any{
							"secretKeyRef": map[string]any{
								"name": secretsName,
								"key":  clusterMonitorPasswordKey,
							},
						},
					},
				},
			},
		}
	}

	clusterSpec := map[string]any{
		// spec.image is required by the Percona operator's own CRD (it has
		// no built-in default), so MongoDBClusterSpec.Image itself carries a
		// +kubebuilder:default and is always non-empty here.
		"image":    spec.Image,
		"replsets": []any{replset},
		"secrets": map[string]any{
			"users": secretsName,
		},
		// enableVolumeScaling is off by default in the Percona operator; without
		// it, growing MongoDBClusterSpec.Storage.Size updates this CR but the
		// Percona operator never resizes the underlying PVC.
		"storageScaling": map[string]any{
			"enableVolumeScaling": true,
		},
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(commonLabels(ownerName))
	// percona.com/delete-psmdb-pvc is off by default in the Percona operator;
	// setting it here has the Percona operator itself clean up the PVCs when
	// this PerconaServerMongoDB is deleted, matching the finalizer-gated
	// deletion this operator already performs for every other child object.
	u.SetFinalizers([]string{"percona.com/delete-psmdb-pvc"})
	u.Object["spec"] = clusterSpec

	return u
}

// ExtraResources builds the PodMonitor and GrafanaDashboard for cr, both
// gated on spec.monitoring.enablePodMonitor (pruned via Desired: nil when
// disabled) -- see the package doc for why this package builds them
// directly rather than asking the Percona operator to.
func (Adapter) ExtraResources(cr *paasv1alpha1.MongoDBCluster, targetName, namespace, owner string) []reconciler.ExtraResource {
	podMonitorName := targetName + "-podmonitor"
	dashboardName := targetName + "-dashboard"

	if !cr.Spec.Monitoring.EnablePodMonitor {
		return []reconciler.ExtraResource{
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
			"matchLabels": map[string]any{
				instanceLabelKey: targetName,
				componentLabel:   componentValue,
			},
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
		"folder":           "MongoDB",
		"datasources": []any{
			map[string]any{"inputName": "DS_PROMETHEUS", "datasourceName": grafana.DatasourceUID},
		},
		"json": dashboardJSON,
	}

	return []reconciler.ExtraResource{
		{GVK: podMonitorGVK, Name: podMonitorName, Desired: podMonitor},
		{GVK: dashboardGVK, Name: dashboardName, Desired: dashboard},
	}
}

// ExtractStatus pulls state/size/ready out of a PerconaServerMongoDB's
// .status. These are top-level, cluster-wide fields (not nested under a
// per-replset map), confirmed directly against the vendored CRD schema.
// Ready is derived from the size/ready counts rather than the state string,
// matching internal/valkey's and internal/cnpg's own rationale for doing so:
// the exact wording of state is not treated as a stable contract here even
// though Percona's docs enumerate it (""|initializing|stopping|paused|
// ready|error).
func (Adapter) ExtractStatus(u *unstructured.Unstructured) reconciler.TargetStatus {
	if u == nil {
		return reconciler.TargetStatus{Phase: unknownPhase}
	}

	state, found, _ := unstructured.NestedString(u.Object, "status", "state")
	if !found || state == "" {
		state = unknownPhase
	}

	size, _, _ := unstructured.NestedInt64(u.Object, "status", "size")
	ready, _, _ := unstructured.NestedInt64(u.Object, "status", "ready")
	observed := int32(size)
	readyCount := int32(ready)

	return reconciler.TargetStatus{
		Phase:         state,
		Ready:         observed > 0 && readyCount >= observed,
		ObservedCount: observed,
		ReadyCount:    readyCount,
	}
}

// ApplyStatus mirrors s onto cr's own .status (replicas/readyReplicas + a
// standard Ready condition).
func (Adapter) ApplyStatus(cr *paasv1alpha1.MongoDBCluster, targetName string, s reconciler.TargetStatus) string {
	var message string
	if s.Ready {
		message = fmt.Sprintf("PerconaServerMongoDB %q is ready (%d/%d members)", targetName, s.ReadyCount, s.ObservedCount)
	} else {
		message = fmt.Sprintf("Waiting for PerconaServerMongoDB %q (state: %s)", targetName, s.Phase)
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
