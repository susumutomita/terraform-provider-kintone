terraform {
  required_providers {
    kintone = {
      source = "susumutomita/kintone"
    }
  }
}

# Set KINTONE_BASE_URL, KINTONE_USERNAME, KINTONE_PASSWORD outside HCL.
provider "kintone" {}

variable "operator_login" {
  type        = string
  description = "Existing provisioning user's login; keep management access."
}
variable "sales_group" {
  type        = string
  description = "Existing sales group code in the dedicated test tenant."
}

resource "kintone_app" "exercise" {
  name        = "TenkaCloud ACL prototype"
  description = ""
  rights = [
    {
      entity_type      = "USER"
      entity_code      = var.operator_login
      app_editable     = true
      record_viewable  = true
      record_addable   = true
      record_editable  = true
      record_deletable = true
    },
    {
      entity_type     = "GROUP"
      entity_code     = var.sales_group
      record_viewable = true
      record_addable  = true
      record_editable = true
    },
    {
      entity_type = "GROUP"
      entity_code = "everyone"
      # Default false: everyone else is denied. Ordered rules are intentional.
    }
  ]
  # Remote deletion is unavailable. Default blocks destroy.
  retain_on_destroy = false
}
output "app_id" {
  value = kintone_app.exercise.id
}
