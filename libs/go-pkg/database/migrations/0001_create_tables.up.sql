CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TABLE IF NOT EXISTS users
(
    id
    UUID
    PRIMARY
    KEY
    DEFAULT
    gen_random_uuid
(
),
    username VARCHAR
(
    255
) UNIQUE NOT NULL,
    email VARCHAR
(
    255
) UNIQUE NOT NULL,
    password_hash BYTEA NOT NULL, -- App-level AES encrypt before insert.
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
                             );

CREATE TABLE IF NOT EXISTS accounts
(
    id
    UUID
    PRIMARY
    KEY
    DEFAULT
    gen_random_uuid
(
),
    user_id UUID REFERENCES users
(
    id
) ON DELETE CASCADE,
    balance DECIMAL
(
    15,
    2
) NOT NULL DEFAULT 0.00, -- App-level encrypt if sensitive.
    currency VARCHAR
(
    3
) NOT NULL DEFAULT 'USD',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP
  WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
      );

CREATE TABLE IF NOT EXISTS orders
(
    id
    UUID
    PRIMARY
    KEY
    DEFAULT
    gen_random_uuid
(
),
    user_id UUID REFERENCES users
(
    id
) ON DELETE CASCADE,
    account_id UUID REFERENCES accounts
(
    id
)
  ON DELETE CASCADE,
    idempotency_key UUID,
    amount DECIMAL
(
    15,
    2
) NOT NULL,
    status VARCHAR
(
    50
) NOT NULL DEFAULT 'pending', -- e.g., pending, processed, failed.
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP
  WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
      );

-- Indexes for scalability (e.g., Kafka partitioning by user_id).
CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_accounts_idempotency_key ON orders(idempotency_key);