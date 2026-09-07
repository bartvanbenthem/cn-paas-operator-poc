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

// ValkeyClusterSpec defines the desired state of ValkeyCluster.
//
// It is a minimal, opinionated front for a valkey-operator valkey.io/v1alpha1
// ValkeyCluster: the operator creates and manages a same-named ValkeyCluster
// from this spec. Only the fields most deployments need to set are exposed
// here; everything else in the generated ValkeyCluster is left at the
// valkey-operator's own defaults. See internal/valkey for the mapping.
type ValkeyClusterSpec struct {
	// shards is the number of shard groups. Each shard group has one
	// primary and `replicas` replicas.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	Shards int32 `json:"shards,omitempty"`

	// replicas is the number of replicas for each shard group.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// image is the full container image reference for the Valkey instances.
	// Defaults to the valkey-operator's own default image when unset.
	// +optional
	Image string `json:"image,omitempty"`

	// persistence settings for each node's data volume.
	// +required
	Persistence PersistenceSpec `json:"persistence"`

	// resources are the optional compute resource requests/limits for the
	// Valkey containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// expose creates an externally-reachable Service for the cluster's
	// data-plane port (6379). The valkey-operator has no native
	// externally-reachable Service of its own (its only generated Service is
	// headless/ClusterIP), so when set this operator also creates and
	// manages a separate LoadBalancer/NodePort Service.
	// +optional
	Expose *ServiceExposeSpec `json:"expose,omitempty"`

	// monitoring configures Prometheus metrics collection for the underlying
	// ValkeyCluster. Unlike PostgresCluster/MariaDBCluster, the
	// valkey-operator has no native PodMonitor toggle of its own -- setting
	// this also drives its per-pod metrics-exporter sidecar (spec.exporter),
	// and this operator creates the PodMonitor directly rather than asking
	// the valkey-operator to.
	// +optional
	Monitoring MonitoringSpec `json:"monitoring,omitzero"`
}

// PersistenceSpec describes the data volume for each Valkey node.
type PersistenceSpec struct {
	// size is the volume size, e.g. "10Gi".
	// +required
	Size string `json:"size"`

	// storageClass name. Defaults to the cluster's default StorageClass when unset.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`
}

// ValkeyClusterStatus defines the observed state of ValkeyCluster.
//
// It mirrors the subset of the underlying ValkeyCluster's status that
// matters for "is my cache usable yet".
type ValkeyClusterStatus struct {
	// ready is true once the underlying ValkeyCluster reports all shards ready.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is a verbatim copy of the underlying ValkeyCluster's status.state.
	// +optional
	Phase string `json:"phase,omitempty"`

	// shards is the underlying ValkeyCluster's status.shards.
	// +optional
	Shards int32 `json:"shards,omitempty"`

	// readyShards is the underlying ValkeyCluster's status.readyShards.
	// +optional
	ReadyShards int32 `json:"readyShards,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the ValkeyCluster resource.
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
// +kubebuilder:resource:shortName=vkc
// +kubebuilder:printcolumn:name="Shards",type=integer,JSONPath=`.spec.shards`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ValkeyCluster is the Schema for the valkeyclusters API
type ValkeyCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ValkeyCluster
	// +required
	Spec ValkeyClusterSpec `json:"spec"`

	// status defines the observed state of ValkeyCluster
	// +optional
	Status ValkeyClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ValkeyClusterList contains a list of ValkeyCluster
type ValkeyClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ValkeyCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ValkeyCluster{}, &ValkeyClusterList{})
		return nil
	})
}
