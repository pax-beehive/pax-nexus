terraform {
  required_version = ">= 1.9"

  backend "gcs" {
    bucket = "paxtech-stg-tfstate"
    prefix = "staging"
  }

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 4.40"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# Credentials come from CLOUDFLARE_API_TOKEN so the token never lands in a
# variable file or in state.
provider "cloudflare" {}
