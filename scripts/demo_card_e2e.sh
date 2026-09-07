#!/usr/bin/env bash
# MonzoBank real-time card authorization E2E: real DB data end-to-end.
# Usage: bash scripts/demo_card_e2e.sh
set -e
USER="5f0c2e10-3c4a-4a5b-9c6d-7e8f9a0b1c2d"
CLEARING="00000000-0000-0000-0000-000000000001"
TS=$(date +%s)
J() { curl -s -H "Content-Type: application/json" "$@"; }

echo "=== 1. CREATE ACCOUNT ==="
ACCT=$(J -X POST -d "{\"user_id\":\"$USER\",\"account_type\":\"CURRENT\",\"currency\":\"GBP\"}" http://localhost:8083/v1/accounts)
echo "$ACCT"
AID=$(echo "$ACCT" | grep -o '"account_id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "account_id=$AID"
[ -z "$AID" ] && exit 1

echo "=== 2. FUND GBP 1000 VIA DOUBLE-ENTRY LEDGER (REAL DB) ==="
FUND=$(J -X POST -d "{\"debit_account_id\":\"$CLEARING\",\"credit_account_id\":\"$AID\",\"amount\":100000,\"currency\":\"GBP\",\"description\":\"Opening balance\",\"idempotency_key\":\"fund-$TS\"}" http://localhost:8084/v1/ledger/transactions)
echo "$FUND" | head -c 300; echo

echo "=== 3. BALANCE FROM LEDGER OF RECORD ==="
BAL=$(curl -s http://localhost:8083/v1/accounts/$AID/balance)
echo "$BAL"
echo "$BAL" | grep -q '"available_balance":{"amount":100000' && echo "PASS: balance=100000 from ledger" || { echo "FAIL: balance"; exit 1; }

echo "=== 4. CREATE VIRTUAL CARD ==="
CARD=$(J -X POST -H "X-User-ID: $USER" -d "{\"account_id\":\"$AID\",\"card_type\":\"VIRTUAL\",\"spending_limit\":500000,\"daily_limit\":200000,\"monthly_limit\":2000000,\"currency\":\"GBP\"}" http://localhost:8087/v1/cards)
echo "$CARD" | head -c 300; echo
CID=$(echo "$CARD" | grep -o '"card_id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "card_id=$CID"
[ -z "$CID" ] && exit 1

echo "=== 5. START SSE STREAM ==="
curl -sN "http://localhost:8090/v1/stream?user_id=$USER" > /tmp/sse_$TS.out &
SSEPID=$!
sleep 2

echo "=== 6. REAL-TIME AUTHORIZATION GBP 35 @ TESCO ==="
AUTH=$(J -X POST -d '{"amount":3500,"currency":"GBP","merchant":"TESCO STORE 1234","merchant_category":"GROCERY","merchant_city":"LONDON","merchant_country":"GB","terminal_id":"TERM-8871","device_id":"DEV-ANDROID-99","latitude":51.5074,"longitude":-0.1278}' http://localhost:8087/v1/cards/$CID/authorize)
echo "$AUTH"
echo "$AUTH" | grep -q '"status":"APPROVED"' && echo "PASS: authorized" || echo "FAIL: auth not approved"
AUTHID=$(echo "$AUTH" | grep -o '"authorization_id":"[^"]*"' | head -1 | cut -d'"' -f4)
RESID=$(echo "$AUTH" | grep -o '"reservation_id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "auth_id=$AUTHID reservation_id=$RESID"
[ -z "$AUTHID" ] && exit 1

echo "=== 7. BALANCE WHILE HOLD IS LIVE ==="
BAL=$(curl -s http://localhost:8083/v1/accounts/$AID/balance)
echo "$BAL"
echo "$BAL" | grep -q '"current_balance":{"amount":100000' && echo "PASS: current unchanged" || echo "WARN: current"
echo "$BAL" | grep -q '"available_balance":{"amount":96500' && echo "PASS: available dropped by hold" || echo "FAIL: available"

echo "=== 8. FEED ITEM (REAL, FROM DB) ==="
sleep 1
FEED=$(curl -s "http://localhost:8090/v1/feed?user_id=$USER")
echo "$FEED" | head -c 700; echo
echo "$FEED" | grep -qi "TESCO" && echo "PASS: feed has the authorization" || echo "WARN: feed item missing"

echo "=== 9. OVERDRAW ATTEMPT GBP 20000 ==="
DEC=$(J -X POST -d '{"amount":2000000,"currency":"GBP","merchant":"HARRODS","merchant_category":"LUXURY","merchant_city":"LONDON","merchant_country":"GB","terminal_id":"TERM-0001","device_id":"DEV-ANDROID-99"}' http://localhost:8087/v1/cards/$CID/authorize)
echo "$DEC"
echo "$DEC" | grep -q 'declined_by_risk_engine' && echo "PASS: declined by risk engine (daily-limit breach)" || echo "WARN: risk decline missing"

echo "=== 9b. UNDER DAILY LIMIT BUT OVER BALANCE (insufficient funds) ==="
INS=$(J -X POST -d '{"amount":150000,"currency":"GBP","merchant":"APPLE STORE LONDON","merchant_category":"ELECTRONICS","merchant_city":"LONDON","merchant_country":"GB","terminal_id":"TERM-5500","device_id":"DEV-ANDROID-99"}' http://localhost:8087/v1/cards/$CID/authorize)
echo "$INS" | head -c 500; echo
echo "$INS" | grep -q 'insufficient_funds' && echo "PASS: declined insufficient funds (real available balance)" || echo "WARN: insufficient-funds decline missing"

echo "=== 10. CAPTURE THE GBP 35 HOLD ==="
CAP=$(J -X POST http://localhost:8087/v1/cards/$CID/authorizations/$AUTHID/capture)
echo "$CAP" | head -c 300; echo
echo "$CAP" | grep -q '"status":"CAPTURED"' && echo "PASS: captured" || echo "FAIL: capture"

echo "=== 11. BALANCE AFTER CAPTURE (BOOKED) ==="
BAL=$(curl -s http://localhost:8083/v1/accounts/$AID/balance)
echo "$BAL"
echo "$BAL" | grep -q '"current_balance":{"amount":96500' && echo "PASS: money booked, balance 96500" || echo "FAIL: booked balance"
echo "$BAL" | grep -q '"reserved_balance":{"amount":0' && echo "PASS: reservation cleared" || echo "WARN: reserved"

sleep 2
echo "=== 12. SSE PUSHES RECEIVED ==="
cat /tmp/sse_$TS.out
kill $SSEPID 2>/dev/null || true
echo "=== E2E DONE ==="
