package catalog

const (
	QuerySetup = `
		SELECT
			coalesce(source_pg_uri, ''),
			coalesce(target_pg_uri, ''),
			coalesce(snapshot, ''),
			coalesce(split_tables_larger_than, 0),
			coalesce(split_max_parts, 0),
			coalesce(plugin, ''),
			coalesce(slot_name, '')
		FROM setup
		WHERE id = 1`

	QuerySections = `
		SELECT
			name,
			coalesce(fetched, 0),
			coalesce(start_time_epoch, 0),
			coalesce(done_time_epoch, 0),
			coalesce(duration, 0)
		FROM section
		ORDER BY start_time_epoch`

	QueryTables = `
		SELECT
			t.oid,
			t.qname,
			coalesce(t.nspname, ''),
			coalesce(t.relname, ''),
			coalesce(t.relpages, 0),
			coalesce(t.reltuples, 0),
			coalesce(ts.bytes, 0),
			coalesce(ts.bytes_pretty, ''),
			coalesce(t.exclude_data, 0),
			coalesce(t.part_key, '')
		FROM s_table t
		LEFT JOIN s_table_size ts ON ts.oid = t.oid
		ORDER BY coalesce(ts.bytes, 0) DESC`

	QueryTableParts = `
		SELECT
			oid,
			partnum,
			partcount,
			coalesce(min, 0),
			coalesce(max, 0),
			coalesce(count, 0)
		FROM s_table_part
		WHERE oid = ?
		ORDER BY partnum`

	QueryActiveProcesses = `
		SELECT
			pid,
			coalesce(ps_type, ''),
			coalesce(ps_title, ''),
			coalesce(tableoid, 0),
			coalesce(partnum, 0),
			coalesce(indexoid, 0)
		FROM process
		ORDER BY pid`

	QuerySummaries = `
		SELECT
			coalesce(pid, 0),
			coalesce(tableoid, 0),
			coalesce(partnum, 0),
			coalesce(indexoid, 0),
			coalesce(conoid, 0),
			coalesce(start_time_epoch, 0),
			coalesce(done_time_epoch, 0),
			coalesce(duration, 0),
			coalesce(bytes, 0),
			coalesce(command, '')
		FROM summary
		ORDER BY start_time_epoch`

	QueryTimings = `
		SELECT
			id,
			coalesce(label, ''),
			coalesce(start_time_epoch, 0),
			coalesce(done_time_epoch, 0),
			coalesce(duration, 0),
			coalesce(duration_pretty, ''),
			coalesce(count, 0),
			coalesce(bytes, 0),
			coalesce(bytes_pretty, '')
		FROM timings
		ORDER BY id`

	QuerySentinel = `
		SELECT
			coalesce(startpos, ''),
			coalesce(endpos, ''),
			coalesce(apply, 0),
			coalesce(write_lsn, ''),
			coalesce(flush_lsn, ''),
			coalesce(replay_lsn, '')
		FROM sentinel
		WHERE id = 1`
)
