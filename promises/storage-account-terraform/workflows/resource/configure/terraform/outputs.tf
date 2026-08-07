output "storage_account_name" {
  description = "Provisioned Azure Storage Account name"
  value       = azurerm_storage_account.this.name
}

output "primary_blob_endpoint" {
  description = "Primary blob service endpoint URL"
  value       = azurerm_storage_account.this.primary_blob_endpoint
}

output "id" {
  description = "Azure Resource ID of the storage account"
  value       = azurerm_storage_account.this.id
}