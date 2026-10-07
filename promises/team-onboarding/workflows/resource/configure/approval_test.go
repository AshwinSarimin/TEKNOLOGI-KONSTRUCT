package main

import "testing"

func approvalRequest() Request {
	var req Request
	req.Metadata.UID = "11111111-1111-1111-1111-111111111111"
	req.Spec.AppName = "approval-test"
	req.Spec.BusinessUnit = "product"
	req.Spec.Description = "A test team"
	req.Spec.DisplayName = "Approval Test"
	req.Spec.Environment = "dev"
	req.Spec.Location = "westeurope"
	req.Spec.Resources = Resources{Namespace: true, KeyVault: false, StorageAccount: true}
	return req
}

func TestApprovalBindsEveryProvisioningField(t *testing.T) {
	approved := approvalRequest()
	file := renderTeamEntity(approved)
	if _, ok := approvedFromMain(approved, file); !ok {
		t.Fatal("first-time approved specification should be accepted")
	}
	changes := map[string]func(*Request){
		"appName":                func(r *Request) { r.Spec.AppName = "other" },
		"businessUnit":           func(r *Request) { r.Spec.BusinessUnit = "commerce" },
		"description":            func(r *Request) { r.Spec.Description = "changed" },
		"displayName":            func(r *Request) { r.Spec.DisplayName = "Changed" },
		"environment":            func(r *Request) { r.Spec.Environment = "tst" },
		"location":               func(r *Request) { r.Spec.Location = "northeurope" },
		"disable namespace":      func(r *Request) { r.Spec.Resources.Namespace = false },
		"enable keyvault":        func(r *Request) { r.Spec.Resources.KeyVault = true },
		"disable storageAccount": func(r *Request) { r.Spec.Resources.StorageAccount = false },
		"request UID":            func(r *Request) { r.Metadata.UID = "22222222-2222-2222-2222-222222222222" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			mutated := approved
			change(&mutated)
			if _, ok := approvedFromMain(mutated, file); ok {
				t.Fatal("previously approved file authorized a changed request")
			}
		})
	}
	for name, change := range map[string]func(*Request){
		"enable namespace":      func(r *Request) { r.Spec.Resources.Namespace = false },
		"disable keyvault":      func(r *Request) { r.Spec.Resources.KeyVault = true },
		"enable storageAccount": func(r *Request) { r.Spec.Resources.StorageAccount = false },
	} {
		t.Run(name, func(t *testing.T) {
			before := approvalRequest()
			change(&before)
			if _, ok := approvedFromMain(approved, renderTeamEntity(before)); ok {
				t.Fatal("opposite resource flag authorized the request")
			}
		})
	}
}

func TestLegacyAndStaleApprovalCannotProvision(t *testing.T) {
	req := approvalRequest()
	legacy := `apiVersion: backstage.io/v1alpha1
kind: Group
metadata:
  name: approval-test
  namespace: default
  description: "A test team"
  annotations:
    backstage.io/kubernetes-id: approval-test
spec:
  type: team
  profile:
    displayName: "Approval Test"
  parent: product
  children: []
`
	if _, ok := approvedFromMain(req, legacy); ok {
		t.Fatal("legacy file without approval metadata was accepted")
	}
	old := req
	old.Spec.Environment = "tst"
	if _, ok := approvedFromMain(req, renderTeamEntity(old)); ok {
		t.Fatal("stale merged PR content was accepted")
	}
	if approvalBranch(req) == approvalBranch(old) {
		t.Fatal("changed request reused the old approval branch")
	}
}

func TestWatcherUsesApprovedFileAsProvisioningSource(t *testing.T) {
	req := approvalRequest()
	file := renderTeamEntity(req)
	provision, ok := approvedFromMain(req, file)
	if !ok {
		t.Fatal("approved file rejected")
	}
	req.Spec.Location = "northeurope" // mutable request after approval resolution
	req.Spec.Resources.KeyVault = true
	if provision.Spec.Location != "westeurope" || provision.Spec.Resources.KeyVault {
		t.Fatal("provisioning still depends on mutable request fields")
	}
}
