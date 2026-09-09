#!/usr/bin/env bash
#
# Create or update the Cloud Scheduler job that refreshes the snapshot.
#
#   ./deploy/scheduler.sh
#
# The job POSTs to /v1/refresh every thirty minutes. The refresh is the slow part;
# Apps Script only ever reads the finished snapshot.
#
# Thirty rather than ten: ticket sales do not move fast enough for the difference to
# matter on a spreadsheet, and the orders endpoint is currently struggling enough to
# spend two minutes on a request before Cloudflare gives up on it. Asking less often
# is the considerate default. Lower it with SCHEDULE= if the upstream recovers and
# fresher numbers actually turn out to be useful.
set -euo pipefail

PROJECT="${PROJECT:-automation-472811}"
REGION="${REGION:-europe-north1}"
SERVICE="${SERVICE:-tbproxy}"
JOB="${JOB:-tbproxy-refresh}"
SCHEDULE="${SCHEDULE:-*/30 * * * *}"

SCHEDULER_SA="${SCHEDULER_SA:-tbproxy-scheduler@${PROJECT}.iam.gserviceaccount.com}"
SECRET_API_TOKENS="${SECRET_API_TOKENS:-tbproxy-api-tokens}"

run() {
  printf '+ %s\n' "$*"
  "$@"
}

if [ -z "${SERVICE_URL:-}" ]; then
  SERVICE_URL=$(gcloud run services describe "$SERVICE" \
    --project="$PROJECT" --region="$REGION" --format='value(status.url)')
fi
if [ -z "$SERVICE_URL" ]; then
  echo "could not determine the service URL; deploy the service first" >&2
  exit 1
fi
URI="${SERVICE_URL}/v1/refresh"

# The service authenticates callers against API_TOKENS, so the job has to carry a
# token like any other client. Taking the first entry of the secret means
# a rotation that appends a new token keeps working, and the old token can be
# dropped once this job has been pointed at the new one.
if [ -z "${API_TOKEN:-}" ]; then
  API_TOKEN=$(gcloud secrets versions access latest \
    --secret="$SECRET_API_TOKENS" --project="$PROJECT" | cut -d, -f1 | tr -d '[:space:]')
fi
if [ -z "$API_TOKEN" ]; then
  echo "no API token available from secret $SECRET_API_TOKENS" >&2
  exit 1
fi

# Cloud Scheduler puts its OIDC token in the Authorization header, which is where a
# bearer token would otherwise go. The service therefore also accepts its own token
# on X-Api-Key, and checks both headers, so the two can be sent together: this job
# always carries the service token on X-Api-Key, and adds an OIDC token when
# SCHEDULER_OIDC=1.
#
# On a service deployed with --allow-unauthenticated, which is how it has to be
# deployed while Apps Script is a client, that OIDC token changes nothing: nothing
# checks it. It is still worth setting, because it makes this job already correct for
# a service running with --no-allow-unauthenticated, where Cloud Run IAM rejects
# unauthenticated callers before the request reaches the process. setup.sh grants
# roles/run.invoker to the scheduler service account for exactly that day.
#
# Apps Script is what stands in the way: UrlFetchApp cannot mint a Google ID token
# for the Cloud Run audience, so the service cannot be locked down while the
# spreadsheets call it directly.
AUTH_ARGS=()
if [ "${SCHEDULER_OIDC:-0}" = "1" ]; then
  AUTH_ARGS+=(--oidc-service-account-email="$SCHEDULER_SA" --oidc-token-audience="$SERVICE_URL")
  echo "auth: X-Api-Key from secret $SECRET_API_TOKENS, plus OIDC as $SCHEDULER_SA"
else
  AUTH_ARGS+=(--clear-auth-token)
  echo "auth: X-Api-Key from secret $SECRET_API_TOKENS"
fi

echo "job:  $JOB in $REGION"
echo "uri:  $URI"
echo "cron: $SCHEDULE (Europe/Copenhagen)"
echo

# attempt-deadline matches the Cloud Run request timeout. A refresh that outlives
# both is retried rather than left half-done, which is safe because a refresh either
# replaces the snapshot or leaves the previous one in place.
COMMON_ARGS=(
  --project="$PROJECT"
  --location="$REGION"
  --schedule="$SCHEDULE"
  --time-zone=Europe/Copenhagen
  --uri="$URI"
  --http-method=POST
  --attempt-deadline=900s
)

if gcloud scheduler jobs describe "$JOB" --project="$PROJECT" --location="$REGION" >/dev/null 2>&1; then
  echo "job exists, updating"
  run gcloud scheduler jobs update http "$JOB" \
    "${COMMON_ARGS[@]}" \
    --update-headers="X-Api-Key=${API_TOKEN}" \
    "${AUTH_ARGS[@]}"
else
  run gcloud scheduler jobs create http "$JOB" \
    "${COMMON_ARGS[@]}" \
    --description="Refresh the TicketButler snapshot" \
    --headers="X-Api-Key=${API_TOKEN}" \
    "${AUTH_ARGS[@]}"
fi

cat <<NEXT

Trigger it once to check it works:
  gcloud scheduler jobs run $JOB --project=$PROJECT --location=$REGION

Then look at the result:
  gcloud scheduler jobs describe $JOB --project=$PROJECT --location=$REGION \\
    --format='value(status,lastAttemptTime)'

NEXT
