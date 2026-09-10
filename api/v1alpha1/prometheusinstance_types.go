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

// PrometheusInstanceSpec defines the desired state of PrometheusInstance.
//
// It is a minimal, opinionated front for a Prometheus Operator
// monitoring.coreos.com/v1 Prometheus: the operator creates and manages a
// same-named Prometheus from this spec. Only the fields most deployments
// need to set are exposed here; everything else in the generated Prometheus
// is left at the Prometheus Operator's own defaults. See internal/prometheus
// for the mapping.
//
// The generated Prometheus always selects ServiceMonitors/PodMonitors/Probes
// from its own namespace only -- its
// serviceMonitorNamespaceSelector/podMonitorNamespaceSelector/
// probeNamespaceSelector are deliberately left unset, which the Prometheus
// Operator treats as "current namespace only" rather than cluster-wide. This
// is not configurable: a PrometheusInstance never watches monitors outside
// its own namespace.
type PrometheusInstanceSpec struct {
	// version sets the tag of the default quay.io/prometheus/prometheus
	// image. Defaults to the Prometheus Operator's own default version when
	// unset.
	// +optional
	Version string `json:"version,omitempty"`

	// replicas is the number of Prometheus pods to run.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// retention is how long to keep scraped metrics, e.g. "15d". Defaults to
	// the Prometheus Operator's own default (24h) when unset.
	// +optional
	Retention string `json:"retention,omitempty"`

	// storage settings for Prometheus's TSDB volume. When unset, no PVC is
	// requested and Prometheus runs with ephemeral storage.
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// resources are the optional compute resource requests/limits for the
	// Prometheus container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// ingress exposes Prometheus's web UI outside the cluster. The
	// Prometheus Operator creates no Service of its own for Prometheus, so
	// when set this operator also creates and manages a ClusterIP Service
	// (selecting the Prometheus Operator's own documented
	// "operator.prometheus.io/name" pod label) alongside the Ingress.
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// expose controls the type of the ClusterIP Service this operator
	// creates fronting Prometheus's web UI/API (see Ingress's own doc
	// comment for why that Service always exists regardless of Ingress),
	// mirroring ServiceExposeSpec's use on
	// PostgresCluster/MariaDBCluster/ValkeyCluster. Defaults to
	// LoadBalancer when set; the Service stays ClusterIP when this is left
	// unset entirely.
	// +optional
	Expose *ServiceExposeSpec `json:"expose,omitempty"`
}

// PrometheusInstanceStatus defines the observed state of PrometheusInstance.
//
// It mirrors the subset of the underlying Prometheus's status that matters
// for "is my Prometheus usable yet".
type PrometheusInstanceStatus struct {
	// ready is true once the underlying Prometheus reports all replicas
	// available.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is a short human-readable summary derived from the underlying
	// Prometheus's status (it has no single phase field of its own).
	// +optional
	Phase string `json:"phase,omitempty"`

	// replicas is the underlying Prometheus's status.replicas.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// readyReplicas is the underlying Prometheus's status.availableReplicas.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the PrometheusInstance resource.
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
// +kubebuilder:resource:shortName=promi
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PrometheusInstance is the Schema for the prometheusinstances API
type PrometheusInstance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PrometheusInstance
	// +required
	Spec PrometheusInstanceSpec `json:"spec"`

	// status defines the observed state of PrometheusInstance
	// +optional
	Status PrometheusInstanceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PrometheusInstanceList contains a list of PrometheusInstance
type PrometheusInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PrometheusInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PrometheusInstance{}, &PrometheusInstanceList{})
		return nil
	})
}
