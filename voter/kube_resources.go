package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func coffeeConfigGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "examples.configbutler.ai",
		Version:  "v1alpha1",
		Resource: "coffeeconfigs",
	}
}

func toCoffeeConfig(obj *unstructured.Unstructured) (coffeeConfig, error) {
	var out coffeeConfig
	if obj == nil {
		return out, errors.New("nil coffee config object")
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &out); err != nil {
		return out, fmt.Errorf("failed to decode coffee config: %w", err)
	}
	return out, nil
}

// Namespace discovery needs no server credential or Kubernetes client.
// Explicit configuration supports local development; pods use their mounted
// namespace, with the demo namespace as the standalone default.
func applicationNamespace(cfg config) string {
	if ns := strings.TrimSpace(cfg.KubernetesNamespace); ns != "" {
		return ns
	}
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil {
		if ns := strings.TrimSpace(string(data)); ns != "" {
			return ns
		}
	}
	return "voter"
}
