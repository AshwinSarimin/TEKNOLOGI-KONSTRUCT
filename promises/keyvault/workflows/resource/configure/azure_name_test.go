package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestVaultCandidateSeparatesNormalizedCollisions(t *testing.T) {
	for _, tc := range []struct{ app, want string }{
		{"a-b", "kvabdevf79f2139fb"},
		{"ab", "kvabdev7c81fe2370"},
		{"case", "kvcasedeva5ce1785f6"},
		{"Case", "kvcasedev60b244d1eb"},
		{"abcdefghij-one", "kvabcdefghidev08f56e483c"},
		{"abcdefghij-two", "kvabcdefghidev4395a61bb1"},
	} {
		if got := vaultCandidate(tc.app, "dev"); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.app, got, tc.want)
		}
	}
}

func TestVaultLegacyClaimRequiresExactOwnerAndTarget(t *testing.T) {
	legacy := computeVaultName("a-b", "dev")
	for _, tc := range []struct {
		name, body, want string
		status           int
		lookupErr        error
		wantErr          bool
	}{
		{"owned", fmt.Sprintf(`{"data":{"target":"azure-keyvault/%s","ownerUID":"uid-1"}}`, legacy), legacy, 200, nil, false},
		{"foreign", fmt.Sprintf(`{"data":{"target":"azure-keyvault/%s","ownerUID":"uid-2"}}`, legacy), "kvabdevf79f2139fb", 200, nil, false},
		{"missing", `{}`, "kvabdevf79f2139fb", 404, nil, false},
		{"malformed", `{`, "", 200, nil, true},
		{"wrong target", `{"data":{"target":"azure-keyvault/other","ownerUID":"uid-1"}}`, "", 200, nil, true},
		{"empty owner", fmt.Sprintf(`{"data":{"target":"azure-keyvault/%s","ownerUID":""}}`, legacy), "", 200, nil, true},
		{"forbidden", `{}`, "", 403, nil, true},
		{"api error", ``, "", 0, errors.New("connection refused"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveVaultName(context.Background(), "a-b", "dev", "uid-1", func(_ context.Context, _ string) (int, []byte, error) {
				return tc.status, []byte(tc.body), tc.lookupErr
			})
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %q, %v; want %q, error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
	if _, err := resolveVaultName(context.Background(), "a-b", "dev", "", func(context.Context, string) (int, []byte, error) {
		t.Fatal("must fail before lookup")
		return 0, nil, nil
	}); err == nil {
		t.Fatal("empty request UID accepted")
	}
}
