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
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
	"github.com/bartvanbenthem/paas-operator/internal/valkey"
)

// ValkeyClusterReconciler reconciles a ValkeyCluster object. It is the
// shared reconciler.GenericReconciler engine wired to the valkey-operator
// mapping in internal/valkey — see internal/reconciler for the finalizer /
// Server-Side Apply / status-mirroring logic every vendor integration
// shares, and internal/valkey for what's specific to the valkey-operator.
type ValkeyClusterReconciler = reconciler.GenericReconciler[paasv1alpha1.ValkeyCluster, *paasv1alpha1.ValkeyCluster]

// ValkeyClusterControllerName is the controller name used both when
// registering with the manager and in logs/events.
const ValkeyClusterControllerName = "valkeycluster"

// +kubebuilder:rbac:groups=paas.example.com,resources=valkeyclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=paas.example.com,resources=valkeyclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=paas.example.com,resources=valkeyclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=valkey.io,resources=valkeyclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// NewValkeyClusterReconciler builds the ValkeyCluster controller.
func NewValkeyClusterReconciler(c client.Client, scheme *runtime.Scheme, recorder events.EventRecorder) *ValkeyClusterReconciler {
	return &ValkeyClusterReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: recorder,
		Adapter:  valkey.Adapter{},
		Name:     ValkeyClusterControllerName,
	}
}
