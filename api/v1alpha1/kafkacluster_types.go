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

// KafkaClusterSpec defines the desired state of KafkaCluster.
//
// It is a minimal, opinionated front for a Strimzi Kafka Operator
// kafka.strimzi.io/v1 Kafka: the operator creates and manages a same-named
// Kafka, plus one KafkaNodePool combining the controller and broker roles,
// from this spec. Strimzi is KRaft-only (ZooKeeper has been fully removed)
// and requires at least one KafkaNodePool with the controller role and one
// with the broker role -- this can be the same pool or separate ones.
// KafkaClusterSpec always uses a single combined-role pool sized by
// Replicas, matching Strimzi's own documented shape for small/dev clusters
// (roughly up to 5 brokers) and keeping this spec to one replica count, like
// every other building block here. Dedicated controller/broker pools are
// out of scope. Only the fields most deployments need to set are exposed
// here; everything else in the generated Kafka/KafkaNodePool is left at
// Strimzi's own defaults. See internal/strimzi for the mapping.
//
// Like MongoDBCluster, there is no DatabaseSpec-equivalent field here: Kafka
// has no bootstrap-database-with-an-owning-role concept to bootstrap.
type KafkaClusterSpec struct {
	// replicas is the number of nodes in the single combined controller+
	// broker KafkaNodePool this operator manages. An odd count is
	// recommended for KRaft controller-quorum elections, mirroring
	// MariaDBCluster's Galera-quorum recommendation.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// version is the Kafka version, e.g. "4.3.1". Defaults to the Strimzi
	// Kafka Operator's own default supported version when unset.
	// +optional
	Version string `json:"version,omitempty"`

	// storage settings for the node pool's data (and KRaft metadata) volume.
	// +required
	Storage StorageSpec `json:"storage"`

	// resources are the optional compute resource requests/limits for the
	// Kafka node containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// monitoring configures Prometheus metrics collection for the underlying
	// Kafka. The Strimzi Kafka Operator has no native PodMonitor/
	// ServiceMonitor toggle of its own, so enabling this sets
	// spec.kafka.metricsConfig to use the Strimzi Metrics Reporter (a native
	// Kafka plugin, not a sidecar) and this operator creates the PodMonitor
	// directly, the same approach internal/valkey and internal/psmdb take
	// for their own vendor operators.
	// +kubebuilder:default={}
	// +optional
	Monitoring MonitoringSpec `json:"monitoring,omitzero"`

	// expose adds a second, externally-reachable listener (spec.kafka.
	// listeners) to the underlying Kafka alongside its always-present
	// internal listener -- no separate Service object is created by this
	// operator (Strimzi manages its own Services per listener).
	// +optional
	Expose *ServiceExposeSpec `json:"expose,omitempty"`
}

// KafkaClusterStatus defines the observed state of KafkaCluster.
//
// Unlike MariaDBClusterStatus/MongoDBClusterStatus, this carries no replica/
// ready-count fields: the underlying Kafka's own .status has none (only
// status.conditions[], confirmed against Strimzi's CRD schema) -- Strimzi
// reports per-node-pool counts on each KafkaNodePool's own status instead,
// which this operator's ExtractStatus never sees (it's an auxiliary
// ExtraResource, not the primary target). Readiness is derived purely from
// the underlying Kafka's "Ready" condition.
type KafkaClusterStatus struct {
	// ready is true once the underlying Kafka reports its "Ready" condition
	// as True.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is the reason of the underlying Kafka's "Ready" condition
	// (Strimzi has no separate status.phase field).
	// +optional
	Phase string `json:"phase,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the KafkaCluster resource.
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
// +kubebuilder:resource:shortName=kfc
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KafkaCluster is the Schema for the kafkaclusters API
type KafkaCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of KafkaCluster
	// +required
	Spec KafkaClusterSpec `json:"spec"`

	// status defines the observed state of KafkaCluster
	// +optional
	Status KafkaClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// KafkaClusterList contains a list of KafkaCluster
type KafkaClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KafkaCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &KafkaCluster{}, &KafkaClusterList{})
		return nil
	})
}
