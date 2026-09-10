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

// GrafanaInstanceSpec defines the desired state of GrafanaInstance.
//
// It is a minimal, opinionated front for a grafana-operator
// grafana.integreatly.org/v1beta1 Grafana instance: the operator creates and
// manages a same-named Grafana from this spec. Only the fields most
// deployments need to set are exposed here; everything else in the
// generated Grafana (ingress/route, SMTP, plugins, jsonnet, service
// accounts, etc.) is left at the grafana-operator's own defaults. See
// internal/grafana for the mapping.
type GrafanaInstanceSpec struct {
	// version sets the tag of the default docker.io/grafana/grafana image.
	// Defaults to the grafana-operator's own default version when unset.
	// +optional
	Version string `json:"version,omitempty"`

	// replicas is the number of Grafana pods to run.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// persistence settings for Grafana's data volume. When unset, no PVC is
	// requested and Grafana runs with ephemeral storage.
	// +optional
	Persistence *PersistenceSpec `json:"persistence,omitempty"`

	// ingress exposes Grafana's web UI outside the cluster. Applied directly
	// as the underlying Grafana's own spec.ingress -- grafana-operator
	// creates and owns the resulting Ingress object itself (so no separate
	// one is created by this operator), but mirrors Grafana.spec.ingress.spec
	// onto it verbatim; it does not fill in the backend on its own, so
	// internal/grafana's BuildManifest routes the rule at its own generated
	// Service (see grafana.ServiceName/WebPort) itself.
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// expose controls the type of the underlying Grafana's own generated
	// Service (applied as its spec.service.spec), mirroring
	// ServiceExposeSpec's use on PostgresCluster/MariaDBCluster/
	// ValkeyCluster for a consistent way to reach any resource this operator
	// manages directly by Service, independent of Ingress. Defaults to
	// LoadBalancer when set; grafana-operator itself defaults to ClusterIP
	// when this is left unset entirely.
	// +optional
	Expose *ServiceExposeSpec `json:"expose,omitempty"`

	// prometheusRef names the PrometheusInstance (in this same namespace)
	// this Grafana should be wired to via an auto-created GrafanaDatasource.
	// Required, and deliberately has no convention-based default (e.g.
	// assuming a PrometheusInstance is always named "prometheus"): a wrong
	// guess produces a GrafanaDatasource pointing at a Service that doesn't
	// exist, which fails silently -- grafana-operator reports the
	// GrafanaDatasource itself as successfully applied, since "applied"
	// only means the config was pushed to Grafana, not that Grafana can
	// actually reach it. The referenced PrometheusInstance need not exist
	// yet: the datasource is created regardless of whether it currently
	// resolves to anything.
	// +kubebuilder:validation:MinLength=1
	// +required
	PrometheusRef string `json:"prometheusRef"`

	// lokiRef names the LokiInstance (in this same namespace) this Grafana
	// should be wired to via a second auto-created GrafanaDatasource,
	// mirroring PrometheusRef. Unlike PrometheusRef this is optional -- not
	// every Grafana needs a Loki datasource. The same PrometheusRef caveats
	// apply: no convention-based default, and the referenced LokiInstance
	// need not exist yet when this is set.
	// +optional
	LokiRef string `json:"lokiRef,omitempty"`
}

// GrafanaInstanceStatus defines the observed state of GrafanaInstance.
//
// It mirrors the subset of the underlying Grafana's status that matters for
// "is my Grafana usable yet".
type GrafanaInstanceStatus struct {
	// ready is true once the underlying Grafana reports its reconciliation
	// stage as complete.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// phase is a verbatim copy of the underlying Grafana's status.stage.
	// +optional
	Phase string `json:"phase,omitempty"`

	// replicas is the underlying Grafana's status.replicas.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// message is a short human-readable summary of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the GrafanaInstance resource.
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
// +kubebuilder:resource:shortName=gfi
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GrafanaInstance is the Schema for the grafanainstances API
type GrafanaInstance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GrafanaInstance
	// +required
	Spec GrafanaInstanceSpec `json:"spec"`

	// status defines the observed state of GrafanaInstance
	// +optional
	Status GrafanaInstanceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GrafanaInstanceList contains a list of GrafanaInstance
type GrafanaInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GrafanaInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GrafanaInstance{}, &GrafanaInstanceList{})
		return nil
	})
}
