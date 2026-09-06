package commandcode

const commandCodeCreditsFixture = `{
  "credits": {
    "belowThreshold": false,
    "creditThreshold": 0,
    "monthlyCredits": 59.5594989538,
    "purchasedCredits": 0,
    "premiumMonthlyCredits": 0,
    "opensourceMonthlyCredits": 59.5594989538
  },
  "windowLimits": {
    "limited": true,
    "exceeded": null,
    "fiveHour": {
      "used": 1.8402861307,
      "cap": 14,
      "exceeded": false,
      "resetAt": 1787208520419
    },
    "weekly": {
      "used": 10.4405010462,
      "cap": 35,
      "exceeded": false,
      "resetAt": 1787675714733
    }
  }
}`

const commandCodeSubscriptionsFixture = `{
  "success": true,
  "data": {
    "id": "sub_sanitized",
    "status": "active",
    "planId": "individual-goat",
    "currentPeriodStart": "2026-08-18T16:30:16.000Z",
    "currentPeriodEnd": "2026-09-18T16:30:16.000Z",
    "cancelAtPeriodEnd": false,
    "quantity": 1,
    "pendingPhase": null
  }
}`
