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
var prometheusinstancelog = logf.Log.WithName("prometheusinstance-resource")

// SetupPrometheusInstanceWebhookWithManager registers the webhook for PrometheusInstance in the manager.
func SetupPrometheusInstanceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &paasv1alpha1.PrometheusInstance{}).
		WithValidator(&PrometheusInstanceCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// NOTE: If you want to customise the 'path', use the flags '--defaulting-path' or '--validation-path'.
// +kubebuilder:webhook:path=/validate-paas-cncp-nl-v1alpha1-prometheusinstance,mutating=false,failurePolicy=fail,sideEffects=None,groups=paas.cncp.nl,resources=prometheusinstances,verbs=create,versions=v1alpha1,name=vprometheusinstance-v1alpha1.kb.io,admissionReviewVersions=v1

// PrometheusInstanceCustomValidator validates that a namespace has at most
// one PrometheusInstance -- part of the monitoring/logging stack singleton
// rule shared with GrafanaInstanceCustomValidator and LokiInstanceCustomValidator.
type PrometheusInstanceCustomValidator struct {
	Client client.Client
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type PrometheusInstance.
func (v *PrometheusInstanceCustomValidator) ValidateCreate(ctx context.Context, obj *paasv1alpha1.PrometheusInstance) (admission.Warnings, error) {
	prometheusinstancelog.Info("Validation for PrometheusInstance upon creation", "name", obj.GetName())

	if err := rejectIfSiblingExists(ctx, v.Client, &paasv1alpha1.PrometheusInstanceList{}, obj.GetNamespace(), "PrometheusInstance", obj.GetName()); err != nil {
		return nil, err
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so PrometheusInstanceCustomValidator
// satisfies the interface. The webhook is only registered for "create" (see the
// marker above): namespace is immutable, so the singleton check never needs to
// re-run on update.
func (v *PrometheusInstanceCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *paasv1alpha1.PrometheusInstance) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so PrometheusInstanceCustomValidator
// satisfies the interface. Not registered (see the marker above): deleting an
// instance never needs validation.
func (v *PrometheusInstanceCustomValidator) ValidateDelete(_ context.Context, obj *paasv1alpha1.PrometheusInstance) (admission.Warnings, error) {
	return nil, nil
}
