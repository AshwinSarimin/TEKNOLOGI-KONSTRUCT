package main

import (
	"context"
	"fmt"
	"testing"
)

func TestStorageCandidateSeparatesNormalizedCollisions(t *testing.T) {
	for _, tc := range []struct{ app, want string }{
		{"a-b", "tekabdev9b1eebd050sa"},
		{"ab", "tekabdeved81cd984esa"},
		{"case", "tekcasedev3b876f687csa"},
		{"Case", "tekcasedev333b23ad09sa"},
		{"abcdefghij-one", "tekabcdefdev706f80ab6csa"},
		{"abcdefghij-two", "tekabcdefdevc73a2c3667sa"},
	} {
		if got := storageCandidate(tc.app, "dev"); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.app, got, tc.want)
		}
	}
}

func TestStorageLegacyClaimRequiresExactOwner(t *testing.T) {
	legacy := computeStorageAccountName("a-b", "dev")
	for _, tc := range []struct {
		name, body, want string
		status           int
		wantErr          bool
	}{
		{"owned", fmt.Sprintf(`{"data":{"target":"azure-storage/%s","ownerUID":"uid-1"}}`, legacy), legacy, 200, false},
		{"foreign", fmt.Sprintf(`{"data":{"target":"azure-storage/%s","ownerUID":"uid-2"}}`, legacy), "tekabdev9b1eebd050sa", 200, false},
		{"missing", `{}`, "tekabdev9b1eebd050sa", 404, false},
		{"wrong target", `{"data":{"target":"azure-storage/other","ownerUID":"uid-1"}}`, "", 200, true},
		{"invalid", `{`, "", 200, true},
		{"forbidden", `{}`, "", 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveStorageName(context.Background(), "a-b", "dev", "uid-1", func(context.Context, string) (int, []byte, error) { return tc.status, []byte(tc.body), nil })
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("got %q, %v; want %q error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
