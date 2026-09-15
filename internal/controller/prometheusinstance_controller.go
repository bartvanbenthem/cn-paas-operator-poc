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

package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
	"github.com/bartvanbenthem/paas-operator/internal/prometheus"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

// PrometheusInstanceReconciler reconciles a PrometheusInstance object. It is
// the shared reconciler.GenericReconciler engine wired to the Prometheus
// Operator mapping in internal/prometheus — see internal/reconciler for the
// finalizer / Server-Side Apply / status-mirroring logic every vendor
// integration shares, and internal/prometheus for what's specific to the
// Prometheus Operator.
type PrometheusInstanceReconciler = reconciler.GenericReconciler[paasv1alpha1.PrometheusInstance, *paasv1alpha1.PrometheusInstance]

// PrometheusInstanceControllerName is the controller name used both when
// registering with the manager and in logs/events.
const PrometheusInstanceControllerName = "prometheusinstance"

// +kubebuilder:rbac:groups=paas.cncp.nl,resources=prometheusinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=paas.cncp.nl,resources=prometheusinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=paas.cncp.nl,resources=prometheusinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=prometheuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;endpoints,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// NewPrometheusInstanceReconciler builds the PrometheusInstance controller.
func NewPrometheusInstanceReconciler(c client.Client, scheme *runtime.Scheme, recorder events.EventRecorder) *PrometheusInstanceReconciler {
	return &PrometheusInstanceReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: recorder,
		Adapter:  prometheus.Adapter{},
		Name:     PrometheusInstanceControllerName,
	}
}
