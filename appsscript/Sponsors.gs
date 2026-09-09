// The proxy already filters to the sponsor ticket types listed in the service's
// SPONSOR_TICKET_TYPE_PKS and deduplicates by company name, so the ticket_type_pk
// check that used to live here is gone. Each entry carries company_name, email,
// full_name, ticket_type_pk and ticket_type_name.
function processCommunitySponsors() {
  const sponsors = tbSponsors_().sponsors || [];
  Logger.log(`tbproxy returned ${sponsors.length} community sponsors`);

  sponsors.forEach(sponsor => {
    updateSponsorSheetCommunity(sponsor);
  });

  sendCommunitySponsorCodes();
}

function updateSponsorSheetCommunity(ticket) {
  const sheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName("SPONSORS");
  if (!sheet) {
    Logger.log("Sheet 'SPONSORS' not found.");
    return;
  }

  const dataRange = sheet.getDataRange();
  const values = dataRange.getValues();

  var foundCompany = false;
  for (let i = 0; i < values.length; i++) {
    if (values[i][0] === ticket.company_name) {
      foundCompany = true;
      break;
    }
  }

  if (!foundCompany) {
    sheet.appendRow([ticket.company_name, ticket.email, 'Community', digest(ticket.company_name)]);
  }
}

function sendCommunitySponsorCodes() {
  const sheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName("SPONSORS");
  if (!sheet) {
    Logger.log("Sheet 'SPONSORS' not found.");
    return;
  }

  const draft = getDraft('Cloud Native Denmark 2026 - Welcome Community Sponsor')
  if (!draft) {
    Logger.log("GMail Draft not found for community sponsor codes")
    return;
  }

  const dataRange = sheet.getDataRange();
  const values = dataRange.getValues();

  for (let i = 4; i < values.length; i++) {
    const sentDate = values[i][5];
    const code = values[i][3]
    const email = values[i][1];
    const sponsorType = values[i][2];
    if (sentDate === '' && email !== '' && sponsorType === 'Community') {
      ticketButlerDiscount(code, 5, 100);

      const text = draft.getMessage().getPlainBody().replace('FREECODE', code)
      const html = draft.getMessage().getBody().replace('FREECODE', code)
      //Logger.log('sending email: ' + email + "\nSubject: " + draft.getMessage().getSubject() + "\n" + text + "\n" + html)
      Logger.log('sending email:' + email  + ", Subject: " + draft.getMessage().getSubject())
      GmailApp.sendEmail(email, draft.getMessage().getSubject(), text, {
        htmlBody: html,
        cc: 'sponsor@cloudnativedenmark.dk',
        replyTo: 'sponsor@cloudnativedenmark.dk',
      });
      sheet.getRange(i + 1, 6).setValue(new Date())
    }
  }
}

function processPaidSponsors() {
  const sheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName('SPONSORS');
  if (!sheet) {
    Logger.log("Sheet 'SPONSORS' not found.");
    return;
  }

  const dataRange = sheet.getDataRange();
  const values = dataRange.getValues();

  for (let i = 3; i < values.length; i++) {
    const row = values[i];
    const sponsorName = row[0];
    const sponsorType = row[2];

    if (!sponsorName || !sponsorType) {
      continue;
    }

    const typeLower = sponsorType.toLowerCase();
    if (typeLower !== 'bronze' && typeLower !== 'gold' && typeLower !== 'platinum') {
      continue;
    }

    let freeTickets = 0;
    let discountTickets = 0;

    if (typeLower === 'platinum') {
      freeTickets = 6;
      discountTickets = 10;
    } else if (typeLower === 'gold') {
      freeTickets = 4;
      discountTickets = 10;
    } else if (typeLower === 'bronze') {
      freeTickets = 2;
      discountTickets = 4;
    }

    let freeCode = row[3];
    if (!freeCode) {
      freeCode = digest(sponsorName);
      sheet.getRange(i + 1, 4).setValue(freeCode);
    }

    let discountCode = row[4];
    if (!discountCode) {
      discountCode = digest(sponsorName + '-discount');
      sheet.getRange(i + 1, 5).setValue(discountCode);
    }

    try {
      if (freeTickets > 0) {
        ticketButlerDiscount(freeCode, freeTickets, 100);
      }
      if (discountTickets > 0) {
        ticketButlerDiscount(discountCode, discountTickets, 30);
      }
      Logger.log(`Successfully processed discount codes for ${sponsorName}`);
    } catch (e) {
      Logger.log(`Error processing discount codes for ${sponsorName}: ${e.message}`);
    }
  }
}

function sendPaidSponsorCodes() {
  const sheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName('SPONSORS');
  if (!sheet) {
    Logger.log("Sheet 'SPONSORS' not found.");
    return;
  }

  const dataRange = sheet.getDataRange();
  const values = dataRange.getValues();

  for (let i = 3; i < values.length; i++) {
    const sentDate = values[i][5];
    const freeCode = values[i][3];
    const discountCode = values[i][4];
    const email = values[i][1];
    const sponsorType = values[i][2];
    const sponsorName = values[i][0];

    if (!sponsorType || !email) {
      continue;
    }

    const typeLower = sponsorType.toLowerCase();
    if (typeLower !== 'bronze' && typeLower !== 'gold' && typeLower !== 'platinum') {
      continue;
    }

    if (sentDate === '' && freeCode !== '' && discountCode !== '') {
      let draftSubject = '';
      let freeTickets = 0;
      let discountTickets = 0;

      if (typeLower === 'platinum') {
        draftSubject = 'Cloud Native Denmark 2026 - Welcome Platinum Sponsor';
        freeTickets = 6;
        discountTickets = 10;
      } else if (typeLower === 'gold') {
        draftSubject = 'Cloud Native Denmark 2026 - Welcome Gold Sponsor';
        freeTickets = 4;
        discountTickets = 10;
      } else if (typeLower === 'bronze') {
        draftSubject = 'Cloud Native Denmark 2026 - Welcome Bronze Sponsor';
        freeTickets = 2;
        discountTickets = 4;
      }

      const draft = getDraft(draftSubject);
      if (!draft) {
        Logger.log('GMail Draft not found for: ' + draftSubject);
        continue;
      }

      try {
        if (freeTickets > 0) {
          ticketButlerDiscount(freeCode, freeTickets, 100);
        }
        if (discountTickets > 0) {
          ticketButlerDiscount(discountCode, discountTickets, 30);
        }
      } catch (e) {
        Logger.log('Failed to create TicketButler codes for ' + sponsorName + ': ' + e.message);
        continue;
      }

      const text = draft.getMessage().getPlainBody()
        .replace(/FREECODE/g, freeCode)
        .replace(/DISCOUNTCODE/g, discountCode)
        .replace(/SPONSORNAME/g, sponsorName);

      const html = draft.getMessage().getBody()
        .replace(/FREECODE/g, freeCode)
        .replace(/DISCOUNTCODE/g, discountCode)
        .replace(/SPONSORNAME/g, sponsorName);

      Logger.log('sending email:' + email + ", Subject: " + draft.getMessage().getSubject());
      GmailApp.sendEmail(email, draft.getMessage().getSubject(), text, {
        htmlBody: html,
        cc: 'sponsor@cloudnativedenmark.dk',
        replyTo: 'sponsor@cloudnativedenmark.dk',
      });
      sheet.getRange(i + 1, 6).setValue(new Date());
    }
  }
}
