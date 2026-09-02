package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	k8sAPIBase = "https://kubernetes.default.svc:443"
	tokenPath  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caPath     = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	notifyMeta = "/kratix/metadata/notify.json"
)

// NotifyMeta is written by the configure container to /kratix/metadata/notify.json.
type NotifyMeta struct {
	ResourceName       string `json:"resourceName"`
	ResourceType       string `json:"resourceType"`
	RequesterNamespace string `json:"requesterNamespace"`
}

func main() {
	stage := getEnv("NOTIFY_STAGE", "1")

	// watch runs as a persistent Deployment on the workload cluster — see runWatch.
	if stage == "watch" {
		runWatch(os.Getenv("SLACK_WEBHOOK_URL"))
		return
	}

	meta, err := readNotifyMeta()
	if err != nil {
		log.Fatalf("Failed to read notify metadata: %v", err)
	}

	slackURL := os.Getenv("SLACK_WEBHOOK_URL")
	argocdURL := getEnv("ARGOCD_URL", "")

	log.Printf("notify: stage=%s resource=%s/%s requester=%s",
		stage, meta.ResourceType, meta.ResourceName, meta.RequesterNamespace)

	switch stage {
	case "1":
		runStage1(meta, slackURL, argocdURL)
	default:
		log.Fatalf("Unknown NOTIFY_STAGE=%q (expected 1 or watch)", stage)
	}
}

// runStage1 posts "request accepted" to Slack. Resource-live notification is handled
// separately by the persistent watcher (see runWatch) — not by anything this pipeline writes.
func runStage1(meta NotifyMeta, slackURL, argocdURL string) {
	if slackURL == "" {
		log.Println("SLACK_WEBHOOK_URL not set — skipping stage 1 notification")
		return
	}
	msg := buildAcceptedMessage(meta.ResourceName, meta.ResourceType, meta.RequesterNamespace, argocdURL)
	if err := sendSlack(slackURL, msg); err != nil {
		log.Printf("WARNING: stage 1 Slack notification failed: %v", err)
	} else {
		log.Println("Stage 1 notification sent")
	}
}

// --- Watch (persistent controller, runs as a Deployment on the workload cluster) ---
//
// Replaces the old per-resource PostSync-hook Job: that Job was written fresh to
// /kratix/output on every pipeline run, but PostSync hooks aren't part of ArgoCD's
// sync-status/drift comparison, so a pipeline rerun with byte-identical output
// produced no new git commit and the hook silently never re-ran. This watches XR
// Ready conditions directly via the Kubernetes API instead, independent of git and
// ArgoCD's sync lifecycle entirely.

// notifiedAnnotation marks an XR as already notified, so a restart's initial list
// (which replays every currently-Ready object through the same handler) doesn't
// re-send Slack messages for resources that were already live before the watcher
// last started.
const notifiedAnnotation = "notify.platform.teknologi.io/notified"

var watchedResources = []schema.GroupVersionResource{
	{Group: "platform.teknologi.io", Version: "v1alpha1", Resource: "xkeyvaults"},
	{Group: "platform.teknologi.io", Version: "v1alpha1", Resource: "xnamespaces"},
	{Group: "platform.teknologi.io", Version: "v1alpha1", Resource: "xstorageaccounts"},
	{Group: "platform.teknologi.io", Version: "v1alpha1", Resource: "xresourcegroups"},
}

func runWatch(slackURL string) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("in-cluster config: %v", err)
	}
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("dynamic client: %v", err)
	}

	factory := dynamicinformer.NewDynamicSharedInformerFactory(dynClient, 10*time.Hour)
	for _, gvr := range watchedResources {
		gvr := gvr
		informer := factory.ForResource(gvr).Informer()
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { handleXR(dynClient, gvr, slackURL, obj) },
			UpdateFunc: func(_, obj interface{}) { handleXR(dynClient, gvr, slackURL, obj) },
		})
	}

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)
	log.Println("notify-watcher: cache synced, watching for Ready transitions")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	log.Println("notify-watcher: shutting down")
	close(stopCh)
}

func handleXR(dynClient dynamic.Interface, gvr schema.GroupVersionResource, slackURL string, obj interface{}) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	if u.GetAnnotations()[notifiedAnnotation] == "true" {
		return
	}
	if !isReady(u) {
		return
	}

	name := u.GetName()
	resourceType := strings.TrimPrefix(u.GetKind(), "X")
	elapsed := time.Since(u.GetCreationTimestamp().Time).Round(time.Second)

	log.Printf("notify-watcher: %s/%s is Ready (elapsed: %s) — notifying", resourceType, name, elapsed)

	// Record first, alert second: the Event is the durable, queryable trace of "this
	// fired" independent of whether Slack is reachable or configured at all — best-effort,
	// since a notification-history gap shouldn't block marking the resource as notified.
	if err := recordNotificationEvent(resourceType, name, elapsed); err != nil {
		log.Printf("WARNING: failed to record notification Event for %s/%s: %v", resourceType, name, err)
	}

	if slackURL == "" {
		log.Println("SLACK_WEBHOOK_URL not set — skipping notification")
	} else {
		msg := buildLiveMessage(name, resourceType, elapsed.String())
		if err := sendSlack(slackURL, msg); err != nil {
			log.Printf("WARNING: Slack notification failed for %s/%s: %v", resourceType, name, err)
		} else {
			log.Printf("notify-watcher: Slack notification sent for %s/%s", resourceType, name)
		}
	}

	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:"true"}}}`, notifiedAnnotation))
	if _, err := dynClient.Resource(gvr).Patch(context.Background(), name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		log.Printf("WARNING: failed to mark %s/%s as notified (may re-notify on restart): %v", resourceType, name, err)
	}
}

func isReady(u *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == "Ready" && cond["status"] == "True" {
			return true
		}
	}
	return false
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

// buildLiveMessage no longer includes "Requested by" — the watcher observes the XR
// directly on the workload cluster and has no visibility into which platform-cluster
// namespace originated the request (that would require a cross-cluster API call,
// which this design deliberately avoids; see Architecture.md's GitOps-only boundary
// between the platform and workload clusters).
func buildLiveMessage(name, resourceType, elapsed string) slackPayload {
	return slackPayload{Blocks: []slackBlock{
		{Type: "section", Text: &slackText{
			Type: "mrkdwn",
			Text: fmt.Sprintf("🚀 *%s is live*", resourceType),
		}},
		{Type: "section", Fields: []slackText{
			{Type: "mrkdwn", Text: fmt.Sprintf("*Resource*\n%s", name)},
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

// --- Durable notification record (runs on the workload cluster) ---

// recordNotificationEvent creates a core v1 Event on the workload cluster recording
// that a "resource live" notification fired — a durable, queryable (`kubectl get
// events`) trace independent of Slack, which can be down, rate-limited, or simply
// not yet configured (SLACK_WEBHOOK_URL is optional).
func recordNotificationEvent(resourceType, resourceName string, elapsed time.Duration) error {
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
			"namespace":    "kratix-workloads",
		},
		"involvedObject": map[string]interface{}{
			"apiVersion": "platform.teknologi.io/v1alpha1",
			"kind":       resourceType,
			"name":       resourceName,
			"namespace":  "kratix-workloads",
		},
		"reason":         "ResourceLive",
		"message":        fmt.Sprintf("%s/%s became Ready after %s", resourceType, resourceName, elapsed),
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

	req, err := http.NewRequest(http.MethodPost, k8sAPIBase+"/api/v1/namespaces/kratix-workloads/events", bytes.NewReader(body))
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

// --- Helpers ---

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
