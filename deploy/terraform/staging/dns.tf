# Cloudflare proxies the hostname (TLS, WAF, DDoS) and talks to the load
# balancer over its own TLS connection, so the origin needs a real
# certificate of its own: Cloudflare's "Full (strict)" mode validates it.
#
# DNS authorization works while the record is proxied, because it validates
# through a separate CNAME rather than by reaching the host.

data "cloudflare_zone" "main" {
  name = var.cloudflare_zone
}

resource "google_certificate_manager_dns_authorization" "main" {
  name   = "team-memory-stg"
  domain = var.host
}

resource "cloudflare_record" "certificate_authorization" {
  zone_id = data.cloudflare_zone.main.id
  name    = google_certificate_manager_dns_authorization.main.dns_resource_record[0].name
  type    = google_certificate_manager_dns_authorization.main.dns_resource_record[0].type
  content = google_certificate_manager_dns_authorization.main.dns_resource_record[0].data
  # Validation records must never be proxied, or the CA cannot read them.
  proxied = false
  ttl     = 300
  comment = "Google Certificate Manager DNS authorization for ${var.host}"
}

resource "google_certificate_manager_certificate" "main" {
  name = "team-memory-stg"

  managed {
    domains            = [var.host]
    dns_authorizations = [google_certificate_manager_dns_authorization.main.id]
  }

  depends_on = [cloudflare_record.certificate_authorization]
}

# A load balancer cannot reference a Certificate Manager certificate
# directly; it attaches a map, and the map routes each SNI hostname to a
# certificate.
resource "google_certificate_manager_certificate_map" "main" {
  name = "team-memory-stg"
}

resource "google_certificate_manager_certificate_map_entry" "main" {
  name         = "team-memory-stg"
  map          = google_certificate_manager_certificate_map.main.name
  certificates = [google_certificate_manager_certificate.main.id]
  hostname     = var.host
}

resource "cloudflare_record" "host" {
  zone_id = data.cloudflare_zone.main.id
  name    = trimsuffix(var.host, ".${var.cloudflare_zone}")
  type    = "A"
  content = google_compute_global_address.main.address
  # Proxied is the whole point: DDoS protection and the WAF sit here, and
  # Cloud Armor below only admits Cloudflare's ranges.
  proxied = true
  comment = "Team Memory staging"
}
