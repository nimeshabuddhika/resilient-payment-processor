create table users
(
    id         uuid                     default gen_random_uuid() not null
        primary key,
    username   varchar(255)                                       not null
        unique,
    created_at timestamp with time zone default CURRENT_TIMESTAMP,
    updated_at timestamp with time zone default CURRENT_TIMESTAMP
);

create table accounts
(
    id         uuid                     default gen_random_uuid() not null,
    user_id    uuid                                               not null
        references users
            on delete cascade,
    balance    text                     default ''::text                 not null,
    currency   varchar(3)               default 'CAD'::character varying not null,
    created_at timestamp with time zone default CURRENT_TIMESTAMP,
    updated_at timestamp with time zone default CURRENT_TIMESTAMP,
    primary key (id, user_id)
);

create index idx_accounts_user_id
    on accounts (user_id);

create table orders
(
    id              uuid                     default gen_random_uuid() not null
        constraint orders_pk
            primary key,
    user_id         uuid                                               not null,
    account_id      uuid                                               not null,
    idempotency_key uuid                                               not null,
    amount          text                     default ''::text                     not null,
    currency        varchar(3)               default 'CAD'::character varying     not null,
    status          varchar(50)              default 'pending'::character varying not null,
    message         varchar(200)             default ''::character varying,
    created_at      timestamp with time zone default CURRENT_TIMESTAMP,
    updated_at      timestamp with time zone default CURRENT_TIMESTAMP,
    constraint orders_accounts_id_user_id_fk
        foreign key (account_id, user_id) references accounts
);


create index idx_orders_user_id
    on orders (user_id);

create index idx_orders_idempotency_key
    on orders (idempotency_key);

create index orders_account_id_index
    on orders (account_id);

create table orders_ai_dataset
(
    id                   uuid                     default gen_random_uuid() not null
        constraint orders_ai_dataset_pk
            primary key,
    user_id              uuid                                               not null,
    account_id           uuid                                               not null,
    idempotency_key      uuid                                               not null,
    amount               float                    default 0 not null,      -- Naked amount as string
    currency             varchar(3)               default 'CAD'::character varying     not null,
    is_fraud             boolean                  default false,
    transaction_velocity integer                  default 0                 not null,            -- Orders per hour for user (simulated)
    amount_deviation     numeric(10, 2)           default 0.00              not null,            -- Deviation from user's avg order amount
    created_at           timestamp with time zone default CURRENT_TIMESTAMP,
    updated_at           timestamp with time zone default CURRENT_TIMESTAMP,
    constraint orders_ai_dataset_accounts_id_user_id_fk
        foreign key (account_id, user_id) references accounts
);

create index idx_orders_ai_dataset_user_id
    on orders_ai_dataset (user_id);

create index idx_orders_ai_dataset_idempotency_key
    on orders_ai_dataset (idempotency_key);

create index orders_ai_dataset_account_id_index
    on orders_ai_dataset (account_id);

