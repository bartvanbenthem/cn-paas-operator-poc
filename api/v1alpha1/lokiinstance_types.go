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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// LokiInstanceSpec defines the desired state of LokiInstance.
//
// It is a minimal, opinionated front for a Loki Operator loki.grafana.com/v1
// LokiStack: the operator creates and manages a same-named LokiStack from
// this spec. Unlike every other "instance"-shaped building block here
// (GrafanaInstance, PrometheusInstance), a LokiStack has no ephemeral/
// local-disk mode at all -- it always writes chunks and the index to an
// external object store, and is sized by a t-shirt size rather than a
// replica count. Only S3-compatible object storage (AWS S3, or any
// S3-compatible endpoint such as MinIO) is supported; GCS/Azure/Swift/
// AlibabaCloud are out of scope for this first pass. See internal/loki for
// the mapping.
type LokiInstanceSpec struct {
	// size selects one of the Loki Operator's supported deployment scale-out
	// sizes, which determines replica counts and resource requests for every
	// LokiStack component (distributor, ingester, querier, ...). Defaults to
	// "1x.demo", the smallest, single-replica-per-component size meant for
	// evaluation/development, not production use.
	// +kubebuilder:validation:Enum="1x.demo";"1x.pico";"1x.extra-small";"1x.small";"1x.medium"
	// +kubebuilder:default="1x.demo"
	// +optional
	Size string `json:"size,omitempty"`

	// storageClassName is the StorageClass used for the PVCs the Loki
	// Operator provisions for its stateful components (ingester WAL, index
	// gateway cache, compactor working directory, ...). The Loki Operator
	// has no ephemeral-storage fallback the way the Prometheus Operator/
	// grafana-operator do, so this is required.
	// +kubebuilder:validation:MinLength=1
	// +required
	StorageClassName string `json:"storageClassName"`

	// objectStorage configures the S3-compatible bucket the LokiStack reads
	// and writes chunks and the index to.
	// +required
	ObjectStorage LokiObjectStorageSpec `json:"objectStorage"`
}

// LokiObjectStorageSpec configures an S3-compatible object storage backend
// for a LokiInstance.
type LokiObjectStorageSpec struct {
	// secretName names a Secret, in the same namespace, laid out the way the
	// Loki Operator's own S3 object storage secret expects: a "bucketnames"
	// key (comma-separated if more than one), an "endpoint" key (a full
	// "https://..." URL -- AWS S3 itself expects
	// "https://s3.<region>.amazonaws.com"), an optional "region" key
	// (required for AWS S3, optional for other S3-compatible endpoints),
	// and "access_key_id"/"access_key_secret" keys. Must already exist --
	// this operator never creates it or reads its contents, only references
	// it by name, so a wrong or missing Secret surfaces as the underlying
	// LokiStack's own "MissingObjectStorageSecret"/"InvalidObjectStorageSecret"
	// Degraded condition rather than a failure here.
	// +kubebuilder:validation:MinLength=1
	// +required
	SecretName string `json:"secretName"`
}

// LokiInstanceStatus defines the observed state of LokiInstance.
//
// Like KafkaClusterStatus, this carries no replica/ready-count fields: the
// underlying LokiStack's own .status has no such field (only
// status.conditions[], status.components, and status.storage -- confirmed
// against the Loki Operator's CRD schema), and status.components reports
// per-pod status per component rather than a single count this operator
// could meaningfully roll up. Readiness is derived purely from the
// underlying LokiStack's "Ready" condition -- the Loki Operator reports
// mutually exclusive Ready/Pending/Failed/Degraded/Warning condition types
// (each its own Type, not a single Ready type toggling True/False).
type LokiInstanceStatus struct {
	// ready is true once the underlying LokiStack reports its "Ready"
	// condition as True.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is the reason of the underlying LokiStack's most recently
	// observed condition (Ready, or whichever of Pending/Failed/Degraded is
	// currently true; the Loki Operator has no separate status.phase field).
	// +optional
	Phase string `json:"phase,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the LokiInstance resource.
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
// +kubebuilder:resource:shortName=loki
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=`.spec.size`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LokiInstance is the Schema for the lokiinstances API
type LokiInstance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LokiInstance
	// +required
	Spec LokiInstanceSpec `json:"spec"`

	// status defines the observed state of LokiInstance
	// +optional
	Status LokiInstanceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LokiInstanceList contains a list of LokiInstance
type LokiInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LokiInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &LokiInstance{}, &LokiInstanceList{})
		return nil
	})
}
