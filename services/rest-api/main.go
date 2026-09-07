package main

import (
	"log"
	"net/http"
	"os"
)

// rest-api is the thin HTTP service for Phase 5 — Architecture.md's "tertiary"
// consumption pattern. It translates authenticated HTTP calls into Promise CRDs
// on the hub cluster, for non-Backstage, non-kubectl consumers. Same outcome as
// the Backstage kratix:apply scaffolder action and kubectl — one generic /apply
// endpoint, not one endpoint per Promise, so new Promises need no API changes.
func main() {
	port := getEnv("PORT", "8080")
	apiKey := requireEnv("API_KEY")

	client, err := newDynamicClient()
	if err != nil {
		log.Fatalf("build k8s client: %v", err)
	}

	// Plain path patterns, not "METHOD /path" — this repo's Go containers build
	// on golang:1.21-alpine, and method-prefixed ServeMux patterns are a Go 1.22+
	// feature; on 1.21 the whole string is matched as a literal path, so nothing
	// ever routes. Method checks happen inside each handler instead.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", requireMethod(http.MethodGet, handleHealthz))
	mux.HandleFunc("/apply", requireMethod(http.MethodPost, withAuth(apiKey, handleApply(client))))

	log.Printf("rest-api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var %s", key)
	}
	return v
}

func requireMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// withAuth checks a single shared bearer token — matches this platform's
// current local-demo security posture (see Experiments.md). Not production-grade;
// production would need per-caller tokens or OIDC, not a shared secret.
func withAuth(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer "+apiKey {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
