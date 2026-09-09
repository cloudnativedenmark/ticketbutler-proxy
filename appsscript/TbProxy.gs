/**
 * Client for ticketbutler-proxy, the caching service in front of the TicketButler
 * API. Every other script in this project goes through the helpers here instead of
 * calling TicketButler for orders, because the orders endpoint takes longer than
 * the 60 second UrlFetchApp limit that Apps Script enforces.
 *
 * Two Script Properties are required (Project Settings, Script Properties):
 *   TBPROXY_URL    base URL of the service, for example https://tbproxy-xxxxx.a.run.app
 *   TBPROXY_TOKEN  one of the bearer tokens listed in the service's API_TOKENS
 *
 * Nothing in this file may contain a token. This repository is public.
 */

// One memo per execution, keyed by path, so two functions in the same run share a
// single fetch. This replaces the old cachedOrders global.
var tbProxyMemo = {};

function tbProxyConfig_() {
  const props = PropertiesService.getScriptProperties();
  const url = props.getProperty('TBPROXY_URL');
  const token = props.getProperty('TBPROXY_TOKEN');
  if (!url) {
    throw new Error('Script Property TBPROXY_URL is not set. Add it under Project Settings, Script Properties, with the base URL of the ticketbutler-proxy Cloud Run service.');
  }
  if (!token) {
    throw new Error('Script Property TBPROXY_TOKEN is not set. Add it under Project Settings, Script Properties, with one of the tokens from the service API_TOKENS.');
  }
  return {
    'url': url.replace(/\/+$/, ''),
    'token': token,
  };
}

/**
 * GET a path on the proxy, retrying with exponential backoff. Adapted from the
 * fetchWithRetry that used to live in TicketSales.gs.
 *
 * 4xx responses are not retried. A 401 means the token is wrong and a 404 means
 * the path is wrong; repeating the call only hides the real error behind a delay.
 */
function tbProxyGet_(path, maxRetries = 3, delayMs = 1000) {
  const config = tbProxyConfig_();
  const options = {
    'headers': {
      'Authorization': `Bearer ${config.token}`
    },
    'contentType': 'application/json',
    'method': 'GET',
    'muteHttpExceptions': true,
  };

  for (let i = 0; i < maxRetries; i++) {
    var response = null;
    try {
      response = UrlFetchApp.fetch(config.url + path, options);
    } catch (e) {
      Logger.log(`Fetch attempt ${i + 1} for ${path} failed: ${e.message}`);
    }
    if (response) {
      var code = response.getResponseCode();
      if (code === 200) {
        return response;
      }
      Logger.log(`Fetch attempt ${i + 1} for ${path} returned status code ${code}`);
      if (code >= 400 && code < 500) {
        throw new Error(`tbproxy ${path} returned ${code}. Check TBPROXY_URL and TBPROXY_TOKEN.`);
      }
    }
    if (i < maxRetries - 1) {
      Utilities.sleep(delayMs * Math.pow(2, i));
    }
  }
  throw new Error(`Failed to fetch ${path} from tbproxy after ${maxRetries} attempts.`);
}

function tbProxyJson_(path) {
  if (tbProxyMemo[path]) {
    return tbProxyMemo[path];
  }
  var payload = JSON.parse(tbProxyGet_(path).getContentText());
  tbWarnIfStale_(payload);
  tbProxyMemo[path] = payload;
  return payload;
}

/**
 * Log a warning when the snapshot is older than the service's STALE_AFTER. Stale
 * data is still served and still written to the sheets: an old number with a
 * warning in the log is more useful than no number. Repeated warnings mean the
 * Cloud Scheduler job that calls /v1/refresh is failing.
 */
function tbWarnIfStale_(payload) {
  if (!payload || !payload.stale) {
    return;
  }
  Logger.log(`WARNING: tbproxy snapshot is stale, age_seconds=${payload.age_seconds}, fetched_at=${payload.fetched_at}. Sheets were updated from it anyway. Check the Cloud Scheduler refresh job.`);
}

/** Pre-aggregated ticket, t-shirt and merchandise numbers. */
function tbSummary_() {
  return tbProxyJson_('/v1/summary');
}

/** Community sponsors, deduplicated by company name and filtered by ticket type. */
function tbSponsors_() {
  return tbProxyJson_('/v1/sponsors');
}
