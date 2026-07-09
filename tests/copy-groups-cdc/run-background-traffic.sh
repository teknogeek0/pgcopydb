#! /bin/bash

set -euo pipefail

wait_for_clone_log()
{
    local pattern="$1"
    local description="$2"

    for _ in $(seq 1 1200)
    do
        if grep -q "${pattern}" /tmp/clone.log 2>/dev/null
        then
            return 0
        fi

        sleep 0.05
    done

    echo "FAIL: timed out waiting for ${description}"
    return 1
}

# Phase 1: a bounded burst of mixed-group, cross-group transactions, run
# concurrently with the start of the clone --follow --copy-groups 2 copy phase.
# Each transaction touches tables in BOTH copy groups (customers in group 1,
# orders in group 0) and creates a cross-group parent/child pair, so the
# common-LSN barrier has to converge both groups before the orders -> customers
# FK can be validated.
#
# The burst is short (and the orders table is bulk-seeded so its COPY outlasts
# it), so every one of these transactions is committed BEFORE the parent picks
# the common cutover LSN (after both group copies finish). They are therefore
# all <= LSN_C and all applied, so the target ends up equal to the source.

rounds=${1:-12}

wait_for_clone_log "STEP 4: COPY the data for group 0 of 2" "group 0 COPY to start"

psql -v ON_ERROR_STOP=1 -d "${PGCOPYDB_SOURCE_PGURI}" <<'SQL'
begin;
truncate only scratch;
insert into scratch(payload)
select 'post-truncate scratch ' || g
  from generate_series(1, 5) as g;
commit;
SQL

pre_g1_lsn=$(psql -qAtX -v ON_ERROR_STOP=1 \
    -d "${PGCOPYDB_SOURCE_PGURI}" <<'SQL' \
    | grep -E '^[0-9A-Fa-f]+/[0-9A-Fa-f]+$' | tail -1
begin;
insert into scratch(payload)
values ('pre-g1-insert-only-marker'),
       ('pre-g1-update-marker'),
       ('pre-g1-delete-marker');

insert into "orders""quoted"(id, payload)
values (2, 'pre-g1 quoted marker');
commit;
select pg_current_wal_flush_lsn();
SQL
)

echo "${pre_g1_lsn}" > /tmp/pre-g1-marker-lsn

(
    wait_for_clone_log "Group 1 copy threshold LSN is" "group 1 threshold"

    post_g1_lsn=$(psql -qAtX -v ON_ERROR_STOP=1 \
        -d "${PGCOPYDB_SOURCE_PGURI}" <<'SQL' \
        | grep -E '^[0-9A-Fa-f]+/[0-9A-Fa-f]+$' | tail -1
begin;
update scratch
   set payload = 'pre-g1-updated-after-threshold'
 where payload = 'pre-g1-update-marker';

delete from scratch
 where payload = 'pre-g1-delete-marker';

insert into scratch(payload) values ('post-g1-apply-marker');

update scratch
   set payload = 'post-g1-updated-marker'
 where payload = 'post-g1-apply-marker';
commit;
select pg_current_wal_flush_lsn();
SQL
)

	echo "${post_g1_lsn}" > /tmp/post-g1-marker-lsn
) &
GROUP1_WATCHER_PID=$!

for i in $(seq ${rounds})
do
    psql -v ON_ERROR_STOP=1 -d "${PGCOPYDB_SOURCE_PGURI}" <<'SQL'
begin;
with c as (
    insert into customers(name, notes)
    values ('live ' || clock_timestamp()::text, repeat('y', 100))
    returning id
)
insert into orders(customer_id, amount, payload)
select c.id, (random() * 500)::numeric(12,2), repeat('p', 200)
  from c, generate_series(1, 1 + (random() * 5)::int);

-- also update and delete existing rows to exercise UPDATE/DELETE apply
update orders set amount = amount + 1
 where id in (select id from orders order by id desc limit 3);

delete from orders
 where id in (select id from orders order by id asc limit 2);
commit;
SQL

    sleep 0.1
done

wait "${GROUP1_WATCHER_PID}"

echo "background data traffic complete after ${rounds} rounds"
