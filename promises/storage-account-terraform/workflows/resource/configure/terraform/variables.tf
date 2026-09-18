variable "storage_account_name" {
  type        = string
  description = "Azure Storage Account name (3-24 chars, lowercase alphanumeric — derived by the pipeline)"
}

variable "resource_group_name" {
  type        = string
  description = "Azure Resource Group that owns this storage account"
}

variable "location" {
  type        = string
  description = "Azure region"
  default     = "westeurope"
}

variable "app_name" {
  type        = string
  description = "Application name — used for tagging"
}

variable "environment" {
  type        = string
  description = "Deployment environment (dev / tst / acc / prd)"
}