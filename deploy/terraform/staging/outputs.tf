output "url" {
  description = "Public entrypoint."
  value       = "https://${var.host}"
}

output "load_balancer_address" {
  description = "Origin address Cloudflare proxies to."
  value       = google_compute_global_address.main.address
}

output "image_repository" {
  description = "Artifact Registry path images are pushed to."
  value = format(
    "%s-docker.pkg.dev/%s/%s",
    var.region, var.project_id, google_artifact_registry_repository.images.repository_id,
  )
}

output "cloud_run_service" {
  description = "Service name CI deploys new revisions to."
  value       = google_cloud_run_v2_service.api.name
}

output "database_connection_name" {
  description = "Cloud SQL connection name for the migration job and the socket mount."
  value       = google_sql_database_instance.main.connection_name
}

output "web_service" {
  description = "Cloud Run service serving the portal."
  value       = google_cloud_run_v2_service.web.name
}

output "runtime_service_account" {
  description = "Service account both the service and the migration job run as."
  value       = google_service_account.runtime.email
}

output "pending_external_secrets" {
  description = <<-EOT
    Secrets whose values this configuration deliberately does not hold. Add a
    version to each before the first deploy:
      gcloud secrets versions add <name> --data-file=-
  EOT
  value       = [for secret in google_secret_manager_secret.external : secret.secret_id]
}
