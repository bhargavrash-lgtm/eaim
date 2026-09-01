# eami-backup — scheduled Postgres backups (B-029) + optional offsite
# transport (B-144).
#
# Base image already carries pg_dump/pg_restore/psql (needed for
# backup-db.sh/restore-db.sh) plus Alpine's busybox crond (B-029). This
# adds the two tools the optional offsite step needs:
#   - age:    encrypts the local backup with a customer-supplied public
#             key before it ever leaves the host (B-144) — EAMI's own
#             process never holds the private key.
#   - rclone: transports the encrypted file to a customer-configured
#             remote (S3-compatible or SFTP, one config format for both)
#             using only the customer's own credentials. Chosen over
#             restic specifically because restic owns its own repository
#             encryption/format, which would conflict with the
#             customer-supplied-key requirement above; rclone is a
#             single stateless "copy this file to that remote" binary
#             with no competing encryption or repository state of its
#             own — see B-144's investigation for the full reasoning.
#
# Both packages confirmed present in this exact base image's apk index
# before adding this Dockerfile (age 1.3.1, rclone 1.74.1 on Alpine
# 3.24.1) — not assumed.
FROM postgres:16-alpine

RUN apk add --no-cache age rclone
