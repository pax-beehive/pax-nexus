# Cloud SQL reached through the Cloud Run socket integration rather than a
# VPC connector: Cloud Run mounts a Google-managed proxy socket, so the
# instance needs no VPC peering and no authorized networks, and nothing can
# connect without IAM. That is both cheaper and less machinery than a private
# IP plus connector, which is what the hosting ADR assumed.
resource "google_sql_database_instance" "main" {
  name             = "team-memory-stg"
  database_version = "POSTGRES_17"
  region           = var.region

  settings {
    # Enterprise Plus is the API default and rejects shared-core tiers, which
    # are the only ones priced sensibly for staging.
    edition           = "ENTERPRISE"
    tier              = var.database_tier
    availability_type = "ZONAL"
    disk_size         = 10
    disk_type         = "PD_SSD"
    disk_autoresize   = true

    # pgvector ships with Cloud SQL; the extension is created by the
    # application's own migrations.
    database_flags {
      name  = "cloudsql.iam_authentication"
      value = "on"
    }

    backup_configuration {
      enabled                        = true
      start_time                     = "18:00" # 03:00 JST
      point_in_time_recovery_enabled = true
      transaction_log_retention_days = 7
    }

    ip_configuration {
      # No public network is authorized. The only path in is the Cloud Run
      # socket, which authenticates with the runtime service account.
      ipv4_enabled = true
      ssl_mode     = "ENCRYPTED_ONLY"
    }

    maintenance_window {
      day  = 7  # Sunday
      hour = 19 # 04:00 JST Monday
    }
  }

  # Staging is disposable, but an accidental `terraform destroy` should still
  # not be the thing that loses the dogfooding corpus.
  deletion_protection = true
}

resource "google_sql_database" "team_memory" {
  name     = "team_memory"
  instance = google_sql_database_instance.main.name
}

resource "random_password" "database" {
  length  = 32
  special = false
}

resource "google_sql_user" "app" {
  name     = "team_memory"
  instance = google_sql_database_instance.main.name
  password = random_password.database.result
}
