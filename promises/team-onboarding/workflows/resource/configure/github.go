package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const githubAPIBase = "https://api.github.com"

// githubClient authenticates as a GitHub App installation. Mints its own short-lived
// installation access token from the App's private key — same mechanism Backstage uses
// for its own GitHub integration (see backstage/app/.../app-config.yaml integrations.github).
type githubClient struct {
	hc    *http.Client
	token string
}

func newGitHubClient() (*githubClient, error) {
	appID := requireEnv("GITHUB_APP_ID")
	installationID := requireEnv("GITHUB_APP_INSTALLATION_ID")
	privateKeyPEM := requireEnv("GITHUB_APP_PRIVATE_KEY")

	rsaKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("parse GitHub App private key: %w", err)
	}

	now := time.Now()
	t := jwt.New(jwt.GetSigningMethod("RS256"))
	t.Claims = jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	}
	appJWT, err := t.SignedString(rsaKey)
	if err != nil {
		return nil, fmt.Errorf("sign App JWT: %w", err)
	}

	hc := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/app/installations/%s/access_tokens", githubAPIBase, installationID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange installation token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("installation token request failed (%d): %s", resp.StatusCode, string(body))
	}
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decode installation token: %w", err)
	}

	return &githubClient{hc: hc, token: tokenResp.Token}, nil
}

func (c *githubClient) do(method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, githubAPIBase+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.hc.Do(req)
}

func (c *githubClient) getJSON(path string, out interface{}) error {
	resp, err := c.do(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("GET %s failed (%d): %s", path, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// getDefaultBranchSHA returns the latest commit SHA on the repo's default branch.
func (c *githubClient) getDefaultBranchSHA(owner, repo, branch string) (string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	path := fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", owner, repo, branch)
	if err := c.getJSON(path, &ref); err != nil {
		return "", err
	}
	return ref.Object.SHA, nil
}

func (c *githubClient) createBranch(owner, repo, newBranch, fromSHA string) error {
	body := map[string]string{
		"ref": "refs/heads/" + newBranch,
		"sha": fromSHA,
	}
	resp, err := c.do(http.MethodPost, fmt.Sprintf("/repos/%s/%s/git/refs", owner, repo), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		return nil
	}
	// 422 "Reference already exists" is fine on retry — branch was created by a previous attempt.
	if resp.StatusCode == http.StatusUnprocessableEntity {
		respBody, _ := ioutil.ReadAll(resp.Body)
		if strings.Contains(string(respBody), "Reference already exists") {
			return nil
		}
		return fmt.Errorf("create branch failed (%d): %s", resp.StatusCode, string(respBody))
	}
	respBody, _ := ioutil.ReadAll(resp.Body)
	return fmt.Errorf("create branch failed (%d): %s", resp.StatusCode, string(respBody))
}

// getFileContent fetches a file's decoded content from the given ref. Returns
// exists=false (no error) if the file simply doesn't exist at that ref yet.
func (c *githubClient) getFileContent(owner, repo, filePath, ref string) (content string, exists bool, err error) {
	var existing struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	path := fmt.Sprintf("/repos/%s/%s/contents/%s?ref=%s", owner, repo, filePath, ref)
	resp, err := c.do(http.MethodGet, path, nil)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := ioutil.ReadAll(resp.Body)
		return "", false, fmt.Errorf("GET %s failed (%d): %s", path, resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(&existing); err != nil {
		return "", false, fmt.Errorf("decode file content: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(existing.Content, "\n", ""))
	if err != nil {
		return "", false, fmt.Errorf("base64-decode file content: %w", err)
	}
	return string(decoded), true, nil
}

// putFile creates or updates a file on the given branch via the Contents API.
// On retry it fetches the existing file's SHA so the update can proceed.
func (c *githubClient) putFile(owner, repo, branch, filePath, message, content string) error {
	body := map[string]interface{}{
		"message": message,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
	}

	// If the file already exists we need its SHA to update it.
	var existing struct {
		SHA string `json:"sha"`
	}
	if err := c.getJSON(fmt.Sprintf("/repos/%s/%s/contents/%s?ref=%s", owner, repo, filePath, branch), &existing); err == nil && existing.SHA != "" {
		body["sha"] = existing.SHA
	}

	resp, err := c.do(http.MethodPut, fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, filePath), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("put file failed (%d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

type pullRequest struct {
	Number   int    `json:"number"`
	HTMLURL  string `json:"html_url"`
	State    string `json:"state"`
	Merged   bool   `json:"merged"`
	MergedAt string `json:"merged_at"`
	MergedBy *struct {
		Login string `json:"login"`
	} `json:"merged_by"`
}

func (c *githubClient) createPullRequest(owner, repo, head, base, title, body string) (*pullRequest, error) {
	reqBody := map[string]string{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  body,
	}
	resp, err := c.do(http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls", owner, repo), reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		var pr pullRequest
		if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
			return nil, fmt.Errorf("decode PR response: %w", err)
		}
		return &pr, nil
	}
	// 422 "A pull request already exists" on retry — find and return the existing PR.
	if resp.StatusCode == http.StatusUnprocessableEntity {
		respBody, _ := ioutil.ReadAll(resp.Body)
		if strings.Contains(string(respBody), "pull request already exists") || strings.Contains(string(respBody), "A pull request already exists") {
			return c.findOpenPR(owner, repo, head, base)
		}
		return nil, fmt.Errorf("create PR failed (%d): %s", resp.StatusCode, string(respBody))
	}
	respBody, _ := ioutil.ReadAll(resp.Body)
	return nil, fmt.Errorf("create PR failed (%d): %s", resp.StatusCode, string(respBody))
}

func (c *githubClient) findOpenPR(owner, repo, head, base string) (*pullRequest, error) {
	var prs []pullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=open&head=%s:%s&base=%s", owner, repo, owner, head, base)
	if err := c.getJSON(path, &prs); err != nil {
		return nil, fmt.Errorf("list PRs: %w", err)
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("no open PR found for branch %s", head)
	}
	return &prs[0], nil
}

// findPR looks up a PR by head branch regardless of state (open, closed, merged) — unlike
// findOpenPR, this also surfaces a PR that was closed without merging, which pr-watcher
// needs to distinguish from "still open" rather than reporting a false "not found".
func (c *githubClient) findPR(owner, repo, head, base string) (*pullRequest, error) {
	var prs []pullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=all&head=%s:%s&base=%s", owner, repo, owner, head, base)
	if err := c.getJSON(path, &prs); err != nil {
		return nil, fmt.Errorf("list PRs: %w", err)
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("no PR found for branch %s", head)
	}
	return &prs[0], nil
}

// getPullRequest polls the current state of a PR — merged is only true once merged
// (closed-without-merge stays false).
func (c *githubClient) getPullRequest(owner, repo string, number int) (*pullRequest, error) {
	var pr pullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	if err := c.getJSON(path, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}
