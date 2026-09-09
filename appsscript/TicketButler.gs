/**
 * Direct calls to the TicketButler API. Only the ones that are fast enough to run
 * inside Apps Script live here; orders go through TbProxy.gs instead.
 *
 * The event UUID and the API token come from Script Properties (Project Settings,
 * Script Properties):
 *   TB_EVENT  TicketButler event UUID
 *   TB_TOKEN  TicketButler API token
 *
 * Both used to be hardcoded at the top of this file. They are read at call time
 * now because this file is published in a public repository.
 */

const TB_BASE_URL = 'https://cloudnativedenmark.ticketbutler.io';

function tbScriptProperty_(name, description) {
  const value = PropertiesService.getScriptProperties().getProperty(name);
  if (!value) {
    throw new Error(`Script Property ${name} is not set. Add it under Project Settings, Script Properties, with ${description}.`);
  }
  return value;
}

function tbEvent_() {
  return tbScriptProperty_('TB_EVENT', 'the TicketButler event UUID');
}

function tbToken_() {
  return tbScriptProperty_('TB_TOKEN', 'a TicketButler API token');
}

function ticketButlerDiscount(code, tickets, discount) {
  const create = [{
    'active': true,
    'amount': discount,
    'code': code,
    'discount_type': 'PERCENTAGE',
    'usage_tickets_limit': tickets,
  }]
  const options = {
    'headers': {
      'Authorization': `Token ${tbToken_()}`
    },
    'contentType': 'application/json',
    'method': 'POST',
    'payload': JSON.stringify(create),
    'muteHttpExceptions': true,
  };
  var response = UrlFetchApp.fetch(`${TB_BASE_URL}/api/v3/events/${tbEvent_()}/discount-codes/`, options)
  var responseCode = response.getResponseCode();
  if (responseCode != 200 && responseCode != 400) {
    throw new Error("unexpected response from ticketbutler API trying to create discount code")
  }
}
