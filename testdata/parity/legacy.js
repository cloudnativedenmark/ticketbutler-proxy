// The aggregation exactly as TicketSales.gs performs it today, lifted unchanged
// apart from replacing the UrlFetchApp call with a file read and printing the
// result instead of writing it to a sheet.
const fs = require('fs');
const orders = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));

function normalizeSize(size) {
  if (!size) return "";
  size = size.toString().trim().toLowerCase();
  if (size === "s" || size === "small") return "small";
  if (size === "m" || size === "medium") return "medium";
  if (size === "l" || size === "large") return "large";
  if (size === "xl" || size === "extra large") return "xl";
  if (size === "2xl" || size === "xxl") return "2xl";
  if (size === "3xl" || size === "xxxl") return "3xl";
  return size;
}

const ticketCounts = {};
const tshirtSizes = { "small":0,"medium":0,"large":0,"xl":0,"2xl":0,"3xl":0 };
const merchCounts = {};

(orders.orders || []).forEach(order => {
  (order.tickets || []).forEach(ticket => {
    var group = ticket.ticket_type_name;
    var isMerch = group.toLowerCase().includes("hoodie");
    var parsedMerchType = group;
    var merchSize = null;
    if (isMerch) {
      var parts = group.split(" - ");
      if (parts.length > 1) {
        merchSize = normalizeSize(parts[parts.length - 1]);
        parsedMerchType = parts.slice(0, parts.length - 1).join(" - ").trim();
      }
      group = parsedMerchType;
    }
    if (isMerch && merchSize) {
      if (!merchCounts[parsedMerchType]) {
        merchCounts[parsedMerchType] = {"small":0,"medium":0,"large":0,"xl":0,"2xl":0,"3xl":0};
      }
      if (merchCounts[parsedMerchType][merchSize] !== undefined) {
        merchCounts[parsedMerchType][merchSize] += 1;
      }
    }
    if (ticket.ticket_answer_collection && ticket.ticket_answer_collection.answers) {
      ticket.ticket_answer_collection.answers.forEach(ans => {
        if (ans.question_heading && ans.question_heading.trim() === "T-Shirt Size") {
          if (ans.answered_choices && ans.answered_choices.length > 0) {
            var choice = ans.answered_choices[0].choice_heading;
            var normalizedChoice = normalizeSize(choice);
            if (tshirtSizes[normalizedChoice] !== undefined) {
              tshirtSizes[normalizedChoice] += 1;
            }
          }
        }
      });
    }
    var income = parseFloat(ticket.price_total);
    var price = parseFloat(ticket.price);
    if (income === 0) {
      group = "Sponsors and other free tickets";
    } else if (income < price) {
      group = "Discounted";
    }
    if (!order.vat_exempt) {
      income = income / (1 + order.vat_rate);
    }
    if (!ticketCounts[group]) ticketCounts[group] = { count: 0, income: 0 };
    ticketCounts[group].count += 1;
    ticketCounts[group].income += income;
  });
});

// Sponsors.gs: community sponsor tickets, ticket_type_pk 183067, first per company.
const sponsors = [];
const seen = new Set();
(orders.orders || []).forEach(order => {
  (order.tickets || []).forEach(t => {
    if (t.ticket_type_pk === 183067) {
      const key = (t.company_name || '').trim().toLowerCase();
      if (key && !seen.has(key)) { seen.add(key); sponsors.push(t.company_name); }
    }
  });
});

console.log(JSON.stringify({ ticketCounts, tshirtSizes, merchCounts, sponsors: sponsors.sort((a,b)=>a.toLowerCase()<b.toLowerCase()?-1:1) }, null, 2));
