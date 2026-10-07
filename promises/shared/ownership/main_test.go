package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type reservationServer struct {
	mu      sync.Mutex
	objects map[string]configMap
}

func (s *reservationServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const prefix = "/api/v1/namespaces/kratix-workloads/configmaps"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, prefix+"/")
	switch r.Method {
	case http.MethodPost:
		var cm configMap
		if err := json.NewDecoder(r.Body).Decode(&cm); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, exists := s.objects[cm.Metadata.Name]; exists {
			http.Error(w, "already exists", http.StatusConflict)
			return
		}
		cm.Metadata.UID = "object-" + cm.Metadata.Name
		s.objects[cm.Metadata.Name] = cm
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cm)
	case http.MethodGet:
		cm, exists := s.objects[name]
		if !exists {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(cm)
	case http.MethodDelete:
		cm, exists := s.objects[name]
		if !exists {
			http.NotFound(w, r)
			return
		}
		var options struct {
			Preconditions struct {
				UID string `json:"uid"`
			} `json:"preconditions"`
		}
		json.NewDecoder(r.Body).Decode(&options)
		if options.Preconditions.UID != cm.Metadata.UID {
			http.Error(w, "UID conflict", http.StatusConflict)
			return
		}
		delete(s.objects, name)
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestClient(t *testing.T) (*kubeClient, *reservationServer, func()) {
	t.Helper()
	store := &reservationServer{objects: map[string]configMap{}}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		return w.Result(), nil
	})}
	return &kubeClient{baseURL: "http://kubernetes.test", http: client}, store, func() {}
}

func request(kind, uid, name string) []byte {
	return []byte(fmt.Sprintf("kind: %s\nmetadata:\n  uid: %s\n  name: %s\n  namespace: kratix-workloads\nspec:\n  appName: app-a\n  environment: dev\n  resourceGroupName: test-rg\n", kind, uid, name))
}

func TestSecondRequestCannotTakeOverExistingTarget(t *testing.T) {
	client, store, closeServer := newTestClient(t)
	defer closeServer()
	ctx := context.Background()
	a := owner{UID: "uid-a", Kind: "StorageAccountRequest", Name: "first", Namespace: "kratix-workloads"}
	b := owner{UID: "uid-b", Kind: "TeamOnboardingRequest", Name: "second", Namespace: "kratix-workloads"}
	if err := reserve(ctx, client, a, []string{"azure-storage/tekappadevsa"}); err != nil {
		t.Fatal(err)
	}
	if err := reserve(ctx, client, b, []string{"azure-storage/tekappadevsa"}); err == nil || !strings.Contains(err.Error(), "uid-a") {
		t.Fatalf("foreign request should be rejected with current owner: %v", err)
	}
	if len(store.objects) != 1 {
		t.Fatalf("collision changed reservations: %+v", store.objects)
	}
}

func TestSameRequestUpdateAndVerifiedCleanupRelease(t *testing.T) {
	client, store, closeServer := newTestClient(t)
	defer closeServer()
	ctx := context.Background()
	a := owner{UID: "uid-a", Kind: "StorageAccountRequest", Name: "first", Namespace: "kratix-workloads"}
	b := owner{UID: "uid-b", Kind: "StorageAccountRequest", Name: "second", Namespace: "kratix-workloads"}
	target := "azure-storage/tekappadevsa"
	if err := reserve(ctx, client, a, []string{target}); err != nil {
		t.Fatal(err)
	}
	if err := reserve(ctx, client, a, []string{target}); err != nil {
		t.Fatalf("same UID update failed: %v", err)
	}
	if err := reserve(ctx, client, b, []string{target}); err == nil {
		t.Fatal("second request took over before cleanup")
	}
	// An operator releases the reservation only after observing downstream deletion.
	name := reservationName(target)
	delete(store.objects, name)
	if err := reserve(ctx, client, b, []string{target}); err != nil {
		t.Fatalf("reclaim after verified cleanup failed: %v", err)
	}
}

func TestMultiTargetConflictRollsBackOnlyNewReservations(t *testing.T) {
	client, store, closeServer := newTestClient(t)
	defer closeServer()
	ctx := context.Background()
	a := owner{UID: "uid-a", Kind: "TeamOnboardingRequest", Name: "first", Namespace: "kratix-workloads"}
	b := owner{UID: "uid-b", Kind: "TeamOnboardingRequest", Name: "second", Namespace: "kratix-workloads"}
	if err := reserve(ctx, client, a, []string{"azure-storage/tekappadevsa"}); err != nil {
		t.Fatal(err)
	}
	if err := reserve(ctx, client, b, []string{"azure-rg/test-rg", "azure-storage/tekappadevsa"}); err == nil {
		t.Fatal("multi-target request should have failed")
	}
	if _, exists := store.objects[reservationName("azure-rg/test-rg")]; exists {
		t.Fatal("failed request retained its new reservation")
	}
	if store.objects[reservationName("azure-storage/tekappadevsa")].Data["ownerUID"] != "uid-a" {
		t.Fatal("rollback removed or changed the other request's reservation")
	}
}

func TestTargetsCoverXRAndPhysicalResources(t *testing.T) {
	outputs := map[string][]byte{
		"rg.yaml": []byte("kind: XResourceGroup\nmetadata:\n  name: teknologi-eur1-dev-app-a-rg\n"),
		"ns.yaml": []byte("kind: XNamespace\nmetadata:\n  name: app-a-dev\nspec:\n  namespaceName: app-a-dev\n"),
		"kv.yaml": []byte("kind: XKeyVault\nmetadata:\n  name: kv-app-a-dev\nspec:\n  vaultName: kvappadev\n"),
		"sa.yaml": []byte("kind: XStorageAccount\nmetadata:\n  name: sa-app-a-dev\nspec:\n  storageAccountName: tekappadevsa\n"),
	}
	got, err := targetsForRequest(request("TeamOnboardingRequest", "uid-a", "first"), outputs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"azure-keyvault/kvappadev", "azure-rg/teknologi-eur1-dev-app-a-rg", "azure-storage/tekappadevsa", "k8s-namespace/app-a-dev", "xr/XKeyVault/kv-app-a-dev", "xr/XNamespace/app-a-dev", "xr/XResourceGroup/teknologi-eur1-dev-app-a-rg", "xr/XStorageAccount/sa-app-a-dev"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestPendingOnboardingTakesNoReservation(t *testing.T) {
	got, err := targetsForRequest(request("TeamOnboardingRequest", "uid-a", "first"), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("pending approval reserved targets: %v, %v", got, err)
	}
}

func TestMissingStandaloneOutputCannotBypassGate(t *testing.T) {
	if _, err := targetsForRequest(request("StorageAccountRequest", "uid-a", "first"), nil); err == nil {
		t.Fatal("standalone pipeline with no XR output bypassed the ownership gate")
	}
}

func TestUnknownOutputCannotBypassGate(t *testing.T) {
	outputs := map[string][]byte{"other.yaml": []byte("kind: ConfigMap\nmetadata:\n  name: other\n")}
	if _, err := targetsForRequest(request("StorageAccountRequest", "uid-a", "first"), outputs); err == nil {
		t.Fatal("unknown output bypassed the ownership gate")
	}
}

func TestTerraformReservesStateAndCrossplaneAzureNameBeforeApply(t *testing.T) {
	got, err := targetsForRequest(request("StorageAccountTerraformRequest", "uid-a", "first"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"azure-storage/tekappadevsa", "terraform-state/storage-account-app-a-dev.tfstate"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestTerraformAndCrossplaneCannotPublishSameAzureAccount(t *testing.T) {
	client, _, closeServer := newTestClient(t)
	defer closeServer()
	tfRaw := request("StorageAccountTerraformRequest", "uid-tf", "terraform")
	tfTargets, err := targetsForRequest(tfRaw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := reserve(context.Background(), client, owner{UID: "uid-tf", Kind: "StorageAccountTerraformRequest", Name: "terraform", Namespace: namespace}, tfTargets); err != nil {
		t.Fatal(err)
	}
	xr := map[string][]byte{"sa.yaml": []byte("kind: XStorageAccount\nmetadata:\n  name: sa-app-a-dev\nspec:\n  storageAccountName: tekappadevsa\n")}
	xrTargets, err := targetsForRequest(request("StorageAccountRequest", "uid-xr", "crossplane"), xr)
	if err != nil {
		t.Fatal(err)
	}
	if err := reserve(context.Background(), client, owner{UID: "uid-xr", Kind: "StorageAccountRequest", Name: "crossplane", Namespace: namespace}, xrTargets); err == nil {
		t.Fatal("Crossplane request bypassed Terraform's Azure account reservation")
	}
}
