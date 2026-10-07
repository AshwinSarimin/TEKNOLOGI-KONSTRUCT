# TEKNOLOGI-KONSTRUCT
Complete setup of Kratix platform with ArgoCD, Backstage, Crossplane, and multi-cluster GitOps.

**Repositories**

- [TEKNOLOGI-KONSTRUCT](https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT) - contains source code
- [TEKNOLOGI-KONSTRUCT-STATE](https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT-STATE) - contains workloads manifests created by Kratix

**URLs**
Kratix ArgoCD:   https://argocd.konstruct.teknologik8s.nl
Workload ArgoCD: https://argocd.workload.teknologik8s.nl:9443
Backstage:       http://backstage.localhost:8080

## Prerequisites

✓ macOS with 
  ✓ Orbstack: Used for the Docker Engine, k3d depends on it.
  ✓ k3d: k3d creates k8s clusters as Docker containers, and OrbStack is the Docker engine.
  ✓ Kubectl
  ✓ ArgoCD CLI
  ✓ GitHub CLI

```bash
# Install k3d
brew install k3d

# Install ArgoCD CLI
brew install argocd

# Install GitHub CLI
brew install gh
```

## One-time Setup

### Resolve locally

The URLs needs to resolve locally to the K3d ingress IP.
```bash
echo "127.0.0.1 argocd.konstruct.teknologik8s.nl" | sudo tee -a /etc/hosts
echo "127.0.0.1 backstage.konstruct.teknologik8s.nl" | sudo tee -a /etc/hosts
echo "127.0.0.1 argocd.workload.teknologik8s.nl" | sudo tee -a /etc/hosts
```

### GitHub Action

An Azure managed identity is necessary for the GitHub Action workflows to push to the ACR.
I'm using the existing managed identity teknologi-eur1-prd-github-mi.

```bash
# Variables
TENANT_ID="99f9af3a-fd8b-41ec-b487-483a0c562b5c"
SUBSCRIPTION_ID="e229909d-d13f-44aa-ae26-046922d181eb"
RESOURCE_GROUP_NAME="teknologi-eur1-prd-management-rg"
LOCATION="westeurope"
IDENTITY_NAME="teknologi-eur1-prd-github-mi"
GH_USERNAME="AshwinSarimin"
GH_REPO_NAME="TEKNOLOGI-KONSTRUCT"
GH_ENV_NAME="prd"

az login --tenant "$TENANT_ID" --use-device-code

OWNER_ID=$(gh api users/$GH_USERNAME --jq .id)
REPO_ID=$(gh api repos/$GH_USERNAME/$GH_REPO_NAME --jq .id)

# Create federated identity credentials
az identity federated-credential create \
    --resource-group "$RESOURCE_GROUP_NAME" \
    --identity-name "$IDENTITY_NAME" \
    --name "$GH_REPO_NAME" \
    --issuer "https://token.actions.githubusercontent.com" \
    --subject "repo:${GH_USERNAME}/${GH_REPO_NAME}:environment:${GH_ENV_NAME}" \
    --audiences "api://AzureADTokenExchange"

IDENTITY=$(az identity show \
    --name "$IDENTITY_NAME" \
    --resource-group "$RESOURCE_GROUP_NAME" \
    --output json)

# Output subscription id, client id and tenant id
SUBSCRIPTION_ID=$(az account show --query id -o tsv)
CLIENT_ID=$(echo "$IDENTITY" | jq -r '.clientId')
TENANT_ID=$(echo "$IDENTITY" | jq -r '.tenantId')

# Create JSON output
OUTPUT=$(jq -n \
    --arg sub "$SUBSCRIPTION_ID" \
    --arg client "$CLIENT_ID" \
    --arg tenant "$TENANT_ID" \
    '{SUBSCRIPTION_ID: $sub, CLIENT_ID: $client, TENANT_ID: $tenant}')

echo "$OUTPUT"

# Set GitHub secrets
gh auth login

gh secret set SUBSCRIPTION_ID --body "$SUBSCRIPTION_ID" --env "$GH_ENV_NAME" --repo ${GH_USERNAME}/${GH_REPO_NAME}
gh secret set CLIENT_ID --body "$CLIENT_ID" --env "$GH_ENV_NAME" --repo ${GH_USERNAME}/${GH_REPO_NAME}
gh secret set TENANT_ID --body "$TENANT_ID" --env "$GH_ENV_NAME" --repo ${GH_USERNAME}/${GH_REPO_NAME}
```

### Github App

A GitHub App is necessary to connect to a private repo for:
- ArgoCD
- Kratix (write to orchestration repo) 
- Backstage (GitHub Signin and retrieve templates from private repositories)

More information: https://docs.kratix.io/main/reference/statestore/gitstatestore#github-app 

**Create App**
- Developer settings > New GitHub App
  - GitHub App name: TEKNOLOGI-KONSTRUCT
  - Homepage URL
  - Permissions: 
    - Commit statuses: Read-only (Will be used by Backstage to create a PR)
    - Contents: Read and write (Will be used by ArgoCD to read, and by Kratix to write CRD's)
    - Pull request: Read and write (To be used by Kratix)

**Install App on repositories**
- Developer settings > GitHub Apps > TEKNOLOGI-KONSTRUCT
  - Install App
  - Repositories: TEKNOLOGI-KONSTRUCT & TEKNOLOGI-KONSTRUCT-STATE

**Generate a private key**
- https://github.com/settings/apps/teknologi-konstruct
  - Generate a private key

**Create client secret for Backstage**
- https://github.com/settings/apps/TEKNOLOGI-KONSTRUCT

The following data will be stored in the platform configmaps `tenants/platform/kratix/base/configs/workload/platform-config.yaml` and `tenants/platform/backstage/base/configs/platform-config`

- `githubAppId` = [https://github.com/settings/apps/teknologi-konstruct](https://github.com/settings/apps/teknologi-konstruct)
- `githubAppInstallationId` = [https://github.com/settings/installations](https://github.com/settings/installations) > Select App > Check the URL for the installation id.
- `githubAppClientId` = [https://github.com/settings/apps/teknologi-konstruct](https://github.com/settings/apps/teknologi-konstruct) as Backstage's own copy only, Kratix pipelines don't need it.

The following secret data will be stored in Key Vault, so the External Secrets Operator can sync it into the cluster. Note the values below, you'll add them to Key Vault in [KeyVault secrets](#keyvault-secrets).

- PRIVATE_KEY_LOCATION = The location to the private key that is stored locally
- CLIENT_SECRET
- GITHUB_APP_ID = necessary for the GitStateStore
- GITHUB_APP_INSTALLATION_ID = necessary for the GitStateStore

**GitHub Signin**

The GitHub App needs configurations for Backstage to have GitHub signin:
- https://github.com/settings/apps 
- General → Identifying and authorizing users:
  - Enable "Request user authorization (OAuth) during installation" (this is what turns on "Sign in with GitHub App")
  - Add the Callback URL field: https://backstage.konstruct.teknologik8s.nl/api/auth/github/handler/frame

### Azure resources

#### Terraform storage account

The Terraform backend uses the existing storage account `teknologieur1sa` in
`teknologi-eur1-prd-management-rg`, with the `terraform` blob container. These
values are configured for the Kratix pipeline in
`tenants/platform/kratix/base/configs/workload/platform-config.yaml`.
Each storage-account request uses the state key
`storage-account-{appName}-{environment}.tfstate` inside that container. The
backend resource group is separate from `teknologi-eur1-prd-platform-rg`,
which holds the Azure resources created by the platform.

```bash
RESOURCE_GROUP="teknologi-eur1-prd-management-rg"
LOCATION="westeurope"
SA_NAME="teknologieur1sa"
CONTAINER_NAME="terraform"

az storage container create \
  --name "$CONTAINER_NAME" \
  --account-name "$SA_NAME" \
  --auth-mode login

az storage container show \
  --name "$CONTAINER_NAME" \
  --account-name "$SA_NAME" \
  --auth-mode login
```

#### Azure KeyVault

The Key Vault `teknologi-eur1-kv` will be used to store secrets

### Service Principals

The following service principals are necessary for the platform, and the secret data will be stored in Key Vault, so the External Secrets Operator can sync it into the cluster. Note the values below, you'll add them to Key Vault in [KeyVault secrets](#keyvault-secrets):

**teknologi-konstruct-acr**
- Pull from ACR from within the cluster

**teknologi-konstruct-cloud**
- Terraform tfstate in Storage Account: teknologieur1sa
- External Secrets Operator access to KeyVault
- Contributor to workload resource group(s) to create the resources with Terraform/Crossplane

**teknologi-konstruct-authentication**
- Backstage Entra sign-in provider
- ArgoCD Entra sign-in provider


```bash
ACRSPNAME="teknologi-konstruct-acr"
CLOUDSPNAME="teknologi-konstruct-cloud"
AUTHSPNAME="teknologi-konstruct-authentication"
ACRNAME="teknologieur1acr"
RESOURCE_GROUP="teknologi-eur1-prd-management-rg"
SA_NAME="teknologieur1sa"
KV_NAME="teknologi-eur1-kv"

## teknologi-konstruct-acr
ACR_REGISTRY_ID=$(az acr show --name "$ACRNAME" --query id --output tsv)

ACR_SP=$(az ad sp create-for-rbac \
  --name "$ACRSPNAME" \
  --scopes $ACR_REGISTRY_ID \
  --role acrpull \
  --output json)

ACR_SP_CLIENT_ID=$(echo $ACR_SP | jq -r '.appId')
ACR_SP_PASSWORD=$(echo $ACR_SP | jq -r '.password')

## teknologi-konstruct-cloud

CLOUD_SP=$(az ad sp create-for-rbac \
  --name "$CLOUDSPNAME" \
  --skip-assignment \
  --output json)

CLOUD_SP_CLIENT_ID=$(echo $CLOUD_SP | jq -r '.appId')
CLOUD_SP_PASSWORD=$(echo $CLOUD_SP | jq -r '.password')

# Role assignment 1: Terraform tfstate backend
STORAGE_ACCOUNT_ID=$(az storage account show \
  --name "$SA_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --query id -o tsv)

az role assignment create \
  --assignee "$CLOUD_SP_CLIENT_ID" \
  --role "Storage Blob Data Contributor" \
  --scope "$STORAGE_ACCOUNT_ID"

# Role assignment 2: ESO reads secrets from Key Vault
KEY_VAULT_ID=$(az keyvault show \
  --name "$KV_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --query id -o tsv)

az role assignment create \
  --assignee "$CLOUD_SP_CLIENT_ID" \
  --role "Key Vault Secrets User" \
  --scope "$KEY_VAULT_ID"

# Role assignment 3: Contributor on subcription level for Terraform and Crossplane
az role assignment create \
  --assignee "$CLOUD_SP_CLIENT_ID" \
  --role Contributor \
  --scope /subscriptions/e229909d-d13f-44aa-ae26-046922d181eb

## teknologi-konstruct-authentication

AUTH_SP=$(az ad app create \
  --display-name "$AUTHSPNAME" \
  --web-redirect-uris "https://backstage.konstruct.teknologik8s.nl/api/auth/microsoft/handler/frame" "https://argocd.konstruct.teknologik8s.nl/auth/callback" "https://argocd.workload.teknologik8s.nl:9443/auth/callback" \
  --sign-in-audience AzureADMyOrg \
  --output json)

# If this App Registration already exists (not a fresh create), add the redirect URIs instead of recreating it:
# az ad app update --id "$AUTH_SP_CLIENT_ID" --web-redirect-uris "https://backstage.konstruct.teknologik8s.nl/api/auth/microsoft/handler/frame" "https://argocd.konstruct.teknologik8s.nl/auth/callback" "https://argocd.workload.teknologik8s.nl:9443/auth/callback"

az ad app permission add --id "$AUTH_SP_CLIENT_ID" \
  --api 00000003-0000-0000-c000-000000000000 \
  --api-permissions e1fe6dd8-ba31-4d61-89e7-88639da4683d=Scope
# permission grant needs a Service Principal for the app in this tenant
# az ad app create only creates the App Registration, not the SP
az ad sp create --id "$AUTH_SP_CLIENT_ID"
az ad app permission grant --id "$AUTH_SP_CLIENT_ID" --api 00000003-0000-0000-c000-000000000000 --scope User.Read

AUTH_SP_CLIENT_ID=$(echo $AUTH_SP | jq -r '.appId')
AUTH_SP_PASSWORD=$(az ad app credential reset --id "$AUTH_SP_CLIENT_ID" --query password -o tsv)


echo "ACR_SP_CLIENT_ID:  $ACR_SP_CLIENT_ID"
echo "ACR_SP_PASSWORD: $ACR_SP_PASSWORD"
echo "CLOUD_SP_CLIENT_ID:  $CLOUD_SP_CLIENT_ID"
echo "CLOUD_SP_PASSWORD: $CLOUD_SP_PASSWORD"
echo "AUTH_SP_CLIENT_ID:  $AUTH_SP_CLIENT_ID"
echo "AUTH_SP_PASSWORD:  $AUTH_SP_PASSWORD"
```

### Slack integration

Slack will be used by Kratix to send messages about the Promise worklows

Create incoming webhook
1. Create a Slack App: 
  - api.slack.com/apps → Create New App → From scratch
  - App name: TEKNOLOGI-KONSTRUCT
  - Pick your workspace → Create App
2. Enable Incoming Webhooks
  - In the app settings sidebar → Incoming Webhooks → toggle Activate Incoming Webhooks to ON
3. Add a webhook to a channel
  - Scroll down → Add New Webhook to Workspace → pick the channel (e.g. #platform-notifications) → Allow
  - You'll get a URL like: https://hooks.slack.com/services/xxxxxxx/xxxxxx/xxxxxxxxxxxxxxx
4. Test it immediately
```bash
curl -X POST -H 'Content-type: application/json' --data '{"text":"Hello, World!"}' https://hooks.slack.com/services/xxxxxxx/xxxxxx/xxxxxxxxxxxxxxx
```
Should reply ok and post in the channel.

The webhook URL will be stored in Key Vault, so the External Secrets Operator can sync it into the cluster. Note the webhook URL value, you'll add them to Key Vault in [KeyVault secrets](#keyvault-secrets).

### KeyVault secrets

The following secret data must be stored in Key Vault, so the External Secrets Operator can sync it into the cluster.

```bash
KV_NAME="teknologi-eur1-kv"

GITHUB_APP_PRIVATE_KEY_LOCATION="/Users/ashwin/Documents/teknologi-konstruct.2026-08-02.private-key.pem"
GITHUB_APP_CLIENT_SECRET=""
GITHUB_APP_ID=""
GITHUB_APP_INSTALLATION_ID=""
ACR_SP_CLIENT_ID=""
ACR_SP_PASSWORD=""
CLOUD_SP_CLIENT_SECRET=""

AUTHENTICATION_SP_CLIENT_ID=""
AUTHENTICATION_SP_CLIENT_SECRET=""

SLACK_WEBHOOK=""

# GitHub App
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-private-key" --file "$GITHUB_APP_PRIVATE_KEY_LOCATION"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-client-secret" --value "$GITHUB_APP_CLIENT_SECRET"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-id" --value "$GITHUB_APP_ID"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-installation-id" --value "$GITHUB_APP_INSTALLATION_ID"

# ACR credentials (teknologi-konstruct-acr SP)
az keyvault secret set --vault-name "$KV_NAME" --name "platform-acr-username" --value "$ACR_SP_CLIENT_ID"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-acr-password" --value "$ACR_SP_PASSWORD"

# Terraform Azure credentials (teknologi-konstruct-cloud SP)
az keyvault secret set --vault-name "$KV_NAME" --name "platform-cloud-client-secret" --value "$CLOUD_SP_CLIENT_SECRET"

# Entra authentication login — clientId stays a Key Vault secret (not moved to
# platform-config) specifically because ArgoCD's oidc.config reads it via
# `$argocd-entra-id:clientID`, a substitution mechanism that only resolves
# against labeled Secrets, never ConfigMaps.
az keyvault secret set --vault-name teknologi-eur1-kv --name platform-authentication-client-id --value "$AUTHENTICATION_SP_CLIENT_ID"
az keyvault secret set --vault-name teknologi-eur1-kv --name platform-authentication-client-secret --value "$AUTHENTICATION_SP_CLIENT_SECRET"

# Slack webhook
az keyvault secret set --vault-name "$KV_NAME" --name "platform-slack-webhook-url" --value "$SLACK_WEBHOOK"

# REST API: one distinct token per caller, keyed by a stable caller ID.
# Keep IDs stable when rotating tokens; do not share a token between callers.
REST_API_KEYS_FILE="$(mktemp)"
jq -n --arg token "$(openssl rand -hex 32)" \
  '{"platform-automation": $token}' > "$REST_API_KEYS_FILE"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-rest-api-keys" \
  --file "$REST_API_KEYS_FILE" --output none
rm "$REST_API_KEYS_FILE"
```

### REST API caller credentials and request ownership

For an existing installation, create `platform-rest-api-keys` using the commands
above **before** syncing the updated REST API manifests. Supply a JSON object
containing every caller that needs access, for example two independently generated
tokens under `team-a-ci` and `team-b-ci`. Caller IDs must be lowercase letters,
digits or hyphens, start and end with a letter or digit, and be at most 63
characters. Tokens must be distinct and contain 32–256 non-space ASCII characters;
use `openssl rand -hex 32`. Store this file securely outside the repository.
Writing the Key Vault secret replaces the entire caller map. The legacy shared
`platform-rest-api-key` is no longer accepted by the updated service.

After committing and pushing the code, wait for both image builds to succeed and
for ArgoCD to sync the manifests. Refresh ESO and restart the REST API so it loads
the new credentials and image (also required after token rotation):

Plan a maintenance window for this migration: ArgoCD can sync the new `API_KEYS`
environment and reduced RBAC before CI finishes building the new image. The old
image may fail startup until the build succeeds and the deployment is restarted.

```bash
kubectl --context k3d-k3d-teknologi-hub-cluster -n rest-api annotate externalsecret rest-api-key \
  force-sync="$(date +%s)" --overwrite
kubectl --context k3d-k3d-teknologi-hub-cluster -n rest-api wait \
  --for=jsonpath='{.data.apiKeys}' secret/rest-api-key --timeout=120s
kubectl --context k3d-k3d-teknologi-hub-cluster -n rest-api rollout restart deployment/rest-api
kubectl --context k3d-k3d-teknologi-hub-cluster -n rest-api rollout status deployment/rest-api
```

For a rotation, wait until `ExternalSecret.status.refreshTime` advances after the
refresh annotation before restarting; an existing `apiKeys` entry alone does not
prove that the new value has arrived. Rebuild and roll out Backstage after its image
build succeeds as well. This is required before its submissions acquire owners.

Submit with the token for the intended caller:

```bash
REST_API_TOKEN="$(az keyvault secret show --vault-name "$KV_NAME" \
  --name platform-rest-api-keys --query value -o tsv | jq -r '.["platform-automation"]')"
curl --fail-with-body http://rest-api.localhost:8080/apply \
  -H "Authorization: Bearer $REST_API_TOKEN" -H 'Content-Type: application/json' \
  --data '{"kind":"NamespaceRequest","name":"example-dev-ns","spec":{"appName":"example","environment":"dev","networkVisibility":"public"}}'
unset REST_API_TOKEN
```

The endpoint returns `201` for a new request and `200` for the same owner's retry
or update. A different owner or an existing unowned request returns `403`; a
concurrent change returns `409` and can be retried. Backstage uses the authenticated
user's entity reference as owner. Ownership is separate for REST API callers and
Backstage users, and cannot be supplied in a request body or template manifest.

Existing requests require an administrator to review and assign an owner before
either broker can update them. Inspect the request first, then use its current
resourceVersion to avoid overwriting a concurrent change. For example:

```bash
REQUEST_KIND="keyvaultrequests"
REQUEST_NAME="replace-with-reviewed-request"
REQUEST_OWNER="rest-api:platform-automation" # or backstage:user:default/<actual-user>
kubectl --context k3d-k3d-teknologi-hub-cluster -n kratix-workloads \
  get "$REQUEST_KIND" "$REQUEST_NAME" -o yaml
# Copy resourceVersion from the object you reviewed above.
REQUEST_RESOURCE_VERSION="replace-with-reviewed-resource-version"
kubectl --context k3d-k3d-teknologi-hub-cluster -n kratix-workloads \
  annotate "$REQUEST_KIND" "$REQUEST_NAME" \
  teknologi.io/request-owner="$REQUEST_OWNER" \
  --resource-version="$REQUEST_RESOURCE_VERSION"
```

### Resource target reservations

The five Promise pipelines reserve XR names and their Kubernetes or Azure targets
in `kratix-workloads` ConfigMaps before publishing manifests or running Terraform.
Inspect the reservations for a request UID with:

```bash
REQUEST_UID="replace-with-request-uid"
kubectl --context k3d-k3d-teknologi-hub-cluster -n kratix-workloads \
  get configmaps -l teknologi.io/ownership-reservation=true -o json |
  jq -r --arg uid "$REQUEST_UID" '.items[] | select(.data.ownerUID == $uid) | [.metadata.name, .data.target] | @tsv'
```

Reservations remain after request deletion. For each target, first verify that
the corresponding workload XR and Kubernetes or Azure resource are gone. For a
Terraform target, also verify that the Azure account and its backend state blob
are gone; the Terraform Promise currently has no automatic destroy workflow.
Then remove only the verified reservation:

```bash
TARGET="azure-storage/replace-with-account-name"
RESERVATION="ownership-$(printf '%s' "$TARGET" | shasum -a 256 | cut -c1-32)"
kubectl --context k3d-k3d-teknologi-hub-cluster -n kratix-workloads \
  get configmap "$RESERVATION" -o yaml
kubectl --context k3d-k3d-teknologi-hub-cluster -n kratix-workloads \
  delete configmap "$RESERVATION"
```

Keep `legacy-unowned` reservations until an administrator has resolved the
corresponding older XR. Recreating a request creates a new UID and cannot reuse
its old reservation until the above cleanup is complete.

## Clusters Bootstrap

### Set variables

```bash
HUB_CLUSTER_NAME="k3d-teknologi-hub-cluster"
WORKLOAD_CLUSTER_NAME="k3d-teknologi-workload-cluster"
HUB_CONFIG_NAME="k3d-k3d-teknologi-hub-cluster"
WORKLOAD_CONFIG_NAME="k3d-k3d-teknologi-workload-cluster"

REPO_PLATFORM_URL="https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT.git"
GITHUB_APP_ID=""
GITHUB_INSTALLATION_ID=""
GITHUB_PRIVATE_KEY_LOCATION=""

CLOUD_SP_CLIENT_ID=""
CLOUD_SP_CLIENT_SECRET=""
CLOUD_SP_TENANT_ID=""
```

### k3d

```bash
# Create clusters
k3d cluster create "$HUB_CLUSTER_NAME" --api-port 6550 --servers 1 -p "8080:80@loadbalancer" -p "443:443@loadbalancer" --k3s-arg '--kube-proxy-arg=proxy-mode=ipvs@server:*'
k3d cluster create "$WORKLOAD_CLUSTER_NAME" --api-port 6551 --servers 1 -p "9080:80@loadbalancer" -p "9443:443@loadbalancer" --k3s-arg '--kube-proxy-arg=proxy-mode=ipvs@server:*'

# Verify both contexts are available
kubectl config get-contexts
```

The `--k3s-arg` requests IPVS kube-proxy mode instead of the default iptables mode. This avoids avoids a known iptables DNAT/conntrack race on single-endpoint Services (e.g. the `kubernetes` service itself) that causes intermittent "connection refused errors on a pod's second-or-later connection to it.

### ArgoCD

#### Install on clusters

```bash
# Add Argo Helm repo
helm repo add argo https://argoproj.github.io/argo-helm
helm repo update

# Install ArgoCD on hub cluster
helm upgrade --install argocd argo/argo-cd \
  --version 10.9.2 \
  -n argocd \
  --create-namespace \
  --set 'server.extraArgs[0]=--insecure' \
  -f tenants/platform/argocd/base/helm-values.yaml \
  -f tenants/platform/argocd/overlays/dev/hub/helm-values.yaml \
  --kube-context "$HUB_CONFIG_NAME"

# Existing hub installation: apply changed ArgoCD Helm values with this upgrade
# command too; argocd-configs only reconciles the Kustomize resources, not Helm.
# The hub server cannot directly patch/delete workload objects through the UI.
# Application sync/prune continues through the application-controller.

kubectl wait --for=condition=available deployment/argocd-server \
  -n argocd \
  --timeout=120s \
  --context "$HUB_CONFIG_NAME"

# Install ArgoCD on workload cluster
helm upgrade --install argocd argo/argo-cd \
  -n argocd \
  --create-namespace \
  --set 'server.extraArgs[0]=--insecure' \
  -f tenants/platform/argocd/base/helm-values.yaml \
  -f tenants/platform/argocd/overlays/dev/workload/helm-values.yaml \
  --kube-context "$WORKLOAD_CONFIG_NAME"

kubectl wait --for=condition=available deployment/argocd-server \
  -n argocd \
  --timeout=120s \
  --context "$HUB_CONFIG_NAME"
```

> To reach the ArgoCD UI for now without Ingress, use port-forwarding:
```bash
kubectl port-forward svc/argocd-server -n argocd 8080:80 --context "$HUB_CONFIG_NAME"
kubectl port-forward svc/argocd-server -n argocd 8081:80 --context "$WORKLOAD_CONFIG_NAME"

# Admin password still works as a fallback even with SSO configured above. ESO needs to sync the argocd-entra-id secret before SSO login actually works, which happens later in the bootstrap.
HUB_ARGO_PASSWORD=$(kubectl -n argocd get secret argocd-initial-admin-secret \
  --context "$HUB_CONFIG_NAME" \
  -o jsonpath="{.data.password}" | base64 -d)

WORKLOAD_ARGO_PASSWORD=$(kubectl -n argocd get secret argocd-initial-admin-secret \
  --context "$WORKLOAD_CONFIG_NAME" \
  -o jsonpath="{.data.password}" | base64 -d)

echo "Hub ArgoCD password: ${HUB_ARGO_PASSWORD}"
echo "Workload ArgoCD password: ${WORKLOAD_ARGO_PASSWORD}"
```

#### Create GitHub credentials

```bash
# Hub cluster
kubectl create secret generic github-app-creds \
  -n argocd \
  --context "$HUB_CONFIG_NAME" \
  --from-literal=type=git \
  --from-literal=url="$REPO_PLATFORM_URL" \
  --from-literal=githubAppID="$GITHUB_APP_ID" \
  --from-literal=githubAppInstallationID="$GITHUB_INSTALLATION_ID" \
  --from-file=githubAppPrivateKey="$GITHUB_PRIVATE_KEY_LOCATION"

kubectl label secret github-app-creds \
  -n argocd \
  --context "$HUB_CONFIG_NAME" \
  argocd.argoproj.io/secret-type=repository

# Workload cluster
kubectl create secret generic github-app-creds \
  -n argocd \
  --context "$WORKLOAD_CONFIG_NAME" \
  --from-literal=type=git \
  --from-literal=url="$REPO_PLATFORM_URL" \
  --from-literal=githubAppID="$GITHUB_APP_ID" \
  --from-literal=githubAppInstallationID="$GITHUB_INSTALLATION_ID" \
  --from-file=githubAppPrivateKey="$GITHUB_PRIVATE_KEY_LOCATION"

kubectl label secret github-app-creds \
  -n argocd \
  --context "$WORKLOAD_CONFIG_NAME" \
  argocd.argoproj.io/secret-type=repository
```

### External Secrets Operator

#### Bootstrap ESO

Create secret to bootstrap ESO
It's the credential ESO itself needs to connect to Key Vault. Everything else will sync automatically from here.

```bash
# Bootstrap ESO on hub cluster
kubectl create ns external-secrets --context "$HUB_CONFIG_NAME"

kubectl create secret generic azure-kv-credentials \
  --from-literal=clientId="$CLOUD_SP_CLIENT_ID" \
  --from-literal=clientSecret="$CLOUD_SP_CLIENT_SECRET" \
  --from-literal=tenantId="$CLOUD_SP_TENANT_ID" \
  -n external-secrets \
  --context "$HUB_CONFIG_NAME"

# Bootstrap ESO on workload cluster
kubectl create ns external-secrets --context "$WORKLOAD_CONFIG_NAME"

kubectl create secret generic azure-kv-credentials \
  --from-literal=clientId="$CLOUD_SP_CLIENT_ID" \
  --from-literal=clientSecret="$CLOUD_SP_CLIENT_SECRET" \
  --from-literal=tenantId="$CLOUD_SP_TENANT_ID" \
  -n external-secrets \
  --context "$WORKLOAD_CONFIG_NAME"
```

### Hub cluster

#### Bootstrap Cluster

```bash
# Bootstrap
kubectl apply -f bootstrap/k3d-teknologi-hub-cluster.yaml --context "$HUB_CONFIG_NAME"
```

### Workload cluster

#### Bootstrap cluster

```bash
# Bootstrap
kubectl apply -f bootstrap/k3d-teknologi-workload-cluster.yaml --context "$WORKLOAD_CONFIG_NAME"
```
