--
-- Schema for the --copy-groups N (N=2) end-to-end CDC test.
--
-- Two tables with a cross-group foreign key:
--
--   * "orders" is deliberately the largest table (bulk-seeded below), so the
--     --copy-groups 2 bin-packer places it alone in group 0.
--   * "customers" is small, so it lands in group 1.
--
-- The FK orders.customer_id -> customers.id therefore spans the two copy
-- groups. This is the cross-group FK relationship the barrier must validate
-- once, after every group converges at the common LSN.
--

begin;

create table customers
(
    id    bigint generated always as identity primary key,
    name  text not null,
    notes text
);

create table orders
(
    id          bigint generated always as identity primary key,
    customer_id bigint not null references customers(id),
    amount      numeric(12,2) not null,
    payload     text
);

create index orders_customer_id_idx on orders(customer_id);

create table scratch
(
    id      bigint generated always as identity primary key,
    payload text not null
);

create table "orders""quoted"
(
    id      bigint primary key,
    payload text not null
);

commit;
