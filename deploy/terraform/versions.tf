terraform {
  required_version = ">= 1.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }

  # State stays local (and gitignored). An S3 backend would add a bucket and
  # a lock table to the bill for a single-operator project that doesn't need
  # shared state or locking.
}

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project   = "torii"
      ManagedBy = "terraform"
    }
  }
}
