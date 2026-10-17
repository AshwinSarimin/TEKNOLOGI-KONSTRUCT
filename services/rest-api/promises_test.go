package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const aliceToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const bobToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// Exercise the real dynamic client over HTTP. The fixture models Kubernetes
// create conflicts and resourceVersion checks, which client-go's fake omits.
type kubeFixture struct {
	mu        sync.Mutex
	object    map[string]interface{}
	writes    int
	onWrite   func(*kubeFixture)
	getStatus int
}

func newKubeFixture(t *testing.T, object map[string]interface{}) (*kubeFixture, dynamic.Interface) {
	t.Helper()
	f := &kubeFixture{object: object}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fail := func(code int, reason string) {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": reason, "code": code,
			})
		}
		if !strings.HasPrefix(r.URL.Path, "/apis/marketplace.kratix.io/v1alpha1/namespaces/kratix-workloads/") {
			t.Errorf("unexpected Kubernetes path: %s", r.URL.Path)
			fail(404, "NotFound")
			return
		}
		if r.Method == http.MethodGet {
			if f.getStatus != 0 {
				fail(f.getStatus, "InternalError")
				return
			}
			if f.object == nil {
				fail(404, "NotFound")
				return
			}
			_ = json.NewEncoder(w).Encode(f.object)
			return
		}
		f.writes++
		if f.onWrite != nil {
			f.onWrite(f)
		}
		var submitted map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
			t.Errorf("decode Kubernetes write: %v", err)
			fail(400, "BadRequest")
			return
		}
		switch r.Method {
		case http.MethodPost:
			if f.object != nil {
				fail(409, "AlreadyExists")
				return
			}
			metadata := submitted["metadata"].(map[string]interface{})
			metadata["resourceVersion"], metadata["uid"] = "1", "original-uid"
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			if f.object == nil {
				fail(404, "NotFound")
				return
			}
			old := f.object["metadata"].(map[string]interface{})
			meta := submitted["metadata"].(map[string]interface{})
			if meta["resourceVersion"] != old["resourceVersion"] || meta["uid"] != old["uid"] {
				fail(409, "Conflict")
				return
			}
		case http.MethodPatch:
			// Characterize the original force-apply bug: it overwrites regardless
			// of the current request owner.
		default:
			t.Errorf("unexpected Kubernetes method: %s", r.Method)
			fail(405, "MethodNotAllowed")
			return
		}
		f.object = submitted
		_ = json.NewEncoder(w).Encode(submitted)
	}))
	t.Cleanup(srv.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return f, client
}

func existingRequest(owner string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "marketplace.kratix.io/v1alpha1", "kind": "KeyVaultRequest",
		"metadata": map[string]interface{}{
			"name": "shared", "namespace": "kratix-workloads", "resourceVersion": "7", "uid": "original-uid",
			"annotations": map[string]interface{}{"teknologi.io/request-owner": owner, "controller.example/keep": "yes"},
			"finalizers":  []interface{}{"platform.kratix.io/resource-cleanup"},
		},
		"spec":   map[string]interface{}{"appName": "original", "environment": "dev"},
		"status": map[string]interface{}{"message": "controller owned"},
	}
}

func applyBody(kind string) string {
	return `{"kind":"` + kind + `","name":"shared","spec":{"appName":"changed","environment":"dev"}}`
}

func callApply(handler http.Handler, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/apply", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestApplyRejectsExistingUnownedRequest(t *testing.T) {
	f, client := newKubeFixture(t, existingRequest(""))
	w := callApply(testHandler(t, client), aliceToken, applyBody("KeyVaultRequest"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if f.writes != 0 {
		t.Fatal("unowned request was written")
	}
	name, _, _ := unstructured.NestedString(f.object, "spec", "appName")
	if name != "original" {
		t.Fatalf("unowned request changed to %q", name)
	}
}

func testHandler(t *testing.T, client dynamic.Interface) http.Handler {
	t.Helper()
	keys, err := parseAPIKeys(`{"alice":"` + aliceToken + `","bob":"` + bobToken + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	return withAuth(keys, handleApply(client))
}

func TestApplyOwnershipForEveryKind(t *testing.T) {
	for _, kind := range []string{"KeyVaultRequest", "NamespaceRequest", "StorageAccountRequest", "StorageAccountTerraformRequest", "TeamOnboardingRequest"} {
		t.Run(kind, func(t *testing.T) {
			f, client := newKubeFixture(t, nil)
			handler := testHandler(t, client)
			body := applyBody(kind)
			if w := callApply(handler, aliceToken, body); w.Code != 201 {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			owner, _, _ := unstructured.NestedString(f.object, "metadata", "annotations", "teknologi.io/request-owner")
			if owner != "rest-api:alice" {
				t.Fatalf("created owner = %q", owner)
			}
			before := f.writes
			if w := callApply(handler, bobToken, body); w.Code != 403 || f.writes != before {
				t.Fatalf("foreign caller: %d, writes %d -> %d", w.Code, before, f.writes)
			}
			if w := callApply(handler, aliceToken, body); w.Code != 200 {
				t.Fatalf("retry: %d %s", w.Code, w.Body.String())
			}
			if w := callApply(handler, aliceToken, strings.Replace(body, "changed", "updated", 1)); w.Code != 200 {
				t.Fatalf("update: %d %s", w.Code, w.Body.String())
			}
			name, _, _ := unstructured.NestedString(f.object, "spec", "appName")
			if name != "updated" {
				t.Fatalf("same-owner update not persisted: %q", name)
			}
		})
	}
}

func TestApplyPreservesControllerMetadata(t *testing.T) {
	f, client := newKubeFixture(t, existingRequest("rest-api:alice"))
	w := callApply(testHandler(t, client), aliceToken, applyBody("KeyVaultRequest"))
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	annotation, _, _ := unstructured.NestedString(f.object, "metadata", "annotations", "controller.example/keep")
	status, _, _ := unstructured.NestedString(f.object, "status", "message")
	finalizers, _, _ := unstructured.NestedStringSlice(f.object, "metadata", "finalizers")
	if annotation != "yes" || status != "controller owned" || len(finalizers) != 1 {
		t.Fatalf("controller state lost: %#v", f.object)
	}
}

func TestApplyRejectsBackstageOwner(t *testing.T) {
	f, client := newKubeFixture(t, existingRequest("backstage:user:default/alice"))
	w := callApply(testHandler(t, client), aliceToken, applyBody("KeyVaultRequest"))
	if w.Code != 403 || f.writes != 0 {
		t.Fatalf("cross-broker overwrite: %d, writes %d", w.Code, f.writes)
	}
}

func TestApplyRejectsUnauthenticatedAndSpoofedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, token, body string
		status            int
	}{
		{"no token", "", applyBody("KeyVaultRequest"), 401},
		{"bad token", "incorrect", applyBody("KeyVaultRequest"), 401},
		{"caller field", aliceToken, `{"kind":"KeyVaultRequest","name":"shared","spec":{},"caller":"bob"}`, 400},
		{"metadata", aliceToken, `{"kind":"KeyVaultRequest","name":"shared","spec":{},"metadata":{"annotations":{"teknologi.io/request-owner":"rest-api:bob"}}}`, 400},
		{"unknown kind", aliceToken, applyBody("Secret"), 400},
		{"missing spec", aliceToken, `{"kind":"KeyVaultRequest","name":"shared"}`, 400},
		{"missing name", aliceToken, `{"kind":"KeyVaultRequest","spec":{}}`, 400},
		{"invalid name", aliceToken, `{"kind":"KeyVaultRequest","name":"../other","spec":{}}`, 400},
		{"trailing JSON", aliceToken, applyBody("KeyVaultRequest") + `{}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, client := newKubeFixture(t, nil)
			w := callApply(testHandler(t, client), tc.token, tc.body)
			if w.Code != tc.status || f.writes != 0 {
				t.Fatalf("got %d, want %d, writes %d: %s", w.Code, tc.status, f.writes, w.Body.String())
			}
		})
	}
}

func TestApplyFailsClosedOnReadError(t *testing.T) {
	f, client := newKubeFixture(t, nil)
	f.getStatus = 500
	w := callApply(testHandler(t, client), aliceToken, applyBody("KeyVaultRequest"))
	if w.Code != 502 || f.writes != 0 {
		t.Fatalf("read error: %d, writes %d", w.Code, f.writes)
	}
}

func TestApplyRejectsTerminatingRequest(t *testing.T) {
	obj := existingRequest("rest-api:alice")
	obj["metadata"].(map[string]interface{})["deletionTimestamp"] = "2026-10-04T00:00:00Z"
	f, client := newKubeFixture(t, obj)
	w := callApply(testHandler(t, client), aliceToken, applyBody("KeyVaultRequest"))
	if w.Code != 409 || f.writes != 0 {
		t.Fatalf("terminating request: %d, writes %d", w.Code, f.writes)
	}
}

func TestApplyDoesNotOverwriteConcurrentChanges(t *testing.T) {
	for _, mode := range []string{"create race", "owner changed", "deleted and recreated"} {
		t.Run(mode, func(t *testing.T) {
			obj := existingRequest("rest-api:alice")
			if mode == "create race" {
				obj = nil
			}
			f, client := newKubeFixture(t, obj)
			f.onWrite = func(f *kubeFixture) {
				f.object = existingRequest("rest-api:bob")
				meta := f.object["metadata"].(map[string]interface{})
				meta["resourceVersion"] = "8"
				if mode == "deleted and recreated" {
					meta["uid"] = "new-uid"
				}
			}
			w := callApply(testHandler(t, client), aliceToken, applyBody("KeyVaultRequest"))
			if w.Code != 409 || f.writes != 1 {
				t.Fatalf("conflict: %d, writes %d: %s", w.Code, f.writes, w.Body.String())
			}
			owner, _, _ := unstructured.NestedString(f.object, "metadata", "annotations", "teknologi.io/request-owner")
			name, _, _ := unstructured.NestedString(f.object, "spec", "appName")
			if owner != "rest-api:bob" || name != "original" {
				t.Fatalf("concurrent object overwritten: %#v", f.object)
			}
		})
	}
}
