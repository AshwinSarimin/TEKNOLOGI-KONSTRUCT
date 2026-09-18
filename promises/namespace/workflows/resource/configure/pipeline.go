package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"

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

	log.Printf("Processing NamespaceRequest: team=%s env=%s visibility=%s",
		req.Spec.NamespaceName, req.Spec.Environment, req.Spec.NetworkVisibility)

	if err := writeXNamespace(req); err != nil {
		log.Fatalf("Failed to write XNamespace: %v", err)
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
