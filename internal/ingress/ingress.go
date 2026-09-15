// Package ingress builds standard networking.k8s.io/v1 Ingress objects from
// the shared paasv1alpha1.IngressSpec, for the vendor adapters (rabbitmq,
// prometheus) whose upstream CRD has no native ingress field of its own and
// therefore need this operator to manage a separate Ingress object
// alongside the primary target. Like every other vendor integration, the
// object is built as a plain map and applied via Server-Side Apply.
package ingress

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	paasv1alpha1 "github.com/bartvanbenthem/paas-operator/api/v1alpha1"
)

const (
	Group   = "networking.k8s.io"
	Version = "v1"
	Kind    = "Ingress"
)

// GVK is the GroupVersionKind of the Ingress objects this package builds.
var GVK = schema.GroupVersionKind{Group: Group, Version: Version, Kind: Kind}

// Build returns the desired Ingress routing spec.Host to
// backendServiceName:backendPort at path "/". owner/fieldManager set the
// same paas.cncp.nl/owner and app.kubernetes.io/managed-by labels every
// other object this operator manages carries.
func Build(spec *paasv1alpha1.IngressSpec, name, namespace, owner, fieldManager, backendServiceName string, backendPort int32) *unstructured.Unstructured {
	pathType := "Prefix"

	ingressSpec := map[string]any{
		"rules": []any{
			map[string]any{
				"host": spec.Host,
				"http": map[string]any{
					"paths": []any{
						map[string]any{
							"path":     "/",
							"pathType": pathType,
							"backend": map[string]any{
								"service": map[string]any{
									"name": backendServiceName,
									"port": map[string]any{"number": int64(backendPort)},
								},
							},
						},
					},
				},
			},
		},
	}

	if spec.IngressClassName != "" {
		ingressSpec["ingressClassName"] = spec.IngressClassName
	}
	if spec.TLSSecretName != "" {
		ingressSpec["tls"] = []any{
			map[string]any{
				"hosts":      []any{spec.Host},
				"secretName": spec.TLSSecretName,
			},
		}
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(GVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": fieldManager,
		"paas.cncp.nl/owner":           owner,
	})
	if len(spec.Annotations) > 0 {
		u.SetAnnotations(spec.Annotations)
	}
	u.Object["spec"] = ingressSpec

	return u
}
