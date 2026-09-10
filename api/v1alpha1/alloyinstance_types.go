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

// AlloyInstanceSpec defines the desired state of AlloyInstance.
//
// It is a minimal, opinionated front for the Alloy Operator's
// collectors.grafana.com/v1alpha1 Alloy: the operator creates and manages a
// same-named Alloy from this spec. lokiInstanceRef is the only thing that
// has to be supplied: everything else (the discovery config, the relabeling
// that attaches namespace/pod/container/node labels, the push endpoint, the
// namespace-scoped RBAC letting Alloy read its own namespace's pods) is
// derived and wired up automatically, so pointing an AlloyInstance at a
// LokiInstance is enough to start seeing labeled logs for that namespace
// with no further Alloy configuration required. See internal/alloy for the
// mapping, and its package doc in particular for why Alloy's own
// spec/status have no structured schema to speak of (it's a Helm-based
// operator under the hood).
type AlloyInstanceSpec struct {
	// lokiInstanceRef names a LokiInstance in the same namespace this Alloy
	// ships logs to. Resolved internally to that LokiInstance's own
	// distributor Service (see loki.WriteServiceName) -- no push URL,
	// endpoint, or credential ever needs configuring by hand, and nothing
	// stops this operator from reconciling before the referenced
	// LokiInstance exists: the generated Alloy simply can't push
	// successfully until it does.
	// +kubebuilder:validation:MinLength=1
	// +required
	LokiInstanceRef string `json:"lokiInstanceRef"`

	// replicas is the number of Alloy pods to run. Alloy discovers and reads
	// pod logs via the Kubernetes API (not by tailing local node log files),
	// so unlike the Alloy Helm chart's own default deployment shape (a
	// DaemonSet), this operator always requests the chart's "deployment"
	// controller type instead -- one or a few replicas cover the whole
	// namespace regardless of node count.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`
}

// AlloyInstanceStatus defines the observed state of AlloyInstance.
//
// Like LokiInstanceStatus, this carries no replica/ready-count fields: the
// Alloy Operator is Helm-based (see internal/alloy's package doc), so its
// own .status reports generic Helm-release conditions
// (Initialized/Deployed/ReleaseFailed/Irreconcilable/Paused), never
// anything about the underlying Deployment's actual rollout. Readiness is
// derived purely from the underlying Alloy's "Deployed" condition.
type AlloyInstanceStatus struct {
	// ready is true once the underlying Alloy reports its "Deployed"
	// condition as True.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is the reason of the underlying Alloy's most recently observed
	// "Deployed" condition.
	// +optional
	Phase string `json:"phase,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the AlloyInstance resource.
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
// +kubebuilder:resource:shortName=alloy
// +kubebuilder:printcolumn:name="LokiRef",type=string,JSONPath=`.spec.lokiInstanceRef`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AlloyInstance is the Schema for the alloyinstances API
type AlloyInstance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AlloyInstance
	// +required
	Spec AlloyInstanceSpec `json:"spec"`

	// status defines the observed state of AlloyInstance
	// +optional
	Status AlloyInstanceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AlloyInstanceList contains a list of AlloyInstance
type AlloyInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AlloyInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &AlloyInstance{}, &AlloyInstanceList{})
		return nil
	})
}
