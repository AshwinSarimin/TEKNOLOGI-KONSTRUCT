package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type callerContextKey struct{}

type callerKey struct {
	owner  string
	digest [sha256.Size]byte
}

var callerIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Stable IDs survive token rotation. Tokens must be distinct: sharing one key
// between two identities would make the selected owner ambiguous.
func parseAPIKeys(raw string) ([]callerKey, error) {
	invalid := errors.New("API_KEYS must be a nonempty JSON object of unique caller IDs and distinct tokens of 32-256 non-space ASCII characters")
	decoder := json.NewDecoder(strings.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, invalid
	}
	ids := map[string]bool{}
	digests := map[[sha256.Size]byte]bool{}
	var keys []callerKey
	for decoder.More() {
		name, err := decoder.Token()
		id, ok := name.(string)
		if err != nil || !ok || !callerIDPattern.MatchString(id) || ids[id] {
			return nil, invalid
		}
		var token string
		if decoder.Decode(&token) != nil || len(token) < 32 || len(token) > 256 {
			return nil, invalid
		}
		for _, char := range token {
			if char <= ' ' || char > '~' {
				return nil, invalid
			}
		}
		digest := sha256.Sum256([]byte(token))
		if digests[digest] {
			return nil, invalid
		}
		ids[id], digests[digest] = true, true
		keys = append(keys, callerKey{owner: "rest-api:" + id, digest: digest})
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(keys) == 0 {
		return nil, invalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, invalid
	}
	return keys, nil
}

func withAuth(keys []callerKey, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(r.Header.Values("Authorization")) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		digest := sha256.Sum256([]byte(token))
		owner := ""
		for _, key := range keys {
			if subtle.ConstantTimeCompare(digest[:], key.digest[:]) == 1 {
				owner = key.owner
			}
		}
		if owner == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), callerContextKey{}, owner)))
	}
}
