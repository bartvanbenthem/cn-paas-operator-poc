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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PostgresClusterSpec defines the desired state of PostgresCluster.
//
// It is a minimal, opinionated front for a CloudNativePG postgresql.cnpg.io/v1
// Cluster: the operator creates and manages a same-named CNPG Cluster from
// this spec. Only the fields most deployments need to set are exposed here;
// everything else in the generated CNPG Cluster is left at CNPG's own
// defaults. See internal/cnpg for the mapping.
type PostgresClusterSpec struct {
	// instances is the number of PostgreSQL instances (1 primary + N-1 replicas).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	Instances int32 `json:"instances,omitempty"`

	// image is the full container image reference for the PostgreSQL instances.
	// Defaults to CNPG's own default image when unset.
	// +optional
	Image string `json:"image,omitempty"`

	// storage settings for each instance's PGDATA volume.
	// +required
	Storage StorageSpec `json:"storage"`

	// database is the bootstrap database created via initdb on first startup.
	// +required
	Database DatabaseSpec `json:"database"`

	// resources are the optional compute resource requests/limits for the
	// PostgreSQL containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// monitoring configures Prometheus metrics collection for the underlying
	// CNPG Cluster.
	// +optional
	Monitoring MonitoringSpec `json:"monitoring,omitzero"`

	// expose creates an externally-reachable Service for the primary
	// (read-write) endpoint, via CNPG's own spec.managed.services.additional
	// -- no separate Service object is created by this operator.
	// +optional
	Expose *ServiceExposeSpec `json:"expose,omitempty"`
}

// MonitoringSpec configures Prometheus metrics collection for a managed
// resource. It is shared across every resource kind this operator manages
// (PostgresCluster, MariaDBCluster, ValkeyCluster, RabbitMQCluster) so the
// toggle behaves the same way everywhere it appears -- for PostgresCluster
// and MariaDBCluster the underlying vendor operator creates the PodMonitor/
// ServiceMonitor itself; for ValkeyCluster and RabbitMQCluster, whose vendor
// operators have no such native toggle, this operator builds one directly
// (see internal/valkey and internal/rabbitmq). Enabling it also requests a
// matching GrafanaDashboard, on every resource kind that has one -- see
// internal/grafana's package doc for the instanceSelector/datasource
// convention it relies on.
type MonitoringSpec struct {
	// enablePodMonitor creates namespace-scoped Prometheus scrape config
	// (a PodMonitor or ServiceMonitor, depending on what the underlying
	// vendor operator supports) for this resource, in the same namespace as
	// the resource itself. Enabled by default. Requires the Prometheus
	// Operator's PodMonitor/ServiceMonitor CRDs to be installed. Also
	// requests a matching GrafanaDashboard, on resource kinds that have one
	// -- requires the grafana-operator's GrafanaDashboard CRD to be
	// installed and a GrafanaInstance in the same namespace to pick it up.
	// +kubebuilder:default=true
	// +optional
	EnablePodMonitor bool `json:"enablePodMonitor,omitempty"`
}

// StorageSpec describes the PGDATA volume for each instance.
type StorageSpec struct {
	// size is the volume size, e.g. "10Gi".
	// +required
	Size string `json:"size"`

	// storageClass name. Defaults to the cluster's default StorageClass when unset.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`
}

// DatabaseSpec describes the database created by initdb on first startup.
type DatabaseSpec struct {
	// name of the database created by initdb.
	// +required
	Name string `json:"name"`

	// owner is the owning role of the bootstrap database.
	// +required
	Owner string `json:"owner"`
}

// PostgresClusterStatus defines the observed state of PostgresCluster.
//
// It mirrors the subset of the underlying CNPG Cluster's status that matters
// for "is my database usable yet".
type PostgresClusterStatus struct {
	// ready is true once the underlying CNPG Cluster reports all instances ready.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is a verbatim copy of the underlying CNPG Cluster's status.phase.
	// +optional
	Phase string `json:"phase,omitempty"`

	// instances is the underlying CNPG Cluster's status.instances.
	// +optional
	Instances int32 `json:"instances,omitempty"`

	// readyInstances is the underlying CNPG Cluster's status.readyInstances.
	// +optional
	ReadyInstances int32 `json:"readyInstances,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the PostgresCluster resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=pgc
// +kubebuilder:printcolumn:name="Instances",type=integer,JSONPath=`.spec.instances`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PostgresCluster is the Schema for the postgresclusters API
type PostgresCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PostgresCluster
	// +required
	Spec PostgresClusterSpec `json:"spec"`

	// status defines the observed state of PostgresCluster
	// +optional
	Status PostgresClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PostgresClusterList contains a list of PostgresCluster
type PostgresClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PostgresCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PostgresCluster{}, &PostgresClusterList{})
		return nil
	})
}
