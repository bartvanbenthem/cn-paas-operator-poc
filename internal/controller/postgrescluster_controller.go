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
	"github.com/bartvanbenthem/paas-operator/internal/cnpg"
	"github.com/bartvanbenthem/paas-operator/internal/reconciler"
)

// PostgresClusterReconciler reconciles a PostgresCluster object. It is the
// shared reconciler.GenericReconciler engine wired to the CNPG-specific
// mapping in internal/cnpg — see internal/reconciler for the finalizer /
// Server-Side Apply / status-mirroring logic every vendor integration
// shares, and internal/cnpg for what's specific to CNPG.
type PostgresClusterReconciler = reconciler.GenericReconciler[paasv1alpha1.PostgresCluster, *paasv1alpha1.PostgresCluster]

// PostgresClusterControllerName is the controller name used both when
// registering with the manager and in logs/events.
const PostgresClusterControllerName = "postgrescluster"

// +kubebuilder:rbac:groups=paas.example.com,resources=postgresclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=paas.example.com,resources=postgresclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=paas.example.com,resources=postgresclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// NewPostgresClusterReconciler builds the PostgresCluster controller.
func NewPostgresClusterReconciler(c client.Client, scheme *runtime.Scheme, recorder events.EventRecorder) *PostgresClusterReconciler {
	return &PostgresClusterReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: recorder,
		Adapter:  cnpg.Adapter{},
		Name:     PostgresClusterControllerName,
	}
}
