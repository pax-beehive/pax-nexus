resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = "team-memory"
  format        = "DOCKER"
  description   = "Application images promoted by digest between environments."
}

resource "google_service_account" "runtime" {
  account_id   = "team-memory-runtime"
  display_name = "Team Memory Cloud Run runtime"
}

resource "google_project_iam_member" "runtime_sql_client" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.runtime.email}"
}

# One service, pinned to a single instance.
#
# Run starts every background loop unconditionally, and the Page Wiki
# consumer, audit consumer, maintenance loops, and operations maintenance
# hold no database lease, so a second instance would double-process and pay
# twice for the same LLM calls. Splitting into scalable api plus a pinned
# worker needs a mode switch and the rebuild state machine moved out of
# process memory; until then, one instance is the correct trade.
resource "google_cloud_run_v2_service" "api" {
  name     = "team-memory"
  location = var.region

  # The load balancer is the only path in, so Cloudflare cannot be bypassed.
  ingress = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"

  template {
    service_account = google_service_account.runtime.email

    scaling {
      min_instance_count = 1
      max_instance_count = 1
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.main.connection_name]
      }
    }

    containers {
      image = var.image != "" ? var.image : "us-docker.pkg.dev/cloudrun/container/hello"

      resources {
        limits = {
          cpu    = "1"
          memory = "1Gi"
        }
        # Background scan loops keep running between requests, so CPU must
        # stay allocated rather than throttling to zero after a response.
        cpu_idle          = false
        startup_cpu_boost = true
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      dynamic "env" {
        for_each = local.plain_environment
        content {
          name  = env.key
          value = env.value
        }
      }

      dynamic "env" {
        for_each = local.secret_environment
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      # Liveness stays on /healthz, which never touches Postgres, so a
      # database blip does not restart the container. Readiness gates traffic
      # on the store actually being reachable.
      startup_probe {
        http_get {
          path = "/readyz"
        }
        initial_delay_seconds = 10
        period_seconds        = 5
        failure_threshold     = 30
        timeout_seconds       = 5
      }

      liveness_probe {
        http_get {
          path = "/healthz"
        }
        period_seconds    = 30
        failure_threshold = 3
        timeout_seconds   = 5
      }
    }
  }

  depends_on = [
    google_secret_manager_secret_version.generated,
    google_secret_manager_secret_iam_member.runtime_generated,
    google_secret_manager_secret_iam_member.runtime_external,
  ]

  lifecycle {
    # CI deploys new revisions by digest; Terraform owns the shape of the
    # service, not which build is currently live.
    ignore_changes = [template[0].containers[0].image, client, client_version]
  }
}

locals {
  plain_environment = {
    TEAM_MEMORY_OIDC_ISSUER        = var.oidc_issuer
    TEAM_MEMORY_OIDC_CLIENT_ID     = var.oidc_client_id
    TEAM_MEMORY_OIDC_REDIRECT_URL  = "https://${var.host}/v1/auth/callback"
    TEAM_MEMORY_PORTAL_URL         = "https://${var.host}"
    TEAM_MEMORY_OIDC_AUTH_PARAMS   = var.oidc_authorization_parameters
    TEAM_MEMORY_EXTRACTOR_BASE_URL = var.extractor_base_url
    TEAM_MEMORY_EXTRACTOR_MODEL    = var.extractor_model
  }

  secret_environment = {
    TEAM_MEMORY_DATABASE_URL       = google_secret_manager_secret.generated["database-url"].secret_id
    TEAM_MEMORY_SECRET_PEPPER      = google_secret_manager_secret.generated["secret-pepper"].secret_id
    TEAM_MEMORY_BOOTSTRAP_SECRET   = google_secret_manager_secret.generated["bootstrap-secret"].secret_id
    TEAM_MEMORY_OIDC_FLOW_SECRET   = google_secret_manager_secret.generated["oidc-flow-secret"].secret_id
    TEAM_MEMORY_OIDC_CLIENT_SECRET = google_secret_manager_secret.external["oidc-client-secret"].secret_id
    TEAM_MEMORY_EXTRACTOR_API_KEY  = google_secret_manager_secret.external["extractor-api-key"].secret_id
  }
}
