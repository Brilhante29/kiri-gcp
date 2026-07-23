# ==============================================================================
# kiri Terraform HCL Integration Example
# ==============================================================================
# Directs Terraform GCP resources to local kiri emulator endpoints without
# needing GCP credentials or real cloud accounts.
# ==============================================================================

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project      = "local-terraform-project"
  region       = "us-central1"
  zone         = "us-central1-a"
  access_token = "dummy"

  # Override custom endpoints to point to local kiri instance (:4443)
  storage_custom_endpoint        = "http://localhost:4443/storage/v1/"
  pubsub_custom_endpoint         = "http://localhost:4443/v1/"
  secret_manager_custom_endpoint = "http://localhost:4443/v1/"
  kms_custom_endpoint            = "http://localhost:4443/v1/"
  dns_custom_endpoint            = "http://localhost:4443/dns/v1/"
  cloud_run_custom_endpoint      = "http://localhost:4443/apis/serving.knative.dev/v1/"
}

# Define local test infrastructure
resource "google_storage_bucket" "app_storage" {
  name     = "tf-local-app-bucket"
  location = "US"
}

resource "google_pubsub_topic" "events_topic" {
  name = "tf-local-events-topic"
}

resource "google_secret_manager_secret" "database_password" {
  secret_id = "tf-local-db-password"
  replication {
    user_managed {
      replicas {
        location = "us-central1"
      }
    }
  }
}
