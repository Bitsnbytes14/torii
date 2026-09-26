variable "region" {
  description = "AWS region to deploy into."
  type        = string
  default     = "ap-south-1"
}

variable "ssh_allowed_cidr" {
  description = "CIDR allowed to SSH to the instance, e.g. your IP as \"203.0.113.7/32\". No default on purpose."
  type        = string

  validation {
    condition     = can(cidrhost(var.ssh_allowed_cidr, 0))
    error_message = "ssh_allowed_cidr must be a valid CIDR block, e.g. \"203.0.113.7/32\"."
  }

  # SSH also reaches the k3s API through a tunnel, so an open port 22 is
  # effectively an open cluster-admin door guarded only by the key.
  validation {
    condition     = !contains(["0.0.0.0/0", "::/0"], var.ssh_allowed_cidr)
    error_message = "ssh_allowed_cidr must not be open to the whole internet; use your own IP as a /32."
  }
}

variable "ssh_public_key" {
  description = "Public half of the SSH key used to reach the instance (contents of e.g. ~/.ssh/torii.pub)."
  type        = string
}

variable "budget_alert_email" {
  description = "Email address that receives the AWS Budgets alerts."
  type        = string

  validation {
    condition     = can(regex("^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$", var.budget_alert_email))
    error_message = "budget_alert_email must be an email address."
  }
}

variable "root_volume_size_gb" {
  description = "Root EBS volume size. Holds the OS, k3s, container images, and the Postgres/Redis hostPath data."
  type        = number
  default     = 16

  # 30 GB is the EBS free-tier allowance; anything above it is billed.
  validation {
    condition     = var.root_volume_size_gb >= 12 && var.root_volume_size_gb <= 30
    error_message = "root_volume_size_gb must be between 12 (room for k3s and images) and 30 (the free-tier ceiling)."
  }
}

variable "github_repository" {
  description = "GitHub \"owner/repo\" allowed to assume the deploy role. Case-sensitive: must match GitHub's casing exactly."
  type        = string
  default     = "Bitsnbytes14/torii"
}

variable "create_github_oidc_provider" {
  description = "Create GitHub's OIDC provider in IAM. Set false if the account already has one (only one is allowed per account)."
  type        = bool
  default     = true
}
