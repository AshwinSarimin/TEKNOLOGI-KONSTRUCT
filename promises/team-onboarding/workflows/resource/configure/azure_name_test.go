package main

import (
	"context"
	"fmt"
	"testing"
)

func TestApprovedNamingCandidatesAreIndependent(t *testing.T) {
	for _, tc := range []struct{ app, vault, storage string }{
		{"a-b", "kvabdevf79f2139fb", "tekabdev9b1eebd050sa"},
		{"ab", "kvabdev7c81fe2370", "tekabdeved81cd984esa"},
		{"case", "kvcasedeva5ce1785f6", "tekcasedev3b876f687csa"},
		{"Case", "kvcasedev60b244d1eb", "tekcasedev333b23ad09sa"},
	} {
		if got := vaultCandidate(tc.app, "dev"); got != tc.vault {
			t.Errorf("vault %q = %q", tc.app, got)
		}
		if got := storageCandidate(tc.app, "dev"); got != tc.storage {
			t.Errorf("storage %q = %q", tc.app, got)
		}
	}
}

func TestApprovedMixedLegacyAndNewResource(t *testing.T) {
	legacy := computeVaultName("a-b", "dev")
	get := func(_ context.Context, name string) (int, []byte, error) {
		vaultClaim := claimNameForTarget("azure-keyvault/" + legacy)
		if name == vaultClaim {
			return 200, []byte(fmt.Sprintf(`{"data":{"target":"azure-keyvault/%s","ownerUID":"uid-1"}}`, legacy)), nil
		}
		return 404, nil, nil
	}
	vault, err := resolveVaultName(context.Background(), "a-b", "dev", "uid-1", get)
	if err != nil || vault != legacy {
		t.Fatalf("vault = %q, %v", vault, err)
	}
	storage, err := resolveStorageName(context.Background(), "a-b", "dev", "uid-1", get)
	if err != nil || storage != "tekabdev9b1eebd050sa" {
		t.Fatalf("storage = %q, %v", storage, err)
	}
}
