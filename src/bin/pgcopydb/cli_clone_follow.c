/*
 * src/bin/pgcopydb/cli_clone_follow.c
 *     Implementation of a CLI which lets you run individual routines
 *     directly
 */

#include <errno.h>
#include <getopt.h>
#include <inttypes.h>
#include <sys/wait.h>
#include <unistd.h>

#include "catalog.h"
#include "cli_common.h"
#include "cli_root.h"
#include "copydb.h"
#include "commandline.h"
#include "env_utils.h"
#include "ld_stream.h"
#include "log.h"
#include "parsing_utils.h"
#include "pgsql.h"
#include "progress.h"
#include "signals.h"
#include "string_utils.h"
#include "summary.h"

#define PGCOPYDB_CLONE_GETOPTS_HELP \
	"  --source                      Postgres URI to the source database\n" \
	"  --target                      Postgres URI to the target database\n" \
	"  --dir                         Work directory to use\n" \
	"  --table-jobs                  Number of concurrent COPY jobs to run\n" \
	"  --index-jobs                  Number of concurrent CREATE INDEX jobs to run\n" \
	"  --restore-jobs                Number of concurrent jobs for pg_restore\n" \
	"  --restore-tolerance           Max pg_restore errors to tolerate (default 10)\n" \
	"  --large-objects-jobs          Number of concurrent Large Objects jobs to run\n" \
	"  --split-tables-larger-than    Same-table concurrency size threshold\n" \
	"  --split-max-parts             Maximum number of jobs for Same-table concurrency \n" \
	"  --copy-groups                 Number of sequential table groups to copy under separate snapshots\n" \
	"  --estimate-table-sizes        Allow using estimates for relation sizes\n" \
	"  --drop-if-exists              On the target database, clean-up from a previous run first\n" \
	"  --roles                       Also copy roles found on source to target\n" \
	"  --no-role-passwords           Do not dump passwords for roles\n" \
	"  --no-owner                    Do not set ownership of objects to match the original database\n" \
	"  --no-acl                      Prevent restoration of access privileges (grant/revoke commands).\n" \
	"  --no-comments                 Do not output commands to restore comments\n" \
	"  --no-tablespaces              Do not output commands to select tablespaces\n" \
	"  --skip-large-objects          Skip copying large objects (blobs)\n" \
	"  --skip-extensions             Skip restoring extensions\n" \
	"  --skip-ext-comments           Skip restoring COMMENT ON EXTENSION\n" \
	"  --skip-collations             Skip restoring collations\n" \
	"  --skip-publications           Skip restoring publications\n" \
	"  --skip-vacuum                 Skip running VACUUM ANALYZE\n" \
	"  --skip-analyze                Skip running vacuumdb --analyze-only\n" \
	"  --skip-db-properties          Skip copying ALTER DATABASE SET properties\n" \
	"  --skip-split-by-ctid          Skip spliting tables by ctid\n" \
	"  --skip-xid-check             Skip the XID wraparound proximity check\n" \
	"  --requirements <filename>     List extensions requirements\n" \
	"  --filters <filename>          Use the filters defined in <filename>\n" \
	"  --fail-fast                   Abort early in case of error\n" \
	"  --restart                     Allow restarting when temp files exist already\n" \
	"  --resume                      Allow resuming operations after a failure\n" \
	"  --not-consistent              Allow taking a new snapshot on the source database\n" \
	"  --snapshot                    Use snapshot obtained with pg_export_snapshot\n" \
	"  --follow                      Implement logical decoding to replay changes\n" \
	"  --plugin                      Output plugin to use (test_decoding, wal2json)\n" \
	"  --wal2json-numeric-as-string  Print numeric data type as string when using wal2json output plugin\n" \
	"  --slot-name                   Use this Postgres replication slot name\n" \
	"  --create-slot                 Create the replication slot\n" \
	"  --origin                      Use this Postgres replication origin node name\n" \
	"  --endpos                      Stop replaying changes when reaching this LSN\n" \
	"  --defer-indexes               Defer index building until after all table data is copied\n" \
	"  --defer-analyze               Defer ANALYZE until after post-data restore\n" \
	"  --defer-validate-fks          Create FK constraints as NOT VALID, skipping validation scan\n" \
	"  --use-copy-binary             Use the COPY BINARY format for COPY operations\n" \

CommandLine clone_command =
	make_command(
		"clone",
		"Clone an entire database from source to target",
		" --source ... --target ... [ --table-jobs ... --index-jobs ... ] ",
		PGCOPYDB_CLONE_GETOPTS_HELP,
		cli_copy_db_getopts,
		cli_clone);

CommandLine fork_command =
	make_command(
		"fork",
		"Clone an entire database from source to target",
		" --source ... --target ... [ --table-jobs ... --index-jobs ... ] ",
		PGCOPYDB_CLONE_GETOPTS_HELP,
		cli_copy_db_getopts,
		cli_clone);


CommandLine follow_command =
	make_command(
		"follow",
		"Replay changes from the source database to the target database",
		" --source ... --target ...  ",
		"  --source                      Postgres URI to the source database\n"
		"  --target                      Postgres URI to the target database\n"
		"  --dir                         Work directory to use\n"
		"  --filters <filename>          Use the filters defined in <filename>\n"
		"  --restart                     Allow restarting when temp files exist already\n"
		"  --resume                      Allow resuming operations after a failure\n"
		"  --not-consistent              Allow taking a new snapshot on the source database\n"
		"  --snapshot                    Use snapshot obtained with pg_export_snapshot\n"
		"  --plugin                      Output plugin to use (test_decoding, wal2json)\n"
		"  --wal2json-numeric-as-string  Print numeric data type as string when using wal2json output plugin\n"
		"  --slot-name                   Use this Postgres replication slot name\n"
		"  --create-slot                 Create the replication slot\n"
		"  --origin                      Use this Postgres replication origin node name\n"
		"  --endpos                      Stop replaying changes when reaching this LSN\n",
		cli_copy_db_getopts,
		cli_follow);


static void clone_and_follow(CopyDataSpec *copySpecs);

static bool start_clone_process(CopyDataSpec *copySpecs, pid_t *pid);

static bool clone_groups_init_specs(CopyDataSpec *copySpecs,
									CopyDataSpec *groupSpecs,
									int groupCount);

static bool clone_groups_copy_phase(CopyDataSpec *copySpecs,
									CopyDataSpec *groupSpecs,
									StreamSpecs *streamSpecs,
									uint64_t *groupThresholdLSN,
									int groupCount);

static bool clone_groups_barrier_cutover(CopyDataSpec *copySpecs,
										 CopyDataSpec *groupSpecs,
										 StreamSpecs *streamSpecs,
										 uint64_t *groupThresholdLSN,
										 int groupCount,
										 pid_t followPID);

static bool start_group_copy_process(CopyDataSpec *groupSpecs, pid_t *pid);

static bool start_follow_process(CopyDataSpec *copySpecs,
								 StreamSpecs *streamSpecs,
								 pid_t *pid);

static bool cli_clone_follow_wait_subprocess(const char *name, pid_t pid,
											 CopyDataSpec *copySpecs);

static bool cloneDB(CopyDataSpec *copySpecs);


/*
 * cli_clone implements the command: pgcopydb clone
 */
void
cli_clone(int argc, char **argv)
{
	CopyDataSpec copySpecs = { 0 };

	(void) cli_copy_prepare_specs(&copySpecs, DATA_SECTION_ALL);

	/* at the moment this is not covered by cli_copy_prepare_specs() */
	copySpecs.follow = copyDBoptions.follow;

	/*
	 * When pgcopydb clone --follow is used, we call the clone_and_follow()
	 * function which does it all, and just quit.
	 */
	if (copySpecs.follow)
	{
		(void) clone_and_follow(&copySpecs);
		exit(EXIT_CODE_QUIT);
	}

	/*
	 * From now on, we know the --follow option has not been used, it's all
	 * about doing a bare clone operation.
	 *
	 * First, make sure to export a snapshot.
	 */
	bool exportSnapshot = copydb_should_export_snapshot(&copySpecs);

	if (exportSnapshot && !copydb_prepare_snapshot(&copySpecs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	pid_t clonePID = -1;

	if (!start_clone_process(&copySpecs, &clonePID))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	/* wait until the clone process is finished */
	bool success =
		cli_clone_follow_wait_subprocess("clone", clonePID, &copySpecs);

	/* close our top-level copy db connection and snapshot */
	if (exportSnapshot &&
		copySpecs.sourceSnapshot.state != SNAPSHOT_STATE_CLOSED)
	{
		if (!copydb_close_snapshot(&copySpecs))
		{
			/* errors have already been logged */
			exit(EXIT_CODE_SOURCE);
		}
	}

	/* make sure all sub-processes are now finished */
	bool allExitsAreZero = copydb_wait_for_subprocesses(copySpecs.failFast);

	if (!success || !allExitsAreZero)
	{
		exit(EXIT_CODE_INTERNAL_ERROR);
	}
}


/*
 * clone_and_follow implements the command: pgcopydb clone --follow
 */
static void
clone_and_follow(CopyDataSpec *copySpecs)
{
	/*
	 * --copy-groups is a SINGLE-stream design: one permanent slot and one follow
	 * triplet, regardless of N. So there is exactly one StreamSpecs. The per-group
	 * copy thresholds (LSN_g, one per group) are carried in a small uint64 array,
	 * not in a heavyweight StreamSpecs per group.
	 */
	int groupCount = copySpecs->copyGroups >= 1 ? copySpecs->copyGroups : 1;

	StreamSpecs streamSpecsStorage = { 0 };
	StreamSpecs *streamSpecs = &streamSpecsStorage;

	uint64_t *groupThresholdLSN = NULL;

	if (groupCount > 1)
	{
		groupThresholdLSN =
			(uint64_t *) calloc(groupCount, sizeof(uint64_t));

		if (groupThresholdLSN == NULL)
		{
			log_error(ALLOCATION_FAILED_ERROR);
			exit(EXIT_CODE_INTERNAL_ERROR);
		}
	}

	/*
	 * Refrain from logging SQL statements in the apply module, because they
	 * contain user data. That said, when --trace has been used, bypass that
	 * privacy feature.
	 */
	bool logSQL = log_get_level() <= LOG_TRACE;

	/*
	 * The CDC stream is ALWAYS a single stream, even with --copy-groups N > 1:
	 * one permanent slot decodes all WAL and one follow triplet applies it, with
	 * a per-group commit-LSN threshold enforcing exactly-once. So element [0] is
	 * initialised with groupCount = 1 (today's single-stream identity: default
	 * slot/origin names, the shared source.db catalog). copySpecs->copyGroups
	 * (N) drives only the copy-phase partitioning and the apply threshold, not
	 * the stream topology.
	 */
	if (!stream_init_specs(streamSpecs,
						   &(copySpecs->cfPaths.cdc),
						   &(copySpecs->connStrings),
						   &(copyDBoptions.slot),
						   copyDBoptions.origin,
						   copyDBoptions.endpos,
						   STREAM_MODE_CATCHUP,
						   &(copySpecs->catalogs.source),
						   &(copySpecs->filters),
						   copyDBoptions.stdIn,
						   copyDBoptions.stdOut,
						   logSQL))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	/*
	 * Carry the real number of copy groups onto the (single) stream so the apply
	 * context can gate the per-group commit-LSN threshold filter. The stream
	 * topology stays single (groupCount == 1); copyGroups is only the filter's
	 * on/off + N. Inert at the single-group default.
	 */
	streamSpecs->copyGroups = groupCount;

	/*
	 * When using pgcopydb clone --follow --restart we first cleanup the
	 * previous setup, and that includes dropping the replication slot.
	 */
	if (copySpecs->restart)
	{
		log_info("Clean-up replication setup, per --restart");

		if (!stream_cleanup_databases(copySpecs,
									  copyDBoptions.slot.slotName,
									  copyDBoptions.origin))
		{
			/* errors have already been logged */
			exit(EXIT_CODE_INTERNAL_ERROR);
		}
	}

	/*
	 * First create/export a snapshot for the whole clone --follow operations.
	 */
	if (!follow_export_snapshot(copySpecs, streamSpecs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_SOURCE);
	}

	/*
	 * When the source is a read-only standby, validate that prerequisites
	 * are met: PostgreSQL >= 16 (required for logical replication from
	 * standby) and hot_standby_feedback = on (prevents replication slot
	 * invalidation on the standby).
	 */
	if (copySpecs->sourceSnapshot.isReadOnly)
	{
		PGSQL srcValidation = { 0 };

		if (!pgsql_init(&srcValidation,
						copySpecs->connStrings.source_pguri,
						PGSQL_CONN_SOURCE))
		{
			log_error("Failed to init connection for standby validation");
			exit(EXIT_CODE_SOURCE);
		}

		if (!pgsql_server_version(&srcValidation))
		{
			log_error("Failed to query server version for standby validation");
			pgsql_finish(&srcValidation);
			exit(EXIT_CODE_SOURCE);
		}

		if (srcValidation.pgversion_num < 160000)
		{
			log_fatal("Logical replication from a standby requires "
					  "PostgreSQL 16 or later, source server is %s",
					  srcValidation.pgversion);
			pgsql_finish(&srcValidation);
			exit(EXIT_CODE_SOURCE);
		}

		/*
		 * Query hot_standby_feedback; when it is off the primary may remove
		 * rows that the standby's logical replication slot still needs,
		 * leading to replication slot invalidation.
		 */
		{
			SingleValueResultContext ctx =
			{ { 0 }, PGSQL_RESULT_BOOL, false };

			const char *sql =
				"SELECT current_setting('hot_standby_feedback')::bool";

			if (!pgsql_execute_with_params(&srcValidation, sql,
										   0, NULL, NULL,
										   &ctx,
										   &parseSingleValueResult))
			{
				log_error("Failed to query hot_standby_feedback");
				pgsql_finish(&srcValidation);
				exit(EXIT_CODE_SOURCE);
			}

			if (!ctx.parsedOk || !ctx.boolVal)
			{
				log_fatal("Logical replication from a standby requires "
						  "hot_standby_feedback = on to prevent "
						  "replication slot invalidation");
				pgsql_finish(&srcValidation);
				exit(EXIT_CODE_SOURCE);
			}
		}

		log_info("Standby validation passed: PostgreSQL %s with "
				 "hot_standby_feedback = on",
				 srcValidation.pgversion);

		pgsql_finish(&srcValidation);
	}

	/*
	 * When --follow has been used, we start two subprocess (clone, follow).
	 * Before doing that though, we want to make sure it was possible to setup
	 * the source and target database for Change Data Capture.
	 */
	if (!follow_setup_databases(copySpecs, streamSpecs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	/*
	 * We fetch the schema here, rather than later in the clone subprocess,
	 * which simply reuses this cached data. This is done to avoid lock
	 * contention between the clone and follow subprocesses, as they both try to
	 * write concurrently to the source.db SQLite database, leading one to
	 * failure. This is also necessary for plugins like test_decoding, which
	 * require information such as primary keys.
	 *
	 * In the future, if the follow subprocess doesn't need a catalog (e.g. if
	 * we remove test_decoding), we should separate out tables for the follow
	 * subprocess into their own database.
	 */
	if (!copydb_fetch_schema_and_prepare_specs(copySpecs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	/*
	 * Close catalogs before forking so that each subprocess gets its own
	 * fresh SQLite connection. Sharing a pre-fork SQLite connection leads
	 * to "database disk image is malformed" errors when multiple processes
	 * access the database concurrently (WAL checkpoint interference).
	 *
	 * The schema data is already in memory (in copySpecs), so closing here
	 * is safe. Each subprocess reopens the catalog as needed.
	 */
	if (!catalog_close_from_specs(copySpecs))
	{
		log_warn("Failed to close catalogs before fork");
	}

	/*
	 * When --copy-groups N (N > 1) is used, initialise the per-group CopyDataSpec
	 * structs used by the copy phase. No replication slot or snapshot is created
	 * here; each group's snapshot is exported lazily, immediately before that
	 * group's copy (and released right after), so the source xmin horizon is only
	 * ever pinned for the duration of the group currently being copied, not for
	 * the whole multi-hour run. Skipped at the single-group default.
	 */
	CopyDataSpec *groupSpecs = NULL;

	if (groupCount > 1)
	{
		groupSpecs =
			(CopyDataSpec *) calloc(groupCount, sizeof(CopyDataSpec));

		if (groupSpecs == NULL)
		{
			log_error(ALLOCATION_FAILED_ERROR);
			exit(EXIT_CODE_INTERNAL_ERROR);
		}

		if (!clone_groups_init_specs(copySpecs, groupSpecs, groupCount))
		{
			/* errors have already been logged */
			(void) copydb_fatal_exit();
			exit(EXIT_CODE_INTERNAL_ERROR);
		}
	}

	/*
	 * Preparation and snapshot are now done, time to fork our two main worker
	 * processes.
	 */
	pid_t clonePID = -1;
	pid_t followPID = -1;
	bool success = true;

	if (groupCount > 1)
	{
		/*
		 * --copy-groups N (N > 1): the CDC side is a SINGLE stream (one permanent
		 * slot, one follow triplet). Start the follow FIRST so its receiver drains
		 * the permanent slot from its consistent point CONCURRENTLY with the copy
		 * — this bounds source WAL retention exactly like today's single-group
		 * clone --follow (apply stays gated until the finalize enables it).
		 *
		 * Then drive the per-group copy sequentially: each group is COPYied under
		 * its own snapshot (group 0 = the permanent slot's; groups 1..N-1 = a
		 * throwaway slot dropped right after), releasing each snapshot before the
		 * next group so the source xmin horizon advances across the copy.
		 */
		if (!start_follow_process(copySpecs, streamSpecs, &followPID))
		{
			/* errors have already been logged */
			exit(EXIT_CODE_INTERNAL_ERROR);
		}

		success = clone_groups_copy_phase(copySpecs, groupSpecs,
										  streamSpecs, groupThresholdLSN,
										  groupCount);

		/* every group's snapshot was closed inside clone_groups_copy_phase */
	}
	else
	{
		if (!start_clone_process(copySpecs, &clonePID))
		{
			/* errors have already been logged */
			exit(EXIT_CODE_INTERNAL_ERROR);
		}

		if (!start_follow_process(copySpecs, streamSpecs, &followPID))
		{
			/* errors have already been logged */
			exit(EXIT_CODE_INTERNAL_ERROR);
		}

		/* wait until the clone process is finished */
		success =
			cli_clone_follow_wait_subprocess("clone", clonePID, copySpecs);

		/* close our top-level copy db connection and snapshot */
		if (copySpecs->sourceSnapshot.state != SNAPSHOT_STATE_CLOSED)
		{
			if (!copydb_close_snapshot(copySpecs))
			{
				/* errors have already been logged */
				exit(EXIT_CODE_SOURCE);
			}
		}
	}

	/*
	 * If we failed to do the clone parts (midway through, or entirely maybe),
	 * we need to make it so that the follow sub-process isn't going to wait
	 * forever to reach the apply mode and then the endpos. That will never
	 * happen.
	 */
	if (!success)
	{
		log_warn("Failed to clone the source database, see above for details");

		if (!copydb_fatal_exit())
		{
			/* errors have already been logged */
			exit(EXIT_CODE_INTERNAL_ERROR);
		}

		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	/*
	 * --copy-groups N (N > 1): drive the common-LSN barrier and cutover.
	 *
	 * Each group was copied as-of its own snapshot's consistent point, so the
	 * target is not yet cross-group consistent. The barrier (1) builds indexes
	 * once across the whole DB (apply needs PK/replica-identity for UPDATE/
	 * DELETE), (2) enables apply per group, (3) drives every group to one
	 * common source LSN, waits for convergence, then (4) restores FK
	 * constraints once across the whole DB now that the data is cross-group
	 * consistent. See clone_groups_barrier_cutover for the full sequencing.
	 *
	 * This entirely replaces the single-group deferred-index STEP 10 below at
	 * N > 1; that path stays exactly as-is for the single-group default.
	 */
	if (groupCount > 1)
	{
		if (!clone_groups_barrier_cutover(copySpecs,
										  groupSpecs,
										  streamSpecs,
										  groupThresholdLSN,
										  groupCount,
										  followPID))
		{
			log_error("Failed to reach the common-LSN cutover barrier, "
					  "see above for details");
			(void) copydb_fatal_exit();
			exit(EXIT_CODE_INTERNAL_ERROR);
		}
	}

	/*
	 * When --defer-indexes with --follow, PID B exited after COPY without
	 * building indexes. Run STEP 10 here so index workers are direct
	 * children of this process — their semaphores survive CDC failures.
	 *
	 * Build indexes and restore post-data (FK constraints, triggers) BEFORE
	 * enabling CDC apply. This ensures referential integrity constraints are
	 * in place when DML replay begins, so cascading deletes work correctly.
	 *
	 * copydb_copy_all_indexes uses targeted waitpid (supervisor PID only)
	 * when deferIndexes && follow, so the follow process (PID C) is not
	 * accidentally reaped here. PID C is waited on below at the normal
	 * follow wait.
	 */
	if (groupCount == 1 && copySpecs->deferIndexes)
	{
		log_info("STEP 10: restore the post-data section to the target database");

		/* Open the catalog in this process (closed before fork) */
		if (!catalog_open_from_specs(copySpecs))
		{
			log_error("Failed to open catalogs for deferred index creation");
			(void) copydb_fatal_exit();
			exit(EXIT_CODE_INTERNAL_ERROR);
		}

		/* Build indexes + restore post-data (FK constraints, triggers) */
		if (!copydb_target_finalize_schema(copySpecs))
		{
			log_error("Failed to finalize schema, see above for details");
			(void) copydb_fatal_exit();
			exit(EXIT_CODE_INTERNAL_ERROR);
		}

		/*
		 * Now that FK constraints and triggers are in place, enable CDC
		 * apply so the follow process can start replaying changes.
		 */
		DatabaseCatalog *sourceDB = &(copySpecs->catalogs.source);

		log_info("Updating the pgcopydb.sentinel to enable applying changes");

		if (!sentinel_update_apply(sourceDB, true))
		{
			(void) copydb_fatal_exit();
			exit(EXIT_CODE_INTERNAL_ERROR);
		}

		if (copySpecs->deferAnalyze)
		{
			log_info("Running deferred ANALYZE on target database");

			if (!pg_vacuumdb_analyze_only_target(&(copySpecs->pgPaths),
												 &(copySpecs->connStrings),
												 copySpecs->tableJobs))
			{
				log_warn("Failed to run deferred ANALYZE, "
						 "run vacuumdb --analyze-only manually before cutover");
			}
		}

		if (!catalog_close_from_specs(copySpecs))
		{
			log_error("Failed to close catalogs after deferred index creation");
		}
	}

	/* now wait until the follow process is finished, if it's been started */
	if (followPID != -1)
	{
		success = success &&
				  cli_clone_follow_wait_subprocess("follow", followPID, NULL);
	}

	/*
	 * Now is a good time to reset the sequences on the target database to
	 * match the state they are in at the moment on the source database.
	 * Postgres logical decoding lacks support for syncing sequences.
	 *
	 * This step is implement as if running the following command:
	 *
	 *   $ pgcopydb copy sequences --resume --not-consistent
	 *
	 * The whole idea is to fetch the "new" current values of the
	 * sequences, not the ones that were current when the main snapshot was
	 * exported.
	 */
	if (success)
	{
		if (!follow_reset_sequences(copySpecs, streamSpecs))
		{
			/* errors have already been logged */
			exit(EXIT_CODE_TARGET);
		}
	}

	/* make sure all sub-processes are now finished */
	success = success && copydb_wait_for_subprocesses(copySpecs->failFast);

	if (!success)
	{
		exit(EXIT_CODE_INTERNAL_ERROR);
	}
}


/*
 * cli_follow implements the command: pgcopydb follow
 */
void
cli_follow(int argc, char **argv)
{
	CopyDataSpec copySpecs = { 0 };

	(void) cli_copy_prepare_specs(&copySpecs, DATA_SECTION_ALL);

	/*
	 * Refrain from logging SQL statements in the apply module, because they
	 * contain user data. That said, when --trace has been used, bypass that
	 * privacy feature.
	 */
	bool logSQL = log_get_level() <= LOG_TRACE;

	/*
	 * One StreamSpecs per copy group; see clone_and_follow above. At the
	 * single-group default this is a one-element array and element [0] drives
	 * today's exact single-stream follow path. PR4 forks the N triplets over
	 * the array.
	 */
	int groupCount = copySpecs.copyGroups >= 1 ? copySpecs.copyGroups : 1;

	StreamSpecs *specsArray =
		(StreamSpecs *) calloc(groupCount, sizeof(StreamSpecs));

	if (specsArray == NULL)
	{
		log_error(ALLOCATION_FAILED_ERROR);
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	StreamSpecs *specs = &(specsArray[0]);

	if (!stream_init_specs(specs,
						   &(copySpecs.cfPaths.cdc),
						   &(copySpecs.connStrings),
						   &(copyDBoptions.slot),
						   copyDBoptions.origin,
						   copyDBoptions.endpos,
						   STREAM_MODE_CATCHUP,
						   &(copySpecs.catalogs.source),
						   &(copySpecs.filters),
						   copyDBoptions.stdIn,
						   copyDBoptions.stdOut,
						   logSQL))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	/*
	 * Standalone `pgcopydb follow` does NOT run a grouped copy or the barrier
	 * that records per-group thresholds, so the --copy-groups apply threshold
	 * must stay inert here (copyGroups left at 1): this path applies every
	 * change, exactly as before. The threshold only applies within the
	 * clone --follow flow that populates s_group_lsn.
	 */
	specs->copyGroups = 1;

	/*
	 * First create/export a snapshot for the whole clone --follow operations.
	 */
	if (!follow_export_snapshot(&copySpecs, specs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_SOURCE);
	}

	/*
	 * First create the replication slot on the source database, and the origin
	 * (replication progress tracking) on the target database.
	 */
	if (!follow_setup_databases(&copySpecs, specs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	/*
	 * Before starting the receive, transform, and apply sub-processes, we need
	 * to set the sentinel endpos to the command line --endpos option, when
	 * given.
	 *
	 * Also fetch the current values from the pgcopydb.sentinel. It might have
	 * been updated from a previous run of the command, and we might have
	 * nothing to catch-up to when e.g. the endpos was reached already.
	 */
	CopyDBSentinel sentinel = { 0 };

	if (!follow_init_sentinel(specs, &sentinel))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	if (sentinel.endpos != InvalidXLogRecPtr &&
		sentinel.endpos <= sentinel.replay_lsn)
	{
		log_info("Current endpos %X/%X was previously reached at %X/%X",
				 LSN_FORMAT_ARGS(sentinel.endpos),
				 LSN_FORMAT_ARGS(sentinel.replay_lsn));

		exit(EXIT_CODE_QUIT);
	}

	/* make sure that we have our own process local connection */
	TransactionSnapshot snapshot = { 0 };

	if (!copydb_copy_snapshot(&copySpecs, &snapshot))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_SOURCE);
	}

	/* swap the new instance in place of the previous one */
	copySpecs.sourceSnapshot = snapshot;

	if (!copydb_set_snapshot(&copySpecs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_SOURCE);
	}

	if (!copydb_fetch_schema_and_prepare_specs(&copySpecs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_SOURCE);
	}

	if (!follow_main_loop(&copySpecs, specs))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	/*
	 * When CDC has durably reached endpos (cutover), reset the sequences on the
	 * target database to their current values on the source. Postgres logical
	 * decoding does not replicate sequences, so without this final step the
	 * target sequences are left at the values captured during the initial base
	 * copy. This mirrors what "clone --follow" does at the end of its run, and
	 * makes a resumed follow that catches up to endpos safe to cut over from.
	 *
	 * We only do this once endpos is reached: an interrupted continuous follow
	 * (no endpos, or stopped early by a signal) must not advance sequences ahead
	 * of the data that was actually applied to the target.
	 */
	bool reachedEndpos = false;

	if (!follow_reached_endpos(specs, &reachedEndpos))
	{
		/* errors have already been logged */
		exit(EXIT_CODE_INTERNAL_ERROR);
	}

	if (reachedEndpos)
	{
		log_info("Resetting sequences on the target database to match the "
				 "current values on the source database");

		if (!follow_reset_sequences(&copySpecs, specs))
		{
			/* errors have already been logged */
			exit(EXIT_CODE_TARGET);
		}
	}

	/*
	 * CDC has ended (endpos reached / cutover). If FKs were created NOT VALID
	 * via --defer-validate-fks, remind the operator at this final, visible
	 * point to validate them before relying on the target — the per-FK
	 * warning emitted during STEP 10 may be buried far up in a multi-day log.
	 */
	if (copySpecs.deferValidateFKs)
	{
		log_warn("--defer-validate-fks was used: foreign key constraints on "
				 "the target were created NOT VALID and have NOT been "
				 "validated. Before cutover, validate them on the target with "
				 "ALTER TABLE ... VALIDATE CONSTRAINT <name> for each FK");
	}
}


/*
 * start_clone_process starts a sub-process that clones the source database
 * into the target database.
 */
static bool
start_clone_process(CopyDataSpec *copySpecs, pid_t *pid)
{
	/* now we can fork a sub-process to transform the current file */
	pid_t fpid = fork();

	switch (fpid)
	{
		case -1:
		{
			log_error("Failed to fork a subprocess to prefetch changes: %m");
			return false;
		}

		case 0:
		{
			/* child process runs the command */
			(void) set_ps_title("pgcopydb: clone");

			log_notice("Starting the clone sub-process");

			if (!cloneDB(copySpecs))
			{
				log_error("Failed to clone source database, "
						  "see above for details");
				exit(EXIT_CODE_SOURCE);
			}

			/* and we're done */
			exit(EXIT_CODE_QUIT);
		}

		default:
		{
			*pid = fpid;
			return true;
		}
	}

	return true;
}


/*
 * cloneDB clones a source database into a target database.
 */
static bool
cloneDB(CopyDataSpec *copySpecs)
{
	/*
	 * The top-level process implements the preparation steps and exports a
	 * snapshot, unless the --snapshot option has been used. Then the rest of
	 * the work is split into a clone sub-process and a follow sub-process that
	 * work concurrently.
	 */
	DatabaseCatalog *sourceDB = &(copySpecs->catalogs.source);

	/* grab startTime before opening the catalogs */
	TopLevelTiming *timing = &(topLevelTimingArray[TIMING_SECTION_TOTAL]);
	(void) catalog_start_timing(timing);

	/* fetch schema information from source catalogs, including filtering */
	log_info("STEP 1: fetch source database tables, indexes, and sequences");

	if (!copydb_fetch_schema_and_prepare_specs(copySpecs))
	{
		/* errors have already been logged */
		return false;
	}

	/* now register in the catalogs the already known startTime */
	if (!summary_start_timing(sourceDB, TIMING_SECTION_TOTAL))
	{
		/* errors have already been logged */
		return false;
	}

	if (copySpecs->roles)
	{
		log_info("Copy the source database roles, per --roles");

		if (!pg_copy_roles(&(copySpecs->pgPaths),
						   &(copySpecs->connStrings),
						   copySpecs->dumpPaths.rolesFilename,
						   copySpecs->noRolesPasswords))
		{
			/* errors have already been logged */
			return false;
		}
	}

	/* make sure that we have our own process local connection */
	TransactionSnapshot snapshot = { 0 };

	if (!copydb_copy_snapshot(copySpecs, &snapshot))
	{
		/* errors have already been logged */
		return false;
	}

	/* swap the new instance in place of the previous one */
	copySpecs->sourceSnapshot = snapshot;

	log_info("STEP 2: dump the source database schema (pre/post data)");

	if (!copydb_dump_source_schema(copySpecs, copySpecs->sourceSnapshot.snapshot))
	{
		/* errors have already been logged */
		return false;
	}

	log_info("STEP 3: restore the pre-data section to the target database");

	if (!copydb_target_prepare_schema(copySpecs))
	{
		log_error("Failed to prepare schema on the target database, "
				  "see above for details");
		return false;
	}

	/* STEPs 4, 5, 6, 7, 8, and 9 are printed when starting the sub-processes */
	if (!copydb_copy_all_table_data(copySpecs))
	{
		/* errors have already been logged */
		(void) summary_print_failure_report(copySpecs);
		return false;
	}

	/*
	 * Fallback: write snapshot-done signal if COPY supervisor didn't
	 * (e.g. zero tables, or COPY supervisor failed before writing it).
	 */
	TopLevelTiming snDoneTiming = { 0 };

	if (!summary_lookup_timing(sourceDB, &snDoneTiming,
							   TIMING_SECTION_SNAPSHOT_DONE) ||
		snDoneTiming.doneTime == 0)
	{
		if (!summary_start_timing(sourceDB, TIMING_SECTION_SNAPSHOT_DONE) ||
			!summary_stop_timing(sourceDB, TIMING_SECTION_SNAPSHOT_DONE))
		{
			log_warn("Failed to write snapshot-done signal");

			/* Non-fatal: parent falls back to closing snapshot after clone exits */
		}
	}

	/*
	 * When --defer-indexes AND --follow, STEP 10 is handled by the
	 * parent process (PID A) so that index workers are direct children of
	 * PID A and their semaphores survive CDC failures.
	 */
	if (copySpecs->follow && copySpecs->deferIndexes)
	{
		log_info("STEP 10: deferred to parent process (clone --follow)");
	}
	else
	{
		log_info("STEP 10: restore the post-data section to the target database");

		if (!copydb_target_finalize_schema(copySpecs))
		{
			log_error("Failed to finalize schema on the target database, "
					  "see above for details");
			(void) summary_print_failure_report(copySpecs);
			return false;
		}

		if (copySpecs->deferAnalyze)
		{
			log_info("Running deferred ANALYZE on target database");

			if (!pg_vacuumdb_analyze_only_target(&(copySpecs->pgPaths),
												 &(copySpecs->connStrings),
												 copySpecs->tableJobs))
			{
				log_warn("Failed to run deferred ANALYZE, "
						 "run vacuumdb --analyze-only manually before cutover");
			}
		}

		/*
		 * When --follow has been used, now is the time to allow for the
		 * catchup process to start applying the prefetched changes.
		 */
		if (copySpecs->follow)
		{
			log_info("Updating the pgcopydb.sentinel to enable applying "
					 "changes");

			if (!sentinel_update_apply(sourceDB, true))
			{
				/* errors have already been logged */
				return false;
			}
		}
	}

	/* stop the timing wall-clock, and print the top-level summary */
	if (!summary_stop_timing(sourceDB, TIMING_SECTION_TOTAL))
	{
		/* errors have already been logged */
		return false;
	}

	log_info("All step are now done, %s elapsed", timing->ppDuration);

	(void) print_summary(copySpecs);

	/* time to close the catalogs now */
	if (!catalog_close_from_specs(copySpecs))
	{
		/* errors have already been logged */
		return false;
	}

	return true;
}


/*
 * clone_groups_init_specs initialises, before any copy starts, the per-group
 * CopyDataSpec structs used by the copy phase for --copy-groups N (N > 1).
 *
 * The CDC stream is a SINGLE stream (one permanent slot, one follow triplet,
 * per-group commit-LSN threshold on apply), so there is no per-group StreamSpecs
 * or per-group CDC catalog to set up here. Each group's copy just needs its own
 * CopyDataSpec carrying its group number (for the copy-queue table filter) and
 * its own exported snapshot (created lazily in clone_groups_copy_phase): group 0
 * uses the permanent slot's snapshot, groups 1..N-1 use a throwaway slot.
 *
 * This function is only ever called when groupCount > 1; the single-group
 * default never reaches here.
 */
static bool
clone_groups_init_specs(CopyDataSpec *copySpecs,
						CopyDataSpec *groupSpecs,
						int groupCount)
{
	/*
	 * Group 0 reuses the permanent slot's snapshot the caller already exported
	 * on copySpecs; the other groups start from a wholesale copy of copySpecs
	 * with the exported-snapshot state cleared so the copy phase can export a
	 * fresh throwaway snapshot for each.
	 */
	groupSpecs[0] = *copySpecs;
	groupSpecs[0].currentCopyGroup = 0;

	for (int g = 1; g < groupCount; g++)
	{
		groupSpecs[g] = *copySpecs;
		groupSpecs[g].currentCopyGroup = g;

		/*
		 * Clear the exported-snapshot STATE inherited from group 0 while
		 * preserving the connection identity (pguri / connectionType) that slot
		 * creation's standby-recovery check needs. A bare { 0 } here would NULL
		 * out sourceSnapshot.pguri and crash that check.
		 */
		TransactionSnapshot freshSnapshot = { 0 };
		freshSnapshot.pguri = copySpecs->connStrings.source_pguri;
		freshSnapshot.safeURI = copySpecs->connStrings.safeSourcePGURI;
		freshSnapshot.connectionType = PGSQL_CONN_SOURCE;
		freshSnapshot.isReadOnly = copySpecs->sourceSnapshot.isReadOnly;
		groupSpecs[g].sourceSnapshot = freshSnapshot;
	}

	return true;
}


/*
 * start_group_copy_process forks a copy-only sub-process that COPYs a single
 * copy group's table subset (groupSpecs->currentCopyGroup) under that group's
 * snapshot. Schema dump and pre-data restore have already been done once by the
 * parent before the per-group loop; this child only fills the COPY queue (whose
 * iterator skips tables not assigned to this group) and runs the COPY workers.
 */
static bool
start_group_copy_process(CopyDataSpec *groupSpecs, pid_t *pid)
{
	fflush(stdout);
	fflush(stderr);

	pid_t fpid = fork();

	switch (fpid)
	{
		case -1:
		{
			log_error("Failed to fork a copy sub-process for group %d: %m",
					  groupSpecs->currentCopyGroup);
			return false;
		}

		case 0:
		{
			char title[BUFSIZE] = { 0 };

			sformat(title, sizeof(title),
					"pgcopydb: copy group %d", groupSpecs->currentCopyGroup);

			(void) set_ps_title(title);

			log_info("Copying table data for group %d", groupSpecs->currentCopyGroup);

			/*
			 * Open this child's own SQLite catalog connection. The parent closed
			 * the catalogs before forking (so each child gets a fresh
			 * connection), and copydb_copy_all_table_data starts by recording a
			 * timing row (summary_start_timing), which requires an open catalog.
			 * The schema rows are already populated by the parent's
			 * copydb_fetch_schema_and_prepare_specs; this child only reads/uses
			 * them, so a plain open (no re-fetch) is enough.
			 */
			if (!catalog_open_from_specs(groupSpecs))
			{
				log_error("Failed to open catalogs for group %d copy",
						  groupSpecs->currentCopyGroup);
				exit(EXIT_CODE_INTERNAL_ERROR);
			}

			/*
			 * Re-derive a process-local SQL snapshot from this group's exported
			 * snapshot string, mirroring cloneDB: the COPY workers each then
			 * SET TRANSACTION SNAPSHOT to it. This avoids the child operating on
			 * the parent's inherited logical-replication snapshot connection.
			 */
			TransactionSnapshot snapshot = { 0 };

			if (!copydb_copy_snapshot(groupSpecs, &snapshot))
			{
				log_error("Failed to copy snapshot for group %d",
						  groupSpecs->currentCopyGroup);
				exit(EXIT_CODE_SOURCE);
			}

			groupSpecs->sourceSnapshot = snapshot;

			if (!copydb_copy_all_table_data(groupSpecs))
			{
				log_error("Failed to copy data for group %d, "
						  "see above for details",
						  groupSpecs->currentCopyGroup);
				exit(EXIT_CODE_SOURCE);
			}

			exit(EXIT_CODE_QUIT);
		}

		default:
		{
			*pid = fpid;
			return true;
		}
	}

	return true;
}


/*
 * clone_groups_copy_phase runs the per-group COPY sequentially when
 * --copy-groups N (N > 1) is used. The parent dumps the schema and restores the
 * pre-data section once (under group 0's snapshot), then for each group it:
 *
 *   1. creates that group's replication slot + exported snapshot (lazily, for
 *      groups 1..N-1; group 0's was created up front by the caller and is also
 *      used for the schema dump above),
 *   2. forks a copy-only sub-process scoped to that group's table subset,
 *   3. waits for it to finish, and
 *   4. closes that group's snapshot before moving on to the next group.
 *
 * Creating each group's snapshot immediately before its copy and releasing it
 * immediately after is what lets the source xmin horizon advance between
 * groups: at any moment only the group currently being copied pins the
 * horizon, instead of all N snapshots pinning it from the start. The group
 * slots persist after their snapshot is released (the caller starts the follow
 * supervisor over all N slots once this function returns).
 *
 * Only ever called when groupCount > 1.
 */
static bool
clone_groups_copy_phase(CopyDataSpec *copySpecs,
						CopyDataSpec *groupSpecs,
						StreamSpecs *streamSpecs,
						uint64_t *groupThresholdLSN,
						int groupCount)
{
	/*
	 * Group 0's threshold is the permanent slot's consistent point (LSN_0),
	 * already exported by the caller onto the single stream.
	 */
	groupThresholdLSN[0] = streamSpecs->slot.lsn;

	/*
	 * The caller closed the catalogs before this point (so each forked
	 * sub-process gets its own SQLite connection). Schema dump and pre-data
	 * restore run here in the parent and need the catalog open for their
	 * timing/restore-list bookkeeping, so reopen it for that work and close it
	 * again before the per-group COPY children fork (copydb_copy_all_table_data
	 * manages the catalog open/close around its own fork).
	 */
	if (!catalog_open_from_specs(copySpecs))
	{
		log_error("Failed to open catalogs for multi-group schema dump");
		return false;
	}

	/*
	 * Schema dump and pre-data restore happen once, whole-DB, under group 0's
	 * snapshot. The per-group COPY children below assume the target schema is
	 * already in place. (Post-data finalize across all groups, the common-LSN
	 * barrier and the FK-constraint phase are PR6.)
	 */
	log_info("STEP 2: dump the source database schema (pre/post data)");

	if (!copydb_dump_source_schema(copySpecs,
								   copySpecs->sourceSnapshot.snapshot))
	{
		/* errors have already been logged */
		return false;
	}

	log_info("STEP 3: restore the pre-data section to the target database");

	if (!copydb_target_prepare_schema(copySpecs))
	{
		log_error("Failed to prepare schema on the target database, "
				  "see above for details");
		return false;
	}

	if (!catalog_close_from_specs(copySpecs))
	{
		log_warn("Failed to close catalogs before per-group copy fork");
	}

	for (int g = 0; g < groupCount; g++)
	{
		pid_t copyPID = -1;
		char tmpSlotName[BUFSIZE] = { 0 };

		/*
		 * Obtain this group's consistent snapshot immediately before its copy,
		 * so the snapshot pins the source xmin horizon only for the duration of
		 * this group's copy (not the whole run).
		 *
		 * Group 0 uses the ONE permanent CDC slot's snapshot (already exported
		 * by the caller and used for the schema dump above); its consistent
		 * point LSN_0 is the earliest and its threshold. Groups 1..N-1 create a
		 * throwaway slot ONLY to get an exact (snapshot, consistent point LSN_g)
		 * pair; the permanent slot's stream carries every group's changes, so
		 * these slots are dropped right after the copy. LSN_g is recorded (into
		 * the in-memory slot) and later persisted as this group's apply
		 * threshold.
		 */
		if (g >= 1)
		{
			ReplicationSlot tmpSlot = { 0 };

			sformat(tmpSlotName, sizeof(tmpSlotName),
					"%s_cgtmp_g%d", copyDBoptions.slot.slotName, g);

			log_info("STEP 4: export a snapshot for group %d of %d "
					 "(temporary slot \"%s\")", g, groupCount, tmpSlotName);

			if (!copydb_export_snapshot_temp_slot(
					&(groupSpecs[g]),
					tmpSlotName,
					streamSpecs->slot.plugin,
					&tmpSlot))
			{
				log_error("Failed to export snapshot for group %d", g);
				return false;
			}

			groupThresholdLSN[g] = tmpSlot.lsn;

			log_info("Group %d copy threshold LSN is %X/%X", g,
					 LSN_FORMAT_ARGS(groupThresholdLSN[g]));
		}

		log_info("STEP 4: COPY the data for group %d of %d", g, groupCount);

		if (!start_group_copy_process(&(groupSpecs[g]), &copyPID))
		{
			/* errors have already been logged */
			return false;
		}

		if (!cli_clone_follow_wait_subprocess("clone", copyPID,
											  &(groupSpecs[g])))
		{
			log_error("Failed to copy data for group %d, see above for details",
					  g);
			return false;
		}

		/*
		 * Release this group's snapshot so the source xmin horizon advances
		 * before the next group's copy. Group 0 closes the permanent slot's
		 * exported snapshot but keeps the slot (the single CDC stream needs it);
		 * groups 1..N-1 close their snapshot AND drop the throwaway slot so the
		 * source can reclaim the WAL it was retaining.
		 */
		if (g == 0)
		{
			/*
			 * Group 0 shares the permanent slot's snapshot with copySpecs
			 * (same underlying walsender connection). cli_clone_follow_wait_
			 * subprocess above may have already released it early via its
			 * copySpecs param (== &groupSpecs[0]), so check groupSpecs[0]'s
			 * state — NOT copySpecs' (a separate struct that still reads
			 * EXPORTED) — to avoid a second copydb_close_snapshot on the same
			 * already-finished connection (double PQfinish -> heap corruption).
			 * Then reflect CLOSED on copySpecs so nothing closes it again.
			 */
			if (groupSpecs[0].sourceSnapshot.state != SNAPSHOT_STATE_CLOSED)
			{
				if (!copydb_close_snapshot(&(groupSpecs[0])))
				{
					/* errors have already been logged */
					return false;
				}
			}
			copySpecs->sourceSnapshot.state = SNAPSHOT_STATE_CLOSED;
		}
		else
		{
			if (!copydb_drop_temp_slot(&(groupSpecs[g]), tmpSlotName))
			{
				/* errors have already been logged */
				return false;
			}
		}

		log_info("Closed snapshot for group %d, xmin horizon can advance", g);
	}

	return true;
}


/*
 * clone_groups_barrier_cutover finalizes a --copy-groups N (N > 1) migration.
 * It runs in the parent after every group's COPY has completed and the SINGLE
 * follow triplet (one permanent slot) is already running with apply gated off.
 *
 * The single-stream design makes cross-group consistency automatic: there is
 * one apply position, and every table on the target equals COPY(as-of LSN_g)
 * plus the stream's changes with commit LSN > LSN_g. Once the one apply position
 * passes the largest group's copy point, the target reflects the source's
 * transactionally consistent state. So the elaborate per-group common-LSN
 * synchronization is unnecessary; this collapses to today's single-stream
 * deferred-index finalize, plus recording the per-group apply thresholds:
 *
 *   1. Build indexes ONCE across the whole database (apply needs PK / replica
 *      identity for UPDATE/DELETE lookups). FK constraints are NOT built here.
 *
 *   2. Persist each group's copy threshold LSN_g (from its snapshot's consistent
 *      point), which the apply's per-group commit-LSN filter reads to apply each
 *      change exactly once. Thresholds MUST be written before apply is enabled.
 *
 *   3. Enable apply on the single sentinel and drive the stream to a common
 *      cutover LSN (the source's current flush LSN, necessarily >= every group's
 *      copy point). Wait until the single apply reaches it.
 *
 *   4. Once apply is quiescent at that LSN the target is cross-group consistent,
 *      so restore FK constraints ONCE across the whole DB (existing NOT VALID +
 *      retry validation now passes).
 */
static bool
clone_groups_barrier_cutover(CopyDataSpec *copySpecs,
							 CopyDataSpec *groupSpecs,
							 StreamSpecs *streamSpecs,
							 uint64_t *groupThresholdLSN,
							 int groupCount,
							 pid_t followPID)
{
	DatabaseCatalog *sourceDB = &(copySpecs->catalogs.source);

	/*
	 * STEP 1: build indexes (incl. PK / replica identity) ONCE, whole-DB. FK
	 * constraints are restored later (STEP 4), after apply has caught up and the
	 * target is cross-group consistent.
	 */
	log_info("STEP 10: build indexes across all %d copy groups (whole database)",
			 groupCount);

	if (!catalog_open_from_specs(copySpecs))
	{
		log_error("Failed to open catalogs for whole-DB index build");
		return false;
	}

	if (!copydb_target_finalize_schema_indexes(copySpecs))
	{
		log_error("Failed to build indexes, see above for details");
		(void) catalog_close_from_specs(copySpecs);
		return false;
	}

	if (copySpecs->deferAnalyze)
	{
		log_info("Running deferred ANALYZE on target database");

		if (!pg_vacuumdb_analyze_only_target(&(copySpecs->pgPaths),
											 &(copySpecs->connStrings),
											 copySpecs->tableJobs))
		{
			log_warn("Failed to run deferred ANALYZE, "
					 "run vacuumdb --analyze-only manually before cutover");
		}
	}

	/*
	 * STEP 2: persist each group's copy threshold LSN_g (recorded by the copy
	 * phase in groupThresholdLSN[]) so the single apply's per-group commit-LSN
	 * filter can enforce exactly-once. groupThresholdLSN[0] is the permanent
	 * slot's consistent point; [g] (g >= 1) is each group's temporary-slot
	 * consistent point. Thresholds MUST be written before apply is enabled.
	 */
	for (int g = 0; g < groupCount; g++)
	{
		if (!catalog_set_group_lsn(sourceDB, g, groupThresholdLSN[g]))
		{
			log_error("Failed to persist copy threshold for group %d", g);
			(void) catalog_close_from_specs(copySpecs);
			return false;
		}

		log_info("Group %d apply threshold LSN is %X/%X", g,
				 LSN_FORMAT_ARGS(groupThresholdLSN[g]));
	}

	if (!catalog_close_from_specs(copySpecs))
	{
		log_warn("Failed to close catalogs after whole-DB index build");
	}

	/*
	 * STEP 3: enable apply on the single stream and drive it to a common cutover
	 * LSN (the source's current flush LSN, >= every group's copy point).
	 */
	uint64_t commonLSN = InvalidXLogRecPtr;

	if (!stream_fetch_current_lsn(&commonLSN,
								  copySpecs->connStrings.source_pguri,
								  PGSQL_CONN_SOURCE))
	{
		log_error("Failed to fetch the common cutover LSN from the source");
		return false;
	}

	log_info("Common cutover LSN is %X/%X", LSN_FORMAT_ARGS(commonLSN));

	if (!catalog_open(streamSpecs->sourceDB))
	{
		log_error("Failed to open catalog to enable apply at cutover");
		return false;
	}

	if (!sentinel_update_apply(streamSpecs->sourceDB, true))
	{
		log_error("Failed to enable apply");
		(void) catalog_close(streamSpecs->sourceDB);
		return false;
	}

	if (!sentinel_update_endpos(streamSpecs->sourceDB, commonLSN))
	{
		log_error("Failed to set the cutover endpos");
		(void) catalog_close(streamSpecs->sourceDB);
		return false;
	}

	streamSpecs->endpos = commonLSN;

	/*
	 * Wait until the single apply reaches the cutover LSN. If the follow process
	 * dies before that, bail rather than loop forever.
	 */
	log_info("Waiting for apply to reach the cutover LSN %X/%X",
			 LSN_FORMAT_ARGS(commonLSN));

	/*
	 * Keep the source producing WAL past commonLSN while we wait. On an
	 * otherwise-idle source the replication slot never flushes THROUGH
	 * commonLSN (no new WAL records to receive), so the apply would wait for an
	 * endpos it can never observe and cutover would hang. A lightweight logical
	 * message every couple of seconds guarantees forward progress; it writes no
	 * user data and is a no-op the apply ignores.
	 */
	PGSQL nudge = { 0 };
	bool nudgeReady =
		pgsql_init(&nudge, copySpecs->connStrings.source_pguri, PGSQL_CONN_SOURCE);
	int iterations = 0;

	bool converged = false;

	while (!converged)
	{
		if (asked_to_quit || asked_to_stop || asked_to_stop_fast)
		{
			log_warn("Interrupted while waiting for apply to reach cutover");
			(void) catalog_close(streamSpecs->sourceDB);
			(void) pgsql_finish(&nudge);
			return false;
		}

		if (!follow_reached_endpos(streamSpecs, &converged))
		{
			log_error("Failed to check apply progress, see above");
			(void) catalog_close(streamSpecs->sourceDB);
			(void) pgsql_finish(&nudge);
			return false;
		}

		if (converged)
		{
			break;
		}

		if (followPID > 0)
		{
			bool exited = false;
			int returnCode = -1;
			int sig = 0;

			if (!follow_wait_pid(followPID, &exited, &returnCode, &sig))
			{
				/* errors have already been logged */
				(void) catalog_close(streamSpecs->sourceDB);
				(void) pgsql_finish(&nudge);
				return false;
			}

			if (exited)
			{
				log_error("Follow process exited [%d] before apply reached the "
						  "cutover LSN %X/%X",
						  returnCode, LSN_FORMAT_ARGS(commonLSN));
				(void) catalog_close(streamSpecs->sourceDB);
				(void) pgsql_finish(&nudge);
				return false;
			}
		}

		/* every ~2s (8 * 250ms), nudge the source WAL forward (best-effort) */
		if (nudgeReady && (iterations % 8) == 0)
		{
			char *sql =
				"select pg_logical_emit_message(false, 'pgcopydb', 'cutover')";

			if (!pgsql_execute(&nudge, sql))
			{
				log_warn("Failed to emit a WAL keepalive message on the source; "
						 "cutover relies on source write traffic to reach the "
						 "cutover LSN");
				nudgeReady = false;
			}
		}
		iterations++;

		/* avoid busy looping */
		pg_usleep(250 * 1000);
	}

	(void) pgsql_finish(&nudge);

	log_info("Apply has reached the cutover LSN %X/%X",
			 LSN_FORMAT_ARGS(commonLSN));

	(void) catalog_close(streamSpecs->sourceDB);

	/*
	 * STEP 4: the target is now cross-group consistent at the cutover LSN and
	 * apply is quiescent, so restore FK constraints ONCE across the whole DB.
	 */
	log_info("Restoring FK constraints across all %d copy groups (whole database)",
			 groupCount);

	if (!catalog_open_from_specs(copySpecs))
	{
		log_error("Failed to open catalogs for whole-DB FK constraint restore");
		return false;
	}

	if (!copydb_target_finalize_schema_constraints(copySpecs))
	{
		log_error("Failed to restore FK constraints, see above for details");
		(void) catalog_close_from_specs(copySpecs);
		return false;
	}

	if (!catalog_close_from_specs(copySpecs))
	{
		log_warn("Failed to close catalogs after FK constraint restore");
	}

	return true;
}


/*
 * start_follow_process starts a sub-process that clones the source database
 * into the target database.
 */
static bool
start_follow_process(CopyDataSpec *copySpecs, StreamSpecs *streamSpecs,
					 pid_t *pid)
{
	/*
	 * Before starting the receive, transform, and apply sub-processes, we need
	 * to set the sentinel endpos to the command line --endpos option, when
	 * given.
	 *
	 * Also fetch the current values from the pgcopydb.sentinel. It might have
	 * been updated from a previous run of the command, and we might have
	 * nothing to catch-up to when e.g. the endpos was reached already.
	 */
	CopyDBSentinel *sentinel = &(streamSpecs->sentinel);

	if (!follow_init_sentinel(streamSpecs, sentinel))
	{
		log_error("Failed to initialise sentinel, see above for details");
		return false;
	}

	if (sentinel->endpos != InvalidXLogRecPtr &&
		sentinel->endpos <= sentinel->replay_lsn)
	{
		log_info("Current endpos %X/%X was previously reached at %X/%X",
				 LSN_FORMAT_ARGS(sentinel->endpos),
				 LSN_FORMAT_ARGS(sentinel->replay_lsn));

		return true;
	}

	/* now we can fork a sub-process to transform the current file */
	pid_t fpid = fork();

	switch (fpid)
	{
		case -1:
		{
			log_error("Failed to fork a subprocess to prefetch changes: %m");
			return false;
		}

		case 0:
		{
			/* child process runs the command */
			(void) set_ps_title("pgcopydb: follow");
			log_notice("Starting the follow sub-process");

			if (!follow_main_loop(copySpecs, streamSpecs))
			{
				/* errors have already been logged */
				exit(EXIT_CODE_INTERNAL_ERROR);
			}

			/* and we're done */
			exit(EXIT_CODE_QUIT);
		}

		default:
		{
			*pid = fpid;
			return true;
		}
	}

	return true;
}


/*
 * cli_clone_follow_wait_subprocesses waits until both sub-processes are
 * finished.
 */
static bool
cli_clone_follow_wait_subprocess(const char *name, pid_t pid,
								 CopyDataSpec *copySpecs)
{
	bool exited = false;
	int returnCode = -1;
	int sig = 0;
	bool snapshotClosed = (copySpecs == NULL);
	bool catalogOpenedHere = false;

	if (pid < 0)
	{
		log_error("BUG: cli_clone_follow_wait_subprocess(%s, %d)", name, pid);
		return false;
	}

	while (!exited)
	{
		if (!follow_wait_pid(pid, &exited, &returnCode, &sig))
		{
			/* errors have already been logged */
			return false;
		}

		if (exited)
		{
			char details[BUFSIZE] = { 0 };
			bool exitedSuccessfully = returnCode == 0 && signal_is_handled(sig);

			if (sig != 0)
			{
				sformat(details, sizeof(details), " (%s [%d])",
						signal_to_string(sig),
						sig);
			}

			log_level(exitedSuccessfully ? LOG_DEBUG : LOG_ERROR,
					  "%s process %d has terminated [%d]%s",
					  name,
					  pid,
					  returnCode,
					  details);
		}

		/* avoid busy looping, wait for 150ms before checking again */
		pg_usleep(150 * 1000);

		/*
		 * Poll the catalog for the snapshot-done signal written by the
		 * clone child after all snapshot-dependent work (COPY + blobs)
		 * finishes.  Release the source snapshot early so vacuum can
		 * proceed while indexes/constraints are built on the target.
		 */
		if (!snapshotClosed)
		{
			DatabaseCatalog *sourceDB = &(copySpecs->catalogs.source);

			if (sourceDB->db == NULL && file_exists(sourceDB->dbfile))
			{
				if (catalog_init(sourceDB))
				{
					catalogOpenedHere = true;
				}
			}

			if (sourceDB->db != NULL)
			{
				TopLevelTiming timing = { 0 };

				if (summary_lookup_timing(sourceDB, &timing,
										  TIMING_SECTION_SNAPSHOT_DONE) &&
					timing.doneTime > 0)
				{
					if (copydb_close_snapshot(copySpecs))
					{
						snapshotClosed = true;
						log_info("Source snapshot released: "
								 "all snapshot-dependent work is complete");
					}
					else
					{
						log_warn("Failed to release snapshot early, "
								 "will close after clone completes");
						snapshotClosed = true;
					}
				}
			}
		}
	}

	if (catalogOpenedHere)
	{
		DatabaseCatalog *sourceDB = &(copySpecs->catalogs.source);

		(void) catalog_close(sourceDB);
	}

	return returnCode == 0 && signal_is_handled(sig);
}
