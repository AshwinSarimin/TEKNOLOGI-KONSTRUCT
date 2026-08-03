# TEKNOLOGI-KONSTRUCT
Complete setup of Kratix platform with ArgoCD, Backstage, Crossplane, and multi-cluster GitOps.

**Repositories**

- [TEKNOLOGI-KONSTRUCT](https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT) - contains source code
- [TEKNOLOGI-KONSTRUCT-STATE](https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT-STATE) - contains workloads manifests created by Kratix

**Access points**
Kratix ArgoCD:   http://argocd.localhost:8080
Workload ArgoCD: http://argocd.localhost:9080
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

**Generate a private key**
- https://github.com/settings/apps/teknologi-konstruct
  - Generate a private key

**Create client secret for Backstage**
- https://github.com/settings/apps/TEKNOLOGI-KONSTRUCT

**Install App on repositories**
- Developer settings > GitHub Apps > TEKNOLOGI-KONSTRUCT
  - Install App
  - Repositories: TEKNOLOGI-KONSTRUCT & TEKNOLOGI-KONSTRUCT-STATE

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

The following service principals are necessary for the platform:

**teknologi-platform-acr**
- Pull from ACR from within the cluster

```bash
ACR_REGISTRY_ID=$(az acr show --name teknologieur1acr --query id --output tsv)

SP_PASSWORD=$(az ad sp create-for-rbac \
  --name teknologi-platform-acr \
  --scopes $ACR_REGISTRY_ID \
  --role acrpull \
  --query password \
  --output tsv)

SP_APP_ID=$(az ad sp list --display-name teknologi-platform-acr --query '[].appId' --output tsv)

echo $SP_APP_ID
echo $SP_PASSWORD
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

SP_APP_ID=$(echo $SP | jq -r '.appId')
SP_PASSWORD=$(echo $SP | jq -r '.password')

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

# Role assignement 3: Contributor on subcription level for Terraform and Crossplane
az role assignment create \
  --assignee $SP_APP_ID \
  --role Contributor \
  --scope /subscriptions/e229909d-d13f-44aa-ae26-046922d181eb

echo "SP_APP_ID:  $SP_APP_ID"
echo "SP_PASSWORD: $SP_PASSWORD"  
```

**teknologi-platform-authentication**
- Backstage Entra sign-in provider
- ArgoCD Entra sign-in provider

```bash
APP_ID=$(az ad app create \
  --display-name "teknologi-platform-authentication" \
  --web-redirect-uris "http://backstage.localhost:8080/api/auth/microsoft/handler/frame" "http://argocd.localhost:8080/auth/callback" "http://argocd.localhost:9080/auth/callback" \
  --sign-in-audience AzureADMyOrg \
  --query appId -o tsv)
# If this App Registration already exists (not a fresh create), add the redirect URIs instead of recreating it:
#   az ad app update --id "$APP_ID" --web-redirect-uris "http://backstage.localhost:8080/api/auth/microsoft/handler/frame" "http://argocd.localhost:8080/auth/callback" "http://argocd.localhost:9080/auth/callback"

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
  - You'll get a URL like: https://hooks.slack.com/services/xxxxxxx/xxxxxxxx/xxxxxxxxxx
4. Test it immediately
```bash
curl -X POST -H 'Content-type: application/json' --data '{"text":"Hello, World!"}' https://hooks.slack.com/services/xxxxxxx/xxxxxxxx/xxxxxxxxxx
```
Should reply ok and post in the channel.

