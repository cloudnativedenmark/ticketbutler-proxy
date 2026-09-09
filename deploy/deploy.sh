#!/usr/bin/env bash
#
# Deploy a published image to Cloud Run.
#
#   ./deploy/deploy.sh sha256:1a2b3c...    # preferred
#   ./deploy/deploy.sh sha-9f8e7d6         # works, with a caveat, see below
#
# Every command is echoed before it runs, so a transcript of this script is also
# the documentation for what the service is configured with.
set -euo pipefail

IMAGE_REF="${1:-}"
if [ -z "$IMAGE_REF" ]; then
  cat >&2 <<'USAGE'
usage: deploy/deploy.sh <digest|tag>

Takes the image digest (sha256:...) or a tag from the release workflow's run
summary. There is no default: deploying "whatever latest points at" is how you
end up unsure which code is running.
USAGE
  exit 2
fi

PROJECT="${PROJECT:-automation-472811}"
REGION="${REGION:-europe-north1}"
SERVICE="${SERVICE:-tbproxy}"
IMAGE="${IMAGE:-ghcr.io/cloudnativedenmark/ticketbutler-proxy}"

RUNTIME_SA="${RUNTIME_SA:-tbproxy-run@${PROJECT}.iam.gserviceaccount.com}"
BUCKET="${BUCKET:-${PROJECT}-tbproxy-snapshots}"

# Secret Manager secret names, created by deploy/setup.sh.
SECRET_TICKETBUTLER_TOKEN="${SECRET_TICKETBUTLER_TOKEN:-ticketbutler-token}"
SECRET_API_TOKENS="${SECRET_API_TOKENS:-tbproxy-api-tokens}"

# Non-secret configuration. These are the names internal/config reads.
#
# The event UUID and the sponsor ticket type id both change per conference year,
# and both are defaulted here rather than prompted for or kept in Secret Manager
# because neither is a credential: knowing them gets you nothing without the
# TicketButler API token, which the orders endpoint still requires. Update both
# when the next event's tickets go on sale.
# Unset falls back to the default; explicitly empty does not, so exporting an
# empty value is caught below rather than quietly deploying last year's event.
EVENT_UUID="${TICKETBUTLER_EVENT_UUID-11cc0a9f7f124ddcb11bc027e1a64f23}"
BASE_URL="${TICKETBUTLER_BASE_URL:-https://cloudnativedenmark.ticketbutler.io}"
SNAPSHOT_OBJECT="${SNAPSHOT_OBJECT:-snapshot.json.gz}"
UPSTREAM_TIMEOUT="${UPSTREAM_TIMEOUT:-10m}"
STALE_AFTER="${STALE_AFTER:-90m}"
# Empty here would leave /v1/sponsors reporting nothing, with no error to explain
# why, so the current event's community sponsor ticket type is the default.
SPONSOR_TICKET_TYPE_PKS="${SPONSOR_TICKET_TYPE_PKS:-183067}"
MERCH_NAME_PATTERNS="${MERCH_NAME_PATTERNS:-hoodie}"

if [ -z "$EVENT_UUID" ]; then
  echo "TICKETBUTLER_EVENT_UUID is empty. Find it in the TicketButler event URL," >&2
  echo "or read it off the running service:" >&2
  echo "  gcloud run services describe $SERVICE --project=$PROJECT --region=$REGION \\" >&2
  echo "    --format='value(spec.template.spec.containers[0].env)'" >&2
  exit 2
fi

case "$IMAGE_REF" in
  sha256:*)
    IMAGE_URI="${IMAGE}@${IMAGE_REF}"
    ;;
  *)
    IMAGE_URI="${IMAGE}:${IMAGE_REF}"
    cat >&2 <<'WARNING'

Warning: deploying a mutable tag.

Cloud Run caches images pulled from public third-party registries such as GHCR
for up to an hour. If that tag has since been repointed, Cloud Run may create a
revision from the cached older image and report success, leaving code you did
not deploy in production, with nothing in the deploy output to say so.

Pass the sha256: digest from the release workflow's run summary instead.

WARNING
    ;;
esac

# The service is reachable without IAM authentication on purpose.
#
# Google Apps Script's UrlFetchApp cannot mint a Google-signed ID token for this
# service, so --no-allow-unauthenticated would lock out the only client. Instead
# the service checks a bearer token from API_TOKENS on every request and refuses
# to start when that variable is empty (see internal/config). Authentication is
# enforced, just in the process rather than by Cloud Run's front end.
cat <<NOTE

Service:  $SERVICE ($REGION, project $PROJECT)
Image:    $IMAGE_URI
Identity: $RUNTIME_SA
Access:   --allow-unauthenticated, deliberately. Apps Script cannot present a
          Google ID token, so the service authenticates callers itself against
          API_TOKENS and rejects anything else with 401.

NOTE

run() {
  printf '+ %s\n' "$*"
  "$@"
}

# SPONSOR_TICKET_TYPE_PKS and MERCH_NAME_PATTERNS are themselves comma-separated
# lists, and --set-env-vars splits on commas by default, so a second pattern would
# be read as the start of another variable. gcloud's ^delim^ prefix picks a
# different separator; @ does not occur in any of these values.
env_vars=()
add_env() {
  if [ -n "${2:-}" ]; then
    env_vars+=("$1=$2")
  fi
}
add_env TICKETBUTLER_EVENT_UUID "$EVENT_UUID"
add_env TICKETBUTLER_BASE_URL "$BASE_URL"
add_env SNAPSHOT_BUCKET "$BUCKET"
add_env SNAPSHOT_OBJECT "$SNAPSHOT_OBJECT"
add_env UPSTREAM_TIMEOUT "$UPSTREAM_TIMEOUT"
add_env STALE_AFTER "$STALE_AFTER"
add_env SPONSOR_TICKET_TYPE_PKS "$SPONSOR_TICKET_TYPE_PKS"
add_env MERCH_NAME_PATTERNS "$MERCH_NAME_PATTERNS"
ENV_VARS="^@^$(IFS=@; printf '%s' "${env_vars[*]}")"

run gcloud run deploy "$SERVICE" \
  --project="$PROJECT" \
  --region="$REGION" \
  --image="$IMAGE_URI" \
  --service-account="$RUNTIME_SA" \
  --allow-unauthenticated \
  --port=8080 \
  --timeout=900 \
  --min-instances=0 \
  --max-instances=1 \
  --no-cpu-throttling \
  --memory=512Mi \
  --set-env-vars="$ENV_VARS" \
  --set-secrets="TICKETBUTLER_TOKEN=${SECRET_TICKETBUTLER_TOKEN}:latest,API_TOKENS=${SECRET_API_TOKENS}:latest"

# max-instances=1 keeps concurrent refreshes from fighting over the same snapshot
# object, and one instance is ample for a handful of Apps Script calls a minute.
# no-cpu-throttling lets a refresh that outlives its triggering request keep the
# CPU it needs to finish.

run gcloud run services describe "$SERVICE" \
  --project="$PROJECT" \
  --region="$REGION" \
  --format='value(status.url)'

cat <<'NEXT'

Next:
  - deploy/scheduler.sh, if the Cloud Scheduler job does not exist yet or its
    URL changed.
  - Check the snapshot is fresh:
      curl -sH "Authorization: Bearer $API_TOKEN" "$SERVICE_URL/v1/summary"

NEXT
