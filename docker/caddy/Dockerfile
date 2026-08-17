# eami-proxy — TLS-terminating reverse proxy (B-071)
#
# Fronts eami-ui (:443, which already proxies /v1/* to eami-api internally
# via its own nginx.conf -- unmodified) and eami-gateway (:8443, one port
# serving both the REST control API and MCP SSE). See docker-entrypoint.sh
# for the certificate-selection logic (customer-supplied vs Caddy's
# built-in self-signed internal CA).
FROM caddy:2-alpine

COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

ENTRYPOINT ["/docker-entrypoint.sh"]
