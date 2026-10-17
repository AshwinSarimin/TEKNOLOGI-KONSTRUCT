package main

import (
	"net/http"
	"testing"
)

func TestAPIKeysRejectAmbiguousConfiguration(t *testing.T) {
	for _, raw := range []string{
		``, `null`, `{}`, `[]`, `{"alice":"short"}`,
		`{"":"` + aliceToken + `"}`, `{"Alice":"` + aliceToken + `"}`,
		`{"alice":"` + aliceToken + `","bob":"` + aliceToken + `"}`,
		`{"alice":"` + aliceToken + `","alice":"` + bobToken + `"}`,
		`{"alice":"` + aliceToken + ` "}`, `{"alice":null}`,
		`{"alice":"` + aliceToken + `"} {}`,
	} {
		if _, err := parseAPIKeys(raw); err == nil {
			t.Error("accepted invalid or ambiguous API key configuration")
		}
	}
}

func TestKeyRotationKeepsCallerIdentity(t *testing.T) {
	for _, token := range []string{aliceToken, bobToken} {
		keys, err := parseAPIKeys(`{"alice":"` + token + `"}`)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		handler := withAuth(keys, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, _ = r.Context().Value(callerContextKey{}).(string)
		}))
		callApply(handler, token, `{}`)
		if got != "rest-api:alice" {
			t.Fatalf("caller after rotation: %q", got)
		}
	}
}

func TestApplyRequiresAuthenticationEvenWithoutMiddleware(t *testing.T) {
	f, client := newKubeFixture(t, nil)
	w := callApply(handleApply(client), aliceToken, applyBody("KeyVaultRequest"))
	if w.Code != 401 || f.writes != 0 {
		t.Fatalf("untrusted header admitted without middleware: %d", w.Code)
	}
}
