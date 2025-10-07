CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TABLE IF NOT EXISTS users (
                                     id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                     username VARCHAR(255) UNIQUE NOT NULL,
                                     created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                                     updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS accounts (
                                        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                        user_id UUID REFERENCES users(id) ON DELETE CASCADE,
                                        balance TEXT NOT NULL DEFAULT '', -- Encrypted string (AES-GCM).
                                        currency VARCHAR(3) NOT NULL DEFAULT 'CAD',
                                        created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                                        updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS orders (
                                      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                      user_id UUID REFERENCES users(id) ON DELETE CASCADE NOT NULL,
                                      account_id UUID REFERENCES accounts(id) ON DELETE CASCADE NOT NULL,
                                      idempotency_key UUID NOT NULL UNIQUE,
                                      amount DECIMAL(15, 2) NOT NULL,
                                      status VARCHAR(50) NOT NULL DEFAULT 'pending',
                                      created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                                      updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS orders_ai_dataset (
                                      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                      user_id UUID REFERENCES users(id) ON DELETE CASCADE NOT NULL,
                                      account_id UUID REFERENCES accounts(id) ON DELETE CASCADE NOT NULL,
                                      idempotency_key UUID NOT NULL UNIQUE,
                                      amount DECIMAL(15, 2) NOT NULL,
                                      status VARCHAR(50) NOT NULL DEFAULT 'pending',
                                      is_fraud BOOLEAN NOT NULL DEFAULT FALSE,
                                      created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                                      updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_idempotency_key ON orders(idempotency_key);