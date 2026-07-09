--
-- Seed data for the --copy-groups 2 CDC test.
--
-- A handful of customers (group 1, small) and a large set of orders (group 0,
-- largest table -> its own group). The bulk orders make the orders table the
-- largest relation so the group bin-packer isolates it in group 0, putting the
-- orders -> customers FK across the group boundary. The volume also makes the
-- COPY of group 0 take long enough that concurrent writes overlap the copy.
--

begin;

insert into customers(name, notes)
select 'customer ' || g, repeat('x', 200)
  from generate_series(1, 50) as g;

insert into orders(customer_id, amount, payload)
select 1 + (random() * 49)::int,
       (random() * 1000)::numeric(12,2),
       repeat('o', 400)
  from generate_series(1, 600000) as g;

insert into scratch(payload)
select 'initial scratch ' || g
  from generate_series(1, 20) as g;

insert into "orders""quoted"(id, payload)
values (1, 'initial quoted table');

commit;
