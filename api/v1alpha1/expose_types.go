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
)

// IngressSpec configures an HTTP Ingress in front of a resource's web UI.
// Shared across every resource kind that fronts an HTTP service
// (GrafanaInstance, PrometheusInstance, RabbitMQCluster's management UI) --
// see each spec's own doc comment for whether it's applied as a field on the
// underlying vendor object or as a separate Ingress this operator manages.
type IngressSpec struct {
	// host is the DNS hostname routed to this resource, e.g.
	// "grafana.example.com".
	// +required
	Host string `json:"host"`

	// ingressClassName selects the IngressClass that should implement this
	// Ingress. Defaults to the cluster's default IngressClass when unset.
	// +optional
	IngressClassName string `json:"ingressClassName,omitempty"`

	// tlsSecretName enables TLS termination at the ingress controller using
	// this Secret (must already exist in the same namespace and contain a
	// matching cert/key, e.g. one managed by cert-manager). When unset, the
	// Ingress is served over plain HTTP.
	// +optional
	TLSSecretName string `json:"tlsSecretName,omitempty"`

	// annotations are copied verbatim onto the generated Ingress, e.g. for
	// ingress-controller-specific tuning (cert-manager issuer, nginx rewrite
	// rules, etc.).
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ServiceExposeSpec exposes a resource's primary TCP endpoint outside the
// cluster via a Service of type LoadBalancer. Shared across every resource
// kind that speaks a raw TCP protocol rather than HTTP (PostgresCluster,
// MariaDBCluster, ValkeyCluster) -- see each spec's own doc comment for
// whether it's applied as a field on the underlying vendor object or as a
// separate Service this operator manages.
type ServiceExposeSpec struct {
	// type is the Service type to create. LoadBalancer is the only
	// supported value: this operator's clusters run MetalLB with
	// BGP-advertised address pools, so a NodePort Service would never be
	// reachable from outside the cluster.
	// +kubebuilder:validation:Enum=LoadBalancer
	// +kubebuilder:default=LoadBalancer
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// annotations are copied verbatim onto the generated/underlying Service,
	// e.g. cloud load-balancer tuning annotations.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}
