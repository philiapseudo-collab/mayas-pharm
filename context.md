# Maya's Pharm Context

## Mission
Build a WhatsApp-first pharmacy ordering system for Philia Technologies that lets customers order day-to-day pharmacy essentials, upload prescriptions, pay with M-Pesa or Airtel Money/Card, and receive fulfillment updates without leaving WhatsApp.

The launch shape is:
- Single pharmacy branch
- Pickup plus Nairobi delivery
- Same-day fulfillment during business hours
- 24/7 intake, with after-hours messaging and next-step expectations
- OTC ordering plus pharmacist-reviewed prescription orders
- No insurance workflows
- No controlled narcotics or poison-schedule medicines

## Product Thesis
The customer surface should stay extremely simple:
- Browse
- Search
- Add to cart
- Choose pickup or delivery
- Pay
- Track status

Staff complexity belongs in the admin dashboard:
- Pharmacist review
- Order operations
- Delivery settings
- Catalog visibility
- Audit trail

## Architecture

### Runtime shape
- `cmd/server`
  - One Go binary for API and worker runtime
  - Use `APP_RUNTIME_MODE=api`, `worker`, or `all`
- `admin/`
  - Next.js TypeScript admin dashboard
  - Separate Railway service

### Core backend stack
- Go 1.22
- Fiber HTTP server
- PostgreSQL on Railway
- Redis on Railway
- Durable payment attempt queue
- Durable outbound message records

### Deployable services on Railway
- `api`
  - Runs `cmd/server` with `APP_RUNTIME_MODE=api`
  - Handles WhatsApp webhooks, customer APIs, admin APIs, payment callbacks
- `worker`
  - Runs `cmd/server` with `APP_RUNTIME_MODE=worker`
  - Handles queued M-Pesa dispatch, expiry sweeps, reconciliation tasks
- `admin`
  - Runs the Next.js app in `admin/`
  - Calls the API service over HTTPS
- `deploy/railway/`
  - Service-by-service Railway env templates for `api`, `worker`, and `admin`
  - Deployment notes for Postgres and Redis wiring

## Domain Model

### Main entities
- `categories`
- `products`
- `users`
- `orders`
- `order_items`
- `prescriptions`
- `prescription_reviews`
- `delivery_zones`
- `business_hours`
- `payment_attempts`
- `provider_throttles`
- `unmatched_payments`
- `outbound_messages`
- `admin_users`
- `otp_codes`
- `audit_logs`

### Staff roles
- `OWNER`
  - Catalog, settings, staff management
- `PHARMACIST`
  - Prescription review, clinical gatekeeping
- `DISPATCHER`
  - Packing, readiness, delivery-state updates

### Order states
- `PENDING_REVIEW`
- `APPROVED_AWAITING_PAYMENT`
- `PAID`
- `PACKING`
- `READY`
- `OUT_FOR_DELIVERY`
- `COMPLETED`
- `REJECTED`
- `FAILED`
- `CANCELLED`
- `EXPIRED`

## Customer Workflow

### OTC flow
1. Customer opens WhatsApp
2. Bot shows product categories or search path
3. Customer adds items to cart
4. Bot asks for pickup or delivery
5. Bot asks for payment method
6. Customer pays
7. Order moves through paid, packing, ready, complete

### Prescription flow
1. Customer adds one or more prescription-only items
2. Entire cart becomes prescription-gated
3. Bot creates pending-review order
4. Customer uploads prescription image or document in WhatsApp
5. Pharmacist approves or rejects in dashboard
6. Approved order becomes payable
7. Customer completes M-Pesa or Airtel Money/Card payment

### Delivery flow
1. Customer chooses delivery
2. Bot presents Nairobi delivery zones
3. Customer sends address and landmark
4. Delivery fee is added to the order
5. Dispatcher can mark order `OUT_FOR_DELIVERY`

## Payments

### M-Pesa
- Use the Kopo Kopo-based STK flow already proven in `destination-cocktails`
- Queue STK requests instead of firing them directly at burst traffic
- Idempotency and durable attempts are mandatory

### Airtel Money/Card
- Use Pesapal hosted checkout
- Customer receives a link in WhatsApp
- Order status is reconciled from Pesapal callback data

## Compliance Guardrails
- Do not provide diagnosis, dosage advice, medicine recommendations, or substitution advice in the bot
- Do not sell controlled narcotics through this workflow
- Prescription-only items must remain behind pharmacist approval
- Public seed prices are bootstrap estimates only and must be reviewed before launch
- Physical stock accuracy matters as much as software correctness
- Pharmacy licensing and premises validation must be handled before go-live

Reference links:
- [PPB licensing](https://www.pharmacyboardkenya.org/licensing/)
- [PPB licensed premises search](https://products.pharmacyboardkenya.org/ppb_admin/pages/public_view_premises.php)
- [PPB press statement, May 19, 2024](https://web.pharmacyboardkenya.org/wp-content/uploads/2024/05/PRESS-STATEMENT-ON-ILLEGAL-PHARMACEUTICAL-PRACTICES-19TH-MAY-2024.pdf)
- [Goodlife prescription workflow reference](https://www.goodlife.co.ke/prescription/)

This document is an implementation guide, not legal advice.

## Catalog Strategy

### Launch seed
- 13 pharmacy categories
- 130 starter SKUs
- Mix of OTC and pharmacist-reviewed prescription lines
- Kenya-market estimate pricing calibrated from current public pharmacy storefront references and made editable in Postgres

### Seed price policy
- Store seed prices with `price_source`
- Treat all seed prices as operator-editable bootstrap values
- Final live prices must be confirmed by Maya's Pharm staff before launch

### Important WhatsApp constraint
WhatsApp interactive list messages are capped at 10 options.

Implemented approach:
- The bot uses paged category browsing so more than 10 top-level categories remain fully browseable
- Category selection can branch into subcategory lists before showing products
- Additional categories remain searchable by product name

## Operational Critique

### What is strong in this concept
- WhatsApp is a realistic distribution channel in Kenya
- Pharmacy repeat purchases fit conversational re-ordering well
- 24/7 intake improves conversion even if fulfillment is daytime-only
- Payment reuse from `destination-cocktails` reduces execution risk

### What is risky
- Prescription review becomes the first real scaling bottleneck before infrastructure does
- A 500-order burst is not just an API problem; it is also a stock, pharmacist, and dispatch capacity problem
- Price drift and stock drift will break customer trust fast if the physical store is not tightly synchronized
- Too many top-level categories create friction on WhatsApp because the UI has hard limits

### Recommended additions
- Reorder shortcuts for repeat customers
- Explicit “prescription received, pharmacist ETA” messaging
- Substitute-request flow that always requires pharmacist approval
- Branch-ready design in schema even if launch is single-branch
- Inventory adjustment audit entries for every stock correction

## Environment Variables

### Core
- `APP_PORT`
- `APP_ENV`
- `APP_RUNTIME_MODE`
- `APP_SHUTDOWN_TIMEOUT_SECONDS`
- `BUSINESS_TIMEZONE`

### Database
- `DATABASE_URL` or `DB_URL`
- `DB_HOST`
- `DB_PORT`
- `DB_USER`
- `DB_PASSWORD`
- `DB_NAME`

### Redis
- `REDIS_URL`
- `REDIS_PASSWORD`

### WhatsApp
- `WHATSAPP_TOKEN`
- `WHATSAPP_PHONE_NUMBER_ID`
- `WHATSAPP_VERIFY_TOKEN`

### Payments
- `KOPOKOPO_CLIENT_ID`
- `KOPOKOPO_CLIENT_SECRET`
- `KOPOKOPO_WEBHOOK_SECRET`
- `KOPOKOPO_TILL_NUMBER`
- `KOPOKOPO_BASE_URL`
- `KOPOKOPO_CALLBACK_URL`
- `PESAPAL_CLIENT_ID`
- `PESAPAL_CLIENT_SECRET`
- `PESAPAL_ENVIRONMENT`
- `PESAPAL_IPN_URL`
- `PESAPAL_IPN_TYPE`
- `PESAPAL_NOTIFICATION_ID`
- `PAYMENT_RETURN_URL`

### Admin and operations
- `JWT_SECRET`
- `ALLOWED_ORIGIN`
- `ALLOWED_ORIGINS`
- `OPS_ALERT_PHONE`

### Admin frontend
- `NEXT_PUBLIC_API_BASE_URL`

## Folder Guide
- `cmd/server`
  - Go API and worker entrypoint
- `cmd/seeder`
  - Bootstrap categories, catalog, zones, hours
- `internal/service`
  - Bot, payments, customer flow, dashboard flow
- `internal/adapters/http`
  - Fiber handlers and admin/customer endpoints
- `internal/adapters/postgres`
  - Repository implementations
- `migrations/001_init.sql`
  - Pharmacy-first schema
- `admin/`
  - Next.js operations dashboard

## Phased Build Order
1. Stabilize domain model and schema
2. Seed the pharmacy catalog and delivery defaults
3. Complete WhatsApp browse, cart, prescription and payment flows
4. Harden workers, idempotency and expiry behavior
5. Ship admin review and operations dashboard
6. Load test burst traffic and refine queue intervals
7. Add repeat orders and branch expansion

## Definition of Done for Launch
- Customer can place OTC order fully through WhatsApp
- Customer can submit prescription order through WhatsApp
- Pharmacist can approve or reject prescription in dashboard
- Customer can pay through M-Pesa or Airtel Money/Card
- Staff can see and progress order states in dashboard
- Delivery fees and business hours are configurable
- Railway deployment uses Postgres plus Redis
- Queueing and expiry logic prevent duplicate payments and stale pending orders
