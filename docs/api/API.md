# API Reference

## Overview

Nexora provides REST/JSON APIs for client applications and gRPC APIs for internal service communication.

## Base URL

```
http://localhost:8000
```

## Authentication

### JWT Tokens

All authenticated endpoints require a valid JWT token in the Authorization header:

```
Authorization: Bearer <access_token>
```

### Token Generation

```http
POST /api/v1/auth/login
Content-Type: application/json

{
    "email": "user@example.com",
    "password": "securePassword123",
    "device_id": "device-abc"
}
```

**Response:**
```json
{
    "success": true,
    "data": {
        "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "expires_at": 1735689600
    }
}
```

## Endpoints

### Auth Endpoints

#### Register
```http
POST /api/v1/auth/register
Content-Type: application/json

{
    "email": "user@example.com",
    "password": "securePassword123",
    "first_name": "John",
    "last_name": "Doe",
    "phone": "+447700900000"
}
```

**Response:**
```json
{
    "success": true,
    "data": {
        "user_id": "usr-123",
        "email": "user@example.com",
        "status": "PENDING_VERIFICATION"
    }
}
```

#### Verify OTP
```http
POST /api/v1/auth/verify-otp
Content-Type: application/json

{
    "email": "user@example.com",
    "otp": "123456"
}
```

#### Refresh Token
```http
POST /api/v1/auth/refresh
Content-Type: application/json

{
    "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

### Account Endpoints

#### List Accounts
```http
GET /api/v1/accounts
Authorization: Bearer <token>
```

**Response:**
```json
{
    "success": true,
    "data": [
        {
            "account_id": "acc-123",
            "user_id": "usr-456",
            "name": "Main Account",
            "type": "PERSONAL",
            "currency": "GBP",
            "status": "ACTIVE",
            "balance": 10000,
            "created_at": "2024-01-01T00:00:00Z"
        }
    ]
}
```

#### Get Account
```http
GET /api/v1/accounts/:account_id
Authorization: Bearer <token>
```

#### Get Balance
```http
GET /api/v1/accounts/:account_id/balance
Authorization: Bearer <token>
```

**Response:**
```json
{
    "success": true,
    "data": {
        "account_id": "acc-123",
        "balance": 10000,
        "pending": 3000,
        "available": 7000,
        "currency": "GBP",
        "total_debits": 5000,
        "total_credits": 15000
    }
}
```

### Payment Endpoints

#### Create Payment
```http
POST /api/v1/payments
Authorization: Bearer <token>
Content-Type: application/json

{
    "idempotency_key": "unique-key-123",
    "account_id": "acc-123",
    "payment_type": "CARD",
    "amount": 10000,
    "currency": "GBP",
    "counterparty_id": "cp-456",
    "counterparty_name": "John Smith",
    "reference": "Invoice payment",
    "metadata": {
        "source": "mobile_app",
        "version": "1.0"
    }
}
```

**Response:**
```json
{
    "success": true,
    "data": {
        "payment_id": "pay-789",
        "account_id": "acc-123",
        "state": "CREATED",
        "amount": 10000,
        "currency": "GBP",
        "counterparty_id": "cp-456",
        "reference": "Invoice payment",
        "created_at": "2024-01-01T00:00:00Z"
    }
}
```

#### Get Payment
```http
GET /api/v1/payments/:payment_id
Authorization: Bearer <token>
```

#### Process Payment
```http
POST /api/v1/payments/:payment_id/process
Authorization: Bearer <token>
```

#### Cancel Payment
```http
POST /api/v1/payments/:payment_id/cancel
Authorization: Bearer <token>
```

### Transaction Endpoints

#### List Transactions
```http
GET /api/v1/accounts/:account_id/transactions?limit=20&offset=0
Authorization: Bearer <token>
```

**Response:**
```json
{
    "success": true,
    "data": [
        {
            "transaction_id": "txn-123",
            "account_id": "acc-456",
            "type": "TRANSFER",
            "status": "COMPLETED",
            "amount": 5000,
            "currency": "GBP",
            "description": "Test transfer",
            "created_at": "2024-01-01T00:00:00Z"
        }
    ],
    "pagination": {
        "total": 100,
        "limit": 20,
        "offset": 0,
        "has_more": true
    }
}
```

#### Get Transaction
```http
GET /api/v1/transactions/:transaction_id
Authorization: Bearer <token>
```

### Ledger Endpoints

#### Get Ledger Entries
```http
GET /api/v1/ledger/accounts/:account_id/entries?limit=50
Authorization: Bearer <token>
```

#### Create Double Entry
```http
POST /api/v1/ledger/transactions
Authorization: Bearer <token>
Content-Type: application/json

{
    "debit_account_id": "acc-123",
    "credit_account_id": "acc-456",
    "amount": 5000,
    "currency": "GBP",
    "description": "Transfer",
    "idempotency_key": "unique-key-456",
    "transaction_type": "TRANSFER"
}
```

### Health Endpoints

#### Service Health
```http
GET /health
```

**Response:**
```json
{
    "status": "healthy",
    "service": "payment-service",
    "version": "1.0.0",
    "timestamp": "2024-01-01T00:00:00Z"
}
```

## Error Responses

### Standard Error Format
```json
{
    "error": "ERROR_CODE",
    "message": "Human-readable error message"
}
```

### Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| VALIDATION_ERROR | 400 | Request validation failed |
| UNAUTHORIZED | 401 | Authentication required |
| NOT_FOUND | 404 | Resource not found |
| IDEMPOTENCY_CONFLICT | 409 | Request already processed with different payload |
| INSUFFICIENT_FUNDS | 402 | Insufficient funds for transaction |
| INVALID_STATE_TRANSITION | 422 | Invalid state transition |
| RATE_LIMITED | 429 | Too many requests |
| INTERNAL_ERROR | 500 | Internal server error |
| SERVICE_UNAVAILABLE | 503 | Service temporarily unavailable |

## Idempotency

All payment creation endpoints support idempotency via the `idempotency_key` field:

```json
{
    "idempotency_key": "unique-key-123"
}
```

If a request with the same idempotency key is received:
- Same payload: Returns original response
- Different payload: Returns 409 Conflict

## Pagination

List endpoints support pagination via query parameters:

```
GET /api/v1/accounts/:account_id/transactions?limit=20&offset=0
```

**Response includes pagination metadata:**
```json
{
    "pagination": {
        "total": 100,
        "limit": 20,
        "offset": 0,
        "has_more": true
    }
}
```

## Headers

### Request Headers
```
Authorization: Bearer <token>
Content-Type: application/json
X-Request-ID: <request-id>
X-Correlation-ID: <correlation-id>
```

### Response Headers
```
Content-Type: application/json
X-Request-ID: <request-id>
X-Correlation-ID: <correlation-id>
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1735689600
```

## Rate Limiting

API endpoints are rate limited:

- **Default**: 1000 requests per minute
- **Payment endpoints**: 100 requests per minute
- **Auth endpoints**: 10 requests per minute

Rate limit headers are included in responses:
```
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1735689600
```

## Versioning

API version is included in the URL path:
```
/api/v1/...
```

Current version: **v1**

## Changelog

### v1.0.0
- Initial release
- Authentication endpoints
- Account endpoints
- Payment endpoints
- Transaction endpoints
- Ledger endpoints
