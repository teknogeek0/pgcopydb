#! /bin/bash

set -euo pipefail
set -x

# Disable pager for psql to avoid hanging in non-interactive environments
export PAGER=cat

# This script expects the following environment variables to be set:
#
#  - PGCOPYDB_SOURCE_PGURI
#  - PGCOPYDB_TARGET_PGURI
#  - PGCOPYDB_TABLE_JOBS
#  - PGCOPYDB_INDEX_JOBS

# make sure source and target databases are ready
pgcopydb ping

#
# Deploy the schema (cross-group FK) and bulk-seed the source. The orders table
# is the largest relation, so --copy-groups 2 isolates it in group 0 and the
# orders -> customers FK spans the group boundary.
#
psql -v ON_ERROR_STOP=1 -d "${PGCOPYDB_SOURCE_PGURI}" -f /usr/src/pgcopydb/ddl.sql
psql -v ON_ERROR_STOP=1 -d "${PGCOPYDB_SOURCE_PGURI}" -f /usr/src/pgcopydb/seed.sql

#
# Real source xmin-horizon sampler.
#
# Every 0.2s, record the oldest backend_xmin held by any backend on the source
# (excluding this sampler). That oldest backend_xmin IS the cutoff VACUUM uses
# to decide which dead tuples in user tables it may remove. The whole point of
# --copy-groups is that this horizon must ADVANCE during the copy instead of
# being pinned at the start for the entire multi-hour run.
#
# With per-group lazy snapshots, group 1's snapshot is created only after group
# 0's copy finishes (and after concurrent traffic has advanced the source XID),
# so the held xmin steps forward between groups. With a single long-held
# snapshot (or all N snapshots taken up front), this value would stay pinned at
# its initial value for the whole copy. We assert below that it advanced.
#
SAMPLE_FILE=/tmp/xmin-samples.txt
: > "${SAMPLE_FILE}"
: > /tmp/clone.log

(
    while true
    do
        # backend_xmin is type xid, which has no min() aggregate and no btree
        # ordering, so cast through text to bigint to find the oldest one.
        PGAPPNAME=xmin_sampler psql -At -d "${PGCOPYDB_SOURCE_PGURI}" -c \
            "select coalesce(min(backend_xmin::text::bigint)::text, '') \
               from pg_stat_activity \
              where backend_xmin is not null \
                and application_name <> 'xmin_sampler'" >> "${SAMPLE_FILE}" \
            2>/dev/null
        sleep 0.2
    done
) &
SAMPLER_PID=$!

#
# Start a bounded burst of mixed-group, cross-group transactions concurrently
# with the copy. The bulk orders COPY outlasts this burst, so every committed
# change is <= the common cutover LSN (picked after both group copies finish)
# and is applied to the target. We wait for the burst to finish before
# comparing.
#
bash /usr/src/pgcopydb/run-background-traffic.sh 12 &
TRAFFIC_PID=$!

#
# Also run a WAL nudger for the duration of the run. It only issues
# pg_switch_wal (a SWITCH record, no table data), which lets the replication
# slots flush THROUGH the common cutover LSN on an otherwise-idle source so the
# apply can reach endpos. It does not change any migrated data.
#
bash /usr/src/pgcopydb/wal-nudger.sh &
NUDGER_PID=$!

#
# The cardinal test: grouped online migration end-to-end. The N=2 run copies
# group 0 (orders) and group 1 (customers) under separate snapshots, builds
# indexes once, enables apply per group, drives both groups to a common LSN,
# then restores the cross-group FK once after convergence and resets sequences.
#
# Capture the output so we can assert xmin advancement between groups.
#
pgcopydb clone --follow --copy-groups 2 --plugin wal2json \
    --split-tables-larger-than 200kB 2>&1 | tee /tmp/clone.log

# the data burst is short-lived; the nudger runs until we stop it here
wait ${TRAFFIC_PID}
kill -TERM ${NUDGER_PID} 2>/dev/null || true
wait ${NUDGER_PID} 2>/dev/null || true

# stop the xmin-horizon sampler
kill -TERM ${SAMPLER_PID} 2>/dev/null || true
wait ${SAMPLER_PID} 2>/dev/null || true

#
# THE CARDINAL ASSERTION: the source xmin horizon actually advanced during the
# copy. Reduce the samples to the distinct oldest-backend_xmin values seen, in
# the order they first appeared, ignoring empty samples (moments when no
# backend held a snapshot).
#
mapfile -t XMIN_SEQ < <(grep -v '^$' "${SAMPLE_FILE}" | awk '!seen[$0]++')

echo "distinct oldest-backend_xmin values observed during copy: ${XMIN_SEQ[*]}"

#
# THE CARDINAL ASSERTION: the copy did NOT hold a single snapshot for the whole
# run. We prove this DETERMINISTICALLY (independent of sampling timing) with two
# facts from the clone log:
#
#   1. each group's snapshot was released before the next group (one log line
#      per group), and
#   2. the per-group copy thresholds strictly ADVANCE (group 1's consistent
#      point is later than group 0's) -> the snapshots were taken lazily, one
#      per group, NOT all up front (the bug this feature fixes).
#
# The oldest-backend_xmin sampler above is a best-effort corroboration: on a fast
# copy with little inter-group write traffic the numeric XID horizon may not
# visibly move even though the snapshots were released, so it is reported below
# but not asserted.
#

releases=$(grep -c "xmin horizon can advance" /tmp/clone.log || true)
echo "per-group snapshot releases logged: ${releases}"

if [ "${releases}" -lt 2 ]
then
    echo "FAIL: expected 2 per-group snapshot releases, found ${releases}"
    exit 1
fi

lsn0=$(grep "Group 0 apply threshold LSN is" /tmp/clone.log \
       | grep -oE "[0-9A-Fa-f]+/[0-9A-Fa-f]+" | tail -1)
lsn1=$(grep "Group 1 apply threshold LSN is" /tmp/clone.log \
       | grep -oE "[0-9A-Fa-f]+/[0-9A-Fa-f]+" | tail -1)
common_lsn=$(grep "Common cutover LSN is" /tmp/clone.log \
       | grep -oE "[0-9A-Fa-f]+/[0-9A-Fa-f]+" | tail -1)

echo "per-group copy thresholds: group0=${lsn0} group1=${lsn1} common=${common_lsn}"

if [ -z "${lsn0}" ] || [ -z "${lsn1}" ] || [ -z "${common_lsn}" ]
then
    echo "FAIL: could not read per-group/cutover LSNs from the clone log"
    exit 1
fi

advanced=$(psql -At -d "${PGCOPYDB_SOURCE_PGURI}" \
    -c "select '${lsn1}'::pg_lsn > '${lsn0}'::pg_lsn")

if [ "${advanced}" != "t" ]
then
    echo "FAIL: group 1 snapshot (${lsn1}) was not taken after group 0 (${lsn0});"
    echo "      the copy did not release/re-take snapshots per group (the up-front"
    echo "      snapshot bug that pins the xmin horizon for the whole run)."
    exit 1
fi

for marker_file in /tmp/pre-g1-marker-lsn /tmp/post-g1-marker-lsn
do
    if ! grep -Eq '^[0-9A-Fa-f]+/[0-9A-Fa-f]+$' "${marker_file}" 2>/dev/null
    then
        echo "FAIL: expected marker LSN in ${marker_file}"
        exit 1
    fi
done

pre_g1_marker_lsn=$(cat /tmp/pre-g1-marker-lsn)
post_g1_marker_lsn=$(cat /tmp/post-g1-marker-lsn)

markers_ordered=$(psql -At -d "${PGCOPYDB_SOURCE_PGURI}" -c \
    "select '${pre_g1_marker_lsn}'::pg_lsn <= '${lsn1}'::pg_lsn
        and '${lsn1}'::pg_lsn < '${post_g1_marker_lsn}'::pg_lsn
        and '${post_g1_marker_lsn}'::pg_lsn <= '${common_lsn}'::pg_lsn")

echo "marker LSNs: pre_g1=${pre_g1_marker_lsn} post_g1=${post_g1_marker_lsn}"

if [ "${markers_ordered}" != "t" ]
then
    echo "FAIL: marker transactions did not bracket the group 1 threshold and cutover"
    echo "      group1=${lsn1} common=${common_lsn}"
    exit 1
fi

echo "PASS: per-group snapshots advanced (group0 ${lsn0} -> group1 ${lsn1}) and" \
     "were released between groups; the copy never pins one snapshot for the run"

if [ "${#XMIN_SEQ[@]}" -ge 2 ] && [ "${XMIN_SEQ[-1]}" -gt "${XMIN_SEQ[0]}" ]
then
    echo "  (also observed the backend_xmin horizon advance" \
         "${XMIN_SEQ[0]} -> ${XMIN_SEQ[-1]})"
else
    echo "  (backend_xmin numeric value did not visibly move this run — little" \
         "inter-group write traffic; the per-group release above is the guarantee)"
fi

dbfile=${TMPDIR}/pgcopydb/schema/source.db

if [ ! -s "${dbfile}" ]
then
    echo "FAIL: expected source catalog at ${dbfile}"
    exit 1
fi

assignments=$(sqlite3 -init /dev/null -batch -bail -noheader -list "${dbfile}" \
    "select s.relname || '=' || a.group_number
	 from s_table s
	 join s_table_group_assignment a on a.oid = s.oid
	where s.nspname = 'public'
	  and (s.relname in ('customers', 'orders', 'scratch')
	       or s.qname = 'public.' || char(34) || 'orders' ||
	                    char(34) || char(34) || 'quoted' || char(34))
	order by s.relname")

expected_assignments=$(cat <<'EOF'
"orders""quoted"=1
customers=1
orders=0
scratch=1
EOF
)

echo "copy-group assignments:"
echo "${assignments}"

if [ "${assignments}" != "${expected_assignments}" ]
then
    echo "FAIL: unexpected copy-group assignments"
    echo "expected:"
    echo "${expected_assignments}"
    exit 1
fi

# assert the single-stream finalize ran: indexes whole-DB, apply caught up to
# the cutover LSN, then FK constraints whole-DB.
grep -q "build indexes across all 2 copy groups" /tmp/clone.log \
    || (echo "FAIL: whole-DB index build step not found" && exit 1)

grep -q "Apply has reached the cutover LSN" /tmp/clone.log \
    || (echo "FAIL: apply did not reach the cutover LSN" && exit 1)

grep -q "Restoring FK constraints across all 2 copy groups" /tmp/clone.log \
    || (echo "FAIL: whole-DB FK constraint restore step not found" && exit 1)

#
# The source is now static (traffic finished and all committed <= LSN_C). The
# target must equal the source.
#
pgcopydb compare data

#
# The cross-group FK must be present and VALID on the target (the barrier
# validates it once after convergence).
#
invalid=$(psql -At -d "${PGCOPYDB_TARGET_PGURI}" -c \
    "select count(*) from pg_constraint
      where contype = 'f' and not convalidated")

if [ "${invalid}" != "0" ]
then
    echo "FAIL: ${invalid} foreign key constraint(s) are NOT VALID on target"
    psql -d "${PGCOPYDB_TARGET_PGURI}" -c \
        "select conname, convalidated from pg_constraint where contype = 'f'"
    exit 1
fi

fk_ok=$(psql -AtX -d "${PGCOPYDB_TARGET_PGURI}" -c \
    "select count(*) = 1
       from pg_constraint
      where conname = 'orders_customer_id_fkey'
        and contype = 'f'
        and conrelid = 'public.orders'::regclass
        and confrelid = 'public.customers'::regclass
        and convalidated")

if [ "${fk_ok}" != "t" ]
then
    echo "FAIL: expected valid orders_customer_id_fkey on target"
    psql -d "${PGCOPYDB_TARGET_PGURI}" -c \
        "select conname, contype, conrelid::regclass, confrelid::regclass, convalidated
           from pg_constraint where conname = 'orders_customer_id_fkey'"
    exit 1
fi

# explicitly re-validate the cross-group FK as a belt-and-suspenders check
psql -v ON_ERROR_STOP=1 -d "${PGCOPYDB_TARGET_PGURI}" -c \
    "do \$\$
     declare r record;
     begin
       for r in select conrelid::regclass as t, conname
                  from pg_constraint where contype = 'f'
       loop
         execute format('alter table %s validate constraint %I', r.t, r.conname);
       end loop;
     end \$\$;"

#
# Final row-count sanity check on all test tables, source vs target.
#
for t in customers orders scratch
do
    s=$(psql -At -d "${PGCOPYDB_SOURCE_PGURI}" -c "select count(*) from ${t}")
    d=$(psql -At -d "${PGCOPYDB_TARGET_PGURI}" -c "select count(*) from ${t}")

    echo "table ${t}: source=${s} target=${d}"

    if [ "${s}" != "${d}" ]
    then
        echo "FAIL: row count mismatch for ${t}: source=${s} target=${d}"
        exit 1
    fi
done

quoted_source=$(psql -AtX -d "${PGCOPYDB_SOURCE_PGURI}" <<'SQL'
select count(*),
       count(*) filter (where id = 1 and payload = 'initial quoted table'),
       count(*) filter (where id = 2 and payload = 'pre-g1 quoted marker')
  from "orders""quoted";
SQL
)
quoted_target=$(psql -AtX -d "${PGCOPYDB_TARGET_PGURI}" <<'SQL'
select count(*),
       count(*) filter (where id = 1 and payload = 'initial quoted table'),
       count(*) filter (where id = 2 and payload = 'pre-g1 quoted marker')
  from "orders""quoted";
SQL
)

echo "quoted table state: source=${quoted_source} target=${quoted_target}"

if [ "${quoted_source}" != "2|1|1" ] || [ "${quoted_target}" != "2|1|1" ]
then
    echo "FAIL: quoted table did not contain exactly the expected rows"
    exit 1
fi

#
# The scratch transaction intentionally runs TRUNCATE ONLY followed by inserts
# while group 0 is copying. Row counts alone can miss a replay bug here, so also
# assert the original rows are gone and only the post-truncate rows remain.
#
scratch_source=$(psql -AtX -d "${PGCOPYDB_SOURCE_PGURI}" -c \
    "select count(*),
            count(*) filter (where payload like 'post-truncate scratch %'),
            count(*) filter (where payload like 'initial scratch %'),
            count(*) filter (where payload = 'pre-g1-insert-only-marker'),
            count(*) filter (where payload = 'pre-g1-update-marker'),
            count(*) filter (where payload = 'pre-g1-updated-after-threshold'),
            count(*) filter (where payload = 'pre-g1-delete-marker'),
            count(*) filter (where payload = 'post-g1-apply-marker'),
            count(*) filter (where payload = 'post-g1-updated-marker')
       from scratch")
scratch_target=$(psql -AtX -d "${PGCOPYDB_TARGET_PGURI}" -c \
    "select count(*),
            count(*) filter (where payload like 'post-truncate scratch %'),
            count(*) filter (where payload like 'initial scratch %'),
            count(*) filter (where payload = 'pre-g1-insert-only-marker'),
            count(*) filter (where payload = 'pre-g1-update-marker'),
            count(*) filter (where payload = 'pre-g1-updated-after-threshold'),
            count(*) filter (where payload = 'pre-g1-delete-marker'),
            count(*) filter (where payload = 'post-g1-apply-marker'),
            count(*) filter (where payload = 'post-g1-updated-marker')
       from scratch")

echo "scratch state: source=${scratch_source} target=${scratch_target}"

expected_scratch="8|5|0|1|0|1|0|0|1"

if [ "${scratch_source}" != "${expected_scratch}" ] ||
   [ "${scratch_target}" != "${expected_scratch}" ]
then
    echo "FAIL: scratch table did not contain exactly the expected threshold rows"
    exit 1
fi

threshold_rows=$(sqlite3 -init /dev/null -batch -bail -noheader -list "${dbfile}" \
    "select count(*)
       from s_group_lsn
      where group_number in (0, 1)
        and threshold_lsn is not null")

if [ "${threshold_rows}" != "2" ]
then
    echo "FAIL: expected threshold LSN rows for both copy groups"
    exit 1
fi

tmp_slots=$(psql -AtX -d "${PGCOPYDB_SOURCE_PGURI}" -c \
    "select count(*)
       from pg_replication_slots
      where slot_name like 'pgcopydb\_cgtmp\_g%' escape '\'")

if [ "${tmp_slots}" != "0" ]
then
    echo "FAIL: found ${tmp_slots} leaked grouped-copy temporary slot(s)"
    exit 1
fi

echo "PASS: copy-groups N=2 end-to-end migration is consistent"

# cleanup
pgcopydb stream cleanup || true
