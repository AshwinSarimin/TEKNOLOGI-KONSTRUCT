package main

import "testing"

func TestPrivateNetworkPolicyLimitsIngressToItsNamespace(t *testing.T) {
	policy, needed, err := networkPolicyFor("team-a-dev", "private")
	if err != nil || !needed {
		t.Fatalf("private policy missing: needed=%v err=%v", needed, err)
	}
	metadata := policy["metadata"].(map[string]interface{})
	if metadata["namespace"] != "team-a-dev" {
		t.Fatalf("policy namespace = %v", metadata["namespace"])
	}
	spec := policy["spec"].(map[string]interface{})
	if len(spec["policyTypes"].([]string)) != 1 || spec["policyTypes"].([]string)[0] != "Ingress" {
		t.Fatalf("policy types = %v", spec["policyTypes"])
	}
	ingress := spec["ingress"].([]interface{})
	if len(ingress) != 1 {
		t.Fatalf("ingress rules = %v", ingress)
	}
	from := ingress[0].(map[string]interface{})["from"].([]interface{})
	if len(from) != 1 {
		t.Fatalf("sources = %v", from)
	}
	source := from[0].(map[string]interface{})
	if _, ok := source["podSelector"]; !ok || len(source) != 1 {
		t.Fatalf("expected only same-namespace podSelector source, got %v", source)
	}
}

func TestPublicVisibilityDoesNotEmitRestrictivePolicy(t *testing.T) {
	_, needed, err := networkPolicyFor("team-a-dev", "public")
	if err != nil || needed {
		t.Fatalf("public policy: needed=%v err=%v", needed, err)
	}
}

func TestDefaultVisibilityIsPrivateAndUnknownValueFails(t *testing.T) {
	_, needed, err := networkPolicyFor("team-a-dev", "")
	if err != nil || !needed {
		t.Fatalf("default policy: needed=%v err=%v", needed, err)
	}
	if _, _, err := networkPolicyFor("team-a-dev", "unknown"); err == nil {
		t.Fatal("unknown visibility accepted")
	}
}
