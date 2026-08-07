package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Request mirrors the StorageAccountTerraformRequest CRD spec.
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

	// Apply defaults
	if req.Spec.Environment == "" {
		req.Spec.Environment = "dev"
	}
	if req.Spec.Location == "" {
		req.Spec.Location = "westeurope"
	}
	if req.Spec.ResourceGroupName == "" {
		req.Spec.ResourceGroupName = "teknologi-eur1-prd-platform-rg"
	}

	log.Printf("Processing StorageAccountRequest: team=%s env=%s location=%s rg=%s",
		req.Spec.AppName, req.Spec.Environment, req.Spec.Location, req.Spec.ResourceGroupName)

	// Compute Azure storage account name (3-24 chars, lowercase alphanumeric)
	saName := computeStorageAccountName(req.Spec.AppName, req.Spec.Environment)
	log.Printf("Storage account name: %s", saName)

	slackURL := os.Getenv("SLACK_WEBHOOK_URL")

	// Stage 1 — "provisioning started" (sent before terraform begins, so teams know it's in-flight)
	postSlack(slackURL, buildStartedMessage(req, saName))

	// Run Terraform — synchronous, completes before we post the result
	tfResult, tfErr := runTerraform(req, saName)

	if tfErr != nil {
		log.Printf("Terraform apply failed: %v", tfErr)
		postSlack(slackURL, buildFailedMessage(req, saName, tfErr.Error()))
		writeStatus(req, saName, "failed", tfErr.Error(), "")
		os.Exit(1)
	}

	log.Println("Terraform apply succeeded")

	// Stage 2 — "storage account live" (with actual endpoint from terraform output)
	postSlack(slackURL, buildLiveMessage(req, saName, tfResult.PrimaryBlobEndpoint))

	// Write ConfigMap to /kratix/output — deployed to workload cluster via ORCHESTRATION repo
	// so teams can look up storage account details with kubectl get configmap.
	if err := writeOutputConfigMap(req, saName, tfResult.PrimaryBlobEndpoint); err != nil {
		log.Printf("WARNING: failed to write output configmap: %v", err)
	}

	writeStatus(req, saName, "ready", "", tfResult.PrimaryBlobEndpoint)
	log.Println("Pipeline completed successfully")
}

// computeStorageAccountName derives a valid Azure storage account name.
// Azure rules: 3-24 chars, lowercase alphanumeric only.
// Pattern: tek + team + env + sa — e.g. tekfeaturesdvsa (trimmed to 24).
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

// TerraformResult holds the parsed outputs from terraform output -json.
type TerraformResult struct {
	PrimaryBlobEndpoint string
}

func runTerraform(req Request, saName string) (*TerraformResult, error) {
	// Write tfvars to /tmp so they are accessible regardless of -chdir
	vars := map[string]string{
		"storage_account_name": saName,
		"resource_group_name":  req.Spec.ResourceGroupName,
		"location":             req.Spec.Location,
		"app_name":             req.Spec.AppName,
		"environment":          req.Spec.Environment,
	}
	varsData, err := json.Marshal(vars)
	if err != nil {
		return nil, fmt.Errorf("marshal vars: %w", err)
	}
	varFile := "/tmp/tfvars.json"
	if err := ioutil.WriteFile(varFile, varsData, 0644); err != nil {
		return nil, fmt.Errorf("write tfvars: %w", err)
	}

	// Backend config — pulled from env vars (set from a Kubernetes Secret in the Promise spec)
	backendRG := requireEnv("TF_BACKEND_RESOURCE_GROUP")
	backendSA := requireEnv("TF_BACKEND_STORAGE_ACCOUNT")
	backendContainer := getEnv("TF_BACKEND_CONTAINER", "tfstate")
	stateKey := fmt.Sprintf("storage-account-%s-%s.tfstate", req.Spec.AppName, req.Spec.Environment)

	log.Printf("Terraform backend: sa=%s container=%s key=%s", backendSA, backendContainer, stateKey)

	// terraform init — initialises the Azure backend (reads state if it already exists)
	if err := runCmd("terraform",
		"-chdir=/app/terraform",
		"init",
		"-backend-config=resource_group_name="+backendRG,
		"-backend-config=storage_account_name="+backendSA,
		"-backend-config=container_name="+backendContainer,
		"-backend-config=key="+stateKey,
	); err != nil {
		return nil, fmt.Errorf("terraform init: %w", err)
	}

	// terraform apply — provisions the storage account
	if err := runCmd("terraform",
		"-chdir=/app/terraform",
		"apply",
		"-auto-approve",
		"-var-file="+varFile,
	); err != nil {
		return nil, fmt.Errorf("terraform apply: %w", err)
	}

	// terraform output — capture provisioned resource details
	outputJSON, err := runCmdOutput("terraform", "-chdir=/app/terraform", "output", "-json")
	if err != nil {
		// Non-fatal — we can still report success without the endpoint
		log.Printf("WARNING: terraform output failed: %v", err)
		return &TerraformResult{}, nil
	}

	var outputs map[string]struct {
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal([]byte(outputJSON), &outputs); err != nil {
		log.Printf("WARNING: failed to parse terraform output: %v", err)
		return &TerraformResult{}, nil
	}

	result := &TerraformResult{}
	if o, ok := outputs["primary_blob_endpoint"]; ok {
		if s, ok := o.Value.(string); ok {
			result.PrimaryBlobEndpoint = s
		}
	}
	return result, nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// --- Kubernetes output ---

// writeOutputConfigMap writes a ConfigMap with storage account details to /kratix/output.
// Kratix pushes this to the ORCHESTRATION repo; ArgoCD deploys it to the workload cluster.
// Teams can then do: kubectl get configmap storage-account-{team}-{env}-info -n default
func writeOutputConfigMap(req Request, saName, primaryBlobEndpoint string) error {
	if err := os.MkdirAll("/kratix/output", 0755); err != nil {
		return err
	}

	type configMapData struct {
		StorageAccountName  string `yaml:"storageAccountName"`
		PrimaryBlobEndpoint string `yaml:"primaryBlobEndpoint,omitempty"`
		ResourceGroupName   string `yaml:"resourceGroupName"`
		Location            string `yaml:"location"`
		Environment         string `yaml:"environment"`
	}

	type configMapMeta struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	}

	type configMap struct {
		APIVersion string            `yaml:"apiVersion"`
		Kind       string            `yaml:"kind"`
		Metadata   configMapMeta     `yaml:"metadata"`
		Data       configMapData     `yaml:"data"`
	}

	cm := configMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata: configMapMeta{
			Name:      fmt.Sprintf("storage-account-%s-%s-info", req.Spec.AppName, req.Spec.Environment),
			Namespace: "default",
			Labels: map[string]string{
				"teknologi.io/managed-by": "kratix",
				"teknologi.io/team":       req.Spec.AppName,
				"teknologi.io/promise":    "storage-account",
			},
		},
		Data: configMapData{
			StorageAccountName:  saName,
			PrimaryBlobEndpoint: primaryBlobEndpoint,
			ResourceGroupName:   req.Spec.ResourceGroupName,
			Location:            req.Spec.Location,
			Environment:         req.Spec.Environment,
		},
	}

	out, err := yaml.Marshal(cm)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/kratix/output/storage-account-%s-%s-info.yaml", req.Spec.AppName, req.Spec.Environment)
	if err := ioutil.WriteFile(path, out, 0644); err != nil {
		return err
	}
	log.Printf("Wrote output ConfigMap to %s", path)
	return nil
}

func writeStatus(req Request, saName, status, errMsg, endpoint string) {
	s := map[string]interface{}{
		"storageAccountName": saName,
		"status":             status,
		"environment":        req.Spec.Environment,
		"resourceGroupName":  req.Spec.ResourceGroupName,
	}
	if errMsg != "" {
		s["error"] = errMsg
	}
	if endpoint != "" {
		s["primaryBlobEndpoint"] = endpoint
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := ioutil.WriteFile("/kratix/metadata/status.json", data, 0644); err != nil {
		log.Printf("WARNING: failed to write status.json: %v", err)
	}
}

// --- Slack ---

type slackPayload struct {
	Blocks []slackBlock `json:"blocks"`
}

type slackBlock struct {
	Type   string      `json:"type"`
	Text   *slackText  `json:"text,omitempty"`
	Fields []slackText `json:"fields,omitempty"`
}

type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func buildStartedMessage(req Request, saName string) slackPayload {
	return slackPayload{Blocks: []slackBlock{
		{Type: "section", Text: &slackText{Type: "mrkdwn", Text: "🔄 *Storage Account provisioning started*"}},
		{Type: "section", Fields: []slackText{
			{Type: "mrkdwn", Text: fmt.Sprintf("*Resource*\n%s", saName)},
			{Type: "mrkdwn", Text: fmt.Sprintf("*Requested by*\n%s", req.Metadata.Namespace)},
			{Type: "mrkdwn", Text: fmt.Sprintf("*Environment*\n%s", req.Spec.Environment)},
			{Type: "mrkdwn", Text: fmt.Sprintf("*Time*\n%s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))},
		}},
	}}
}

func buildLiveMessage(req Request, saName, endpoint string) slackPayload {
	fields := []slackText{
		{Type: "mrkdwn", Text: fmt.Sprintf("*Storage Account*\n%s", saName)},
		{Type: "mrkdwn", Text: fmt.Sprintf("*Requested by*\n%s", req.Metadata.Namespace)},
		{Type: "mrkdwn", Text: fmt.Sprintf("*Environment*\n%s", req.Spec.Environment)},
		{Type: "mrkdwn", Text: fmt.Sprintf("*Time*\n%s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))},
	}
	if endpoint != "" {
		fields = append(fields, slackText{
			Type: "mrkdwn",
			Text: fmt.Sprintf("*Blob Endpoint*\n%s", endpoint),
		})
	}
	return slackPayload{Blocks: []slackBlock{
		{Type: "section", Text: &slackText{Type: "mrkdwn", Text: "🚀 *Storage Account is live*"}},
		{Type: "section", Fields: fields},
	}}
}

func buildFailedMessage(req Request, saName, errMsg string) slackPayload {
	// Truncate long error messages so they fit in a Slack field
	if len(errMsg) > 300 {
		errMsg = errMsg[:300] + "…"
	}
	return slackPayload{Blocks: []slackBlock{
		{Type: "section", Text: &slackText{Type: "mrkdwn", Text: "❌ *Storage Account provisioning failed*"}},
		{Type: "section", Fields: []slackText{
			{Type: "mrkdwn", Text: fmt.Sprintf("*Resource*\n%s", saName)},
			{Type: "mrkdwn", Text: fmt.Sprintf("*Requested by*\n%s", req.Metadata.Namespace)},
			{Type: "mrkdwn", Text: fmt.Sprintf("*Time*\n%s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))},
		}},
		{Type: "section", Text: &slackText{
			Type: "mrkdwn",
			Text: fmt.Sprintf("*Error*\n```%s```", errMsg),
		}},
	}}
}

func postSlack(webhookURL string, payload slackPayload) {
	if webhookURL == "" {
		log.Println("SLACK_WEBHOOK_URL not set — skipping notification")
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("WARNING: marshal slack payload: %v", err)
		return
	}
	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Printf("WARNING: Slack POST failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("WARNING: Slack returned HTTP %d", resp.StatusCode)
		return
	}
	log.Println("Slack notification sent")
}

// --- Helpers ---

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("Required env var %s not set", key)
	}
	return v
}