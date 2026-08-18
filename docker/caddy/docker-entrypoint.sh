#!/bin/sh
# eami-proxy entrypoint (B-071) — decides the TLS certificate source before
# starting Caddy, then execs it directly (replaces this shell, so signals/
# `docker stop` reach Caddy correctly).
#
# Certificate precedence, checked at every container start (not just first
# boot — an admin can drop in a real cert and `docker compose restart
# eami-proxy` at any time, no rebuild needed):
#   1. A customer-supplied cert at /certs/custom/{fullchain,privkey}.pem
#      (bind-mounted from the data disk — see appliance/README.md's "TLS
#      certificates" section and eami-stack.sh's EAMI_CERT_DIR).
#   2. Otherwise, Caddy's own built-in `tls internal` — generates and
#      persists a self-signed cert via an internal CA with zero custom
#      crypto scripting here. Persisted across restarts via the caddy_data
#      volume (docker-compose.yml/.prod.yml), so it isn't regenerated (and
#      isn't a fresh, newly-untrusted cert) on every container restart.
set -eu

CERT_DIR=/certs/custom
GENERATED_CONFIG=/etc/caddy/Caddyfile.generated

# EAMI_PUBLIC_HOST: the appliance's real externally-reachable address, used
# ONLY to build the :80 -> :443 redirect target (never for anything
# security-relevant). Deliberately NOT derived from the client-supplied
# Host header (a security-review finding, B-071: reflecting an
# attacker-controlled Host into a redirect Location is a real, if narrow,
# open-redirect/phishing primitive) and NOT auto-detected inside this
# container (a container's own view of its address is its internal
# docker-network IP, not the host's real external one). Set from
# eami-stack.sh's own already-correctly-computed SERVER_IP (the same value
# GATEWAY_UI_BASE_URL uses, detected at the VM host level via `hostname
# -I`, not from inside a container) -- see docker-compose.prod.yml's
# eami-proxy environment. Falls back to "localhost" only if unset (e.g.
# local dev/testing).
PUBLIC_HOST="${EAMI_PUBLIC_HOST:-localhost}"

if [ -f "${CERT_DIR}/fullchain.pem" ] && [ -f "${CERT_DIR}/privkey.pem" ]; then
	echo "[eami-proxy] customer certificate found at ${CERT_DIR} -- using it"
	TLS_DIRECTIVE="tls ${CERT_DIR}/fullchain.pem ${CERT_DIR}/privkey.pem"
	# Bare ports (no host qualifier) -- a customer's real cert may be issued
	# for a DNS name that differs from PUBLIC_HOST's best-effort IP guess
	# (e.g. a real internal hostname), and TLS should accept connections
	# regardless of which host/SNI the client used to reach it; Caddy
	# presents whichever cert is configured either way.
	SITE_443=":443"
	SITE_8443=":8443"
	SITE_8888=":8888"
else
	echo "[eami-proxy] no customer certificate found at ${CERT_DIR} -- using Caddy's internal self-signed CA (tls internal)"
	TLS_DIRECTIVE="tls internal"
	# Host-qualified, unlike the customer-cert branch above -- a security
	# review finding caught that a bare-port site address gives Caddy's
	# internal issuer no fixed identifier to bind the self-signed cert's
	# SAN to, which is ambiguous (and can produce a hostname-mismatch error
	# rather than the expected click-through warning) for the common case
	# of a browser connecting to a raw IP, where RFC 6066 says clients
	# should send no SNI at all. Qualifying the address with PUBLIC_HOST
	# gives Caddy a concrete IP/name to issue the internal cert for.
	SITE_443="${PUBLIC_HOST}:443"
	SITE_8443="${PUBLIC_HOST}:8443"
	SITE_8888="${PUBLIC_HOST}:8888"
fi

# Three site blocks, matching the three real backend targets:
#   :443  -> eami-ui (the SPA + its own existing nginx proxy for /v1/* to
#            eami-api -- confirmed unmodified, zero changes needed there)
#   :8443 -> eami-gateway (a single port serving both the REST control API
#            and MCP SSE -- confirmed via cmd/gateway/main.go's mux
#            registration there is genuinely only one http.Server/listener,
#            not two; the old published 3000/7946 ports were dead, backed
#            by no listener at all, confirmed via repo-wide grep)
#   :8888 -> eami-collector (B-072) -- endpoint-agent report ingestion,
#            reachable across the customer's whole internal network (not
#            confined to the appliance's own docker network the way the
#            other three surfaces are), the gap B-071 explicitly disclosed
#            as still open. eami-collector's own dormant TLSCertPath/
#            TLSKeyPath fields (cmd/collector/main.go) stay unused -- this
#            reuses the exact same edge-termination pattern as the other
#            three, one CA/trust mechanism instead of a second parallel one.
#
# `admin off`: disables Caddy's admin API (unauthenticated by default,
# would otherwise be reachable on 2019 inside the container) -- nothing in
# this deployment needs runtime config changes via that API; all config
# changes go through this script + a container restart instead.
#
# X-Forwarded-For: intentionally NOT configured with `trusted_proxies` --
# Caddy is the outermost hop here (nothing sits in front of it). Confirmed
# directly against Caddy's own reverse_proxy source (not just assumed)
# during this brief's mandatory security review: with no trusted_proxies
# configured, Caddy OVERWRITES X-Forwarded-For entirely with only its own
# observed TCP-peer IP, regardless of what a client sent (one header,
# several comma-joined entries, or several repeated header lines) -- so
# eami-api's clientKey() (ratelimit.go) only ever sees the single IP Caddy
# itself observed. Taking the last comma-separated entry there is
# defense-in-depth on top of that, not the only thing preventing spoofing.
cat > "$GENERATED_CONFIG" <<EOF
{
	admin off
}

# Explicit redirect rather than relying on Caddy's automatic-HTTPS
# hostname-based redirect feature, which doesn't apply here -- these site
# blocks use bare ports (:443/:8443), not hostnames, so that automatic
# behavior never triggers. No content is served over plaintext; this is
# only ever a 308 with a Location header, never a request body/credential.
# Target is the fixed PUBLIC_HOST value above, NOT {host} -- a security
# review finding caught reflecting the client-supplied Host header here
# verbatim, a real open-redirect primitive (an attacker-chosen Host would
# have been echoed straight into the Location header).
:80 {
	redir https://${PUBLIC_HOST}{uri} 308
}

${SITE_443} {
	${TLS_DIRECTIVE}
	reverse_proxy eami-ui:80
}

${SITE_8443} {
	${TLS_DIRECTIVE}
	reverse_proxy eami-gateway:8080
}

${SITE_8888} {
	${TLS_DIRECTIVE}
	reverse_proxy eami-collector:8888
}
EOF

exec caddy run --config "$GENERATED_CONFIG" --adapter caddyfile
