resource "azurerm_storage_account" "this" {
  name                     = var.storage_account_name
  resource_group_name      = var.resource_group_name
  location                 = var.location
  account_tier             = "Standard"
  account_replication_type = "LRS"

  # Blob versioning off for platform scratch storage; enable per-team if needed.
  blob_properties {
    versioning_enabled = false
  }

  tags = {
    managed-by  = "kratix"
    promise     = "storage-account"
    team        = var.app_name
    environment = var.environment
  }
}