package main

import (
	"fmt"
	"strings"
	"testing"
)

func testDestroyRequest() Request {
	var req Request
	req.Spec.AppName = "app-a"
	req.Spec.Environment = "dev"
	req.Spec.Location = "westeurope"
	req.Spec.ResourceGroupName = "teknologi-eur1-prd-platform-rg"
	return req
}

func TestDestroyUsesExistingStateAndChecksItIsEmpty(t *testing.T) {
	t.Setenv("TF_BACKEND_RESOURCE_GROUP", "teknologi-eur1-prd-management-rg")
	t.Setenv("TF_BACKEND_STORAGE_ACCOUNT", "teknologieur1sa")
	t.Setenv("TF_BACKEND_CONTAINER", "terraform")
	var calls []string
	stateReads := 0
	execute := func(_ string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(args) > 2 && args[1] == "state" && args[2] == "pull" {
			stateReads++
			if stateReads == 1 {
				return `{"resources":[{"mode":"managed","type":"azurerm_storage_account","name":"this","instances":[{"attributes":{"name":"tekappadevsa","resource_group_name":"teknologi-eur1-prd-platform-rg"}}]}]}`, nil
			}
			return `{"resources":[]}`, nil
		}
		return `{"resources":[]}`, nil
	}
	if err := destroyTerraform(testDestroyRequest(), "tekappadevsa", execute); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || !strings.Contains(calls[0], "container_name=terraform") || !strings.Contains(calls[0], "key=storage-account-app-a-dev.tfstate") || !strings.Contains(calls[2], "destroy -auto-approve") {
		t.Fatalf("unexpected Terraform sequence: %v", calls)
	}
}

func TestDestroyFailsClosedWhenStateDoesNotTrackAccount(t *testing.T) {
	t.Setenv("TF_BACKEND_RESOURCE_GROUP", "teknologi-eur1-prd-management-rg")
	t.Setenv("TF_BACKEND_STORAGE_ACCOUNT", "teknologieur1sa")
	var calls []string
	execute := func(_ string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}
	if err := destroyTerraform(testDestroyRequest(), "tekappadevsa", execute); err == nil {
		t.Fatal("missing state permitted deletion")
	}
	for _, call := range calls {
		if strings.Contains(call, "destroy") {
			t.Fatalf("destroy ran without tracked account: %v", calls)
		}
	}
}

func TestDestroyFailurePreservesDeletion(t *testing.T) {
	t.Setenv("TF_BACKEND_RESOURCE_GROUP", "teknologi-eur1-prd-management-rg")
	t.Setenv("TF_BACKEND_STORAGE_ACCOUNT", "teknologieur1sa")
	execute := func(_ string, args ...string) (string, error) {
		if len(args) > 2 && args[1] == "state" && args[2] == "pull" {
			return `{"resources":[{"mode":"managed","type":"azurerm_storage_account","name":"this","instances":[{"attributes":{"name":"tekappadevsa","resource_group_name":"teknologi-eur1-prd-platform-rg"}}]}]}`, nil
		}
		if len(args) > 1 && args[1] == "destroy" {
			return "", fmt.Errorf("Azure deletion failed")
		}
		return "", nil
	}
	if err := destroyTerraform(testDestroyRequest(), "tekappadevsa", execute); err == nil || !strings.Contains(err.Error(), "Azure deletion failed") {
		t.Fatalf("destroy failure was lost: %v", err)
	}
}

func TestDestroyRejectsUnexpectedAccountInState(t *testing.T) {
	t.Setenv("TF_BACKEND_RESOURCE_GROUP", "teknologi-eur1-prd-management-rg")
	t.Setenv("TF_BACKEND_STORAGE_ACCOUNT", "teknologieur1sa")
	execute := func(_ string, args ...string) (string, error) {
		if len(args) > 2 && args[1] == "state" && args[2] == "pull" {
			return `{"resources":[{"mode":"managed","type":"azurerm_storage_account","name":"this","instances":[{"attributes":{"name":"someoneelsesa","resource_group_name":"teknologi-eur1-prd-platform-rg"}}]}]}`, nil
		}
		if len(args) > 1 && args[1] == "destroy" {
			t.Fatal("destroyed foreign account")
		}
		return "", nil
	}
	if err := destroyTerraform(testDestroyRequest(), "tekappadevsa", execute); err == nil {
		t.Fatal("foreign state account was accepted")
	}
}

func TestDestroyRequiresStateToBeEmptyAfterApply(t *testing.T) {
	t.Setenv("TF_BACKEND_RESOURCE_GROUP", "teknologi-eur1-prd-management-rg")
	t.Setenv("TF_BACKEND_STORAGE_ACCOUNT", "teknologieur1sa")
	execute := func(_ string, args ...string) (string, error) {
		if len(args) > 2 && args[1] == "state" && args[2] == "pull" {
			return `{"resources":[{"mode":"managed","type":"azurerm_storage_account","name":"this","instances":[{"attributes":{"name":"tekappadevsa","resource_group_name":"teknologi-eur1-prd-platform-rg"}}]}]}`, nil
		}
		return "", nil
	}
	if err := destroyTerraform(testDestroyRequest(), "tekappadevsa", execute); err == nil || !strings.Contains(err.Error(), "still tracks") {
		t.Fatalf("nonempty state after destroy was accepted: %v", err)
	}
}
