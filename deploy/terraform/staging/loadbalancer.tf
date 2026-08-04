# One hostname serves both the API and the portal, so the browser sees a
# single origin: no CORS, and the SameSite=Lax session cookies keep working
# unchanged. The split reproduces deploy/workstation/Caddyfile, which is the
# tested specification of this routing.

# The portal is served by its own Cloud Run service rather than a public
# object-storage bucket. A backend bucket requires the objects to be readable
# by allUsers, which this organization's domain-restricted-sharing policy
# forbids; rather than punch a hole in an org-wide control for a staging
# environment, the static files ship in a small Caddy image.
#
# It is also the better shape: the SPA fallback (/join and /bootstrap are
# client-side routes, not files) is served natively by try_files instead of
# leaning on a load-balancer 404 rewrite, and the service scales to zero.
resource "google_cloud_run_v2_service" "web" {
  name     = "team-memory-web"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"

  template {
    scaling {
      min_instance_count = 0
      max_instance_count = 2
    }

    containers {
      image = var.web_image != "" ? var.web_image : "us-docker.pkg.dev/cloudrun/container/hello"
      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }
    }
  }

  lifecycle {
    # Same rationale as the API service: CI owns the image, and the
    # service-level scaling block is API-populated.
    ignore_changes = [template[0].containers[0].image, client, client_version, scaling]
  }
}

resource "google_compute_region_network_endpoint_group" "web" {
  name                  = "team-memory-web"
  region                = var.region
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = google_cloud_run_v2_service.web.name
  }
}

resource "google_compute_backend_service" "web" {
  name                  = "team-memory-web"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  protocol              = "HTTPS"
  security_policy       = google_compute_security_policy.cloudflare_only.id
  enable_cdn            = true

  backend {
    group = google_compute_region_network_endpoint_group.web.id
  }

  # Caddy sets immutable on hashed assets and no-cache on the entry
  # document, so the CDN follows the origin rather than a blanket TTL.
  cdn_policy {
    cache_mode = "USE_ORIGIN_HEADERS"

    # Static assets are identical for everyone; keeping query strings and
    # cookies out of the cache key stops per-visitor cache fragmentation.
    cache_key_policy {
      include_host         = true
      include_protocol     = true
      include_query_string = false
    }
  }
}

resource "google_compute_region_network_endpoint_group" "api" {
  name                  = "team-memory-api"
  region                = var.region
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = google_cloud_run_v2_service.api.name
  }
}

resource "google_compute_backend_service" "api" {
  name                  = "team-memory-api"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  protocol              = "HTTPS"
  security_policy       = google_compute_security_policy.cloudflare_only.id

  backend {
    group = google_compute_region_network_endpoint_group.api.id
  }

  log_config {
    enable      = true
    sample_rate = 1.0
  }
}

# The whole routing contract: the API owns /v1 and the two probes, the
# portal owns everything else. The portal's own server handles the SPA
# fallback, so the load balancer needs no error-response rewriting.
resource "google_compute_url_map" "main" {
  name            = "team-memory"
  default_service = google_compute_backend_service.web.id

  host_rule {
    hosts        = [var.host]
    path_matcher = "main"
  }

  path_matcher {
    name            = "main"
    default_service = google_compute_backend_service.web.id

    path_rule {
      paths   = ["/v1", "/v1/*", "/healthz", "/readyz"]
      service = google_compute_backend_service.api.id
    }
  }
}

resource "google_compute_target_https_proxy" "main" {
  name    = "team-memory"
  url_map = google_compute_url_map.main.id
  # The proxy wants a fully qualified service reference, not the bare
  # resource path Terraform exposes as .id.
  certificate_map = "//certificatemanager.googleapis.com/${google_certificate_manager_certificate_map.main.id}"
}

resource "google_compute_global_address" "main" {
  name = "team-memory"
}

resource "google_compute_global_forwarding_rule" "https" {
  name                  = "team-memory-https"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_address            = google_compute_global_address.main.id
  port_range            = "443"
  target                = google_compute_target_https_proxy.main.id
}

# Cloudflare terminates TLS and proxies to this address. Restricting the
# origin to Cloudflare's ranges is what stops anyone reaching the backend
# directly and skipping the WAF.
resource "google_compute_security_policy" "cloudflare_only" {
  name = "cloudflare-only"

  # Cloud Armor allows at most ten ranges per rule, so the list is chunked.
  dynamic "rule" {
    for_each = chunklist(var.cloudflare_ipv4_ranges, 10)
    content {
      action   = "allow"
      priority = 1000 + rule.key
      match {
        versioned_expr = "SRC_IPS_V1"
        config {
          src_ip_ranges = rule.value
        }
      }
      description = "Cloudflare edge range chunk ${rule.key}"
    }
  }

  rule {
    action   = "deny(403)"
    priority = 2147483647
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
    description = "Default deny: only Cloudflare may reach the origin"
  }
}
