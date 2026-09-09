// Rolling back means pasting the pre-proxy version of this file back over it. Keep
// a copy for one event cycle: it is the version that called TicketButler directly,
// and it works only while the orders endpoint still answers inside the 60 second
// UrlFetchApp limit, which it stopped doing at roughly 250 attendees.
//
// There is deliberately no feature flag here. A flag would have to carry both
// implementations and both would have to keep working, and a switch that only
// raises an error is not a rollback -- it just looks like one.

function readTicketbutlerOrders() {
  const summary = tbSummary_();
  Logger.log(`tbproxy summary: ${summary.counts.orders} orders, ${summary.counts.tickets} tickets, fetched_at ${summary.fetched_at}`);

  // The service reports income excluding VAT under income_ex_vat; the sheet code
  // below has always called that field income.
  const ticketCounts = {};
  const groups = summary.ticket_groups || {};
  for (const group in groups) {
    ticketCounts[group] = {
      count: groups[group].count,
      income: groups[group].income_ex_vat
    };
  }

  updateRealizedSheet(ticketCounts);
  updateMerchAndTshirtsSheet(summary.tshirt_sizes || {}, summary.merch || {});
}

function updateRealizedSheet(ticketCounts) {
  const sheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName("REALIZED");
  if (!sheet) {
    Logger.log("Sheet 'REALIZED' not found.");
    return;
  }

  const dataRange = sheet.getDataRange();
  const values = dataRange.getValues();

  for (const title in ticketCounts) {
    for (let i = 0; i < values.length; i++) {
      const rowTitle = values[i][0].toString().trim();
      if (isMatchingGroup(rowTitle, title)) {
        // Columns F and G of one row, written together. The matched rows are not
        // contiguous, and writing the whole F:G block back would overwrite any
        // formula in the rows this loop never matched, so the batching stops here.
        sheet.getRange(i + 1, 6, 1, 2).setValues([[ticketCounts[title].count, ticketCounts[title].income]]);
        break;
      }
    }
  }
}

function updateMerchAndTshirtsSheet(tshirtSizes, merchCounts) {
  const sheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName("MERCH AND T-SHIRTS");
  if (!sheet) {
    Logger.log("Sheet 'MERCH AND T-SHIRTS' not found.");
    return;
  }

  // Row order for both blocks below. Sizes sit on consecutive rows, so each block
  // is one setValues call instead of six setValue calls.
  const sizeOrder = ["small", "medium", "large", "xl", "2xl", "3xl"];

  // 1. Write T-Shirt counts (A7:B12)
  const tshirtFirstRow = 7;
  sheet.getRange(tshirtFirstRow, 2, sizeOrder.length, 1)
    .setValues(sizeOrder.map(size => [tshirtSizes[size] || 0]));

  // 2. Write Merch counts (A17:A22 for sizes, row 16 from Column B onwards for headers)
  const merchFirstRow = 17;

  const lastCol = sheet.getLastColumn();
  if (lastCol >= 2) {
    const merchHeaders = sheet.getRange(16, 2, 1, lastCol - 1).getValues()[0];

    // For each column from Column B (index 2) to last column
    for (let colOffset = 0; colOffset < merchHeaders.length; colOffset++) {
      const colName = merchHeaders[colOffset];
      if (!colName) continue;

      const colNum = colOffset + 2; // Column index (1-based, B is 2)

      // Find the corresponding merch count in our merchCounts map
      let matchedMerchKey = null;
      const normalizedColName = normalizeMerchName(colName);

      for (const merchKey in merchCounts) {
        if (normalizeMerchName(merchKey) === normalizedColName) {
          matchedMerchKey = merchKey;
          break;
        }
      }

      // Write the counts for each size in this column. Columns with an empty
      // header are skipped above, so the write stays per column rather than one
      // block across all of them.
      const counts = sizeOrder.map(size => {
        let count = 0;
        if (matchedMerchKey && merchCounts[matchedMerchKey]) {
          count = merchCounts[matchedMerchKey][size] || 0;
        }
        return [count];
      });
      sheet.getRange(merchFirstRow, colNum, sizeOrder.length, 1).setValues(counts);
    }
  }
}

function normalizeMerchName(name) {
  if (!name) return "";
  return name.toString().trim().toLowerCase()
    .replace("zip ", "")
    .replace("zipper ", "");
}

function isMatchingGroup(sheetTitle, ticketGroup) {
  const cleanSheet = sheetTitle.toString().trim().toLowerCase();
  const cleanTicket = ticketGroup.toString().trim().toLowerCase();
  return cleanSheet === cleanTicket ||
         (cleanTicket === "ordinary" && cleanSheet === "ordinary tickets") ||
         (cleanTicket === "ordinary tickets" && cleanSheet === "ordinary") ||
         cleanSheet.startsWith(cleanTicket);
}
