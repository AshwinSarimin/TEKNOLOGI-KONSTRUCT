package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestFailedSlackDeliveryCanRetryAndIsOnlyAcknowledgedAfterSuccess(t *testing.T) {
	notifying = sync.Map{}
	gvr := schema.GroupVersionResource{Group: "platform.teknologi.io", Version: "v1alpha1", Resource: "xnamespaces"}
	xr := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "platform.teknologi.io/v1alpha1",
		"kind":       "XNamespace",
		"metadata":   map[string]interface{}{"name": "retry-dev", "uid": "first-uid"},
		"status": map[string]interface{}{"conditions": []interface{}{
			map[string]interface{}{"type": "Ready", "status": "True"},
		}},
	}}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "XNamespaceList"}, xr)
	status := http.StatusInternalServerError
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	defer server.Close()

	handleXR(client, gvr, server.URL, xr)
	got, err := client.Resource(gvr).Get(context.Background(), xr.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetAnnotations()[notifiedAnnotation] == "true" {
		t.Fatal("failed Slack delivery was permanently acknowledged")
	}

	status = http.StatusOK
	handleXR(client, gvr, server.URL, xr)
	got, err = client.Resource(gvr).Get(context.Background(), xr.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetAnnotations()[notifiedAnnotation] != "true" {
		t.Fatal("successful retry was not acknowledged")
	}
}
