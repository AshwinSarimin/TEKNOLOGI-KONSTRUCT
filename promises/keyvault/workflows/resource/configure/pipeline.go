package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v2"
)

type Request struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		AppName           string `yaml:"appName"`
		Environment       string `yaml:"environment"`
		Location          string `yaml:"location"`
		ResourceGroupName string `yaml:"resourceGroupName"`
	} `yaml:"spec"`
}

type XKeyVault struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string            `yaml:"name"`
		Annotations map[string]string `yaml:"annotations,omitempty"`
		Labels      map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		AppName           string `yaml:"appName"`
		TenantId          string `yaml:"tenantId"`
		Environment       string `yaml:"environment"`
		Location          string `yaml:"location"`
		ResourceGroupName string `yaml:"resourceGroupName"`
		VaultName         string `yaml:"vaultName"`
	} `yaml:"spec"`
}

func main() {
	log.Println("KeyVault pipeline starting...")

	data, err := ioutil.ReadFile("/kratix/input/object.yaml")
	if err != nil {
		log.Fatalf("Failed to read request: %v", err)
	}

	var req Request
	if err := yaml.Unmarshal(data, &req); err != nil {
		log.Fatalf("Failed to parse request: %v", err)
	}

	vaultName := computeVaultName(req.Spec.AppName, req.Spec.Environment)
	log.Printf("Processing KeyVaultRequest: team=%s env=%s location=%s vaultName=%s",
		req.Spec.AppName, req.Spec.Environment, req.Spec.Location, vaultName)

	if err := writeXKeyVault(req, vaultName); err != nil {
		log.Fatalf("Failed to write XKeyVault: %v", err)
	}

	if err := writeStatus(req, vaultName); err != nil {
		log.Printf("Failed to write status: %v", err)
	}

	log.Println("Pipeline completed successfully")
}

// computeVaultName derives a valid Azure Key Vault name.
// Azure rules: 3-24 chars, alphanumeric only, must start with a letter.
// Pattern: kv + team + env — e.g. kvappadev (trimmed to 24). Mirrors
// computeStorageAccountName in the storage-account promise's pipeline.
func computeVaultName(team, env string) string {
	re := regexp.MustCompile(`[^a-z0-9]`)
	team = re.ReplaceAllString(strings.ToLower(team), "")
	env = re.ReplaceAllString(strings.ToLower(env), "")
	name := "kv" + team + env
	if len(name) > 24 {
		name = name[:24]
	}
	return name
}

func writeXKeyVault(req Request, vaultName string) error {
	xkv := XKeyVault{}
	xkv.APIVersion = "platform.teknologi.io/v1alpha1"
	xkv.Kind = "XKeyVault"
	xkv.Metadata.Name = fmt.Sprintf("kv-%s-%s", req.Spec.AppName, req.Spec.Environment)
	xkv.Metadata.Annotations = map[string]string{
		"argocd.argoproj.io/sync-wave": "1", // Apply after XRD (wave 0) is healthy
	}
	xkv.Metadata.Labels = map[string]string{
		"kratix.io/promise-name":  "keyvault",
		"kratix.io/resource-name": req.Metadata.Name,
	}
	tenantId := os.Getenv("AZURE_TENANT_ID")
	if tenantId == "" {
		return fmt.Errorf("AZURE_TENANT_ID not set")
	}
	xkv.Spec.AppName = req.Spec.AppName
	xkv.Spec.TenantId = tenantId
	xkv.Spec.Environment = req.Spec.Environment
	xkv.Spec.Location = req.Spec.Location
	xkv.Spec.ResourceGroupName = req.Spec.ResourceGroupName
	xkv.Spec.VaultName = vaultName

	out, err := yaml.Marshal(xkv)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	if err := os.MkdirAll("/kratix/output", 0755); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	path := fmt.Sprintf("/kratix/output/xkeyvault-%s-%s.yaml", req.Spec.AppName, req.Spec.Environment)
	if err := ioutil.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	log.Printf("Wrote XKeyVault to %s", path)
	return nil
}

func writeStatus(req Request, vaultName string) error {
	status := map[string]interface{}{
		"message":           fmt.Sprintf("KeyVault %s scheduled for provisioning", vaultName),
		"vaultName":         vaultName,
		"environment":       req.Spec.Environment,
		"location":          req.Spec.Location,
		"resourceGroupName": req.Spec.ResourceGroupName,
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}

	if err := ioutil.WriteFile("/kratix/metadata/status.json", data, 0644); err != nil {
		return err
	}

	// notify.json — read by the shared notify container. Uses the XR's own
	// K8s name (kv-<appName>-<env>), not the Azure-safe compact vaultName,
	// for a readable Slack message — mirrors the storage-account promise.
	xrName := fmt.Sprintf("kv-%s-%s", req.Spec.AppName, req.Spec.Environment)
	notify := map[string]string{
		"resourceName":       xrName,
		"resourceType":       "KeyVault",
		"requesterNamespace": req.Metadata.Namespace,
		"xrPlural":           "xkeyvaults",
	}
	notifyData, err := json.MarshalIndent(notify, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile("/kratix/metadata/notify.json", notifyData, 0644)
}