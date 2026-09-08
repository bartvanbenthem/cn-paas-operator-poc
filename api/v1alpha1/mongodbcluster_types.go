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

// MongoDBClusterSpec defines the desired state of MongoDBCluster.
//
// It is a minimal, opinionated front for a Percona Server for MongoDB
// Operator psmdb.percona.com/v1 PerconaServerMongoDB: the operator creates
// and manages a same-named PerconaServerMongoDB with exactly one replica set
// (named "rs0") from this spec. Sharded topologies (multiple replsets,
// mongos, config servers) are out of scope, matching how PostgresCluster/
// MariaDBCluster/ValkeyCluster don't model their own vendor's sharding/
// multi-shard topologies either. Only the fields most deployments need to
// set are exposed here; everything else in the generated PerconaServerMongoDB
// is left at the operator's own defaults. See internal/psmdb for the
// mapping.
//
// Unlike PostgresCluster/MariaDBCluster, there is no DatabaseSpec-equivalent
// field here: MongoDB has no bootstrap-database-with-an-owning-role concept
// analogous to SQL's CREATE DATABASE/initdb -- databases and collections in
// MongoDB are created implicitly on first write, so there is nothing to
// bootstrap at the PerconaServerMongoDB level.
type MongoDBClusterSpec struct {
	// replicas is the number of mongod members in the single replica set
	// ("rs0") this operator manages. The Percona Server for MongoDB Operator
	// requires an odd count for proper primary-election quorum.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// image is the full container image reference for the mongod instances.
	// Percona Server for MongoDB Operator's own CRD requires spec.image to be
	// set (it has no built-in default), so this field defaults to the
	// current Percona Server for MongoDB image rather than being left empty.
	// +kubebuilder:default="percona/percona-server-mongodb:8.0.26-11"
	// +optional
	Image string `json:"image,omitempty"`

	// storage settings for each replica set member's data volume.
	// +required
	Storage StorageSpec `json:"storage"`

	// resources are the optional compute resource requests/limits for the
	// mongod containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// monitoring configures Prometheus metrics collection for the underlying
	// PerconaServerMongoDB. The Percona operator has no native PodMonitor/
	// ServiceMonitor toggle of its own -- it ships its own PMM-based
	// monitoring stack instead, which this operator does not use. Enabling
	// this instead injects a percona/mongodb_exporter sidecar into every
	// mongod pod and creates a PodMonitor targeting it directly, the same
	// approach internal/valkey takes for the valkey-operator.
	// +kubebuilder:default={}
	// +optional
	Monitoring MonitoringSpec `json:"monitoring,omitzero"`

	// expose changes the type of the underlying PerconaServerMongoDB's own
	// replica set Service (spec.replsets[].expose) so it is reachable outside
	// the cluster -- no separate Service object is created by this operator.
	// +optional
	Expose *ServiceExposeSpec `json:"expose,omitempty"`
}

// MongoDBClusterStatus defines the observed state of MongoDBCluster.
//
// It mirrors the subset of the underlying PerconaServerMongoDB's status that
// matters for "is my database usable yet".
type MongoDBClusterStatus struct {
	// ready is true once the underlying PerconaServerMongoDB reports its
	// ready member count as equal to its size.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is a verbatim copy of the underlying PerconaServerMongoDB's
	// status.state (e.g. "initializing", "ready", "error").
	// +optional
	Phase string `json:"phase,omitempty"`

	// replicas is the underlying PerconaServerMongoDB's status.size.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// readyReplicas is the underlying PerconaServerMongoDB's status.ready.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the MongoDBCluster resource.
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
// +kubebuilder:resource:shortName=mgc
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MongoDBCluster is the Schema for the mongodbclusters API
type MongoDBCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of MongoDBCluster
	// +required
	Spec MongoDBClusterSpec `json:"spec"`

	// status defines the observed state of MongoDBCluster
	// +optional
	Status MongoDBClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// MongoDBClusterList contains a list of MongoDBCluster
type MongoDBClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MongoDBCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &MongoDBCluster{}, &MongoDBClusterList{})
		return nil
	})
}
