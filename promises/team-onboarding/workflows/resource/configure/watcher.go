package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

const (
	// pollInterval is deliberately coarser than the old in-process 30s sleep loop:
	// each retryAfter cycle now spins up a brand-new Job/pod (see workflow-control.yaml
	// below), not just a sleep inside an already-running container, so the cost model
	// changed from "cheap in-process wait" to "a Kubernetes Job per check".
	pollInterval       = 2 * time.Minute
	shortRetryInterval = 30 * time.Second
	maxWait            = 2 * time.Hour
)

// runWatcher is a one-shot check, run again by Kratix itself via workflow-control.yaml's
// retryAfter until the team's approval PR is merged (or the wait times out) — replacing
// the old design where a single container held a pod open for up to 2h in a sleep loop.
// pr-watcher is its own Pipeline entry in promise.yaml (not just another container
// alongside configure-team-onboarding), so each retry is a genuinely fresh Job: Kratix's
// own "reader" init container re-fetches the live resource from the k8s API every time,
// and /kratix/metadata starts empty each run — nothing carries over between attempts.
// That's why this function re-derives everything it needs (team file state, PR state)
// instead of reading anything the configure stage might have written for it.
func runWatcher() {
	log.Println("TeamOnboarding pr-watcher stage starting...")

	raw, err := ioutil.ReadFile("/kratix/input/object.yaml")
	if err != nil {
		log.Fatalf("read request: %v", err)
	}
	var req Request
	if err := yaml.Unmarshal(raw, &req); err != nil {
		log.Fatalf("parse request: %v", err)
	}
	applyDefaults(&req)

	gh, err := newGitHubClient()
	if err != nil {
		log.Fatalf("github client: %v", err)
	}

	teamFilePath := fmt.Sprintf("model/organization/%s/teams/%s.yaml", req.Spec.BusinessUnit, req.Spec.AppName)
	teamYAML := renderTeamEntity(req)

	// Single existence check covers both "configure found this team already onboarded
	// and never opened a PR" and "the PR was merged since the last check" — no need to
	// remember which case we're in, since both mean the same thing: provision now.
	existing, exists, err := gh.getFileContent(orchestrationOwner, orchestrationRepo, teamFilePath, baseBranch)
	if err != nil {
		writeWorkflowControl(workflowControl{
			RetryAfter: shortRetryInterval.String(),
			Message:    fmt.Sprintf("error checking team file %s: %v", teamFilePath, err),
		})
		return
	}
	if exists && existing == teamYAML {
		log.Printf("Team file %s is on %s with matching content — provisioning resources", teamFilePath, baseBranch)
		if err := writeXROutputs(req); err != nil {
			log.Fatalf("write XR outputs: %v", err)
		}
		// No approver identity available on this path — either the team was already
		// onboarded before this run (no PR to attribute), or the merge that put the
		// file here happened before a prior check already recorded it.
		writeWatcherStatus(req, "", "")
		return
	}

	branch := fmt.Sprintf("team/%s", req.Spec.AppName)
	prSummary, err := gh.findPR(orchestrationOwner, orchestrationRepo, branch, baseBranch)
	if err != nil {
		writeWorkflowControl(workflowControl{
			RetryAfter: shortRetryInterval.String(),
			Message:    fmt.Sprintf("waiting for approval PR on branch %s to be findable", branch),
		})
		return
	}
	// findPR uses GitHub's list-PRs endpoint, which never populates merged/merged_at/
	// merged_by (only the single-PR endpoint does) — fetch the full PR so the
	// closed-without-merge check below and the approval record we write on success
	// are both based on real data, not the list endpoint's always-false zero value.
	pr, err := gh.getPullRequest(orchestrationOwner, orchestrationRepo, prSummary.Number)
	if err != nil {
		writeWorkflowControl(workflowControl{
			RetryAfter: shortRetryInterval.String(),
			Message:    fmt.Sprintf("error re-fetching PR #%d: %v", prSummary.Number, err),
		})
		return
	}
	if pr.State == "closed" && !pr.Merged {
		log.Fatalf("PR %s was closed without being merged — delete this TeamOnboardingRequest and resubmit to restart the flow", pr.HTMLURL)
	}
	if pr.Merged {
		// Belt-and-braces: in practice the getFileContent check above already catches
		// a just-merged PR, since GitHub creates the merge commit on main atomically
		// as part of the merge itself. Handled explicitly anyway rather than relying
		// on that timing — and it's the one place we actually have merge attribution
		// (who merged it, when) to record as the durable approval record.
		approvedBy := ""
		if pr.MergedBy != nil {
			approvedBy = pr.MergedBy.Login
		}
		log.Printf("PR %s merged by %s — provisioning resources", pr.HTMLURL, approvedBy)
		if err := writeXROutputs(req); err != nil {
			log.Fatalf("write XR outputs: %v", err)
		}
		writeWatcherStatus(req, approvedBy, pr.MergedAt)
		return
	}

	createdAt, err := time.Parse(time.RFC3339, req.Metadata.CreationTimestamp)
	if err != nil {
		log.Fatalf("parse resource creationTimestamp %q: %v", req.Metadata.CreationTimestamp, err)
	}
	elapsed := time.Since(createdAt)
	if elapsed > maxWait {
		log.Fatalf("Timed out after %s waiting for PR %s to be merged", maxWait, pr.HTMLURL)
	}

	log.Printf("PR %s not merged yet (waiting %s) — asking Kratix to check again in %s", pr.HTMLURL, elapsed.Round(time.Second), pollInterval)
	writeWorkflowControl(workflowControl{
		RetryAfter: pollInterval.String(),
		Message:    fmt.Sprintf("waiting for PR %s to be merged", pr.HTMLURL),
	})
}

// workflowControl is Kratix's native pipeline-suspend/retry contract: a pipeline
// container writes this to /kratix/metadata/workflow-control.yaml to ask Kratix to
// either retry it automatically after a delay (RetryAfter) or suspend it until an
// external actor clears the kratix.io/workflow-suspended label (Suspend) — confirmed
// live against this project's installed Kratix build, see Experiments.md 2026-07-30.
type workflowControl struct {
	Suspend    bool   `yaml:"suspend,omitempty"`
	RetryAfter string `yaml:"retryAfter,omitempty"`
	Message    string `yaml:"message,omitempty"`
}

func writeWorkflowControl(wc workflowControl) {
	data, err := yaml.Marshal(wc)
	if err != nil {
		log.Fatalf("marshal workflow-control.yaml: %v", err)
	}
	if err := ioutil.WriteFile("/kratix/metadata/workflow-control.yaml", data, 0644); err != nil {
		log.Fatalf("write workflow-control.yaml: %v", err)
	}
}

// writeXROutputs writes Crossplane XR manifests to /kratix/output. The Kratix work-writer
// pushes these to the ORCHESTRATION repo, and ArgoCD deploys them to the workload cluster
// where Crossplane provisions the actual Azure resources — no in-cluster k8s API calls needed.
func writeXROutputs(req Request) error {
	if err := os.MkdirAll("/kratix/output", 0755); err != nil {
		return fmt.Errorf("mkdir /kratix/output: %w", err)
	}

	rgName := resourceGroupName(req.Spec.AppName, req.Spec.Environment)
	log.Printf("Derived resource group name: %s", rgName)

	// XResourceGroup — sync-wave 1 so it's created before the resources that reference it.
	if err := writeYAML("/kratix/output/xresourcegroup.yaml", buildXResourceGroup(req, rgName)); err != nil {
		return err
	}
	log.Printf("Wrote XResourceGroup %s", rgName)

	// XNamespace — Kubernetes namespace on the workload cluster (no Azure RG dependency).
	if req.Spec.Resources.Namespace {
		if err := writeYAML("/kratix/output/xnamespace.yaml", buildXNamespace(req)); err != nil {
			return err
		}
		log.Printf("Wrote XNamespace %s-%s", req.Spec.AppName, req.Spec.Environment)
	}

	// XKeyVault — Azure KeyVault in the team's resource group.
	if req.Spec.Resources.KeyVault {
		tenantID := requireEnv("AZURE_TENANT_ID")
		if err := writeYAML("/kratix/output/xkeyvault.yaml", buildXKeyVault(req, rgName, tenantID)); err != nil {
			return err
		}
		log.Printf("Wrote XKeyVault kv-%s-%s", req.Spec.AppName, req.Spec.Environment)
	}

	// XStorageAccount — Azure Storage Account in the team's resource group.
	if req.Spec.Resources.StorageAccount {
		if err := writeYAML("/kratix/output/xstorageaccount.yaml", buildXStorageAccount(req, rgName)); err != nil {
			return err
		}
		saName := computeStorageAccountName(req.Spec.AppName, req.Spec.Environment)
		log.Printf("Wrote XStorageAccount sa-%s-%s (Azure name: %s)", req.Spec.AppName, req.Spec.Environment, saName)
	}

	return nil
}

// resourceGroupName computes the per-team Azure resource group name.
// Pattern: teknologi-eur1-{env}-{appName}-rg (e.g. teknologi-eur1-tst-app-b-rg)
func resourceGroupName(appName, env string) string {
	return fmt.Sprintf("teknologi-eur1-%s-%s-rg", env, appName)
}

// computeStorageAccountName mirrors the logic in the standalone storage-account promise.
// Azure rules: 3-24 chars, lowercase alphanumeric only.
func computeStorageAccountName(appName, env string) string {
	re := regexp.MustCompile(`[^a-z0-9]`)
	clean := func(s string) string { return re.ReplaceAllString(strings.ToLower(s), "") }
	name := "tek" + clean(appName) + clean(env) + "sa"
	if len(name) > 24 {
		name = name[:24]
	}
	return name
}

func buildXResourceGroup(req Request, rgName string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "platform.teknologi.io/v1alpha1",
		"kind":       "XResourceGroup",
		"metadata": map[string]interface{}{
			"name": rgName,
			"annotations": map[string]string{
				"argocd.argoproj.io/sync-wave": "1",
			},
		},
		"spec": map[string]interface{}{
			"location": req.Spec.Location,
		},
	}
}

func buildXNamespace(req Request) map[string]interface{} {
	nsName := fmt.Sprintf("%s-%s", req.Spec.AppName, req.Spec.Environment)
	return map[string]interface{}{
		"apiVersion": "platform.teknologi.io/v1alpha1",
		"kind":       "XNamespace",
		"metadata": map[string]interface{}{
			"name": nsName,
			"annotations": map[string]string{
				"argocd.argoproj.io/sync-wave": "2",
			},
		},
		"spec": map[string]interface{}{
			"namespaceName":     nsName,
			"environment":       req.Spec.Environment,
			"networkVisibility": "private",
		},
	}
}

func buildXKeyVault(req Request, rgName, tenantID string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "platform.teknologi.io/v1alpha1",
		"kind":       "XKeyVault",
		"metadata": map[string]interface{}{
			"name": fmt.Sprintf("kv-%s-%s", req.Spec.AppName, req.Spec.Environment),
			"annotations": map[string]string{
				"argocd.argoproj.io/sync-wave": "2",
			},
		},
		"spec": map[string]interface{}{
			"appName":           req.Spec.AppName,
			"tenantId":          tenantID,
			"environment":       req.Spec.Environment,
			"location":          req.Spec.Location,
			"resourceGroupName": rgName,
		},
	}
}

func buildXStorageAccount(req Request, rgName string) map[string]interface{} {
	saName := computeStorageAccountName(req.Spec.AppName, req.Spec.Environment)
	return map[string]interface{}{
		"apiVersion": "platform.teknologi.io/v1alpha1",
		"kind":       "XStorageAccount",
		"metadata": map[string]interface{}{
			"name": fmt.Sprintf("sa-%s-%s", req.Spec.AppName, req.Spec.Environment),
			"annotations": map[string]string{
				"argocd.argoproj.io/sync-wave": "2",
			},
		},
		"spec": map[string]interface{}{
			"appName":            req.Spec.AppName,
			"environment":        req.Spec.Environment,
			"storageAccountName": saName,
			"location":           req.Spec.Location,
			"resourceGroupName":  rgName,
		},
	}
}

func writeYAML(path string, v interface{}) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return ioutil.WriteFile(path, data, 0644)
}

// writeWatcherStatus records the durable approval record — who merged the approval
// PR and when — alongside the existing phase/appName/environment fields. approvedBy
// and approvedAt are "" when this run reached provisioning via the team-file
// existence check rather than a freshly observed PR merge (see call sites): there's
// no PR to attribute in that case, not a lost value.
func writeWatcherStatus(req Request, approvedBy, approvedAt string) {
	s := map[string]interface{}{
		"phase":       "provisioning",
		"appName":     req.Spec.AppName,
		"environment": req.Spec.Environment,
		"approvedBy":  approvedBy,
		"approvedAt":  approvedAt,
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := ioutil.WriteFile("/kratix/metadata/status.json", data, 0644); err != nil {
		log.Printf("WARNING: write status: %v", err)
	}
}
