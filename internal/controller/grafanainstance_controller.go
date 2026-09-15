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
	"github.com/bartvanbenthem/paas-operator/internal/grafana"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

// GrafanaInstanceReconciler reconciles a GrafanaInstance object. It is the
// shared reconciler.GenericReconciler engine wired to the grafana-operator
// mapping in internal/grafana — see internal/reconciler for the finalizer /
// Server-Side Apply / status-mirroring logic every vendor integration
// shares, and internal/grafana for what's specific to grafana-operator.
type GrafanaInstanceReconciler = reconciler.GenericReconciler[paasv1alpha1.GrafanaInstance, *paasv1alpha1.GrafanaInstance]

// GrafanaInstanceControllerName is the controller name used both when
// registering with the manager and in logs/events.
const GrafanaInstanceControllerName = "grafanainstance"

// +kubebuilder:rbac:groups=paas.cncp.nl,resources=grafanainstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=paas.cncp.nl,resources=grafanainstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=paas.cncp.nl,resources=grafanainstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadatasources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// NewGrafanaInstanceReconciler builds the GrafanaInstance controller.
func NewGrafanaInstanceReconciler(c client.Client, scheme *runtime.Scheme, recorder events.EventRecorder) *GrafanaInstanceReconciler {
	return &GrafanaInstanceReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: recorder,
		Adapter:  grafana.Adapter{},
		Name:     GrafanaInstanceControllerName,
	}
}
