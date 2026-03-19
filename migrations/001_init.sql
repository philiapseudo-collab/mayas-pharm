-- Migration: 001_init.sql
-- Description: Initial Maya's Pharm schema for WhatsApp ordering, pharmacist review,
-- Railway Postgres deployment, and payment/queue hardening.

BEGIN;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    phone_number VARCHAR(20) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_users_phone_number ON users(phone_number);

CREATE TABLE IF NOT EXISTS categories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL,
    slug VARCHAR(100) NOT NULL,
    description TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_categories_slug_unique ON categories(slug);
CREATE INDEX IF NOT EXISTS idx_categories_active_sort ON categories(is_active, sort_order, name);

CREATE TABLE IF NOT EXISTS admin_users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    phone_number VARCHAR(20) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'OWNER',
    bartender_code VARCHAR(4),
    pin_hash VARCHAR(255),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_admin_users_role CHECK (role IN ('OWNER', 'PHARMACIST', 'DISPATCHER')),
    CONSTRAINT chk_admin_users_bartender_code_format CHECK (
        bartender_code IS NULL OR bartender_code ~ '^[0-9]{4}$'
    )
);

CREATE INDEX IF NOT EXISTS idx_admin_users_phone_number ON admin_users(phone_number);
CREATE INDEX IF NOT EXISTS idx_admin_users_is_active ON admin_users(is_active);
CREATE INDEX IF NOT EXISTS idx_admin_users_role ON admin_users(role);
CREATE UNIQUE INDEX IF NOT EXISTS idx_admin_users_bartender_code_unique
    ON admin_users (bartender_code)
    WHERE bartender_code IS NOT NULL;

CREATE TABLE IF NOT EXISTS delivery_zones (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL,
    slug VARCHAR(100) NOT NULL,
    fee DECIMAL(10, 2) NOT NULL DEFAULT 0,
    estimated_mins INTEGER NOT NULL DEFAULT 60,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_zones_slug_unique ON delivery_zones(slug);
CREATE INDEX IF NOT EXISTS idx_delivery_zones_active_sort ON delivery_zones(is_active, sort_order, name);

CREATE TABLE IF NOT EXISTS business_hours (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    day_of_week INTEGER NOT NULL,
    open_time VARCHAR(5) NOT NULL,
    close_time VARCHAR(5) NOT NULL,
    is_open BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_business_hours_day_of_week CHECK (day_of_week BETWEEN 0 AND 6),
    CONSTRAINT chk_business_hours_open_time_format CHECK (open_time ~ '^[0-2][0-9]:[0-5][0-9]$'),
    CONSTRAINT chk_business_hours_close_time_format CHECK (close_time ~ '^[0-2][0-9]:[0-5][0-9]$')
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_business_hours_day_unique ON business_hours(day_of_week);

CREATE TABLE IF NOT EXISTS products (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    price DECIMAL(10, 2) NOT NULL,
    category VARCHAR(100) NOT NULL,
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    sku VARCHAR(100),
    brand_name VARCHAR(255),
    generic_name VARCHAR(255),
    strength VARCHAR(64),
    dosage_form VARCHAR(64),
    pack_size VARCHAR(64),
    unit VARCHAR(32),
    active_ingredient VARCHAR(255),
    stock_quantity INTEGER NOT NULL DEFAULT 0,
    image_url VARCHAR(500),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    requires_prescription BOOLEAN NOT NULL DEFAULT FALSE,
    is_controlled BOOLEAN NOT NULL DEFAULT FALSE,
    price_source VARCHAR(64),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_products_price_non_negative CHECK (price >= 0),
    CONSTRAINT chk_products_stock_non_negative CHECK (stock_quantity >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_products_sku_unique
    ON products(sku)
    WHERE sku IS NOT NULL AND sku <> '';
CREATE INDEX IF NOT EXISTS idx_products_category ON products(category);
CREATE INDEX IF NOT EXISTS idx_products_category_id ON products(category_id);
CREATE INDEX IF NOT EXISTS idx_products_is_active ON products(is_active);
CREATE INDEX IF NOT EXISTS idx_products_category_active_name ON products(category, is_active, name);
CREATE INDEX IF NOT EXISTS idx_products_requires_prescription ON products(requires_prescription);
CREATE INDEX IF NOT EXISTS idx_products_name_search ON products(LOWER(name));
CREATE INDEX IF NOT EXISTS idx_products_generic_name_search ON products(LOWER(generic_name));

CREATE SEQUENCE IF NOT EXISTS pickup_code_seq
    START WITH 1
    INCREMENT BY 1
    MINVALUE 1
    NO MAXVALUE
    CACHE 1;

CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    customer_phone VARCHAR(20) NOT NULL,
    table_number VARCHAR(20),
    total_amount DECIMAL(10, 2) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'APPROVED_AWAITING_PAYMENT',
    payment_method VARCHAR(20),
    payment_reference VARCHAR(255),
    idempotency_key VARCHAR(255),
    pickup_code VARCHAR(6),
    fulfillment_type VARCHAR(20) NOT NULL DEFAULT 'PICKUP',
    delivery_zone_id UUID REFERENCES delivery_zones(id) ON DELETE SET NULL,
    delivery_fee DECIMAL(10, 2) NOT NULL DEFAULT 0,
    delivery_address TEXT,
    delivery_contact_name VARCHAR(255),
    delivery_notes TEXT,
    review_required BOOLEAN NOT NULL DEFAULT FALSE,
    review_notes TEXT,
    reviewed_at TIMESTAMP,
    reviewed_by_admin_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    prescription_count INTEGER NOT NULL DEFAULT 0,
    paid_at TIMESTAMP,
    preparing_at TIMESTAMP,
    preparing_by_admin_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    ready_at TIMESTAMP,
    ready_by_admin_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    completed_at TIMESTAMP,
    completed_by_admin_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    expires_at TIMESTAMP,
    stock_released BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_orders_status CHECK (
        status IN (
            'PENDING_REVIEW',
            'APPROVED_AWAITING_PAYMENT',
            'PAID',
            'PACKING',
            'READY',
            'OUT_FOR_DELIVERY',
            'COMPLETED',
            'REJECTED',
            'FAILED',
            'CANCELLED',
            'EXPIRED'
        )
    ),
    CONSTRAINT chk_orders_fulfillment_type CHECK (fulfillment_type IN ('PICKUP', 'DELIVERY')),
    CONSTRAINT chk_orders_total_amount_non_negative CHECK (total_amount >= 0),
    CONSTRAINT chk_orders_delivery_fee_non_negative CHECK (delivery_fee >= 0),
    CONSTRAINT chk_orders_prescription_count_non_negative CHECK (prescription_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_customer_phone ON orders(customer_phone);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_updated_at ON orders(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_expires_at ON orders(expires_at);
CREATE INDEX IF NOT EXISTS idx_orders_status_created_at ON orders(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_status_updated_at ON orders(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_pending_amount_created_at
    ON orders(status, total_amount, created_at DESC)
    WHERE status = 'APPROVED_AWAITING_PAYMENT';
CREATE INDEX IF NOT EXISTS idx_orders_completed_history_lookup
    ON orders(status, completed_at DESC, pickup_code, customer_phone)
    WHERE status = 'COMPLETED';
CREATE INDEX IF NOT EXISTS idx_orders_delivery_zone_id ON orders(delivery_zone_id);
CREATE INDEX IF NOT EXISTS idx_orders_review_required ON orders(review_required);
CREATE INDEX IF NOT EXISTS idx_orders_pickup_code ON orders(pickup_code);
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_pickup_code_unique
    ON orders(pickup_code)
    WHERE pickup_code IS NOT NULL AND pickup_code <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_idempotency_key_unique
    ON orders(idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE TABLE IF NOT EXISTS order_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    quantity INTEGER NOT NULL,
    price_at_time DECIMAL(10, 2) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_order_items_quantity_positive CHECK (quantity > 0),
    CONSTRAINT chk_order_items_price_non_negative CHECK (price_at_time >= 0)
);

CREATE INDEX IF NOT EXISTS idx_order_items_order_id ON order_items(order_id);
CREATE INDEX IF NOT EXISTS idx_order_items_product_id ON order_items(product_id);
CREATE INDEX IF NOT EXISTS idx_order_items_order_created_at ON order_items(order_id, created_at ASC);

CREATE TABLE IF NOT EXISTS otp_codes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    phone_number VARCHAR(20) NOT NULL,
    code VARCHAR(6) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_otp_codes_phone_number ON otp_codes(phone_number);
CREATE INDEX IF NOT EXISTS idx_otp_codes_expires_at ON otp_codes(expires_at);
CREATE INDEX IF NOT EXISTS idx_otp_codes_verified ON otp_codes(verified);
CREATE INDEX IF NOT EXISTS idx_otp_codes_phone_verified ON otp_codes(phone_number, verified);

CREATE TABLE IF NOT EXISTS payment_attempts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    provider VARCHAR(20) NOT NULL,
    status VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(255),
    requested_phone VARCHAR(20),
    amount DECIMAL(10, 2) NOT NULL,
    provider_reference VARCHAR(255),
    checkout_url TEXT,
    last_error TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    dispatched_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_payment_attempts_status CHECK (
        status IN ('QUEUED', 'PROCESSING', 'AWAITING_CUSTOMER', 'SUCCEEDED', 'FAILED', 'EXPIRED')
    ),
    CONSTRAINT chk_payment_attempts_amount_non_negative CHECK (amount >= 0),
    CONSTRAINT chk_payment_attempts_attempts_non_negative CHECK (attempts >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_attempts_idempotency_key_unique
    ON payment_attempts(idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_attempts_provider_reference_unique
    ON payment_attempts(provider, provider_reference)
    WHERE provider_reference IS NOT NULL AND provider_reference <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_attempts_active_order_provider
    ON payment_attempts(order_id, provider)
    WHERE status IN ('QUEUED', 'PROCESSING', 'AWAITING_CUSTOMER');
CREATE INDEX IF NOT EXISTS idx_payment_attempts_provider_queue
    ON payment_attempts(provider, status, next_retry_at, created_at);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_provider_status_created_at
    ON payment_attempts(provider, status, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_order_created_at
    ON payment_attempts(order_id, created_at DESC);

CREATE TABLE IF NOT EXISTS provider_throttles (
    provider VARCHAR(20) PRIMARY KEY,
    last_dispatched_at TIMESTAMP
);

INSERT INTO provider_throttles (provider, last_dispatched_at)
VALUES
    ('MPESA', NULL),
    ('CARD', NULL),
    ('PESAPAL', NULL)
ON CONFLICT (provider) DO NOTHING;

CREATE TABLE IF NOT EXISTS unmatched_payments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    provider VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    provider_reference VARCHAR(255),
    order_id_hint VARCHAR(255),
    phone VARCHAR(20),
    hashed_phone VARCHAR(255),
    amount DECIMAL(10, 2) NOT NULL DEFAULT 0,
    payload TEXT NOT NULL,
    resolution_note TEXT,
    resolved_order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMP,
    CONSTRAINT chk_unmatched_payments_status CHECK (status IN ('PENDING', 'RESOLVED', 'REJECTED')),
    CONSTRAINT chk_unmatched_payments_amount_non_negative CHECK (amount >= 0)
);

CREATE INDEX IF NOT EXISTS idx_unmatched_payments_status_created_at
    ON unmatched_payments(status, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_unmatched_payments_provider_reference_pending
    ON unmatched_payments(provider, provider_reference)
    WHERE provider_reference IS NOT NULL
      AND provider_reference <> ''
      AND status = 'PENDING';

CREATE TABLE IF NOT EXISTS prescriptions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    customer_phone VARCHAR(20) NOT NULL,
    media_id VARCHAR(255) NOT NULL,
    media_type VARCHAR(50) NOT NULL,
    file_name VARCHAR(255),
    caption TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    review_notes TEXT,
    reviewed_by_admin_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    reviewed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_prescriptions_status CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED'))
);

CREATE INDEX IF NOT EXISTS idx_prescriptions_order_id ON prescriptions(order_id);
CREATE INDEX IF NOT EXISTS idx_prescriptions_status_created_at ON prescriptions(status, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_prescriptions_customer_phone ON prescriptions(customer_phone);

CREATE TABLE IF NOT EXISTS prescription_reviews (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    prescription_id UUID NOT NULL REFERENCES prescriptions(id) ON DELETE CASCADE,
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    reviewer_admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE RESTRICT,
    decision VARCHAR(20) NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_prescription_reviews_decision CHECK (decision IN ('APPROVED', 'REJECTED'))
);

CREATE INDEX IF NOT EXISTS idx_prescription_reviews_prescription_id ON prescription_reviews(prescription_id);
CREATE INDEX IF NOT EXISTS idx_prescription_reviews_order_id ON prescription_reviews(order_id);
CREATE INDEX IF NOT EXISTS idx_prescription_reviews_reviewer ON prescription_reviews(reviewer_admin_user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS outbound_messages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    phone_number VARCHAR(20) NOT NULL,
    message_type VARCHAR(50) NOT NULL,
    payload TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    last_error TEXT,
    sent_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_outbound_messages_status_created_at
    ON outbound_messages(status, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_outbound_messages_phone_number
    ON outbound_messages(phone_number, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_type VARCHAR(50) NOT NULL,
    entity_id VARCHAR(64) NOT NULL,
    action VARCHAR(100) NOT NULL,
    actor_id VARCHAR(64) NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_entity ON audit_logs(entity_type, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON audit_logs(actor_id, created_at DESC);

COMMIT;
