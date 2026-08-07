package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"
)

const (
	pollInterval = 10 * time.Second
	maxWait      = 10 * time.Minute
	k8sAPIBase   = "https://kubernetes.default.svc:443"
	tokenPath    = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caPath       = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	notifyMeta   = "/kratix/metadata/notify.json"
)

// NotifyMeta is written by the configure container to /kratix/metadata/notify.json.
// XrPlural tells stage 2 which Crossplane composite resource to poll for Ready status.
type NotifyMeta struct {
	ResourceName       string `json:"resourceName"`
	ResourceType       string `json:"resourceType"`
	RequesterNamespace string `json:"requesterNamespace"`
	XrPlural           string `json:"xrPlural"` // e.g. "xnamespaces", "xkeyvaults"
}

// Stage 2 runs as a PostSync Job on the workload cluster (written to /kratix/output by stage 1).
// It polls XNamespace Ready and posts "resource live" to Slack via a secret on the workload cluster.
const stage2JobTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: notify-stage2-{{.ResourceName}}
  namespace: default
  annotations:
    argocd.argoproj.io/hook: PostSync
    argocd.argoproj.io/hook-delete-policy: HookSucceeded
    argocd.argoproj.io/sync-wave: "2"
spec:
  ttlSecondsAfterFinished: 300
  backoffLimit: 3
  template:
    spec:
      serviceAccountName: notify-watcher
      imagePullSecrets:
      - name: acr-credentials
      containers:
      - name: notify
        image: teknologieur1acr.azurecr.io/kratix-notify:latest
        env:
        - name: NOTIFY_STAGE
          value: "2"
        - name: RESOURCE_NAME
          value: "{{.ResourceName}}"
        - name: RESOURCE_TYPE
          value: "{{.ResourceType}}"
        - name: REQUESTER_NAMESPACE
          value: "{{.RequesterNamespace}}"
        - name: XR_PLURAL
          value: "{{.XrPlural}}"
        - name: SLACK_WEBHOOK_URL
          valueFrom:
            secretKeyRef:
              name: slack-webhook
              key: url
              optional: true
      restartPolicy: OnFailure
`

func main() {
	stage := getEnv("NOTIFY_STAGE", "1")

	// Stage 2 reads resource info from env (set on the Job by stage 1)
	// Stage 1 reads from /kratix/metadata/notify.json (written by the configure container)
	var meta NotifyMeta
	if stage == "2" {
		meta = NotifyMeta{
			ResourceName:       requireEnv("RESOURCE_NAME"),
			ResourceType:       getEnv("RESOURCE_TYPE", "Resource"),
			RequesterNamespace: getEnv("REQUESTER_NAMESPACE", "default"),
			XrPlural:           getEnv("XR_PLURAL", "xnamespaces"),
		}
	} else {
		var err error
		meta, err = readNotifyMeta()
		if err != nil {
			log.Fatalf("Failed to read notify metadata: %v", err)
		}
	}

	slackURL := os.Getenv("SLACK_WEBHOOK_URL")
	argocdURL := getEnv("ARGOCD_URL", "")

	log.Printf("notify: stage=%s resource=%s/%s requester=%s",
		stage, meta.ResourceType, meta.ResourceName, meta.RequesterNamespace)

	switch stage {
	case "1":
		runStage1(meta, slackURL, argocdURL)
	case "2":
		runStage2(meta, slackURL)
	default:
		log.Fatalf("Unknown NOTIFY_STAGE=%q (expected 1 or 2)", stage)
	}
}

// runStage1 posts "request accepted" and writes the stage 2 watcher Job to /kratix/output.
func runStage1(meta NotifyMeta, slackURL, argocdURL string) {
	if slackURL == "" {
		log.Println("SLACK_WEBHOOK_URL not set — skipping stage 1 notification")
	} else {
		msg := buildAcceptedMessage(meta.ResourceName, meta.ResourceType, meta.RequesterNamespace, argocdURL)
		if err := sendSlack(slackURL, msg); err != nil {
			log.Printf("WARNING: stage 1 Slack notification failed: %v", err)
		} else {
			log.Println("Stage 1 notification sent")
		}
	}

	// Always write stage 2 Job — the Job reads SLACK_WEBHOOK_URL from a Secret on the workload cluster.
	if err := writeStage2Job(meta); err != nil {
		log.Printf("WARNING: failed to write stage 2 job: %v", err)
	} else {
		log.Println("Stage 2 watcher job written to /kratix/output")
	}
}

// runStage2 polls the Crossplane XR Ready status and posts "resource live" to Slack.
// Runs as a Kubernetes Job on the workload cluster.
func runStage2(meta NotifyMeta, slackURL string) {
	start := time.Now()
	log.Printf("Polling %s/%s for Ready condition (timeout: %s)...", meta.XrPlural, meta.ResourceName, maxWait)

	for {
		ready, err := isXRReady(meta.XrPlural, meta.ResourceName)
		if err != nil {
			log.Printf("Poll error: %v — retrying in %s", err, pollInterval)
		} else if ready {
			elapsed := time.Since(start).Round(time.Second)
			log.Printf("%s/%s is Ready (elapsed: %s)", meta.ResourceType, meta.ResourceName, elapsed)

			// Record first, alert second: the Event is the durable, queryable trace
			// of "this fired" independent of whether Slack is reachable or configured
			// at all — best-effort, since a notification-history gap shouldn't fail
			// the pipeline over something that already succeeded (the XR is Ready).
			if err := recordNotificationEvent(meta, elapsed); err != nil {
				log.Printf("WARNING: failed to record notification Event: %v", err)
			}

			if slackURL == "" {
				log.Println("SLACK_WEBHOOK_URL not set — skipping stage 2 notification")
				return
			}
			msg := buildLiveMessage(meta.ResourceName, meta.ResourceType, meta.RequesterNamespace, elapsed.String())
			if err := sendSlack(slackURL, msg); err != nil {
				log.Printf("WARNING: stage 2 Slack notification failed: %v", err)
			} else {
				log.Println("Stage 2 notification sent")
			}
			return
		}

		if time.Since(start) > maxWait {
			log.Printf("Timed out waiting for %s/%s to become Ready after %s", meta.ResourceType, meta.ResourceName, maxWait)
			return
		}
		log.Printf("%s/%s not ready yet — checking again in %s", meta.ResourceType, meta.ResourceName, pollInterval)
		time.Sleep(pollInterval)
	}
}

// --- Metadata ---

func readNotifyMeta() (NotifyMeta, error) {
	data, err := os.ReadFile(notifyMeta)
	if err != nil {
		return NotifyMeta{}, fmt.Errorf("read %s: %w", notifyMeta, err)
	}
	var m NotifyMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return NotifyMeta{}, fmt.Errorf("parse notify metadata: %w", err)
	}
	if m.ResourceName == "" {
		return NotifyMeta{}, fmt.Errorf("notify metadata missing resourceName")
	}
	if m.ResourceType == "" {
		m.ResourceType = "Resource"
	}
	if m.RequesterNamespace == "" {
		m.RequesterNamespace = "default"
	}
	if m.XrPlural == "" {
		m.XrPlural = "xnamespaces" // safe default
	}
	return m, nil
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

func buildAcceptedMessage(name, resourceType, requesterNS, argocdURL string) slackPayload {
	fields := []slackText{
		{Type: "mrkdwn", Text: fmt.Sprintf("*Resource*\n%s", name)},
		{Type: "mrkdwn", Text: fmt.Sprintf("*Requested by*\n%s", requesterNS)},
		{Type: "mrkdwn", Text: fmt.Sprintf("*Time*\n%s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))},
	}
	if argocdURL != "" {
		fields = append(fields, slackText{
			Type: "mrkdwn",
			Text: fmt.Sprintf("*ArgoCD*\n<%s|View sync>", argocdURL),
		})
	}
	return slackPayload{Blocks: []slackBlock{
		{Type: "section", Text: &slackText{
			Type: "mrkdwn",
			Text: fmt.Sprintf("✅ *%s request accepted*", resourceType),
		}},
		{Type: "section", Fields: fields},
	}}
}

func buildLiveMessage(name, resourceType, requesterNS, elapsed string) slackPayload {
	return slackPayload{Blocks: []slackBlock{
		{Type: "section", Text: &slackText{
			Type: "mrkdwn",
			Text: fmt.Sprintf("🚀 *%s is live*", resourceType),
		}},
		{Type: "section", Fields: []slackText{
			{Type: "mrkdwn", Text: fmt.Sprintf("*Resource*\n%s", name)},
			{Type: "mrkdwn", Text: fmt.Sprintf("*Requested by*\n%s", requesterNS)},
			{Type: "mrkdwn", Text: fmt.Sprintf("*Provisioned in*\n%s", elapsed)},
			{Type: "mrkdwn", Text: fmt.Sprintf("*Time*\n%s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))},
		}},
	}}
}

func sendSlack(webhookURL string, payload slackPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("HTTP POST: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Slack returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- Stage 2 Job output ---

func writeStage2Job(meta NotifyMeta) error {
	if err := os.MkdirAll("/kratix/output", 0755); err != nil {
		return fmt.Errorf("mkdir /kratix/output: %w", err)
	}
	tmpl, err := template.New("job").Parse(stage2JobTemplate)
	if err != nil {
		return fmt.Errorf("parse job template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, meta); err != nil {
		return fmt.Errorf("render job template: %w", err)
	}
	path := fmt.Sprintf("/kratix/output/notify-stage2-%s.yaml", meta.ResourceName)
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// --- Durable notification record (stage 2, runs on workload cluster) ---

// recordNotificationEvent creates a core v1 Event on the workload cluster recording
// that stage 2's "resource live" notification fired — a durable, queryable
// (`kubectl get events`) trace independent of Slack, which can be down, rate-limited,
// or simply not yet configured (SLACK_WEBHOOK_URL is optional) without losing the
// record that the resource became ready and notification was attempted.
func recordNotificationEvent(meta NotifyMeta, elapsed time.Duration) error {
	client, token, err := buildK8sClient()
	if err != nil {
		return fmt.Errorf("k8s client: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	event := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Event",
		"metadata": map[string]interface{}{
			"generateName": "notify-resource-live-",
			"namespace":    "default",
		},
		"involvedObject": map[string]interface{}{
			"apiVersion": "platform.teknologi.io/v1alpha1",
			"kind":       meta.ResourceType,
			"name":       meta.ResourceName,
			"namespace":  "default",
		},
		"reason":         "ResourceLive",
		"message":        fmt.Sprintf("%s/%s became Ready after %s (requested by %s)", meta.ResourceType, meta.ResourceName, elapsed, meta.RequesterNamespace),
		"type":           "Normal",
		"firstTimestamp": now,
		"lastTimestamp":  now,
		"count":          1,
		"source":         map[string]string{"component": "kratix-notify"},
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, k8sAPIBase+"/api/v1/namespaces/default/events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("k8s API returned %d creating event", resp.StatusCode)
	}
	log.Println("Notification Event recorded")
	return nil
}

// --- Kubernetes polling (stage 2, runs on workload cluster) ---

func buildK8sClient() (*http.Client, string, error) {
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, "", fmt.Errorf("read SA token: %w", err)
	}
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, "", fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, "", fmt.Errorf("failed to parse CA cert")
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	return client, strings.TrimSpace(string(tokenBytes)), nil
}

// isXRReady polls any cluster-scoped Crossplane composite resource for Ready=True.
// plural is the lowercase plural name (e.g. "xnamespaces", "xkeyvaults").
func isXRReady(plural, resourceName string) (bool, error) {
	client, token, err := buildK8sClient()
	if err != nil {
		return false, fmt.Errorf("k8s client: %w", err)
	}

	// Crossplane Composite Resources are always cluster-scoped — no /namespaces/ segment
	url := fmt.Sprintf("%s/apis/platform.teknologi.io/v1alpha1/%s/%s",
		k8sAPIBase, plural, resourceName)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("GET xnamespace: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil // Not yet created — keep polling
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("k8s API returned %d", resp.StatusCode)
	}

	var obj struct {
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return false, fmt.Errorf("decode xnamespace: %w", err)
	}
	for _, c := range obj.Status.Conditions {
		if c.Type == "Ready" && c.Status == "True" {
			return true, nil
		}
	}
	return false, nil
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
		log.Fatalf("Required env var %s is not set", key)
	}
	return v
}