package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"strings"

	"gopkg.in/yaml.v2"
)

const (
	orchestrationOwner = "AshwinSarimin"
	orchestrationRepo  = "TEKNOLOGI-PLATFORM-ORCHESTRATION"
	baseBranch         = "main"
)

// runConfigure opens a single PR in the ORCHESTRATION repo containing the team's SSoT
// definition, and records phase=waiting-for-pr-merge. It does NOT provision any
// resources — that only happens once pr-watcher confirms the PR was merged.
func runConfigure() {
	log.Println("TeamOnboarding configure stage starting...")

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

	// Kratix re-runs this configure workflow for every existing resource whenever
	// the Promise definition changes (see Experiments.md) — without this check,
	// every promise.yaml update would re-open an approval PR for teams that are
	// already onboarded and provisioned. If the team file already exists on main
	// with exactly the content we'd render, there's nothing to review: skip the
	// PR and let pr-watcher proceed straight to provisioning.
	if existing, exists, err := gh.getFileContent(orchestrationOwner, orchestrationRepo, teamFilePath, baseBranch); err != nil {
		log.Fatalf("check existing team file: %v", err)
	} else if exists && existing == teamYAML {
		log.Printf("Team file %s already on %s with matching content — skipping PR, team is already onboarded", teamFilePath, baseBranch)
		writeConfigureStatus(req, "")
		writeNotify(req)
		log.Println("TeamOnboarding configure stage completed — already onboarded, no PR needed")
		return
	}

	branch := fmt.Sprintf("team/%s", req.Spec.AppName)
	mainSHA, err := gh.getDefaultBranchSHA(orchestrationOwner, orchestrationRepo, baseBranch)
	if err != nil {
		log.Fatalf("get default branch sha: %v", err)
	}
	if err := gh.createBranch(orchestrationOwner, orchestrationRepo, branch, mainSHA); err != nil {
		log.Fatalf("create branch: %v", err)
	}

	if err := gh.putFile(orchestrationOwner, orchestrationRepo, branch, teamFilePath,
		fmt.Sprintf("Add team: %s", req.Spec.AppName), teamYAML); err != nil {
		log.Fatalf("commit team definition: %v", err)
	}

	pr, err := gh.createPullRequest(orchestrationOwner, orchestrationRepo, branch, baseBranch,
		fmt.Sprintf("Add team: %s", req.Spec.AppName), renderPRBody(req))
	if err != nil {
		log.Fatalf("create pull request: %v", err)
	}
	log.Printf("Opened PR %s", pr.HTMLURL)

	writeConfigureStatus(req, pr.HTMLURL)
	writeNotify(req)

	log.Println("TeamOnboarding configure stage completed — awaiting PR approval")
}

func renderTeamEntity(req Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: backstage.io/v1alpha1\n")
	fmt.Fprintf(&b, "kind: Group\n")
	fmt.Fprintf(&b, "metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", req.Spec.AppName)
	fmt.Fprintf(&b, "  namespace: default\n")
	fmt.Fprintf(&b, "  description: %q\n", req.Spec.Description)
	fmt.Fprintf(&b, "  annotations:\n")
	fmt.Fprintf(&b, "    backstage.io/kubernetes-id: %s\n", req.Spec.AppName)
	fmt.Fprintf(&b, "spec:\n")
	fmt.Fprintf(&b, "  type: team\n")
	fmt.Fprintf(&b, "  profile:\n")
	fmt.Fprintf(&b, "    displayName: %q\n", req.Spec.DisplayName)
	fmt.Fprintf(&b, "  parent: %s\n", req.Spec.BusinessUnit)
	fmt.Fprintf(&b, "  children: []\n")
	return b.String()
}

func renderPRBody(req Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Registers **%s** (`%s`) in the organisation SSoT.\n\n", req.Spec.DisplayName, req.Spec.AppName)
	fmt.Fprintf(&b, "Opened automatically by the `team-onboarding` Kratix Promise.\n\n")
	fmt.Fprintf(&b, "### Requested resources (environment: `%s`)\n\n", req.Spec.Environment)
	fmt.Fprintf(&b, "| Resource | Requested |\n|---|---|\n")
	fmt.Fprintf(&b, "| Kubernetes Namespace | %s |\n", checkmark(req.Spec.Resources.Namespace))
	fmt.Fprintf(&b, "| Azure KeyVault | %s |\n", checkmark(req.Spec.Resources.KeyVault))
	fmt.Fprintf(&b, "| Azure Storage Account | %s |\n", checkmark(req.Spec.Resources.StorageAccount))
	fmt.Fprintf(&b, "\n**Merging this PR approves resource provisioning.** A `pr-watcher` pipeline stage is polling for the merge — once merged, the resources above will be provisioned automatically.\n")
	return b.String()
}

func checkmark(v bool) string {
	if v {
		return "✅"
	}
	return "—"
}

func writeConfigureStatus(req Request, prURL string) {
	s := map[string]interface{}{
		"phase":       "waiting-for-pr-merge",
		"prUrl":       prURL,
		"appName":     req.Spec.AppName,
		"environment": req.Spec.Environment,
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := ioutil.WriteFile("/kratix/metadata/status.json", data, 0644); err != nil {
		log.Printf("WARNING: write status: %v", err)
	}
}

func writeNotify(req Request) {
	notify := map[string]string{
		// Must match the XNamespace XR name ({appName}-{env}) — the stage-2 notify job
		// polls that XR for readiness.
		"resourceName":       fmt.Sprintf("%s-%s", req.Spec.AppName, req.Spec.Environment),
		"resourceType":       "TeamOnboarding",
		"requesterNamespace": req.Metadata.Namespace,
	}
	data, _ := json.MarshalIndent(notify, "", "  ")
	if err := ioutil.WriteFile("/kratix/metadata/notify.json", data, 0644); err != nil {
		log.Printf("WARNING: write notify: %v", err)
	}
}
