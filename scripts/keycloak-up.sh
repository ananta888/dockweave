#!/usr/bin/env bash
# Start the bundled DockWeave Keycloak (realm import) and print the OIDC
# settings to pass to `dockweave web`.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
KC_DIR="$HERE/deployments/keycloak"

cd "$KC_DIR"
docker compose up -d --wait

ISSUER="http://127.0.0.1:${KEYCLOAK_PORT:-8080}/realms/dockweave"
echo
echo "Keycloak läuft. Issuer: $ISSUER"
echo
echo "dockweave web mit OIDC starten:"
echo
echo "  DOCKWEAVE_OIDC_ISSUER=$ISSUER \\"
echo "  DOCKWEAVE_OIDC_CLIENT_ID=dockweave \\"
echo "  DOCKWEAVE_OIDC_CLIENT_SECRET=dockweave-dev-secret \\"
echo "  DOCKWEAVE_OIDC_ADMIN_GROUPS=dockweave-admin \\"
echo "  DOCKWEAVE_OIDC_MONITOR_GROUPS=dockweave-monitor \\"
echo "  dockweave web --listen 127.0.0.1:8790"
echo
echo "Demo-User: admin/admin (dockweave-admin) · monitor/monitor (dockweave-monitor)"
