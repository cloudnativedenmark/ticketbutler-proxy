# Operating the TicketButler proxy

Everything here is Cloud Run in project `automation-472811`, region
`europe-north1`. Every script takes `PROJECT`, `REGION` and `SERVICE` from the
environment if you need to point it somewhere else, and echoes each `gcloud`
command before running it.

| Script | What it does |
| --- | --- |
| `setup.sh` | One-time bootstrap: APIs, bucket, service accounts, IAM, secrets. Re-runnable. |
| `deploy.sh` | Deploy an image digest to Cloud Run. |
| `scheduler.sh` | Create or update the ten-minute refresh job. |

## First time

```sh
./deploy/setup.sh
```

It prompts for two secrets:

- **TicketButler API token** — from the TicketButler organiser settings.
- **API_TOKENS** — the bearer tokens this service accepts. Generate one with
  `openssl rand -hex 24`. Comma-separate several; each must be at least 16
  characters or the service refuses to start.

Then make the GHCR package readable without credentials, under the repository's
Packages settings. Cloud Run has no way to present GitHub credentials when
pulling from `ghcr.io`, so a private package fails at revision creation with a
manifest error that does not mention permissions. If keeping the image private
matters more than the simplicity, mirror it into Artifact Registry and deploy
from there instead.

Now deploy (below), re-run `setup.sh` to bind `roles/run.invoker` to the
scheduler service account — it cannot be bound before the service exists — and
create the scheduler job:

```sh
./deploy/scheduler.sh
```

## Deploying

The release workflow publishes to
`ghcr.io/cloudnativedenmark/ticketbutler-proxy` on every push to `main` and
writes the digest into the run summary. Copy it and:

```sh
./deploy/deploy.sh sha256:1a2b3c...
```

The event UUID and the community sponsor ticket type id are defaults in
`deploy.sh`, not prompts. Neither is a credential, since the orders endpoint
still needs the TicketButler API token, but both change per conference year, so
edit them there when the next event's tickets go on sale.

A tag works too, and the script will warn you about it. Cloud Run caches images
from public third-party registries for up to an hour, so deploying `latest`
shortly after it moved can produce a revision built from the cached previous
image, reported as a success. The digest cannot do that.

### Why the service is `--allow-unauthenticated`

Google Apps Script's `UrlFetchApp` cannot mint a Google-signed ID token, so
Cloud Run IAM would lock out the only client. The service therefore does its own
check: every request needs a token listed in `API_TOKENS`, and `internal/config`
refuses to start with that variable empty. Unauthenticated at the Cloud Run
layer, authenticated in the process.

`internal/httpapi` accepts that token on either `Authorization: Bearer` or
`X-Api-Key`, and a match on one is enough. The second header exists because
Cloud Scheduler puts its own OIDC token in `Authorization`, so a job that
authenticates to Cloud Run has no room left there for the service token.

### Tightening the scheduler with OIDC

`scheduler.sh` always sends the service token on `X-Api-Key`, and adds an OIDC
token as the scheduler service account when `SCHEDULER_OIDC=1`:

```sh
SCHEDULER_OIDC=1 ./deploy/scheduler.sh
```

Worth doing, and worth being precise about what it buys. The OIDC token is what
lets Cloud Run IAM turn a caller away at the front end, before the request
reaches the process, with the `API_TOKENS` check still applying on top. But IAM
only turns anyone away once the service is `--no-allow-unauthenticated`, and
while Apps Script is a client that switch cannot be flipped, because
`UrlFetchApp` has no way to present a Google ID token.

So today it changes nothing at the front end. Turn it on anyway: the job is then
already correct for the day the spreadsheet stops being the caller and the
service can be locked down, and setting it up while nothing depends on it is
cheaper than debugging a failed refresh during that change. `setup.sh` grants the
scheduler service account `roles/run.invoker` for the same reason.

## Rotating API_TOKENS

`API_TOKENS` accepts a list precisely so this can happen without a window where
neither the old nor the new token works.

1. Add the new token alongside the old one. Secret versions are whole values, so
   write both:

   ```sh
   printf '%s' "OLD_TOKEN,NEW_TOKEN" | gcloud secrets versions add tbproxy-api-tokens \
     --project=automation-472811 --data-file=-
   ```

2. Roll a revision so the new secret version is read. Cloud Run resolves
   `:latest` when an instance starts, so a running service keeps serving the old
   value until then:

   ```sh
   ./deploy/deploy.sh sha256:<the digest currently deployed>
   ```

   Find that digest with
   `gcloud run services describe tbproxy --project=automation-472811 --region=europe-north1 --format='value(spec.template.spec.containers[0].image)'`.

3. Point the clients at the new token: the Apps Script property, and the
   scheduler job (`./deploy/scheduler.sh` re-reads the secret and puts its first
   entry in the job's `X-Api-Key` header, so order the list with the token you
   want the job to use first).

4. Once nothing uses the old token, drop it: add a version containing only the
   new one, and roll a revision again.

Check step 3 landed before doing step 4. A 401 from the spreadsheet is the
symptom, and it looks exactly like the service being down.

## Optional: deploying from CI

The `deploy` job in `.github/workflows/release.yml` is skipped unless the
repository variable `GCP_WIF_PROVIDER` is set. It authenticates through Workload
Identity Federation, so there is no service account key anywhere — GitHub's OIDC
token is exchanged for short-lived Google credentials, and the exchange only
works for this repository.

```sh
PROJECT=automation-472811
PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')

gcloud iam workload-identity-pools create github \
  --project="$PROJECT" --location=global \
  --display-name="GitHub Actions"

gcloud iam workload-identity-pools providers create-oidc github \
  --project="$PROJECT" --location=global \
  --workload-identity-pool=github \
  --display-name="GitHub" \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.repository_owner=assertion.repository_owner" \
  --attribute-condition="assertion.repository_owner == 'cloudnativedenmark'"
```

The attribute condition is not optional in practice. Without it the provider
trusts every GitHub Actions workflow in existence, and only the binding below
stands between that and your project.

A deploy identity, allowed to update Cloud Run and to run the service as the
runtime service account:

```sh
gcloud iam service-accounts create tbproxy-deployer \
  --project="$PROJECT" --display-name="TicketButler proxy CI deployer"

gcloud projects add-iam-policy-binding "$PROJECT" \
  --member="serviceAccount:tbproxy-deployer@${PROJECT}.iam.gserviceaccount.com" \
  --role=roles/run.developer

gcloud iam service-accounts add-iam-policy-binding \
  "tbproxy-run@${PROJECT}.iam.gserviceaccount.com" \
  --project="$PROJECT" \
  --member="serviceAccount:tbproxy-deployer@${PROJECT}.iam.gserviceaccount.com" \
  --role=roles/iam.serviceAccountUser
```

Then let this repository, and nothing else, impersonate it:

```sh
gcloud iam service-accounts add-iam-policy-binding \
  "tbproxy-deployer@${PROJECT}.iam.gserviceaccount.com" \
  --project="$PROJECT" \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/github/attribute.repository/cloudnativedenmark/ticketbutler-proxy"
```

Finally set the repository variables (Settings, Actions, Variables):

| Variable | Value |
| --- | --- |
| `GCP_WIF_PROVIDER` | `projects/<PROJECT_NUMBER>/locations/global/workloadIdentityPools/github/providers/github` |
| `GCP_DEPLOY_SERVICE_ACCOUNT` | `tbproxy-deployer@automation-472811.iam.gserviceaccount.com` |
| `GCP_PROJECT` | `automation-472811` (optional, this is the default) |
| `GCP_REGION` | `europe-north1` (optional, this is the default) |

The CI job replaces the image and nothing else, so environment variables and
secret bindings survive it. Changing any of those still means running
`deploy/deploy.sh`.

## When the numbers look wrong

```sh
SERVICE_URL=$(gcloud run services describe tbproxy \
  --project=automation-472811 --region=europe-north1 --format='value(status.url)')

curl -sH "Authorization: Bearer $API_TOKEN" "$SERVICE_URL/v1/summary" | head -40
```

The response carries the snapshot's age and a stale flag. Stale means the last
refresh failed or has not run; the numbers are still the last good ones. Check
the scheduler job and the service logs:

```sh
gcloud scheduler jobs describe tbproxy-refresh \
  --project=automation-472811 --location=europe-north1

gcloud run services logs read tbproxy \
  --project=automation-472811 --region=europe-north1 --limit=50
```

Forcing a refresh by hand is a POST:

```sh
curl -X POST -sH "Authorization: Bearer $API_TOKEN" "$SERVICE_URL/v1/refresh"
```

It can take several minutes. That slowness is the reason this service exists.
