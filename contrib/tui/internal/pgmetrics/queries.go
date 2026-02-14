package pgmetrics

const (
	QueryVersion = `SELECT version()`

	QueryUptime = `SELECT (now() - pg_postmaster_start_time())::text`

	QueryDatabaseStats = `
		SELECT
			d.datname,
			pg_database_size(d.datname)::bigint AS size_bytes,
			(SELECT count(*) FROM pg_stat_activity WHERE datname = d.datname AND state = 'active') AS active_conns,
			(SELECT count(*) FROM pg_stat_activity WHERE datname = d.datname) AS total_conns,
			s.xact_commit + s.xact_rollback AS total_xacts,
			CASE WHEN s.blks_hit + s.blks_read > 0
				THEN round(s.blks_hit::numeric / (s.blks_hit + s.blks_read) * 100, 1)
				ELSE 0
			END AS cache_hit_ratio
		FROM pg_database d
		JOIN pg_stat_database s ON s.datname = d.datname
		WHERE d.datistemplate = false
		ORDER BY pg_database_size(d.datname) DESC`

	QueryConnectionSummary = `
		SELECT
			count(*) AS total,
			count(*) FILTER (WHERE state = 'active') AS active,
			count(*) FILTER (WHERE state = 'idle') AS idle,
			count(*) FILTER (WHERE state = 'idle in transaction') AS idle_in_tx,
			count(*) FILTER (WHERE state = 'idle in transaction (aborted)') AS idle_in_tx_aborted,
			count(*) FILTER (WHERE wait_event_type IS NOT NULL AND state = 'active') AS waiting
		FROM pg_stat_activity
		WHERE backend_type = 'client backend'`

	QueryReplicationSlots = `
		SELECT
			slot_name,
			slot_type,
			active,
			coalesce(restart_lsn::text, '-') AS restart_lsn,
			coalesce(confirmed_flush_lsn::text, '-') AS confirmed_flush_lsn,
			coalesce(wal_status, '-') AS wal_status,
			safe_wal_size,
			coalesce(pg_wal_lsn_diff(pg_current_wal_flush_lsn(), restart_lsn)::bigint, 0) AS retained_bytes
		FROM pg_replication_slots
		ORDER BY slot_name`

	QueryReplicationStats = `
		SELECT
			pid,
			coalesce(application_name, '') AS application_name,
			coalesce(client_addr::text, '') AS client_addr,
			coalesce(state, '') AS state,
			coalesce(sent_lsn::text, '-') AS sent_lsn,
			coalesce(write_lsn::text, '-') AS write_lsn,
			coalesce(flush_lsn::text, '-') AS flush_lsn,
			coalesce(replay_lsn::text, '-') AS replay_lsn,
			coalesce(write_lag::text, '-') AS write_lag,
			coalesce(flush_lag::text, '-') AS flush_lag,
			coalesce(replay_lag::text, '-') AS replay_lag
		FROM pg_stat_replication
		ORDER BY application_name`

	QueryCurrentWALFlushLSN = `SELECT pg_current_wal_flush_lsn()::text`

	QueryActivity = `
		SELECT
			pid,
			coalesce(datname, ''),
			coalesce(usename, ''),
			coalesce(nullif(application_name, ''), '-') AS application_name,
			coalesce(state, 'unknown') AS state,
			coalesce(wait_event_type, '-') AS wait_event_type,
			coalesce(wait_event, '-') AS wait_event,
			coalesce(query, '') AS query,
			coalesce(EXTRACT(EPOCH FROM (now() - query_start))::float8, 0) AS duration_seconds
		FROM pg_stat_activity
		WHERE backend_type = 'client backend'
			AND pid != pg_backend_pid()
		ORDER BY
			CASE state WHEN 'active' THEN 0 WHEN 'idle in transaction' THEN 1 ELSE 2 END,
			query_start ASC NULLS LAST`
)
