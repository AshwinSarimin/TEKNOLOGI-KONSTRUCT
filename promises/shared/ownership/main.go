// The ownership gate runs inside a Kratix Pipeline before its work-writer (and
// before Terraform apply). ConfigMap creation is the atomic claim operation.
// Claims deliberately outlive requests: only an operator who has verified the
// downstream resource and Terraform state are gone may delete a claim.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

const namespace = "kratix-workloads"

type owner struct {
	UID       string
	Kind      string
	Name      string
	Namespace string
}

type configMap struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Metadata   struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace,omitempty"`
		UID         string            `json:"uid,omitempty"`
		Labels      map[string]string `json:"labels,omitempty"`
		Annotations map[string]string `json:"annotations,omitempty"`
	} `json:"metadata"`
	Data map[string]string `json:"data"`
}

type kubeClient struct {
	baseURL string
	http    *http.Client
	token   string
}

func reservationName(target string) string {
	sum := sha256.Sum256([]byte(target))
	return fmt.Sprintf("ownership-%x", sum[:16])
}

func newClaim(o owner, target string) configMap {
	cm := configMap{APIVersion: "v1", Kind: "ConfigMap"}
	cm.Metadata.Name = reservationName(target)
	cm.Metadata.Namespace = namespace
	cm.Metadata.Labels = map[string]string{"teknologi.io/ownership-reservation": "true"}
	cm.Data = map[string]string{
		"target":           target,
		"ownerUID":         o.UID,
		"requestKind":      o.Kind,
		"requestName":      o.Name,
		"requestNamespace": o.Namespace,
	}
	return cm
}

func (c *kubeClient) call(ctx context.Context, method, path string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, b, err
}

func reservationPath(name string) string {
	return "/api/v1/namespaces/" + namespace + "/configmaps" + name
}

func (c *kubeClient) claim(ctx context.Context, o owner, target string) (string, error) {
	cm := newClaim(o, target)
	collection := reservationPath("")
	for attempt := 0; attempt < 3; attempt++ {
		status, b, err := c.call(ctx, http.MethodPost, collection, cm)
		if err != nil {
			return "", err
		}
		if status == http.StatusCreated {
			var created configMap
			if err := json.Unmarshal(b, &created); err != nil {
				return "", fmt.Errorf("decode created reservation %s: %w", target, err)
			}
			if created.Metadata.UID == "" {
				return "", fmt.Errorf("created reservation %s has no UID", target)
			}
			return created.Metadata.UID, nil
		}
		if status != http.StatusConflict {
			return "", fmt.Errorf("create reservation %s: HTTP %d: %s", target, status, strings.TrimSpace(string(b)))
		}
		status, b, err = c.call(ctx, http.MethodGet, reservationPath("/"+cm.Metadata.Name), nil)
		if err != nil {
			return "", err
		}
		if status == http.StatusNotFound { // Deleted between create and get; retry.
			continue
		}
		if status != http.StatusOK {
			return "", fmt.Errorf("read reservation %s: HTTP %d: %s", target, status, strings.TrimSpace(string(b)))
		}
		var existing configMap
		if err := json.Unmarshal(b, &existing); err != nil {
			return "", err
		}
		if existing.Data["target"] != target {
			return "", fmt.Errorf("reservation hash collision for %s", target)
		}
		if existing.Data["ownerUID"] != o.UID {
			return "", fmt.Errorf("target %s is reserved by %s/%s (UID %s)", target, existing.Data["requestKind"], existing.Data["requestName"], existing.Data["ownerUID"])
		}
		return "", nil // Exact same Kubernetes object may reconcile or update.
	}
	return "", fmt.Errorf("reservation %s changed repeatedly; retry reconcile", target)
}

func (c *kubeClient) undo(ctx context.Context, target, uid string) error {
	options := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": uid}}
	status, b, err := c.call(ctx, http.MethodDelete, reservationPath("/"+reservationName(target)), options)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNotFound {
		return fmt.Errorf("rollback %s: HTTP %d: %s", target, status, strings.TrimSpace(string(b)))
	}
	return nil
}

func reserve(ctx context.Context, c *kubeClient, o owner, targets []string) error {
	if o.UID == "" || o.Kind == "" || o.Name == "" || o.Namespace != namespace {
		return errors.New("request must have UID, kind, name, and kratix-workloads namespace")
	}
	targets = append([]string(nil), targets...)
	sort.Strings(targets)
	type createdClaim struct{ target, uid string }
	var created []createdClaim
	for _, target := range targets {
		uid, err := c.claim(ctx, o, target)
		if err == nil {
			if uid != "" {
				created = append(created, createdClaim{target, uid})
			}
			continue
		}
		var rollbackErrors []error
		for i := len(created) - 1; i >= 0; i-- {
			if undoErr := c.undo(ctx, created[i].target, created[i].uid); undoErr != nil {
				rollbackErrors = append(rollbackErrors, undoErr)
			}
		}
		return errors.Join(append([]error{err}, rollbackErrors...)...)
	}
	return nil
}

type resource struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
		UID       string `yaml:"uid"`
	} `yaml:"metadata"`
	Spec struct {
		AppName            string `yaml:"appName"`
		Environment        string `yaml:"environment"`
		StorageAccountName string `yaml:"storageAccountName"`
		VaultName          string `yaml:"vaultName"`
		NamespaceName      string `yaml:"namespaceName"`
	} `yaml:"spec"`
}

func requestOwner(raw []byte) (owner, resource, error) {
	var req resource
	// The Kratix input is a full Kubernetes object; this gate reads only its
	// identity and target fields.
	if err := yaml.Unmarshal(raw, &req); err != nil {
		return owner{}, resource{}, err
	}
	o := owner{UID: req.Metadata.UID, Kind: req.Kind, Name: req.Metadata.Name, Namespace: req.Metadata.Namespace}
	return o, req, nil
}

func targetsForRequest(raw []byte, outputs map[string][]byte) ([]string, error) {
	_, req, err := requestOwner(raw)
	if err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	if req.Kind == "StorageAccountTerraformRequest" {
		if req.Spec.AppName == "" || req.Spec.Environment == "" {
			return nil, errors.New("Terraform request requires appName and environment")
		}
		clean := regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString
		name := "tek" + clean(strings.ToLower(req.Spec.AppName), "") + clean(strings.ToLower(req.Spec.Environment), "") + "sa"
		if len(name) > 24 {
			name = name[:24]
		}
		targets["azure-storage/"+name] = true
		targets[fmt.Sprintf("terraform-state/storage-account-%s-%s.tfstate", req.Spec.AppName, req.Spec.Environment)] = true
	} else {
		if len(outputs) == 0 && req.Kind != "TeamOnboardingRequest" {
			return nil, fmt.Errorf("%s produced no XR output", req.Kind)
		}
		for file, raw := range outputs {
			var out resource
			if err := yaml.Unmarshal(raw, &out); err != nil {
				return nil, fmt.Errorf("parse output %s: %w", file, err)
			}
			if out.Metadata.Name == "" {
				return nil, fmt.Errorf("output %s has no metadata.name", file)
			}
			targets["xr/"+out.Kind+"/"+out.Metadata.Name] = true
			switch out.Kind {
			case "XResourceGroup":
				targets["azure-rg/"+out.Metadata.Name] = true
			case "XNamespace":
				if out.Spec.NamespaceName == "" {
					return nil, fmt.Errorf("output %s has no namespaceName", file)
				}
				targets["k8s-namespace/"+out.Spec.NamespaceName] = true
			case "XKeyVault":
				if out.Spec.VaultName == "" {
					return nil, fmt.Errorf("output %s has no vaultName", file)
				}
				targets["azure-keyvault/"+out.Spec.VaultName] = true
			case "XStorageAccount":
				if out.Spec.StorageAccountName == "" {
					return nil, fmt.Errorf("output %s has no storageAccountName", file)
				}
				targets["azure-storage/"+out.Spec.StorageAccountName] = true
			default:
				return nil, fmt.Errorf("unsupported output kind %q in %s", out.Kind, file)
			}
		}
	}
	result := make([]string, 0, len(targets))
	for target := range targets {
		result = append(result, target)
	}
	sort.Strings(result)
	return result, nil
}

func inClusterClient() (*kubeClient, error) {
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid Kubernetes CA")
	}
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("Kubernetes service endpoint missing")
	}
	return &kubeClient{
		baseURL: "https://" + host + ":" + port,
		http:    &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
		token:   strings.TrimSpace(string(token)),
	}, nil
}

func run(ctx context.Context) error {
	raw, err := os.ReadFile("/kratix/input/object.yaml")
	if err != nil {
		return err
	}
	o, _, err := requestOwner(raw)
	if err != nil {
		return err
	}
	files, err := filepath.Glob("/kratix/output/*.yaml")
	if err != nil {
		return err
	}
	outputs := make(map[string][]byte, len(files))
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		outputs[file] = b
	}
	targets, err := targetsForRequest(raw, outputs)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	} // Onboarding still awaiting approval.
	client, err := inClusterClient()
	if err != nil {
		return err
	}
	return reserve(ctx, client, o, targets)
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "ownership gate:", err)
		os.Exit(1)
	}
}
