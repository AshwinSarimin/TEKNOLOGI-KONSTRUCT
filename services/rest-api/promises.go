package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// requestNamespace is the single shared namespace all Promise requests go into
// on the hub cluster — see Architecture.md "Single kratix-workloads namespace
// on hub for all Promise submissions". Not caller-configurable.
const requestNamespace = "kratix-workloads"

const promiseAPIVersion = "marketplace.kratix.io/v1alpha1"
const ownerAnnotation = "teknologi.io/request-owner"

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

// handleApply creates a request or updates one owned by the authenticated caller.
// Ownership is never inferred from the name, spec, or client-provided metadata.
func handleApply(client dynamic.Interface) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, _ := r.Context().Value(callerContextKey{}).(string)
		if owner == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var req applyRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeError(w, http.StatusBadRequest, "body must contain one JSON object")
			return
		}
		if len(validation.IsDNS1123Subdomain(req.Name)) != 0 || req.Spec == nil {
			writeError(w, http.StatusBadRequest, "a valid resource name and spec object are required")
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

		resources := client.Resource(gvr).Namespace(requestNamespace)
		existing, err := resources.Get(r.Context(), req.Name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			writeError(w, http.StatusBadGateway, "could not read request")
			return
		}

		status, code := "created", http.StatusCreated
		if err == nil {
			if existing.GetAnnotations()[ownerAnnotation] != owner {
				writeError(w, http.StatusForbidden, "request is not owned by this caller; administrator review required for unowned requests")
				return
			}
			if existing.GetDeletionTimestamp() != nil {
				writeError(w, http.StatusConflict, "request is being deleted")
				return
			}
			status, code = "unchanged", http.StatusOK
			if !reflect.DeepEqual(existing.Object["spec"], req.Spec) {
				// Retain UID, resourceVersion, annotations and controller metadata.
				// Kubernetes rejects stale updates, including deletion/recreation.
				existing.Object["spec"] = req.Spec
				_, err = resources.Update(r.Context(), existing, metav1.UpdateOptions{FieldManager: "rest-api"})
				status = "updated"
			}
		} else {
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": promiseAPIVersion,
					"kind":       req.Kind,
					"metadata": map[string]interface{}{
						"name":        req.Name,
						"namespace":   requestNamespace,
						"annotations": map[string]interface{}{ownerAnnotation: owner},
					},
					"spec": req.Spec,
				},
			}
			_, err = resources.Create(r.Context(), obj, metav1.CreateOptions{FieldManager: "rest-api"})
		}
		if err != nil {
			switch {
			case apierrors.IsConflict(err), apierrors.IsAlreadyExists(err), apierrors.IsNotFound(err):
				writeError(w, http.StatusConflict, "request changed concurrently; retry to recheck ownership")
			case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
				writeError(w, http.StatusBadRequest, "invalid request spec")
			default:
				writeError(w, http.StatusBadGateway, "could not write request")
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(applyResponse{
			Kind:      req.Kind,
			Name:      req.Name,
			Namespace: requestNamespace,
			Status:    status,
		})
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
