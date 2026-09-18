package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// requestNamespace is the single shared namespace all Promise requests go into
// on the hub cluster — see Architecture.md "Single kratix-workloads namespace
// on hub for all Promise submissions". Not caller-configurable.
const requestNamespace = "kratix-workloads"

const promiseAPIVersion = "marketplace.kratix.io/v1alpha1"

// kindToResource is the allowlist of Promise request kinds this service will
// create. Deliberately explicit rather than accepting any GVR — RBAC already
// scopes the service account to just these resources, but validating here
// gives callers a clean 400 instead of a raw k8s API 403, and means loosening
// RBAC later can't silently turn this into a generic cluster-object endpoint.
var kindToResource = map[string]string{
	"TeamOnboardingRequest":          "teamonboardingrequests",
	"NamespaceRequest":               "namespacerequests",
	"KeyVaultRequest":                "keyvaultrequests",
	"StorageAccountRequest":          "storageaccountrequests",
	"StorageAccountTerraformRequest": "storageaccountterraformrequests",
}

func newDynamicClient() (dynamic.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster config: %w", err)
	}
	return dynamic.NewForConfig(cfg)
}

type applyRequest struct {
	Kind string                 `json:"kind"`
	Name string                 `json:"name"`
	Spec map[string]interface{} `json:"spec"`
}

type applyResponse struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

// handleApply is the one generic endpoint this service exposes. It mirrors
// the Backstage kratix:apply scaffolder action and kubectl apply — same
// outcome, same CRD, different consumption pattern (Architecture.md's
// "pluggable access layer" principle).
func handleApply(client dynamic.Interface) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req applyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		resource, ok := kindToResource[req.Kind]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown kind %q — must be one of the Promise request kinds", req.Kind))
			return
		}

		gvr := schema.GroupVersionResource{
			Group:    "marketplace.kratix.io",
			Version:  "v1alpha1",
			Resource: resource,
		}

		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": promiseAPIVersion,
				"kind":       req.Kind,
				"metadata": map[string]interface{}{
					"name":      req.Name,
					"namespace": requestNamespace,
				},
				"spec": req.Spec,
			},
		}

		// Server-side apply — idempotent on retry, matches the idempotency
		// pattern the team-onboarding pipeline's GitHub calls also had to adopt
		// (see Experiments.md "Kratix pipeline retries fail when ... already exist").
		_, err := client.Resource(gvr).Namespace(requestNamespace).Apply(
			r.Context(), req.Name, obj,
			metav1.ApplyOptions{FieldManager: "rest-api", Force: true},
		)
		if err != nil {
			writeError(w, http.StatusBadGateway, "apply failed: "+err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(applyResponse{
			Kind:      req.Kind,
			Name:      req.Name,
			Namespace: requestNamespace,
			Status:    "created",
		})
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
