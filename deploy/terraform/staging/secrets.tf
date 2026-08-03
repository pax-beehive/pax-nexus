# Secrets split into two kinds.
#
# Generated: values this configuration mints, so they exist before the first
# deploy and never need a human to invent them. They do land in Terraform
# state, which is why the state bucket is private and versioned.
#
# External: values that come from another system (WorkOS, DeepSeek). Only the
# container is declared here; versions are added out of band with
#   gcloud secrets versions add <name> --data-file=-
# so those values never enter state at all.

locals {
  generated_secrets = {
    # At least 32 characters. Rotating it invalidates every issued credential.
    secret-pepper = random_password.secret_pepper.result
    # Single-use claim for the first workspace owner.
    bootstrap-secret = random_password.bootstrap_secret.result
    # Seals the short-lived OIDC flow cookie. Every instance must share it,
    # or a login that starts on one instance cannot finish on another.
    oidc-flow-secret = random_password.oidc_flow_secret.result
    database-url     = local.database_url
  }

  external_secrets = [
    "oidc-client-secret", # WorkOS API key
    "extractor-api-key",  # DeepSeek
  ]

  # The Cloud Run socket integration exposes the instance as a unix socket
  # directory, which pgx accepts as the host.
  database_url = format(
    "postgres://%s:%s@/%s?host=/cloudsql/%s&pool_max_conns=8",
    google_sql_user.app.name,
    urlencode(random_password.database.result),
    google_sql_database.team_memory.name,
    google_sql_database_instance.main.connection_name,
  )
}

resource "random_password" "secret_pepper" {
  length  = 48
  special = false
}

resource "random_password" "bootstrap_secret" {
  length  = 48
  special = false
}

resource "random_password" "oidc_flow_secret" {
  length  = 48
  special = false
}

resource "google_secret_manager_secret" "generated" {
  for_each  = local.generated_secrets
  secret_id = each.key

  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }
}

resource "google_secret_manager_secret_version" "generated" {
  for_each    = local.generated_secrets
  secret      = google_secret_manager_secret.generated[each.key].id
  secret_data = each.value
}

resource "google_secret_manager_secret" "external" {
  for_each  = toset(local.external_secrets)
  secret_id = each.key

  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }
}

resource "google_secret_manager_secret_iam_member" "runtime_generated" {
  for_each  = google_secret_manager_secret.generated
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "runtime_external" {
  for_each  = google_secret_manager_secret.external
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}
