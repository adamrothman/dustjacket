#!/bin/sh
# Refreshes the schema snapshot hardcover_docs serves from Hardcover's
# docs repository (MIT; its license travels with the file). Run from the
# repository root, then `go test ./internal/reference/`: it fails if a
# guide's examples no longer validate against the new schema.
set -eu
base=https://raw.githubusercontent.com/hardcoverapp/hardcover-docs/main
curl -fsSL "$base/schema.graphql" -o internal/reference/schema.graphql
curl -fsSL "$base/LICENSE.md" -o internal/reference/SCHEMA_LICENSE.md
