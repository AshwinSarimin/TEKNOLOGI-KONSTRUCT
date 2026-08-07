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

type XStorageAccount struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string            `yaml:"name"`
		Annotations map[string]string `yaml:"annotations,omitempty"`
		Labels      map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		AppName            string `yaml:"appName"`
		Environment        string `yaml:"environment"`
		StorageAccountName string `yaml:"storageAccountName"`
		Location           string `yaml:"location"`
		ResourceGroupName  string `yaml:"resourceGroupName"`
	} `yaml:"spec"`
}

func main() {
	log.Println("Storage Account pipeline starting...")

	data, err := ioutil.ReadFile("/kratix/input/object.yaml")
	if err != nil {
		log.Fatalf("Failed to read request: %v", err)
	}

	var req Request
	if err := yaml.Unmarshal(data, &req); err != nil {
		log.Fatalf("Failed to parse request: %v", err)
	}

	saName := computeStorageAccountName(req.Spec.AppName, req.Spec.Environment)
	log.Printf("Processing StorageAccountRequest: team=%s env=%s saName=%s", req.Spec.AppName, req.Spec.Environment, saName)

	if err := writeXStorageAccount(req, saName); err != nil {
		log.Fatalf("Failed to write XStorageAccount: %v", err)
	}

	if err := writeStatus(req, saName); err != nil {
		log.Printf("Failed to write status: %v", err)
	}

	log.Println("Pipeline completed successfully")
}

// computeStorageAccountName derives a valid Azure storage account name.
// Azure rules: 3-24 chars, lowercase alphanumeric only.
// Pattern: tek + team + env + sa — e.g. tekappadevsa (trimmed to 24).
func computeStorageAccountName(team, env string) string {
	re := regexp.MustCompile(`[^a-z0-9]`)
	team = re.ReplaceAllString(strings.ToLower(team), "")
	env = re.ReplaceAllString(strings.ToLower(env), "")
	name := "tek" + team + env + "sa"
	if len(name) > 24 {
		name = name[:24]
	}
	return name
}

func writeXStorageAccount(req Request, saName string) error {
	xsa := XStorageAccount{}
	xsa.APIVersion = "platform.teknologi.io/v1alpha1"
	xsa.Kind = "XStorageAccount"
	xsa.Metadata.Name = fmt.Sprintf("sa-%s-%s", req.Spec.AppName, req.Spec.Environment)
	xsa.Metadata.Annotations = map[string]string{
		"argocd.argoproj.io/sync-wave": "1",
	}
	xsa.Metadata.Labels = map[string]string{
		"kratix.io/promise-name":  "storage-account",
		"kratix.io/resource-name": req.Metadata.Name,
	}
	xsa.Spec.AppName = req.Spec.AppName
	xsa.Spec.Environment = req.Spec.Environment
	xsa.Spec.StorageAccountName = saName
	xsa.Spec.Location = req.Spec.Location
	xsa.Spec.ResourceGroupName = req.Spec.ResourceGroupName

	out, err := yaml.Marshal(xsa)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	if err := os.MkdirAll("/kratix/output", 0755); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	path := fmt.Sprintf("/kratix/output/xstorageaccount-%s-%s.yaml", req.Spec.AppName, req.Spec.Environment)
	if err := ioutil.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	log.Printf("Wrote XStorageAccount to %s", path)
	return nil
}

func writeStatus(req Request, saName string) error {
	status := map[string]interface{}{
		"message":            fmt.Sprintf("StorageAccount %s scheduled for provisioning", saName),
		"storageAccountName": saName,
		"environment":        req.Spec.Environment,
		"location":           req.Spec.Location,
		"resourceGroupName":  req.Spec.ResourceGroupName,
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}

	if err := ioutil.WriteFile("/kratix/metadata/status.json", data, 0644); err != nil {
		return err
	}

	xrName := fmt.Sprintf("sa-%s-%s", req.Spec.AppName, req.Spec.Environment)
	notify := map[string]string{
		"resourceName":       xrName,
		"resourceType":       "StorageAccount",
		"requesterNamespace": req.Metadata.Namespace,
		"xrPlural":           "xstorageaccounts",
	}
	notifyData, err := json.MarshalIndent(notify, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile("/kratix/metadata/notify.json", notifyData, 0644)
}
