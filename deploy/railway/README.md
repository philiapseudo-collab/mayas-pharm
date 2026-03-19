# Railway Services

This repo is deployed to Railway as four services in one project:

- `mayas-pharm`: public API service from repo root `.` using [`/railway.json`](/D:/projects-2/mayas-pharm/railway.json)
- `mayas-pharm-worker`: private worker service from repo root `.` using the same Go build as the API
- `mayas-pharm-admin`: public Next.js dashboard from [`/admin`](/D:/projects-2/mayas-pharm/admin) using [`/admin/railway.json`](/D:/projects-2/mayas-pharm/admin/railway.json)
- `Postgres` and `Redis`: managed Railway data services

## Service Layout

- `mayas-pharm`
  - Root directory: `.`
  - Build: `go build -o server ./cmd/server`
  - Start: `./server`
  - Runtime mode: `APP_RUNTIME_MODE=api`
  - Public domain required
- `mayas-pharm-worker`
  - Root directory: `.`
  - Build: `go build -o server ./cmd/server`
  - Start: `./server`
  - Runtime mode: `APP_RUNTIME_MODE=worker`
  - No public domain required
- `mayas-pharm-admin`
  - Root directory: `admin`
  - Build: `npm ci && npm run build`
  - Start: `npm run start`
  - Public domain required

## Shared Service Variables

These should be set on both `mayas-pharm` and `mayas-pharm-worker`:

- `APP_ENV=production`
- `BUSINESS_TIMEZONE=Africa/Nairobi`
- `DATABASE_URL=<Railway Postgres internal URL>`
- `REDIS_URL=<Railway Redis internal URL>`
- `WHATSAPP_WEBHOOK_MAX_CONCURRENT=64`
- `PAYMENT_DISPATCH_INTERVAL_MS=2100`
- `PAYMENT_DISPATCH_POLL_INTERVAL_MS=250`
- `ORDER_EXPIRY_SWEEP_INTERVAL_MS=5000`
- `ORDERS_API_MAX_LIMIT=200`
- `DB_MAX_OPEN_CONNS=40`
- `DB_MAX_IDLE_CONNS=20`
- `DB_CONN_MAX_LIFETIME_MINUTES=30`
- `DB_CONN_MAX_IDLE_TIME_MINUTES=10`

These secrets must match the production providers:

- `WHATSAPP_TOKEN`
- `WHATSAPP_PHONE_NUMBER_ID`
- `WHATSAPP_VERIFY_TOKEN`
- `JWT_SECRET`
- `OPS_ALERT_PHONE`
- `KOPOKOPO_CLIENT_ID`
- `KOPOKOPO_CLIENT_SECRET`
- `KOPOKOPO_WEBHOOK_SECRET`
- `KOPOKOPO_TILL_NUMBER`
- `KOPOKOPO_CALLBACK_URL`
- `PESAPAL_CLIENT_ID`
- `PESAPAL_CLIENT_SECRET`
- `PESAPAL_ENVIRONMENT`
- `PESAPAL_IPN_URL`
- `PESAPAL_IPN_TYPE`
- `PESAPAL_NOTIFICATION_ID`
- `PAYMENT_RETURN_URL`

## API-Specific Variables

Set these only on `mayas-pharm`:

- `APP_RUNTIME_MODE=api`
- `ALLOWED_ORIGINS=https://<admin-domain>`
- `PAYMENT_RETURN_URL=https://<admin-domain>/orders/status`
- `KOPOKOPO_CALLBACK_URL=https://<api-domain>/api/webhooks/payment`
- `PESAPAL_IPN_URL=https://<api-domain>/api/webhooks/pesapal`

## Worker-Specific Variables

Set these only on `mayas-pharm-worker`:

- `APP_RUNTIME_MODE=worker`
- `ALLOWED_ORIGINS=https://<admin-domain>`

## Admin-Specific Variables

Set these only on `mayas-pharm-admin`:

- `NEXT_PUBLIC_API_BASE_URL=https://<api-domain>`

## Recommended Provisioning Order

1. Create `Redis` in the Railway project.
2. Create `mayas-pharm-worker` from the same GitHub repo.
3. Create `mayas-pharm-admin` from the same GitHub repo with root directory `admin`.
4. Generate/public domains for `mayas-pharm` and `mayas-pharm-admin`.
5. Set API and worker variables from [`/deploy/railway/api.env.example`](/D:/projects-2/mayas-pharm/deploy/railway/api.env.example) and [`/deploy/railway/worker.env.example`](/D:/projects-2/mayas-pharm/deploy/railway/worker.env.example).
6. Set admin variables from [`/deploy/railway/admin.env.example`](/D:/projects-2/mayas-pharm/deploy/railway/admin.env.example).
7. Deploy API first so the migration runs.
8. Deploy worker and admin after API is healthy.

## CLI Notes

- The API and worker intentionally share the same Railway build/start config; `APP_RUNTIME_MODE` is what splits HTTP and background processing.
- The Go app now honors Railway’s dynamic `PORT` when `APP_PORT` is not explicitly set.
- The admin app uses Next.js standalone output for Railway builds.
