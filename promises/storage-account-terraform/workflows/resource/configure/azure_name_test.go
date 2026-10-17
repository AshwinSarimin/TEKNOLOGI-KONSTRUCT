package main

import (
	"context"
	"fmt"
	"testing"
)

func TestTerraformCandidateMatchesCrossplane(t *testing.T) {
	for _, tc := range []struct{ app, want string }{
		{"a-b", "tekabdev9b1eebd050sa"},
		{"ab", "tekabdeved81cd984esa"},
		{"case", "tekcasedev3b876f687csa"},
		{"Case", "tekcasedev333b23ad09sa"},
	} {
		if got := storageCandidate(tc.app, "dev"); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.app, got, tc.want)
		}
	}
}

func TestTerraformKeepsOwnedLegacyName(t *testing.T) {
	legacy := computeStorageAccountName("a-b", "dev")
	get := func(context.Context, string) (int, []byte, error) {
		return 200, []byte(fmt.Sprintf(`{"data":{"target":"azure-storage/%s","ownerUID":"uid-1"}}`, legacy)), nil
	}
	got, err := resolveStorageName(context.Background(), "a-b", "dev", "uid-1", get)
	if err != nil || got != legacy {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTerraformStopsWhenSelectedClaimDisappears(t *testing.T) {
	get := func(context.Context, string) (int, []byte, error) { return 404, nil, nil }
	called := false
	if err := executeWithClaim(context.Background(), "uid-1", "tekabdev9b1eebd050sa", get, func() error { called = true; return nil }); err == nil {
		t.Fatal("missing selected claim allowed Terraform")
	}
	if called {
		t.Fatal("Terraform command ran without owned claim")
	}
}

func TestTerraformRejectsReplacedClaimAndRunsForOwner(t *testing.T) {
	for _, tc := range []struct {
		owner   string
		wantRun bool
	}{{"uid-other", false}, {"uid-1", true}} {
		called := false
		get := func(context.Context, string) (int, []byte, error) {
			return 200, []byte(fmt.Sprintf(`{"data":{"target":"azure-storage/tekabdev9b1eebd050sa","ownerUID":%q}}`, tc.owner)), nil
		}
		err := executeWithClaim(context.Background(), "uid-1", "tekabdev9b1eebd050sa", get, func() error { called = true; return nil })
		if called != tc.wantRun || (err == nil) != tc.wantRun {
			t.Fatalf("owner=%s called=%v err=%v", tc.owner, called, err)
		}
	}
}
