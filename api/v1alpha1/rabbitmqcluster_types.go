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

// RabbitMQClusterSpec defines the desired state of RabbitMQCluster.
//
// It is a minimal, opinionated front for a RabbitMQ Cluster Operator
// rabbitmq.com/v1beta1 RabbitmqCluster: the operator creates and manages a
// same-named RabbitmqCluster from this spec. Only the fields most
// deployments need to set are exposed here; everything else in the
// generated RabbitmqCluster (TLS, plugins, definitions import, affinity,
// etc.) is left at the RabbitMQ Cluster Operator's own defaults. Unlike
// PostgresCluster/MariaDBCluster, there's no bootstrap-database concept:
// the operator always creates a default vhost and a default user, writing
// its credentials to a `<name>-default-user` Secret it manages itself. See
// internal/rabbitmq for the mapping.
type RabbitMQClusterSpec struct {
	// replicas is the number of RabbitMQ nodes in the cluster. Should be an
	// odd number (1, 3, 5, ...) so the cluster can maintain quorum across a
	// network partition.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// image is the full container image reference for the RabbitMQ
	// instances. Defaults to the RabbitMQ Cluster Operator's own default
	// image when unset.
	// +optional
	Image string `json:"image,omitempty"`

	// storage settings for each node's data volume.
	// +required
	Storage StorageSpec `json:"storage"`

	// resources are the optional compute resource requests/limits for the
	// RabbitMQ containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// ingress exposes RabbitMQ's management UI (port 15672) outside the
	// cluster. The RabbitMQ Cluster Operator has no native ingress field, so
	// when set this operator also creates and manages a separate Ingress
	// object pointing at the RabbitmqCluster's own generated Service.
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// expose controls the type of the RabbitMQ Cluster Operator's own
	// auto-generated Service fronting the cluster (applied as the
	// underlying RabbitmqCluster's spec.service), mirroring
	// ServiceExposeSpec's use on PostgresCluster/MariaDBCluster/
	// ValkeyCluster. Defaults to LoadBalancer when set; the RabbitMQ
	// Cluster Operator itself defaults to ClusterIP when this is left
	// unset entirely.
	// +optional
	Expose *ServiceExposeSpec `json:"expose,omitempty"`

	// monitoring configures Prometheus metrics collection for the underlying
	// RabbitmqCluster. Unlike PostgresCluster/MariaDBCluster, the RabbitMQ
	// Cluster Operator has no native PodMonitor/ServiceMonitor toggle of its
	// own -- rabbitmq_prometheus is one of its always-on essential plugins,
	// exposed on its own generated Service's port 15692, so this operator
	// creates the ServiceMonitor directly rather than asking the RabbitMQ
	// Cluster Operator to.
	// +kubebuilder:default={}
	// +optional
	Monitoring MonitoringSpec `json:"monitoring,omitzero"`
}

// RabbitMQClusterStatus defines the observed state of RabbitMQCluster.
//
// It mirrors the subset of the underlying RabbitmqCluster's status that
// matters for "is my broker usable yet".
type RabbitMQClusterStatus struct {
	// ready is true once the underlying RabbitmqCluster reports its
	// "AllReplicasReady" condition as True.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is the reason of the underlying RabbitmqCluster's most relevant
	// condition (RabbitMQ Cluster Operator has no separate status.phase
	// field).
	// +optional
	Phase string `json:"phase,omitempty"`

	// replicas is the underlying RabbitmqCluster's spec.replicas (the
	// operator's status doesn't carry its own copy).
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// readyReplicas is derived, not a verbatim field: it is replicas when
	// the "AllReplicasReady" condition is True, 0 otherwise.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the RabbitMQCluster resource.
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
// +kubebuilder:resource:shortName=rmq
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RabbitMQCluster is the Schema for the rabbitmqclusters API
type RabbitMQCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of RabbitMQCluster
	// +required
	Spec RabbitMQClusterSpec `json:"spec"`

	// status defines the observed state of RabbitMQCluster
	// +optional
	Status RabbitMQClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RabbitMQClusterList contains a list of RabbitMQCluster
type RabbitMQClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RabbitMQCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RabbitMQCluster{}, &RabbitMQClusterList{})
		return nil
	})
}
