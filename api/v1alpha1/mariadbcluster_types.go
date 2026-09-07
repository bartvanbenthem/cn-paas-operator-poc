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

// MariaDBClusterSpec defines the desired state of MariaDBCluster.
//
// It is a minimal, opinionated front for a mariadb-operator
// k8s.mariadb.com/v1alpha1 MariaDB: the operator creates and manages a
// same-named MariaDB from this spec. Only the fields most deployments need
// to set are exposed here; everything else in the generated MariaDB is left
// at mariadb-operator's own defaults. See internal/mariadb for the mapping.
type MariaDBClusterSpec struct {
	// replicas is the number of MariaDB instances. Values greater than 1
	// enable Galera Cluster (synchronous multi-primary replication) so the
	// cluster tolerates node loss; 1 runs a single standalone instance.
	// mariadb-operator requires an odd count when greater than 1.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// image is the full container image reference ("<image>:<tag>") for the
	// MariaDB instances. Defaults to mariadb-operator's own default image
	// when unset.
	// +optional
	Image string `json:"image,omitempty"`

	// storage settings for each instance's data volume.
	// +required
	Storage StorageSpec `json:"storage"`

	// database is the bootstrap database created on first startup.
	// +required
	Database DatabaseSpec `json:"database"`

	// resources are the optional compute resource requests/limits for the
	// MariaDB containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// monitoring configures Prometheus metrics collection for the underlying
	// MariaDB.
	// +kubebuilder:default={}
	// +optional
	Monitoring MonitoringSpec `json:"monitoring,omitzero"`

	// expose changes the type of the underlying MariaDB's own primary
	// Service (spec.service.type) so it is reachable outside the cluster --
	// no separate Service object is created by this operator.
	// +optional
	Expose *ServiceExposeSpec `json:"expose,omitempty"`
}

// MariaDBClusterStatus defines the observed state of MariaDBCluster.
//
// It mirrors the subset of the underlying MariaDB's status that matters for
// "is my database usable yet".
type MariaDBClusterStatus struct {
	// ready is true once the underlying MariaDB reports its "Ready"
	// condition as True.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is the reason of the underlying MariaDB's "Ready" condition
	// (mariadb-operator has no separate status.phase field).
	// +optional
	Phase string `json:"phase,omitempty"`

	// replicas is the underlying MariaDB's status.replicas.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// readyReplicas is derived, not a verbatim field: mariadb-operator
	// doesn't report a separate ready count, so this is replicas when the
	// "Ready" condition is True, 0 otherwise.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the MariaDBCluster resource.
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
// +kubebuilder:resource:shortName=mdc
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MariaDBCluster is the Schema for the mariadbclusters API
type MariaDBCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of MariaDBCluster
	// +required
	Spec MariaDBClusterSpec `json:"spec"`

	// status defines the observed state of MariaDBCluster
	// +optional
	Status MariaDBClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// MariaDBClusterList contains a list of MariaDBCluster
type MariaDBClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MariaDBCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &MariaDBCluster{}, &MariaDBClusterList{})
		return nil
	})
}
