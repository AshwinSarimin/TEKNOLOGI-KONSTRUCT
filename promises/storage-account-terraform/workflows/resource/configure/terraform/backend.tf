terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
  # Backend is configured at runtime via -backend-config flags in the pipeline container.
  # Key pattern: storage-account-{teamName}-{environment}.tfstate
  # This keeps state isolated per request, so concurrent provisions don't interfere.
  backend "azurerm" {}
}

provider "azurerm" {
  features {}
  # Skip automatic resource provider registration — the SP only has Contributor
  # on the platform resource group, not subscription-level register permissions.
  # Microsoft.Storage is already registered in this subscription.
  # Note: resource_provider_registrations = "none" is azurerm v4 syntax; use v3 equivalent.
  skip_provider_registration = true
}