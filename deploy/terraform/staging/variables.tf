variable "project_id" {
  type        = string
  description = "GCP project hosting the staging environment."
  default     = "paxtech-stg"
}

variable "region" {
  type        = string
  description = "Region for every regional resource. Tokyo per the hosting ADR."
  default     = "asia-northeast1"
}

variable "host" {
  type        = string
  description = <<-EOT
    Public hostname for the environment. A single-level subdomain keeps it
    inside Cloudflare's universal certificate; a second level would require
    Advanced Certificate Manager.
  EOT
  default     = "nexus-stg.paxtech.net"
}

variable "cloudflare_zone" {
  type        = string
  description = "Cloudflare zone the host record belongs to."
  default     = "paxtech.net"
}

variable "database_tier" {
  type        = string
  description = <<-EOT
    Cloud SQL machine type. A shared-core tier is enough for staging, but
    db-f1-micro's 0.6 GB leaves too little room for pgvector index builds, so
    the small tier is the floor.
  EOT
  default     = "db-g1-small"
}

variable "image" {
  type        = string
  description = <<-EOT
    Fully qualified application image. Empty on the first apply, because the
    repository that holds it is created by this configuration; set it once CI
    (or a manual build) has pushed a tag.
  EOT
  default     = ""
}

variable "cloudflare_ipv4_ranges" {
  type        = list(string)
  description = <<-EOT
    Cloudflare's published edge ranges (https://www.cloudflare.com/ips-v4).
    Cloud Armor allows only these to reach the load balancer, so nobody can
    bypass the WAF by hitting the anycast address directly. Refresh when
    Cloudflare publishes a change.
  EOT
  default = [
    "173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
    "141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
    "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
    "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
  ]
}

variable "oidc_issuer" {
  type        = string
  description = <<-EOT
    OIDC issuer URL. For WorkOS AuthKit this is the user-management URL for
    the client, and it must match the discovery document's own issuer field
    exactly, because go-oidc rejects a mismatch at startup.
  EOT
}

variable "oidc_client_id" {
  type        = string
  description = "OIDC client ID. Not a secret; the client secret lives in Secret Manager."
}

variable "extractor_base_url" {
  type        = string
  description = "OpenAI-compatible extraction API base URL."
  default     = "https://api.deepseek.com"
}

variable "extractor_model" {
  type        = string
  description = "Extraction model."
  default     = "deepseek-v4-flash"
}

variable "web_image" {
  type        = string
  description = <<-EOT
    Static portal image (Dockerfile target "web"). Empty on the first apply,
    for the same bootstrap reason as var.image.
  EOT
  default     = ""
}

variable "oidc_authorization_parameters" {
  type        = string
  description = <<-EOT
    Extra name=value pairs appended to the OIDC authorization request.
    WorkOS AuthKit requires provider=authkit to select an authentication
    method; without it the authorize endpoint rejects the request as an
    invalid connection selector and login never reaches the sign-in page.
  EOT
  default     = "provider=authkit"
}
