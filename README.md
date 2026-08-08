# TEKNOLOGI-KONSTRUCT
Complete setup of Kratix platform with ArgoCD, Backstage, Crossplane, and multi-cluster GitOps.

**Repositories**

- [TEKNOLOGI-KONSTRUCT](https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT) - contains source code
- [TEKNOLOGI-KONSTRUCT-STATE](https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT-STATE) - contains workloads manifests created by Kratix

**URLs**
Kratix ArgoCD:   https://argocd.konstruct.teknologik8s.nl
Workload ArgoCD: https://argocd.workload.teknologik8s.nl
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
ACR_NAME="teknologieur1acr"
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
- Backstage (retrieve templates from private repositories)

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

The following secret data will be stored in Key Vault, so the External Secrets Operator can sync it into the cluster. Note the values below, you'll add them to Key Vault in [KeyVault secrets](#keyvault-secrets).

- APP_ID = [https://github.com/settings/apps/teknologi-konstruct](https://github.com/settings/apps/teknologi-konstruct)
- INSTALLATION_ID = [https://github.com/settings/installations](https://github.com/settings/installations) > Select App > Check the URL for the installation id.
- PRIVATE_KEY_LOCATION = The location to the private key that is stored locally
- CLIENT_ID = [https://github.com/settings/apps/teknologi-konstruct](https://github.com/settings/apps/teknologi-konstruct)
- CLIENT_SECRET

### Azure resources

#### Terraform storage account

A Terraform storage account will be used to store the tfstate.

```bash
RESOURCE_GROUP="teknologi-eur1-prd-k8s-rg"
LOCATION="westeurope"
SA_NAME="teknologieur1sa"
CONTAINER_NAME="tfstate"

az storage account create \
  --name "$SA_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --location "$LOCATION" \
  --sku Standard_LRS \
  --kind StorageV2 \
  --min-tls-version TLS1_2 \
  --allow-blob-public-access false

az storage container create \
  --name "$CONTAINER_NAME" \
  --account-name "$SA_NAME" \
  --auth-mode login
```

#### Azure KeyVault

The Key Vault teknologi-eur1-kv will be used to store secrets

### Service Principals

The following service principals are necessary for the platform, and the secret data will be stored in Key Vault, so the External Secrets Operator can sync it into the cluster. Note the values below, you'll add them to Key Vault in [KeyVault secrets](#keyvault-secrets):

**teknologi-platform-acr**
- Pull from ACR from within the cluster

```bash
ACR_REGISTRY_ID=$(az acr show --name teknologieur1acr --query id --output tsv)

ACR_SP_PASSWORD=$(az ad sp create-for-rbac \
  --name teknologi-platform-acr \
  --scopes $ACR_REGISTRY_ID \
  --role acrpull \
  --query password \
  --output tsv)

ACR_SP_CLIENT_ID=$(az ad sp list --display-name teknologi-platform-acr --query '[].appId' --output tsv)

echo "ACR_SP_CLIENT_ID:  $ACR_SP_CLIENT_ID"
echo "ACR_SP_PASSWORD: $ACR_SP_PASSWORD"  
```

**teknologi-platform-cloud**

Consumed by:
- Terraform tfstate in Storage Account: teknologieur1sa
- External Secrets Operator access to KeyVault
- Contributor to workload resource group(s) to create the resources with Terraform/Crossplane

```bash
SP_NAME="teknologi-platform-cloud"
SA_RESOURCE_GROUP="teknologi-eur1-prd-k8s-rg"
KV_RESOURCE_GROUP="teknologi-eur1-prd-management-rg"
SA_NAME="teknologieur1sa"
KV_NAME="teknologi-eur1-kv"

# Create SP without role assignment
SP=$(az ad sp create-for-rbac \
  --name "$SP_NAME" \
  --skip-assignment \
  --output json)

# Role assignment 1: Terraform tfstate backend
STORAGE_ACCOUNT_ID=$(az storage account show \
  --name "$SA_NAME" \
  --resource-group "$SA_RESOURCE_GROUP" \
  --query id -o tsv)

az role assignment create \
  --assignee "$SP_APP_ID" \
  --role "Storage Blob Data Contributor" \
  --scope "$STORAGE_ACCOUNT_ID"

# Role assignment 2: ESO reads secrets from Key Vault
KEY_VAULT_ID=$(az keyvault show \
  --name "$KV_NAME" \
  --resource-group "$KV_RESOURCE_GROUP" \
  --query id -o tsv)

az role assignment create \
  --assignee "$SP_APP_ID" \
  --role "Key Vault Secrets User" \
  --scope "$KEY_VAULT_ID"

# Role assignment 3: Contributor on subcription level for Terraform and Crossplane
az role assignment create \
  --assignee $SP_APP_ID \
  --role Contributor \
  --scope /subscriptions/e229909d-d13f-44aa-ae26-046922d181eb

CLOUD_CLIENT_ID=$(echo $SP | jq -r '.appId')
CLOUD_SP_PASSWORD=$(echo $SP | jq -r '.password')

echo "CLOUD_CLIENT_ID:  $CLOUD_CLIENT_ID"
echo "CLOUD_SP_PASSWORD: $CLOUD_SP_PASSWORD"  
```

**teknologi-platform-authentication**
- Backstage Entra sign-in provider
- ArgoCD Entra sign-in provider

```bash
APP_ID=$(az ad app create \
  --display-name "teknologi-platform-authentication" \
  --web-redirect-uris "https://backstage.konstruct.teknologik8s.nl/api/auth/microsoft/handler/frame" "https://argocd.konstruct.teknologik8s.nl/auth/callback" "https://argocd.workload.teknologik8s.nl:9443/auth/callback" \
  --sign-in-audience AzureADMyOrg \
  --query appId -o tsv)
# If this App Registration already exists (not a fresh create), add the redirect URIs instead of recreating it:
#   az ad app update --id "$APP_ID" --web-redirect-uris "https://backstage.konstruct.teknologik8s.nl/api/auth/microsoft/handler/frame" "https://argocd.konstruct.teknologik8s.nl/auth/callback" "https://argocd.workload.teknologik8s.nl:9443/auth/callback"

az ad app permission add --id "$APP_ID" \
  --api 00000003-0000-0000-c000-000000000000 \
  --api-permissions e1fe6dd8-ba31-4d61-89e7-88639da4683d=Scope
# permission grant needs a Service Principal for the app in this tenant
# az ad app create only creates the App Registration, not the SP
az ad sp create --id "$APP_ID"
az ad app permission grant --id "$APP_ID" --api 00000003-0000-0000-c000-000000000000 --scope User.Read
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

GITHUB_APP_ID=""
GITHUB_APP_INSTALLATION_ID=""
GITHUB_APP_PRIVATE_KEY_LOCATION="/Users/ashwin/Documents/teknologi-konstruct.2026-08-02.private-key.pem"
GITHUB_APP_CLIENT_ID=""
GITHUB_APP_CLIENT_SECRET=""

ACR_NAME="teknologieur1acr.azurecr.io"
ACR_SP_CLIENT_ID=""
ACR_SP_PASSWORD=""

TERRAFORM_RESOURCE_GROUP="teknologi-eur1-prd-k8s-rg"
TERRAFORM_SA_NAME="teknologieur1sa"
TERRAFORM_SA_CONTAINER="tfstate"

CLOUD_SP_CLIENT_ID=""
CLOUD_SP_CLIENT_SECRET=""
CLOUD_SP_TENANT_ID=""
CLOUD_SP_SUBSCRIPTION_ID=""

AUTHENTICATION_SP_CLIENT_ID=""
AUTHENTICATION_SP_CLIENT_SECRET=""
AUTHENTICATION_SP_TENANT_ID=""

SLACK_WEBHOOK=""

# GitHub App
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-id"              --value "$GITHUB_APP_ID"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-installation-id" --value "$GITHUB_APP_INSTALLATION_ID"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-private-key" --file "$GITHUB_APP_PRIVATE_KEY_LOCATION"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-client-id" --value "$GITHUB_APP_CLIENT_ID"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-github-app-client-secret" --value "$GITHUB_APP_CLIENT_SECRET"

# ACR credentials (teknologi-platform-acr SP)
az keyvault secret set --vault-name "$KV_NAME" --name "platform-acr-server" --value "$ACR_NAME"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-acr-username" --value "$ACR_SP_CLIENT_ID"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-acr-password" --value "$ACR_SP_PASSWORD"

# Terraform backend
az keyvault secret set --vault-name "$KV_NAME" --name "platform-terraform-backend-rg" --value "$TERRAFORM_RESOURCE_GROUP"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-terraform-backend-storage-account" --value "$TERRAFORM_SA_NAME"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-terraform-backend-container" --value "$TERRAFORM_SA_CONTAINER"

# Terraform Azure credentials (teknologi-platform-cloud SP)
az keyvault secret set --vault-name "$KV_NAME" --name "platform-cloud-client-id" --value "$CLOUD_SP_CLIENT_ID"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-cloud-client-secret" --value "$CLOUD_SP_CLIENT_SECRET"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-cloud-tenant-id" --value "$CLOUD_SP_TENANT_ID"
az keyvault secret set --vault-name "$KV_NAME" --name "platform-cloud-subscription-id" --value "$CLOUD_SP_SUBSCRIPTION_ID"

# Entra authentication login
az keyvault secret set --vault-name teknologi-eur1-kv --name platform-authentication-client-id --value "$AUTHENTICATION_SP_CLIENT_ID"
az keyvault secret set --vault-name teknologi-eur1-kv --name platform-authentication-client-secret --value "$AUTHENTICATION_SP_CLIENT_SECRET"
az keyvault secret set --vault-name teknologi-eur1-kv --name platform-authentication-tenant-id --value "$AUTHENTICATION_SP_TENANT_ID"

# Slack webhook
az keyvault secret set --vault-name "$KV_NAME" --name "platform-slack-webhook-url" --value "$SLACK_WEBHOOK"

# REST API shared bearer token
az keyvault secret set --vault-name "$KV_NAME" --name "platform-rest-api-key" --value "$(openssl rand -hex 32)"
```

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
  -n argocd \
  --create-namespace \
  --set 'server.extraArgs[0]=--insecure' \
  -f tenants/platform/argocd/base/helm-values.yaml \
  -f tenants/platform/argocd/overlays/dev/hub/helm-values.yaml \
  --kube-context "$HUB_CONFIG_NAME"

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
kubectl port-forward svc/argocd-server -n argocd 8080:80 --context "$WORKLOAD_CONFIG_NAME"
# Open http://localhost:8080

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



-------------------------------

```bash
kubectl create secret generic teknologi-platform-orchestration-repo \
  -n argocd \
  --context k3d-teknologi-workload-cluster \
  --from-literal=type=git \
  --from-literal=url=https://github.com/AshwinSarimin/TEKNOLOGI-PLATFORM-ORCHESTRATION.git \
  --from-literal=githubAppID=3318696 \
  --from-literal=githubAppInstallationID=122452739 \
  --from-file=githubAppPrivateKey=/Users/ashwin/Documents/teknologi-platform.2026-04-08.private-key.pem

kubectl label secret teknologi-platform-orchestration-repo -n argocd \
  --context k3d-teknologi-workload-cluster \
  argocd.argoproj.io/secret-type=repository
```

#### Bootstrap Backstage


The GitHub App needs configurations for Backstage to have GitHub signin
- https://github.com/settings/apps 
- General → Identifying and authorizing users section is where you enable "Request user authorization (OAuth) during installation" (this is what turns on "Sign in with GitHub App"), and the Callback URL field is where you add http://backstage.localhost/api/auth/github/handler/frame.




### Workload cluster


#### Bootstrap cluster

```bash
# Bootstrap
kubectl apply -f bootstrap/k3d-teknologi-workload-cluster.yaml --context k3d-teknologi-workload-cluster
```

### REST API (Phase 5)

Deploys automatically via the `rest-api-configs` ArgoCD Application once `platform-rest-api-key` exists in Key Vault (see [KeyVault secrets](./README.md#keyvault-secrets)) — no separate bootstrap step. Third consumption pattern alongside Backstage and kubectl: a generic `/apply` endpoint that creates any Promise request CRD.

```bash
API_KEY=$(az keyvault secret show --vault-name teknologi-eur1-kv --name platform-rest-api-key --query value -o tsv)

curl -X POST http://rest-api.localhost:8080/apply \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "kind": "NamespaceRequest",
    "name": "app-d-dev-ns",
    "spec": {
      "namespaceName": "app-d-dev",
      "environment": "dev",
      "networkVisibility": "private"
    }
  }'
```

Supported `kind` values: `TeamOnboardingRequest`, `NamespaceRequest`, `KeyVaultRequest`, `StorageAccountRequest`, `StorageAccountTerraformRequest`. Requests always land in `kratix-workloads` on the hub cluster, same as Backstage and kubectl.