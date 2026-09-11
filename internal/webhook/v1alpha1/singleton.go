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
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// rejectIfSiblingExists enforces at most one instance of a given kind per
// namespace -- the monitoring/logging stack (GrafanaInstance,
// PrometheusInstance, LokiInstance) is meant to be provisioned once per
// namespace, not per-team-per-namespace. Each of those three webhooks calls
// this from ValidateCreate with an empty list of its own kind; if the list
// already contains an object other than the one being created, the create is
// rejected.
func rejectIfSiblingExists(ctx context.Context, c client.Client, list client.ObjectList, namespace, kind, newName string) error {
	if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("checking for an existing %s in namespace %q: %w", kind, namespace, err)
	}

	items, err := apimeta.ExtractList(list)
	if err != nil {
		return fmt.Errorf("checking for an existing %s in namespace %q: %w", kind, namespace, err)
	}

	for _, item := range items {
		obj, ok := item.(client.Object)
		if !ok || obj.GetName() == newName {
			continue
		}
		return fmt.Errorf("namespace %q already has a %s named %q; only one %s is allowed per namespace", namespace, kind, obj.GetName(), kind)
	}

	return nil
}
