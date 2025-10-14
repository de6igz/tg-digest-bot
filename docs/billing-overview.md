# Billing Service Business Logic

This document provides a system-level overview of the standalone billing service so that analysts and engineers can understand its responsibilities, data model, and integration points.

## High-level responsibilities

The billing service owns all user balance, invoice, and payment records. It exposes an HTTP API that other components (the bot gateway and public API) call for account operations and SBP (Faster Payments System) invoicing. The service also integrates with Tochka Bank to generate SBP QR codes and process SBP webhooks, without requiring any knowledge of the tg-digest bot internals.【F:billing/cmd/billing/main.go†L17-L80】【F:billing/internal/http/server.go†L48-L109】

## Domain model

The core domain types are shared between the billing service and its clients:

- **Money** keeps an amount in minor units plus a currency code. Currencies default to RUB when Tochka or the storage layer do not provide one explicitly.【F:billing/internal/domain/billing.go†L16-L44】【F:billing/internal/storage/postgres.go†L17-L70】
- **BillingAccount** represents a user’s balance. Accounts are created lazily per user and store the current balance, timestamps, and currency.【F:billing/internal/domain/billing.go†L18-L33】【F:billing/internal/storage/postgres.go†L26-L70】
- **Invoice** captures an amount to be paid, optional description/metadata, current status, and idempotency key. Metadata can embed SBP-specific data about the QR code that was issued.【F:billing/internal/domain/billing.go†L35-L70】【F:billing/internal/domain/billing.go†L72-L95】
- **Payment** records both incoming and outgoing balance movements, including optional linkage to an invoice, metadata, and completion timestamps.【F:billing/internal/domain/billing.go†L97-L125】
- **InvoiceSBPMetadata** stores Tochka QR code details (QR ID, payment link, payloads, expiry, and provider data) in the invoice metadata block so a subsequent call can return the previously issued QR code instead of contacting Tochka again.【F:billing/internal/domain/billing.go†L72-L95】【F:billing/internal/usecase/sbp/service.go†L39-L90】

Every write operation accepts an idempotency key; storage enforces uniqueness and returns an existing record when the same key is replayed. This guarantees that retries from clients or webhook deliveries do not create duplicates.【F:billing/internal/storage/postgres.go†L72-L184】【F:billing/internal/storage/postgres.go†L200-L333】

## Storage logic

The Postgres adapter encapsulates all persistence rules and invariants:

- `EnsureAccount` opens a transaction that upserts an account by user ID and normalises the currency if the database row is missing a value.【F:billing/internal/storage/postgres.go†L26-L80】
- `CreateInvoice` verifies the account exists, enforces consistent currencies, serialises metadata, and inserts a new invoice. On idempotency conflicts it re-reads the existing invoice and validates that the repeated request matches the stored one.【F:billing/internal/storage/postgres.go†L82-L164】
- `RegisterIncomingPayment` locks the account (and invoice if supplied), validates currencies, inserts a completed payment, increases the balance, and marks the invoice as paid once the full amount arrives. Partial payments are rejected to avoid inconsistent invoice state.【F:billing/internal/storage/postgres.go†L166-L289】
- `ChargeAccount` performs the inverse operation: it ensures sufficient balance, inserts a negative completed payment, and decrements the account balance atomically.【F:billing/internal/storage/postgres.go†L291-L372】

These invariants let upstream services treat the billing API as the source of truth without sharing a database connection.

## HTTP API surface

The HTTP server exposes REST-like endpoints for each domain operation. Key routes include:

- `POST /api/v1/accounts/ensure` and `GET /api/v1/accounts/by-user/{id}` for account provisioning and lookup.【F:billing/internal/http/server.go†L57-L138】
- `POST /api/v1/accounts/charge` to debit an account idempotently; validation propagates business errors such as insufficient funds via structured error responses.【F:billing/internal/http/server.go†L112-L182】
- `POST /api/v1/invoices` plus `GET /api/v1/invoices/{id}` and `/api/v1/invoices/idempotency/{key}` for invoice creation and retrieval.【F:billing/internal/http/server.go†L184-L236】
- `POST /api/v1/payments/incoming` for internal systems to register non-SBP incoming payments (for example, manual top-ups).【F:billing/internal/http/server.go†L238-L272】
- `POST /api/v1/sbp/invoices` to request a Tochka SBP QR code and persist the linked invoice, and `POST /api/v1/sbp/webhook` for Tochka callbacks. These endpoints are available only when Tochka credentials are configured.【F:billing/internal/http/server.go†L274-L347】【F:billing/internal/http/server.go†L349-L406】

Each handler validates required fields, translates domain errors into HTTP status codes, and serialises domain objects back to callers, ensuring downstream services receive consistent JSON schemas.【F:billing/internal/http/server.go†L57-L406】

## SBP invoice issuance flow

When a client calls `POST /api/v1/sbp/invoices` the server delegates to the SBP use case:

1. Validate the payload (user, amount, idempotency key) and fill defaults such as the notification URL if Tochka credentials specify one.【F:billing/internal/http/server.go†L274-L336】【F:billing/internal/usecase/sbp/service.go†L34-L67】
2. Check whether an invoice already exists for the idempotency key; if so, extract the cached Tochka QR metadata and return it immediately without making an external API call.【F:billing/internal/usecase/sbp/service.go†L48-L80】
3. Ensure the user has a billing account, normalise the currency, and build a Tochka QR registration request. Because Tochka’s SBP API does not accept an `order_id`, the payload carries only the amount, description, payment purpose, QR type, webhook URL, and any extra fields supplied by the caller.【F:billing/internal/usecase/sbp/service.go†L82-L114】【F:billing/internal/tochka/client.go†L40-L120】
4. Call Tochka, parse the QR ID, payment link, payload strings, status, and expiry timestamp, and stash the full provider response in invoice metadata so retries are idempotent.【F:billing/internal/tochka/client.go†L122-L196】【F:billing/internal/usecase/sbp/service.go†L116-L142】
5. Create the invoice in Postgres with the SBP metadata attached and return both the invoice and the QR data to the caller.【F:billing/internal/usecase/sbp/service.go†L138-L157】

The stored metadata (including the Tochka `qrId`) allows the billing service to correlate subsequent webhooks with the correct invoice, even though Tochka does not provide an `order_id` field in either the registration response or webhook payload.【F:billing/internal/usecase/sbp/service.go†L48-L157】【F:billing/internal/tochka/webhook.go†L34-L116】

## SBP webhook processing

The webhook handler enforces the configured shared secret or JWT signature (when Tochka sends signed notifications), parses the body into a normalised notification structure, and hands it to the use case for settlement.【F:billing/internal/http/server.go†L349-L406】【F:billing/internal/tochka/jwt.go†L19-L170】

The SBP use case performs the following steps:

1. Require a `qrId` because it uniquely maps back to the stored invoice idempotency key; webhooks without it are rejected.【F:billing/internal/usecase/sbp/service.go†L146-L152】
2. Look up the invoice by the QR ID idempotency key and convert the amount into minor units using Tochka helpers.【F:billing/internal/usecase/sbp/service.go†L152-L166】【F:billing/internal/tochka/webhook.go†L96-L116】
3. Build payment metadata describing the Tochka event (status, payment purpose, payer information, raw payload) and register the incoming payment through the billing repository. The metadata only includes `order_id` if Tochka happens to send it, keeping compatibility with payloads that omit the field.【F:billing/internal/usecase/sbp/service.go†L166-L201】
4. The storage layer credits the account balance, marks the invoice as paid when the amounts match, and returns the completed payment.【F:billing/internal/storage/postgres.go†L166-L289】

Idempotency keys for webhook payments fall back to the Tochka payment ID or event ID, with the QR ID as a final fallback. This ensures the service can safely handle duplicate webhook deliveries without generating multiple payments even when Tochka omits optional identifiers.【F:billing/internal/tochka/webhook.go†L96-L116】

## Sequence summary

Putting the pieces together:

1. A client ensures the user has a billing account and requests an SBP invoice via the billing API.
2. The billing service talks to Tochka to issue a QR code, persists the invoice with Tochka metadata, and returns the QR payload to the client.
3. The user pays by scanning the QR code; Tochka notifies the billing webhook.
4. The webhook is validated, normalised, and registered as an incoming payment, which in turn credits the user account and marks the invoice as paid.

Through strict idempotency and metadata storage the billing service can recover from retries at any step without inconsistencies, while also hiding Tochka-specific details from other parts of the system.【F:billing/internal/usecase/sbp/service.go†L34-L201】【F:billing/internal/storage/postgres.go†L72-L372】
