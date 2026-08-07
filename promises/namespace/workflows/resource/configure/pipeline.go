package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"gopkg.in/yaml.v2"
)

type Request struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		NamespaceName     string `yaml:"namespaceName"`
		Environment       string `yaml:"environment"`
		NetworkVisibility string `yaml:"networkVisibility"`
	} `yaml:"spec"`
}

type XNamespace struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string            `yaml:"name"`
		Annotations map[string]string `yaml:"annotations,omitempty"`
		Labels      map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		NamespaceName     string `yaml:"namespaceName"`
		Environment       string `yaml:"environment"`
		NetworkVisibility string `yaml:"networkVisibility"`
	} `yaml:"spec"`
}

func main() {
	log.Println("Namespace pipeline starting...")

	data, err := ioutil.ReadFile("/kratix/input/object.yaml")
	if err != nil {
		log.Fatalf("Failed to read request: %v", err)
	}

	var req Request
	if err := yaml.Unmarshal(data, &req); err != nil {
		log.Fatalf("Failed to parse request: %v", err)
	}

	if req.Spec.Environment == "" {
		req.Spec.Environment = "dev"
	}
	if req.Spec.NetworkVisibility == "" {
		req.Spec.NetworkVisibility = "private"
	}

	log.Printf("Processing NamespaceRequest: team=%s env=%s visibility=%s",
		req.Spec.NamespaceName, req.Spec.Environment, req.Spec.NetworkVisibility)

	if err := writeXNamespace(req); err != nil {
		log.Fatalf("Failed to write XNamespace: %v", err)
	}

	if err := configureCloudflare(req); err != nil {
		log.Printf("Cloudflare configuration skipped: %v", err)
	}

	if err := writeStatus(req); err != nil {
		log.Printf("Failed to write status: %v", err)
	}

	log.Println("Pipeline completed successfully")
}

func writeXNamespace(req Request) error {
	xns := XNamespace{}
	xns.APIVersion = "platform.teknologi.io/v1alpha1"
	xns.Kind = "XNamespace"
	xns.Metadata.Name = req.Spec.NamespaceName
	xns.Metadata.Annotations = map[string]string{
		"argocd.argoproj.io/sync-wave": "1", // Apply after XRD (wave 0) is healthy
	}
	xns.Metadata.Labels = map[string]string{
		"kratix.io/promise-name":  "namespace",
		"kratix.io/resource-name": req.Metadata.Name,
	}
	xns.Spec.NamespaceName = req.Spec.NamespaceName
	xns.Spec.Environment = req.Spec.Environment
	xns.Spec.NetworkVisibility = req.Spec.NetworkVisibility

	out, err := yaml.Marshal(xns)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	if err := os.MkdirAll("/kratix/output", 0755); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	path := fmt.Sprintf("/kratix/output/xnamespace-%s.yaml", req.Spec.NamespaceName)
	if err := ioutil.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	log.Printf("Wrote XNamespace to %s", path)
	return nil
}

func configureCloudflare(req Request) error {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	zoneID := os.Getenv("CLOUDFLARE_ZONE_ID")

	if token == "" || zoneID == "" {
		return fmt.Errorf("CLOUDFLARE_API_TOKEN or CLOUDFLARE_ZONE_ID not set — skipping DNS configuration")
	}

	// Create a DNS record for the team namespace ingress
	// Pattern: {teamName}.teknologi.nl → workload cluster ingress
	dnsName := fmt.Sprintf("%s.teknologi.nl", strings.ToLower(req.Spec.NamespaceName))

	payload := map[string]interface{}{
		"type":    "CNAME",
		"name":    dnsName,
		"content": "ingress.teknologi.nl",
		"ttl":     1,
		"proxied": req.Spec.NetworkVisibility == "public",
		"comment": fmt.Sprintf("managed by kratix namespace-promise team=%s", req.Spec.NamespaceName),
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID)

	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to build Cloudflare request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("Cloudflare API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != 200 {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("Cloudflare returned %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("Cloudflare DNS record created: %s", dnsName)
	return nil
}

func writeStatus(req Request) error {
	status := map[string]interface{}{
		"message":           fmt.Sprintf("Namespace %s scheduled for provisioning", req.Spec.NamespaceName),
		"namespaceName":     req.Spec.NamespaceName,
		"environment":       req.Spec.Environment,
		"networkVisibility": req.Spec.NetworkVisibility,
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}

	if err := ioutil.WriteFile("/kratix/metadata/status.json", data, 0644); err != nil {
		return err
	}

	// Write notify.json for the shared notify container (stage 1 + stage 2)
	notify := map[string]string{
		"resourceName":       req.Spec.NamespaceName,
		"resourceType":       "Namespace",
		"requesterNamespace": req.Metadata.Namespace,
		"xrPlural":           "xnamespaces",
	}
	notifyData, err := json.MarshalIndent(notify, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile("/kratix/metadata/notify.json", notifyData, 0644)
}
