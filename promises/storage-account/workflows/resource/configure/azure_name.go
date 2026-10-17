package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type claimGet func(context.Context, string) (int, []byte, error)

func azureCandidate(kind, appName, env string) string {
	clean := regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString
	stem := clean(strings.ToLower(appName), "")
	limit := 9
	if kind == "storage" {
		limit = 6
	}
	if len(stem) > limit {
		stem = stem[:limit]
	}
	sum := sha256.Sum256([]byte("v2\x00" + kind + "\x00" + appName + "\x00" + env))
	digest := fmt.Sprintf("%x", sum[:5])
	if kind == "storage" {
		return "tek" + stem + clean(strings.ToLower(env), "") + digest + "sa"
	}
	return "kv" + stem + clean(strings.ToLower(env), "") + digest
}

func storageCandidate(appName, env string) string { return azureCandidate("storage", appName, env) }

func resolveStorageName(ctx context.Context, appName, env, requestUID string, get claimGet) (string, error) {
	return resolveAzureName(ctx, "azure-storage/"+computeStorageAccountName(appName, env), requestUID, storageCandidate(appName, env), get)
}

func resolveAzureName(ctx context.Context, legacyTarget, requestUID, candidate string, get claimGet) (string, error) {
	if requestUID == "" {
		return "", fmt.Errorf("request metadata.uid is required to resolve %s", legacyTarget)
	}
	sum := sha256.Sum256([]byte(legacyTarget))
	claimName := fmt.Sprintf("ownership-%x", sum[:16])
	status, body, err := get(ctx, claimName)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", claimName, err)
	}
	if status == http.StatusNotFound {
		return candidate, nil
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("read %s: HTTP %d", claimName, status)
	}
	var claim struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &claim); err != nil {
		return "", fmt.Errorf("decode %s: %w", claimName, err)
	}
	if claim.Data["target"] != legacyTarget || claim.Data["ownerUID"] == "" {
		return "", fmt.Errorf("invalid reservation %s for %s", claimName, legacyTarget)
	}
	if claim.Data["ownerUID"] == requestUID {
		return legacyTarget[strings.IndexByte(legacyTarget, '/')+1:], nil
	}
	return candidate, nil
}

func inClusterClaimGet(ctx context.Context, claimName string) (int, []byte, error) {
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return 0, nil, err
	}
	ca, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return 0, nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return 0, nil, fmt.Errorf("invalid Kubernetes CA")
	}
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return 0, nil, fmt.Errorf("Kubernetes service endpoint missing")
	}
	url := "https://" + host + ":" + port + "/api/v1/namespaces/kratix-workloads/configmaps/" + claimName
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, body, err
}
