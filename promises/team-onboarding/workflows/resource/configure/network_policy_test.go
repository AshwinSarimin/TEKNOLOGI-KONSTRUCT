package main

import "testing"

func TestOnboardingPrivateNamespaceGetsIngressIsolation(t *testing.T) {
	req := approvalRequest()
	policy := buildPrivateNetworkPolicy(req)
	metadata := policy["metadata"].(map[string]interface{})
	if metadata["namespace"] != "approval-test-dev" {
		t.Fatalf("policy namespace = %v", metadata["namespace"])
	}
	spec := policy["spec"].(map[string]interface{})
	if got := spec["policyTypes"].([]string); len(got) != 1 || got[0] != "Ingress" {
		t.Fatalf("policy types = %v", got)
	}
	ingress := spec["ingress"].([]interface{})
	if len(ingress) != 1 {
		t.Fatalf("ingress = %v", ingress)
	}
	from := ingress[0].(map[string]interface{})["from"].([]interface{})
	if len(from) != 1 || len(from[0].(map[string]interface{})) != 1 {
		t.Fatalf("expected only same-namespace pods, got %v", from)
	}
	if _, ok := from[0].(map[string]interface{})["podSelector"]; !ok {
		t.Fatalf("missing pod selector in %v", from)
	}
}
