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
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var lokiinstancelog = logf.Log.WithName("lokiinstance-resource")

// SetupLokiInstanceWebhookWithManager registers the webhook for LokiInstance in the manager.
func SetupLokiInstanceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &paasv1alpha1.LokiInstance{}).
		WithValidator(&LokiInstanceCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// NOTE: If you want to customise the 'path', use the flags '--defaulting-path' or '--validation-path'.
// +kubebuilder:webhook:path=/validate-paas-cncp-nl-v1alpha1-lokiinstance,mutating=false,failurePolicy=fail,sideEffects=None,groups=paas.cncp.nl,resources=lokiinstances,verbs=create,versions=v1alpha1,name=vlokiinstance-v1alpha1.kb.io,admissionReviewVersions=v1

// LokiInstanceCustomValidator validates that a namespace has at most one
// LokiInstance -- part of the monitoring/logging stack singleton rule shared
// with GrafanaInstanceCustomValidator and PrometheusInstanceCustomValidator.
type LokiInstanceCustomValidator struct {
	Client client.Client
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type LokiInstance.
func (v *LokiInstanceCustomValidator) ValidateCreate(ctx context.Context, obj *paasv1alpha1.LokiInstance) (admission.Warnings, error) {
	lokiinstancelog.Info("Validation for LokiInstance upon creation", "name", obj.GetName())

	if err := rejectIfSiblingExists(ctx, v.Client, &paasv1alpha1.LokiInstanceList{}, obj.GetNamespace(), "LokiInstance", obj.GetName()); err != nil {
		return nil, err
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so LokiInstanceCustomValidator
// satisfies the interface. The webhook is only registered for "create" (see the
// marker above): namespace is immutable, so the singleton check never needs to
// re-run on update.
func (v *LokiInstanceCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *paasv1alpha1.LokiInstance) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so LokiInstanceCustomValidator
// satisfies the interface. Not registered (see the marker above): deleting an
// instance never needs validation.
func (v *LokiInstanceCustomValidator) ValidateDelete(_ context.Context, obj *paasv1alpha1.LokiInstance) (admission.Warnings, error) {
	return nil, nil
}
