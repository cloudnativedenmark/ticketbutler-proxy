#!/usr/bin/env bash
#
# One-time project bootstrap for the TicketButler proxy: APIs, snapshot bucket,
# service accounts, IAM and secrets.
#
#   ./deploy/setup.sh
#
# Safe to re-run. Everything it creates is checked for first, and the secret
# prompts can be skipped with an empty line, so re-running does not overwrite a
# token you already stored.
set -euo pipefail

PROJECT="${PROJECT:-automation-472811}"
REGION="${REGION:-europe-north1}"
SERVICE="${SERVICE:-tbproxy}"

BUCKET="${BUCKET:-${PROJECT}-tbproxy-snapshots}"
RUNTIME_SA_ID="${RUNTIME_SA_ID:-tbproxy-run}"
SCHEDULER_SA_ID="${SCHEDULER_SA_ID:-tbproxy-scheduler}"
RUNTIME_SA="${RUNTIME_SA_ID}@${PROJECT}.iam.gserviceaccount.com"
SCHEDULER_SA="${SCHEDULER_SA_ID}@${PROJECT}.iam.gserviceaccount.com"

SECRET_TICKETBUTLER_TOKEN="${SECRET_TICKETBUTLER_TOKEN:-ticketbutler-token}"
SECRET_API_TOKENS="${SECRET_API_TOKENS:-tbproxy-api-tokens}"

run() {
  printf '+ %s\n' "$*"
  "$@"
}

echo "Project $PROJECT, region $REGION"
echo

# --- APIs ------------------------------------------------------------------

# Enabling an already-enabled API is a no-op, so this needs no existence check.
run gcloud services enable \
  run.googleapis.com \
  cloudscheduler.googleapis.com \
  storage.googleapis.com \
  secretmanager.googleapis.com \
  --project="$PROJECT"

# --- Snapshot bucket -------------------------------------------------------

# The snapshot holds attendee names and email addresses, so the bucket is created
# with public access prevention on and no ACLs. Uniform access means the only way
# in is the IAM binding granted below.
if gcloud storage buckets describe "gs://${BUCKET}" --project="$PROJECT" >/dev/null 2>&1; then
  echo "bucket gs://${BUCKET} exists"
else
  run gcloud storage buckets create "gs://${BUCKET}" \
    --project="$PROJECT" \
    --location="$REGION" \
    --uniform-bucket-level-access \
    --public-access-prevention
fi

# --- Service accounts ------------------------------------------------------

create_sa() {
  local id="$1" display="$2" email="${1}@${PROJECT}.iam.gserviceaccount.com"
  if gcloud iam service-accounts describe "$email" --project="$PROJECT" >/dev/null 2>&1; then
    echo "service account $email exists"
  else
    run gcloud iam service-accounts create "$id" \
      --project="$PROJECT" \
      --display-name="$display"
  fi
}

create_sa "$RUNTIME_SA_ID" "TicketButler proxy runtime"
create_sa "$SCHEDULER_SA_ID" "TicketButler proxy scheduler"

# Scoped to this one bucket rather than granted at project level: the service
# reads and writes exactly one object, and a project-wide storage role would let a
# compromised revision read every other bucket in the project.
run gcloud storage buckets add-iam-policy-binding "gs://${BUCKET}" \
  --project="$PROJECT" \
  --member="serviceAccount:${RUNTIME_SA}" \
  --role=roles/storage.objectAdmin

# --- Secrets ---------------------------------------------------------------

# Prompts are silent and the values go to gcloud on stdin, so no token reaches
# the shell history or the process list.
create_secret() {
  local name="$1" prompt="$2" value=""

  if ! gcloud secrets describe "$name" --project="$PROJECT" >/dev/null 2>&1; then
    run gcloud secrets create "$name" \
      --project="$PROJECT" \
      --replication-policy=automatic
  else
    echo "secret $name exists"
  fi

  printf '%s (empty to keep the current value): ' "$prompt" >&2
  read -rs value
  printf '\n' >&2
  if [ -z "$value" ]; then
    echo "  unchanged"
  else
    printf '%s' "$value" | run gcloud secrets versions add "$name" \
      --project="$PROJECT" \
      --data-file=-
  fi

  run gcloud secrets add-iam-policy-binding "$name" \
    --project="$PROJECT" \
    --member="serviceAccount:${RUNTIME_SA}" \
    --role=roles/secretmanager.secretAccessor
}

create_secret "$SECRET_TICKETBUTLER_TOKEN" "TicketButler API token"
create_secret "$SECRET_API_TOKENS" "API_TOKENS (comma-separated, 16+ chars each)"

# --- Scheduler invoker -----------------------------------------------------

# The service runs with --allow-unauthenticated because Apps Script cannot present
# a Google ID token, so run.invoker is not what lets Cloud Scheduler in today. It
# is granted anyway: the scheduler job sends an OIDC token, and the binding is
# what keeps the refresh working if the service is ever locked down at the IAM
# layer.
if gcloud run services describe "$SERVICE" --project="$PROJECT" --region="$REGION" >/dev/null 2>&1; then
  run gcloud run services add-iam-policy-binding "$SERVICE" \
    --project="$PROJECT" \
    --region="$REGION" \
    --member="serviceAccount:${SCHEDULER_SA}" \
    --role=roles/run.invoker
else
  echo
  echo "Cloud Run service $SERVICE does not exist yet, so run.invoker cannot be"
  echo "bound to it. Deploy first, then re-run this script (or run just that one"
  echo "binding) to grant it."
fi

cat <<NEXT

Done. Next:
  1. deploy/deploy.sh <digest>   deploy the image
  2. deploy/setup.sh             again, to bind run.invoker now the service exists
  3. deploy/scheduler.sh         create the refresh job

Bucket:      gs://${BUCKET}
Runtime SA:  ${RUNTIME_SA}
Scheduler SA:${SCHEDULER_SA}

NEXT
