/*
 * src/bin/pgcopydb/ld_apply.c
 *     Implementation of a CLI to copy a database between two Postgres instances
 */

#include <errno.h>
#include <dirent.h>
#include <getopt.h>
#include <inttypes.h>
#include <sys/wait.h>
#include <unistd.h>

#include "postgres.h"
#include "postgres_fe.h"
#include "access/xlog_internal.h"
#include "access/xlogdefs.h"

#include "parson.h"

#include "cli_common.h"
#include "cli_root.h"
#include "copydb.h"
#include "env_utils.h"
#include "ld_stream.h"
#include "lock_utils.h"
#include "log.h"
#include "parsing_utils.h"
#include "pidfile.h"
#include "pg_utils.h"
#include "schema.h"
#include "signals.h"
#include "string_utils.h"
#include "summary.h"

/*
 * libpq's output buffer uses a signed int for its size, so it can only grow
 * to ~1 GB before the doubling arithmetic overflows.  When the CDC apply
 * pipelines many EXECUTE statements with large parameter values (e.g. rows
 * containing 300 MB email bodies), the accumulated data can exceed that
 * limit.  We force a pipeline sync at transaction boundaries once the
 * estimated parameter data reaches this threshold.
 */
#define PIPELINE_BYTES_SYNC_THRESHOLD (512ULL * 1024 * 1024)

GUC applySettingsSync[] = {
	COMMON_GUC_SETTINGS,
	{ "synchronous_commit", "on" },
	{ "session_replication_role", "'replica'" },
	{ NULL, NULL },
};

GUC applySettings[] = {
	COMMON_GUC_SETTINGS,
	{ "synchronous_commit", "off" },
	{ "session_replication_role", "'replica'" },
	{ NULL, NULL },
};

static bool readTxnCommitLSN(LogicalMessageMetadata *metadata, const char *dir,
							 bool *txnCommitLSNFound);
static bool parseTxnMetadataFile(const char *filename, LogicalMessageMetadata *metadata);

static bool computeTxnMetadataFilename(uint32_t xid, const char *dir, char *filename);

static bool stream_apply_find_next_sql_file(StreamApplyContext *context,
											const char *missingSQLFileName,
											bool *found);

static bool setupConnection(PGSQL *pgsql, StreamApplyContext *context);

static bool extractTableNameFromPrepare(const char *stmt,
										char *nspname, size_t nspnameSize,
										char *relname, size_t relnameSize);

static void skipSQLWhitespace(const char **ptr);
static bool identifierCanBeUnquoted(const char *identifier);
static bool quoteSQLIdentifierAlways(const char *identifier,
									 char *quotedIdentifier,
									 size_t quotedIdentifierSize);
static bool parseSQLIdentifier(const char **ptr,
							   char *identifier, size_t identifierSize);
static bool parseSQLQualifiedTableName(const char *tableStart,
									   char *nspname, size_t nspnameSize,
									   char *relname, size_t relnameSize);
static bool catalogLookupTableByParsedName(StreamApplyContext *context,
										   const char *nspname,
										   const char *relname,
										   SourceTable *table);

static bool shouldSkipChangeByThreshold(StreamApplyContext *context,
										const char *nspname, const char *relname,
										bool *ok);

/*
 * stream_apply_catchup catches up with SQL files that have been prepared by
 * either the `pgcopydb stream prefetch` command.
 */
bool
stream_apply_catchup(StreamSpecs *specs)
{
	StreamApplyContext context = { 0 };

	if (!stream_apply_setup(specs, &context))
	{
		log_error("Failed to setup for catchup, see above for details");
		return false;
	}

	if (!context.apply)
	{
		/* errors have already been logged */
		return true;
	}

	/*
	 * Our main loop reads the current SQL file, applying all the queries from
	 * there and tracking progress, and then goes on to the next file, until no
	 * such file exists.
	 */
	char currentSQLFileName[MAXPGPATH] = { 0 };
	bool appliedAnyFile = false;

	for (;;)
	{
		strlcpy(currentSQLFileName, context.sqlFileName, MAXPGPATH);

		if (asked_to_stop || asked_to_stop_fast || asked_to_quit)
		{
			break;
		}

		/*
		 * When the expected SQL file doesn't exist yet and we haven't
		 * applied any file, wait briefly — the prefetch and transform
		 * processes may still be creating it (this happens when
		 * --defer-indexes delays apply start until after index building).
		 *
		 * If a later transformed SQL file already exists, this WAL segment was
		 * empty for pgcopydb and must be skipped before switching to replay mode.
		 */
		if (!file_exists(context.sqlFileName))
		{
			bool foundNextSQLFile = false;

			if (!appliedAnyFile)
			{
				int maxWaitSecs = 30;

				for (int i = 0; i < maxWaitSecs; i++)
				{
					log_info("File \"%s\" does not exist yet, "
							 "waiting (%d/%d)...",
							 context.sqlFileName, i + 1, maxWaitSecs);

					pg_usleep(1000 * 1000); /* 1 second */

					if (file_exists(context.sqlFileName))
					{
						break;
					}

					if (!stream_apply_find_next_sql_file(&context,
														 currentSQLFileName,
														 &foundNextSQLFile))
					{
						/* errors have already been logged */
						(void) stream_apply_cleanup(&context);
						return false;
					}

					if (foundNextSQLFile)
					{
						break;
					}

					if (asked_to_stop || asked_to_stop_fast || asked_to_quit)
					{
						break;
					}
				}
			}
			else if (!stream_apply_find_next_sql_file(&context,
													  currentSQLFileName,
													  &foundNextSQLFile))
			{
				/* errors have already been logged */
				(void) stream_apply_cleanup(&context);
				return false;
			}

			if (foundNextSQLFile)
			{
				log_notice("Skipping missing SQL file \"%s\"; "
						   "next available file is \"%s\"",
						   currentSQLFileName,
						   context.sqlFileName);
				strlcpy(currentSQLFileName, context.sqlFileName, MAXPGPATH);
			}
			else if (!file_exists(context.sqlFileName))
			{
				log_info("File \"%s\" does not exist yet, exit",
						 context.sqlFileName);

				(void) stream_apply_cleanup(&context);
				return true;
			}
		}

		/*
		 * The SQL file exists already, apply it now.
		 */
		if (!stream_apply_file(&context))
		{
			/* errors have already been logged */
			(void) stream_apply_cleanup(&context);
			return false;
		}

		appliedAnyFile = true;

		/*
		 * When syncing with the pgcopydb sentinel we might receive a new
		 * endpos, and it might mean we're done already.
		 */
		if (!context.reachedEndPos &&
			context.endpos != InvalidXLogRecPtr &&
			context.endpos <= context.previousLSN)
		{
			context.reachedEndPos = true;

			log_info("Apply reached end position %X/%X at %X/%X",
					 LSN_FORMAT_ARGS(context.endpos),
					 LSN_FORMAT_ARGS(context.previousLSN));
		}

		if (context.reachedEndPos)
		{
			/* information has already been logged */
			break;
		}

		log_info("Apply reached %X/%X in \"%s\"",
				 LSN_FORMAT_ARGS(context.previousLSN),
				 currentSQLFileName);

		if (!computeSQLFileName(&context))
		{
			/* errors have already been logged */
			(void) stream_apply_cleanup(&context);
			return false;
		}

		/*
		 * If we reached the end of the file and the current LSN still belongs
		 * to the same file (a SWITCH did not occur), then we exit so that the
		 * calling process may switch from catchup mode to live replay mode.
		 */
		if (streq(context.sqlFileName, currentSQLFileName))
		{
			log_info("Reached end of file \"%s\" at %X/%X.",
					 currentSQLFileName,
					 LSN_FORMAT_ARGS(context.previousLSN));

			/* make sure we close the connection on the way out */
			(void) stream_apply_cleanup(&context);
			return true;
		}

		log_notice("Apply new filename: \"%s\"", context.sqlFileName);
	}

	/* make sure we close the connection on the way out */
	(void) stream_apply_cleanup(&context);
	return true;
}


/*
 * stream_apply_setup does the required setup for then starting to catchup or
 * to replay changes from the SQL input (files or Unix PIPE) to the target
 * database.
 */
bool
stream_apply_setup(StreamSpecs *specs, StreamApplyContext *context)
{
	/* init our context */
	if (!stream_apply_init_context(context,
								   specs->sourceDB,
								   &(specs->paths),
								   specs->connStrings,
								   specs->origin,
								   specs->endpos,
								   specs->filters))
	{
		/* errors have already been logged */
		return false;
	}

	context->logSQL = specs->logSQL;

	/*
	 * Carry the real number of copy groups onto the apply context. The CDC
	 * stream is always single; copyGroups (the real N) gates the per-group
	 * commit-LSN threshold filter that enforces exactly-once apply across
	 * groups. At the single-group default (copyGroups <= 1) the filter is inert
	 * and apply behaves byte-for-byte as today.
	 */
	context->copyGroups = specs->copyGroups;

	/* wait until the sentinel enables the apply process */
	if (!stream_apply_wait_for_sentinel(specs, context))
	{
		/* errors have already been logged */
		return false;
	}

	if (!context->apply)
	{
		log_notice("Apply mode is still disabled, quitting now");
		return true;
	}

	if (specs->system.timeline == 0)
	{
		if (!stream_read_context(specs))
		{
			log_error("Failed to read the streaming context information "
					  "from the source database and internal catalogs, "
					  "see above for details");
			return false;
		}
	}

	context->system = specs->system;
	context->WalSegSz = specs->WalSegSz;

	log_debug("Source database wal_segment_size is %u", context->WalSegSz);
	log_debug("Source database timeline is %d", context->system.timeline);

	/*
	 * Use the replication origin for our setup (context->previousLSN).
	 */
	if (!setupReplicationOrigin(context))
	{
		log_error("Failed to setup replication origin on the target database");
		return false;
	}

	char *process =
		specs->mode == STREAM_MODE_CATCHUP ? "Catchup-up with" : "Replaying";

	if (context->endpos != InvalidXLogRecPtr)
	{
		if (context->endpos <= context->previousLSN)
		{
			log_info("Current endpos %X/%X was previously reached at %X/%X",
					 LSN_FORMAT_ARGS(context->endpos),
					 LSN_FORMAT_ARGS(context->previousLSN));

			return true;
		}

		log_info("%s changes from LSN %X/%X up to endpos LSN %X/%X",
				 process,
				 LSN_FORMAT_ARGS(context->previousLSN),
				 LSN_FORMAT_ARGS(context->endpos));
	}
	else
	{
		log_info("%s changes from LSN %X/%X",
				 process,
				 LSN_FORMAT_ARGS(context->previousLSN));
	}

	return true;
}


/*
 * stream_apply_cleanup cleans up the resources used by the apply process.
 */
bool
stream_apply_cleanup(StreamApplyContext *context)
{
	/* make sure we close the connection on the way out */
	(void) pgsql_finish(&(context->controlPgConn));

	(void) pgsql_finish(&(context->applyPgConn));

	return true;
}


/*
 * stream_apply_wait_for_sentinel fetches the current pgcopydb sentinel values:
 * the catchup processing only gets to start when the sentinel "apply" column
 * has been set to true.
 */
bool
stream_apply_wait_for_sentinel(StreamSpecs *specs, StreamApplyContext *context)
{
	bool firstLoop = true;
	CopyDBSentinel sentinel = { 0 };

	/* make sure context->apply is false before entering the loop */
	context->apply = false;

	while (!context->apply)
	{
		if (asked_to_stop || asked_to_stop_fast || asked_to_quit)
		{
			log_info("Apply process received a shutdown signal "
					 "while waiting for apply mode, "
					 "quitting now");
			return true;
		}

		/* this reconnects on each loop iteration, every 10s by default */
		if (!sentinel_get(specs->sourceDB, &sentinel))
		{
			log_warn("Retrying to fetch pgcopydb sentinel values in %ds",
					 CATCHINGUP_SLEEP_MS / 10);
			pg_usleep(CATCHINGUP_SLEEP_MS * 1000);

			continue;
		}

		/*
		 * Now grab the current sentinel values.
		 *
		 * The pgcopydb sentinel table contains an endpos. The --endpos command
		 * line option (found in specs->endpos) prevails, but when it's not
		 * been used, we have a look at the sentinel value.
		 */
		context->startpos = sentinel.startpos;
		context->apply = sentinel.apply;

		if (specs->endpos == InvalidXLogRecPtr)
		{
			context->endpos = sentinel.endpos;
		}
		else if (context->endpos != sentinel.endpos)
		{
			log_warn("Sentinel endpos is %X/%X, overriden by --endpos %X/%X",
					 LSN_FORMAT_ARGS(sentinel.endpos),
					 LSN_FORMAT_ARGS(specs->endpos));
		}

		/* TODO: find more about this */
		if (context->previousLSN == InvalidXLogRecPtr)
		{
			context->previousLSN = sentinel.replay_lsn;
		}
		else
		{
			log_warn("stream_apply_wait_for_sentinel: "
					 "previous lsn %X/%X, replay_lsn %X/%X",
					 LSN_FORMAT_ARGS(context->previousLSN),
					 LSN_FORMAT_ARGS(sentinel.replay_lsn));
		}

		log_debug("startpos %X/%X endpos %X/%X apply %s",
				  LSN_FORMAT_ARGS(context->startpos),
				  LSN_FORMAT_ARGS(context->endpos),
				  context->apply ? "enabled" : "disabled");

		if (context->apply)
		{
			break;
		}

		if (firstLoop)
		{
			firstLoop = false;

			log_info("Waiting until the pgcopydb sentinel apply is enabled");
		}

		/* avoid buzy looping and avoid hammering the source database */
		pg_usleep(CATCHINGUP_SLEEP_MS * 1000);
	}

	/* when apply was already set on first loop, don't even mention it */
	if (!firstLoop)
	{
		log_info("The pgcopydb sentinel has enabled applying changes");
	}

	return true;
}


/*
 * stream_apply_sync_sentinel sync with the pgcopydb sentinel table, sending
 * the current replay LSN position and fetching the maybe new endpos and apply
 * values.
 */
bool
stream_apply_sync_sentinel(StreamApplyContext *context, bool findDurableLSN)
{
	uint64_t durableLSN = InvalidXLogRecPtr;

	/*
	 * If we know we reached endpos, then publish that as the replay_lsn.
	 */
	if (context->reachedEndPos || !findDurableLSN)
	{
		durableLSN = context->previousLSN;
	}
	else
	{
		if (!stream_apply_find_durable_lsn(context, &durableLSN))
		{
			log_warn("Skipping sentinel replay_lsn update: "
					 "failed to find a durable LSN matching current flushLSN");
			return true;
		}
	}

	CopyDBSentinel sentinel = { 0 };

	if (!sentinel_sync_apply(context->sourceDB, durableLSN, &sentinel))
	{
		log_warn("Failed to sync progress with the pgcopydb sentinel");
		return true;
	}

	context->apply = sentinel.apply;
	context->endpos = sentinel.endpos;
	context->startpos = sentinel.startpos;
	context->sentinelSyncTime = time(NULL);

	log_debug("stream_apply_sync_sentinel: "
			  "write_lsn %X/%X flush_lsn %X/%X replay_lsn %X/%X "
			  "startpos %X/%X endpos %X/%X apply %s",
			  LSN_FORMAT_ARGS(sentinel.write_lsn),
			  LSN_FORMAT_ARGS(sentinel.flush_lsn),
			  LSN_FORMAT_ARGS(sentinel.replay_lsn),
			  LSN_FORMAT_ARGS(context->startpos),
			  LSN_FORMAT_ARGS(context->endpos),
			  context->apply ? "enabled" : "disabled");

	return true;
}


/*
 * stream_apply_file connects to the target database system and applies the
 * given SQL file as prepared by the stream_transform_file function.
 */
bool
stream_apply_file(StreamApplyContext *context)
{
	StreamContent content = { 0 };
	long size = 0L;

	strlcpy(content.filename, context->sqlFileName, sizeof(content.filename));

	char *contents = NULL;

	if (!read_file(content.filename, &contents, &size))
	{
		/* errors have already been logged */
		return false;
	}

	if (!splitLines(&(content.lbuf), contents))
	{
		/* errors have already been logged */
		return false;
	}

	log_info("Replaying changes from file \"%s\"", context->sqlFileName);

	log_debug("Read %lld lines in file \"%s\"",
			  (long long) content.lbuf.count,
			  content.filename);

	/*
	 * If the file contains zero lines, we're done already, Also malloc(zero)
	 * leads to "corrupted size vs. prev_size" run-time errors.
	 */
	if (content.lbuf.count == 0)
	{
		return true;
	}

	LogicalMessageMetadata *mArray =
		(LogicalMessageMetadata *) calloc(content.lbuf.count,
										  sizeof(LogicalMessageMetadata));

	LogicalMessageMetadata *lastCommit = NULL;

	/* parse the SQL commands metadata from the SQL file */
	for (uint64_t i = 0; i < content.lbuf.count && !context->reachedEndPos; i++)
	{
		const char *sql = content.lbuf.lines[i];
		LogicalMessageMetadata *metadata = &(mArray[i]);

		if (!parseSQLAction(sql, metadata, context->filters))
		{
			/* errors have already been logged */
			return false;
		}

		/*
		 * The SWITCH WAL command should always be the last line of the file.
		 */
		if (metadata->action == STREAM_ACTION_SWITCH &&
			i != (content.lbuf.count - 1))
		{
			log_error("SWITCH command for LSN %X/%X found in \"%s\" line %lld, "
					  "before last line %lld",
					  LSN_FORMAT_ARGS(metadata->lsn),
					  content.filename,
					  (long long) i + 1,
					  (long long) content.lbuf.count);

			return false;
		}

		if (metadata->action == STREAM_ACTION_COMMIT)
		{
			lastCommit = metadata;
		}
	}

	/* replay the SQL commands from the SQL file */
	for (uint64_t i = 0; i < content.lbuf.count && !context->reachedEndPos; i++)
	{
		const char *sql = content.lbuf.lines[i];
		LogicalMessageMetadata *metadata = &(mArray[i]);

		/* last commit of a file requires synchronous_commit on */
		context->reachedEOF = metadata == lastCommit;

		if (!stream_apply_sql(context, metadata, sql))
		{
			log_error("Failed to apply SQL from file \"%s\", "
					  "see above for details",
					  content.filename);

			return false;
		}


		/*
		 * Sync the pipeline at transaction boundaries (COMMIT or
		 * KEEPALIVE) when either the 1-second timer has elapsed or
		 * when accumulated parameter data approaches libpq's output
		 * buffer limit.
		 *
		 * libpq's output buffer size is tracked with a signed int,
		 * so the buffer can grow to ~1 GB before the doubling logic
		 * overflows.  We sync well before that at 512 MB to leave
		 * headroom for wire-protocol framing and PREPARE overhead.
		 */
		if (metadata->action == STREAM_ACTION_COMMIT ||
			metadata->action == STREAM_ACTION_KEEPALIVE)
		{
			bool timeToSync =
				1 < (time(NULL) - context->applyPgConn.pipelineSyncTime);

			bool bufferNearFull =
				context->pipelineBytes >= PIPELINE_BYTES_SYNC_THRESHOLD;

			if (timeToSync || bufferNearFull)
			{
				/* fetch results until done */
				if (!pgsql_sync_pipeline(&(context->applyPgConn)))
				{
					log_error("Failed to sync the pipeline, "
							  "see previous error for details");
					return false;
				}

				context->pipelineBytes = 0;
			}
		}
	}

	/* Always sync pipline at the end of file */
	if (!pgsql_sync_pipeline(&(context->applyPgConn)))
	{
		log_error("Failed to sync the pipeline, see previous error for "
				  "details");
		return false;
	}
	context->pipelineBytes = 0;

	/*
	 * Each time we are done applying a file, we update our progress and
	 * fetch new values from the pgcopydb sentinel. Errors are warning
	 * here, we'll update next time.
	 */
	bool findDurableLSN = false;

	if (!stream_apply_sync_sentinel(context, findDurableLSN))
	{
		log_error("Failed to sync replay_lsn %X/%X",
				  LSN_FORMAT_ARGS(context->previousLSN));
		return false;
	}

	return true;
}


/*
 * stream_apply_sql connects to the target database system and applies the
 * given SQL command as prepared by the stream_transform_file or
 * stream_transform_stream function.
 */
bool
stream_apply_sql(StreamApplyContext *context,
				 LogicalMessageMetadata *metadata,
				 const char *sql)
{
	PGSQL *applyPgConn = &(context->applyPgConn);

	switch (metadata->action)
	{
		case STREAM_ACTION_SWITCH:
		{
			log_debug("SWITCH from %X/%X to %X/%X",
					  LSN_FORMAT_ARGS(context->switchLSN),
					  LSN_FORMAT_ARGS(metadata->lsn));

			/*
			 * Track the SWITCH LSN, it helps to determine the next
			 * .sql file to apply.
			 */
			context->switchLSN = metadata->lsn;

			break;
		}

		case STREAM_ACTION_BEGIN:
		{
			if (metadata->lsn == InvalidXLogRecPtr ||
				IS_EMPTY_STRING_BUFFER(metadata->timestamp))
			{
				log_fatal("Failed to parse BEGIN message: %s", sql);
				return false;
			}

			bool txnCommitLSNFound = false;

			if (!readTxnCommitLSN(metadata,
								  context->paths.dir,
								  &txnCommitLSNFound))
			{
				log_error("Failed to read transaction metadata file");
				return false;
			}

			/*
			 * Few a time, BEGIN won't have a txnCommitLSN for the txn which
			 * spread across multiple WAL segments. We call that txn as
			 * a continuedTxn and allow it to be replayed until we encounter
			 * a COMMIT message.
			 *
			 * The lsn of a COMMIT message determines whether to keep txn or
			 * abort.
			 */
			context->continuedTxn = !txnCommitLSNFound;

			/*
			 * Remember this transaction's commit LSN for the --copy-groups
			 * per-group threshold filter applied to its changes. When the commit
			 * LSN is unknown (continuedTxn, txn spanning WAL segments) leave it
			 * Invalid so group 0 can continue to apply everything while group
			 * > 0 changes fail closed instead of making an unsafe threshold
			 * decision.
			 */
			context->currentTxnCommitLSN =
				txnCommitLSNFound ? metadata->txnCommitLSN : InvalidXLogRecPtr;

			/* did we reach the starting LSN positions now? */
			if (!context->reachedStartPos)
			{
				/*
				 * compare previousLSN with COMMIT LSN to safely include
				 * complete transactions while skipping already applied
				 * changes.
				 *
				 * this is particularly useful at the beginnig where
				 * BEGIN LSN of some transactions could be less than
				 * `consistent_point`, but COMMIT LSN of those transactions
				 * is guaranteed to be greater.
				 *
				 * in case of interruption and this is the first
				 * transaction to be applied, previousLSN should be equal
				 * to the last transaction's COMMIT LSN or the LSN of
				 * non-transaction action. Therefore, this condition will
				 * still hold true.
				 */
				context->reachedStartPos =
					context->previousLSN < metadata->txnCommitLSN;
			}

			bool skip = !context->reachedStartPos && !context->continuedTxn;

			log_debug("BEGIN %lld LSN %X/%X @%s, previous LSN %X/%X, COMMIT LSN %X/%X %s",
					  (long long) metadata->xid,
					  LSN_FORMAT_ARGS(metadata->lsn),
					  metadata->timestamp,
					  LSN_FORMAT_ARGS(context->previousLSN),
					  LSN_FORMAT_ARGS(metadata->txnCommitLSN),
					  skip ? "[skipping]" : "");

			/*
			 * Check if we reached the endpos LSN already.
			 */
			if (context->endpos != InvalidXLogRecPtr &&
				context->endpos <= metadata->lsn)
			{
				context->reachedEndPos = true;

				log_notice("Apply reached end position %X/%X at BEGIN %X/%X",
						   LSN_FORMAT_ARGS(context->endpos),
						   LSN_FORMAT_ARGS(metadata->lsn));

				return true;
			}

			/* actually skip this one if we didn't reach start pos yet */
			if (skip)
			{
				return true;
			}

			/*
			 * We're all good to replay that transaction, let's BEGIN and
			 * register our origin tracking on the target database.
			 */
			if (!pgsql_begin(applyPgConn))
			{
				/* errors have already been logged */
				return false;
			}

			/*
			 * If this transaction is going to reach the endpos, then we're
			 * happy to wait until it's been sync'ed on-disk by Postgres on the
			 * target.
			 *
			 * In other words, use synchronous_commit = on.
			 */
			bool commitLSNreachesEndPos =
				context->endpos != InvalidXLogRecPtr &&
				!context->continuedTxn &&
				context->endpos <= metadata->txnCommitLSN;

			GUC *settings =
				commitLSNreachesEndPos || context->reachedEOF
				? applySettingsSync
				: applySettings;

			if (commitLSNreachesEndPos)
			{
				log_notice("BEGIN transaction with COMMIT LSN %X/%X which is "
						   "reaching endpos %X/%X, synchronous_commit is on",
						   LSN_FORMAT_ARGS(metadata->txnCommitLSN),
						   LSN_FORMAT_ARGS(context->endpos));
			}

			if (!pgsql_set_gucs(applyPgConn, settings))
			{
				log_error("Failed to set the apply GUC settings, "
						  "see above for details");
				return false;
			}

			context->transactionInProgress = true;

			break;
		}

		case STREAM_ACTION_ROLLBACK:
		{
			/* Rollback the transaction */
			if (!pgsql_execute(applyPgConn, "ROLLBACK"))
			{
				/* errors have already been logged */
				return false;
			}

			/* Clean up prepared statements (see COMMIT for explanation) */
			if (context->preparedStmt != NULL)
			{
				if (!pgsql_execute(applyPgConn, "DEALLOCATE ALL"))
				{
					log_warn("Failed to deallocate prepared statements");
				}

				PreparedStmt *current, *tmp;

				HASH_ITER(hh, context->preparedStmt, current, tmp)
				{
					HASH_DEL(context->preparedStmt, current);
					free(current);
				}

				context->preparedStmt = NULL;
			}

			/* Reset the transactionInProgress after abort */
			context->transactionInProgress = false;

			/* Reevaluate reachedStartPos after rollback */
			context->reachedStartPos = false;

			break;
		}

		case STREAM_ACTION_COMMIT:
		{
			context->reachedStartPos = context->previousLSN < metadata->lsn;

			if (context->continuedTxn)
			{
				/*
				 * Write the transaction metadata file for continuedTxn.
				 * This file will be used for the resumed transaction
				 * to determine whether allow the transaction to be
				 * replayed or not.
				 * Without this, executing the same continuedTxn twice
				 * will result in duplicate key errors if the table has
				 * unique constraints.
				 */
				if (!writeTxnCommitMetadata(metadata, context->paths.dir))
				{
					log_error("Failed to write transaction metadata file, "
							  "see above for details");
					return false;
				}
			}

			if (!context->reachedStartPos)
			{
				/*
				 * Abort if we are not yet reachedStartPos and txn is a
				 * continuedTxn.
				 */
				if (context->continuedTxn)
				{
					log_notice("Skip(abort) applied transaction %lld LSN %X/%X "
							   "@%s, previous LSN %X/%X",
							   (long long) metadata->xid,
							   LSN_FORMAT_ARGS(metadata->lsn),
							   metadata->timestamp,
							   LSN_FORMAT_ARGS(context->previousLSN));

					/* Rollback the transaction */
					if (!pgsql_execute(applyPgConn, "ROLLBACK"))
					{
						/* errors have already been logged */
						return false;
					}

					/* Reset the transactionInProgress after abort */
					context->transactionInProgress = false;
					context->continuedTxn = false;
				}

				return true;
			}

			/*
			 * update replication progress with metadata->lsn, that is,
			 * transaction COMMIT LSN
			 */
			char lsn[PG_LSN_MAXLENGTH] = { 0 };

			sformat(lsn, sizeof(lsn), "%X/%X",
					LSN_FORMAT_ARGS(metadata->lsn));

			if (!pgsql_replication_origin_xact_setup(applyPgConn,
													 lsn,
													 metadata->timestamp))
			{
				log_error("Failed to setup apply transaction, "
						  "see above for details");
				return false;
			}

			log_trace("COMMIT %lld LSN %X/%X",
					  (long long) metadata->xid,
					  LSN_FORMAT_ARGS(metadata->lsn));


			/* calling pgsql_commit() would finish the connection, avoid */
			if (!pgsql_execute(applyPgConn, "COMMIT"))
			{
				/* errors have already been logged */
				return false;
			}

			/*
			 * Deallocate all prepared statements on the server and clear
			 * the client-side hash table. Prepared statement names are
			 * 32-bit hashes of the SQL text, so hash collisions are
			 * possible when many unique statements accumulate across
			 * transactions (birthday paradox). Clearing at COMMIT
			 * prevents a new PREPARE from being silently skipped when
			 * its hash collides with a previously prepared statement
			 * that has a different parameter count.
			 */
			if (context->preparedStmt != NULL)
			{
				if (!pgsql_execute(applyPgConn, "DEALLOCATE ALL"))
				{
					log_warn("Failed to deallocate prepared statements");
				}

				PreparedStmt *current, *tmp;

				HASH_ITER(hh, context->preparedStmt, current, tmp)
				{
					HASH_DEL(context->preparedStmt, current);
					free(current);
				}

				context->preparedStmt = NULL;
			}

			context->transactionInProgress = false;
			context->previousLSN = metadata->lsn;

			/*
			 * At COMMIT time we might have reached the endpos: we know
			 * that already when endpos <= lsn. It's important to check
			 * that at COMMIT record time, because that record might be the
			 * last entry of the file we're applying.
			 */
			if (context->endpos != InvalidXLogRecPtr &&
				context->endpos <= context->previousLSN)
			{
				context->reachedEndPos = true;

				log_notice("Apply reached end position %X/%X at COMMIT %X/%X",
						   LSN_FORMAT_ARGS(context->endpos),
						   LSN_FORMAT_ARGS(context->previousLSN));
				return true;
			}

			break;
		}

		case STREAM_ACTION_ENDPOS:
		{
			if (!context->reachedStartPos && !context->continuedTxn)
			{
				return true;
			}

			log_debug("ENDPOS %X/%X found at %X/%X",
					  LSN_FORMAT_ARGS(metadata->lsn),
					  LSN_FORMAT_ARGS(context->previousLSN));

			/*
			 * It could be the current endpos, or the endpos of a previous
			 * run.
			 */
			if (context->endpos != InvalidXLogRecPtr &&
				context->endpos <= metadata->lsn)
			{
				context->previousLSN = metadata->lsn;
				context->reachedEndPos = true;

				log_notice("Apply reached end position %X/%X at ENDPOS %X/%X",
						   LSN_FORMAT_ARGS(context->endpos),
						   LSN_FORMAT_ARGS(context->previousLSN));

				if (context->transactionInProgress)
				{
					if (!pgsql_execute(applyPgConn, "ROLLBACK"))
					{
						/* errors have already been logged */
						return false;
					}

					context->transactionInProgress = false;
				}

				return true;
			}

			break;
		}

		/*
		 * A KEEPALIVE message is replayed as its own transaction where the
		 * only thgin we do is call into the replication origin tracking
		 * API to advance our position on the target database.
		 */
		case STREAM_ACTION_KEEPALIVE:
		{
			/* did we reach the starting LSN positions now? */
			if (!context->reachedStartPos && !context->continuedTxn)
			{
				context->reachedStartPos =
					context->previousLSN < metadata->lsn;
			}

			/* in a transaction only the COMMIT LSN is tracked */
			if (context->transactionInProgress)
			{
				return true;
			}

			log_trace("KEEPALIVE LSN %X/%X @%s, previous LSN %X/%X %s",
					  LSN_FORMAT_ARGS(metadata->lsn),
					  metadata->timestamp,
					  LSN_FORMAT_ARGS(context->previousLSN),
					  context->reachedStartPos ? "" : "[skipping]");

			if (metadata->lsn == InvalidXLogRecPtr ||
				IS_EMPTY_STRING_BUFFER(metadata->timestamp))
			{
				log_fatal("Failed to parse KEEPALIVE message: %s", sql);
				return false;
			}

			/*
			 * Check if we reached the endpos LSN already. If the keepalive
			 * message is the endpos, still apply it: its only purpose is
			 * to maintain our replication origin tracking on the target
			 * database.
			 */
			if (context->endpos != InvalidXLogRecPtr &&
				context->endpos < metadata->lsn)
			{
				context->reachedEndPos = true;
				context->previousLSN = metadata->lsn;

				log_notice("Apply reached end position %X/%X at KEEPALIVE %X/%X",
						   LSN_FORMAT_ARGS(context->endpos),
						   LSN_FORMAT_ARGS(context->previousLSN));

				return true;
			}

			/* actually skip this one if we didn't reach start pos yet */
			if (!context->reachedStartPos)
			{
				return true;
			}

			/* skip KEEPALIVE message that won't make progress */
			if (metadata->lsn == context->previousLSN)
			{
				return true;
			}

			if (!pgsql_begin(applyPgConn))
			{
				/* errors have already been logged */
				return false;
			}

			/*
			 * Replication origin is handled differently by the postgres
			 * backend to avoid database bloat and runtime overhead[1].
			 * This optimization leads to persist origin progress only when
			 * the txn modifies the state of the database. So, an empty txn
			 * created to update KEEPALIVE LSN effectively ignored by the
			 * backend leading to not updating the origin progress.
			 *
			 * To workaround this, we execute `SELECT txid_current()` query to
			 * force the backend to update the origin progress.
			 *
			 * [1] https://www.postgresql.org/docs/current/replication-origins.html
			 */
			char *sql = "SELECT txid_current()";

			if (!pgsql_execute(applyPgConn, sql))
			{
				/* errors have already been logged */
				return false;
			}

			char lsn[PG_LSN_MAXLENGTH] = { 0 };

			sformat(lsn, sizeof(lsn), "%X/%X",
					LSN_FORMAT_ARGS(metadata->lsn));

			if (!pgsql_replication_origin_xact_setup(applyPgConn,
													 lsn,
													 metadata->timestamp))
			{
				/* errors have already been logged */
				return false;
			}

			/* calling pgsql_commit() would finish the connection, avoid */
			if (!pgsql_execute(applyPgConn, "COMMIT"))
			{
				/* errors have already been logged */
				return false;
			}

			context->previousLSN = metadata->lsn;

			/*
			 * At COMMIT time we might have reached the endpos: we know
			 * that already when endpos <= lsn. It's important to check
			 * that at COMMIT record time, because that record might be the
			 * last entry of the file we're applying.
			 */
			if (context->endpos != InvalidXLogRecPtr &&
				context->endpos <= context->previousLSN)
			{
				context->reachedEndPos = true;

				log_notice("Apply reached end position %X/%X at KEEPALIVE %X/%X",
						   LSN_FORMAT_ARGS(context->endpos),
						   LSN_FORMAT_ARGS(context->previousLSN));
				break;
			}

			break;
		}

		case STREAM_ACTION_INSERT:
		case STREAM_ACTION_UPDATE:
		case STREAM_ACTION_DELETE:
		{
			/*
			 * We still allow continuedTxn, COMMIT message determines whether
			 * to keep the transaction or abort it.
			 */
			if (!context->reachedStartPos && !context->continuedTxn)
			{
				return true;
			}

			uint32_t hash = metadata->hash;
			PreparedStmt *stmtHashTable = context->preparedStmt;
			PreparedStmt *stmt = NULL;

			HASH_FIND(hh, stmtHashTable, &hash, sizeof(hash), stmt);

			if (stmt == NULL)
			{
				/* Add to hash table even if filtered, so EXECUTE can find it */
				stmt = (PreparedStmt *) calloc(1, sizeof(PreparedStmt));
				stmt->hash = hash;
				stmt->filterOut = metadata->filterOut;
				stmt->prepared = false;

				/* Extract and store schema.table name for logging and tracking */
				char nspname[PG_NAMEDATALEN] = { 0 };
				char relname[PG_NAMEDATALEN] = { 0 };

				if (extractTableNameFromPrepare(metadata->stmt,
												nspname, sizeof(nspname),
												relname, sizeof(relname)))
				{
					strlcpy(stmt->nspname, nspname, sizeof(stmt->nspname));
					strlcpy(stmt->relname, relname, sizeof(stmt->relname));
				}
				else
				{
					return false;
				}

				HASH_ADD(hh, stmtHashTable, hash, sizeof(hash), stmt);

				/* HASH_ADD can change the pointer in place, update */
				context->preparedStmt = stmtHashTable;
			}

			/* Skip filtered statements - don't prepare or execute them */
			if (stmt != NULL && stmt->filterOut)
			{
				log_trace("Skipping filtered %s statement",
						  metadata->action == STREAM_ACTION_INSERT ? "INSERT" :
						  metadata->action == STREAM_ACTION_UPDATE ? "UPDATE" :
						  "DELETE");
				return true;
			}

			/* Only prepare if we haven't already */
			if (stmt != NULL && !stmt->prepared)
			{
				/* Prepare the statement for later execution */
				char name[NAMEDATALEN] = { 0 };
				sformat(name, sizeof(name), "%x", metadata->hash);

				if (!pgsql_prepare(applyPgConn, name, metadata->stmt, 0, NULL))
				{
					/* errors have already been logged */
					return false;
				}

				stmt->prepared = true;
			}

			break;
		}

		case STREAM_ACTION_EXECUTE:
		{
			/*
			 * We still allow continuedTxn, COMMIT message determines whether
			 * to keep the transaction or abort it.
			 */
			if (!context->reachedStartPos && !context->continuedTxn)
			{
				return true;
			}

			uint32_t hash = metadata->hash;
			PreparedStmt *stmtHashTable = context->preparedStmt;
			PreparedStmt *stmt = NULL;

			HASH_FIND(hh, stmtHashTable, &hash, sizeof(hash), stmt);

			if (stmt == NULL)
			{
				log_warn("BUG: Failed to find statement %x in stmtHashTable",
						 hash);
			}

			/* Skip filtered out statements - check if corresponding PREPARE was filtered */
			if (stmt != NULL && stmt->filterOut)
			{
				log_trace("Skipping filtered EXECUTE statement for %s.%s",
						  stmt->nspname, stmt->relname);
				return true;
			}

			/*
			 * --copy-groups: skip this change when its transaction committed at
			 * or before its table's copy group threshold LSN_g (already captured
			 * in the COPY). Inert at copyGroups <= 1. A false ok means the
			 * decision could not be made safely -> abort rather than risk a
			 * duplicate/lost apply.
			 */
			{
				bool ok = true;

				if (stmt != NULL &&
					shouldSkipChangeByThreshold(context, stmt->nspname,
												stmt->relname, &ok))
				{
					return true;
				}

				if (!ok)
				{
					return false;
				}
			}

			char name[NAMEDATALEN] = { 0 };
			sformat(name, sizeof(name), "%x", metadata->hash);

			JSON_Value *js = json_parse_string(metadata->jsonBuffer);

			if (json_value_get_type(js) != JSONArray)
			{
				log_error("Failed to parse EXECUTE array: %s",
						  metadata->jsonBuffer);
				return false;
			}

			JSON_Array *jsArray = json_value_get_array(js);

			int count = json_array_get_count(jsArray);

			if (0 < count)
			{
				const char **paramValues =
					(const char **) calloc(count, sizeof(char *));

				if (paramValues == NULL)
				{
					log_error(ALLOCATION_FAILED_ERROR);
					return false;
				}

				for (int i = 0; i < count; i++)
				{
					const char *value = json_array_get_string(jsArray, i);
					paramValues[i] = value;
				}

				if (!pgsql_execute_prepared(applyPgConn, name,
											count, paramValues,
											NULL, NULL))
				{
					/* errors have already been logged */
					return false;
				}

				/*
				 * Track accumulated parameter bytes in the pipeline.
				 * libpq output buffer uses int (~1 GB effective max
				 * due to doubling), force sync before overflow.
				 */
				for (int j = 0; j < count; j++)
				{
					if (paramValues[j] != NULL)
					{
						context->pipelineBytes += strlen(paramValues[j]);
					}
				}

				/*
				 * When a single transaction contains many large rows
				 * (e.g. 330 MB email bodies), the pipeline buffer can
				 * exceed libpq's ~1 GB limit before reaching COMMIT.
				 * Sync mid-transaction to drain the buffer.  This is
				 * safe: we use explicit BEGIN/COMMIT, so a pipeline
				 * sync only flushes pending results without affecting
				 * the transaction.
				 */
				if (context->pipelineBytes >= PIPELINE_BYTES_SYNC_THRESHOLD)
				{
					if (!pgsql_sync_pipeline(applyPgConn))
					{
						log_error("Failed to sync the pipeline, "
								  "see previous error for details");
						return false;
					}

					context->pipelineBytes = 0;
				}
			}


			break;
		}

		case STREAM_ACTION_TRUNCATE:
		{
			/* Skip filtered out statements */
			if (metadata->filterOut)
			{
				log_trace("Skipping filtered TRUNCATE statement");
				return true;
			}

			/*
			 * We still allow continuedTxn, COMMIT message determines whether
			 * to keep the transaction or abort it.
			 */
			if (!context->reachedStartPos && !context->continuedTxn)
			{
				return true;
			}

			/*
			 * --copy-groups: a TRUNCATE is applied directly from the SQL file
			 * (not via PREPARE/EXECUTE), so it must consult the per-group
			 * threshold here too, or a TRUNCATE already captured in the group's
			 * COPY would replay and wipe copied rows.
			 */
			{
				bool ok = true;

				if (shouldSkipChangeByThreshold(context, metadata->nspname,
												metadata->relname, &ok))
				{
					return true;
				}

				if (!ok)
				{
					return false;
				}
			}

			/* chomp the final semi-colon that we added */
			int len = strlen(sql);

			if (sql[len - 1] == ';')
			{
				char *ptr = (char *) sql + len - 1;
				*ptr = '\0';
			}

			if (!pgsql_execute(applyPgConn, sql))
			{
				/* errors have already been logged */
				return false;
			}
			break;
		}

		default:
		{
			log_error("Failed to parse action %c for SQL query: %s",
					  metadata->action,
					  sql);

			return false;
		}
	}

	return true;
}


/*
 * setupConnection sets up a connection to the target database.
 */
static bool
setupConnection(PGSQL *pgsql, StreamApplyContext *context)
{
	if (!pgsql_init(pgsql,
					context->connStrings->target_pguri,
					PGSQL_CONN_TARGET))
	{
		/* errors have already been logged */
		return false;
	}

	/* we're going to send several replication origin commands */
	pgsql->connectionStatementType = PGSQL_CONNECTION_MULTI_STATEMENT;

	/* we also might want to skip logging any SQL query that we apply */
	pgsql->logSQL = context->logSQL;

	/*
	 * Grab the Postgres server version on the target, we need to know that for
	 * being able to call pgsql_current_wal_insert_lsn using the right Postgres
	 * function name.
	 */
	if (!pgsql_server_version(pgsql))
	{
		/* errors have already been logged */
		return false;
	}

	return true;
}


/*
 * setupReplicationOrigin ensures that a replication origin has been created on
 * the target database, and if it has been created previously then fetches the
 * previous LSN position it was at.
 *
 * Also setupReplicationOrigin calls pg_replication_origin_setup() in the
 * current connection.
 */
bool
setupReplicationOrigin(StreamApplyContext *context)
{
	char *nodeName = context->origin;

	/*
	 * A dedicated connection to apply logical messages into the target.
	 * This will be converted to pipeline mode after we have setup the
	 * replication origin.
	 */
	PGSQL *applyPgConn = &(context->applyPgConn);
	if (!setupConnection(applyPgConn, context))
	{
		/* errors have already been logged */
		return false;
	}

	/*
	 * Establish a regular connection for operations requiring immediate
	 * responses, such as finding the WAL insert LSN.
	 */
	if (!setupConnection(&context->controlPgConn, context))
	{
		log_error("Failed to setup pipeline mode on target connection");
		return false;
	}

	uint32_t oid = 0;

	if (!pgsql_replication_origin_oid(applyPgConn, nodeName, &oid))
	{
		/* errors have already been logged */
		return false;
	}

	log_debug("setupReplicationOrigin: oid == %u", oid);

	if (oid == 0)
	{
		log_error("Failed to fetch progress for replication origin \"%s\": "
				  "replication origin not found on target database",
				  nodeName);
		(void) pgsql_finish(applyPgConn);
		(void) pgsql_finish(&context->controlPgConn);
		return false;
	}

	/*
	 * Fetch the replication origin LSN tracking, which is maintained in a
	 * transactional fashion with the SQL that's been replayed. It's the
	 * authoritative value for progress at reconnect, given that we use
	 * synchronous_commit off.
	 */
	uint64_t originLSN = InvalidXLogRecPtr;

	if (!pgsql_replication_origin_progress(applyPgConn, nodeName, true, &originLSN))
	{
		/* errors have already been logged */
		return false;
	}

	/*
	 * The context->previousLSN may have been initialized already from the
	 * sentinel, when restarting a follow operation. For more details see
	 * function stream_apply_wait_for_sentinel().
	 */
	if (context->previousLSN == InvalidXLogRecPtr)
	{
		log_info("Setting up previous LSN from "
				 "replication origin \"%s\" progress at %X/%X",
				 nodeName,
				 LSN_FORMAT_ARGS(originLSN));

		context->previousLSN = originLSN;
	}
	else if (context->previousLSN != originLSN)
	{
		log_info("Setting up previous LSN from "
				 "replication origin \"%s\" progress at %X/%X, "
				 "overriding previous value %X/%X",
				 nodeName,
				 LSN_FORMAT_ARGS(originLSN),
				 LSN_FORMAT_ARGS(context->previousLSN));

		context->previousLSN = originLSN;
	}

	if (IS_EMPTY_STRING_BUFFER(context->sqlFileName))
	{
		if (!computeSQLFileName(context))
		{
			/* errors have already been logged */
			return false;
		}
	}

	/* compute the WAL filename that would host the previous LSN */
	log_debug("setupReplicationOrigin: replication origin \"%s\" "
			  "found at %X/%X, expected at \"%s\"",
			  nodeName,
			  LSN_FORMAT_ARGS(context->previousLSN),
			  context->sqlFileName);

	bool sessionOk =
		pgsql_replication_origin_session_setup(applyPgConn, nodeName);

	if (!sessionOk && pgsql_is_origin_in_use_error(applyPgConn))
	{
		/*
		 * Another backend is still holding the replication origin session
		 * (SQLSTATE 55006). This happens when the client-side connection was
		 * killed by a network failure but the server-side backend has not yet
		 * detected the dead connection. Terminate that backend and retry once.
		 */
		log_warn("Replication origin \"%s\" is already held by another "
				 "backend; terminating it and retrying session setup",
				 nodeName);

		if (pgsql_terminate_origin_holder(&context->controlPgConn, applyPgConn, nodeName))
		{
			pg_usleep(500 * 1000); /* 500ms for the backend to exit */
			sessionOk =
				pgsql_replication_origin_session_setup(applyPgConn, nodeName);
		}
	}

	if (!sessionOk)
	{
		/* errors have already been logged */
		return false;
	}

	/*
	 * Enter into pipeline mode, SQL statements which expects sync responses
	 * are not allowed in this connection anymore.
	 */
	if (!pgsql_enable_pipeline_mode(applyPgConn))
	{
		/* errors have already been logged */
		return false;
	}

	return true;
}


/*
 * stream_apply_init_context initializes our context from pieces.
 */
bool
stream_apply_init_context(StreamApplyContext *context,
						  DatabaseCatalog *sourceDB,
						  CDCPaths *paths,
						  ConnStrings *connStrings,
						  char *origin,
						  uint64_t endpos,
						  SourceFilters *filters)
{
	context->sourceDB = sourceDB;
	context->paths = *paths;
	context->filters = filters;

	/*
	 * We have to consider both the --endpos command line option and the
	 * pgcopydb sentinel endpos value. Typically the sentinel is updated after
	 * the fact, but we still give precedence to --endpos.
	 *
	 * The endpos parameter here comes from the --endpos command line option,
	 * the context->endpos might have been set by calling
	 * stream_apply_wait_for_sentinel() earlier (when in STREAM_MODE_PREFETCH).
	 */
	if (endpos != InvalidXLogRecPtr)
	{
		if (context->endpos != InvalidXLogRecPtr && context->endpos != endpos)
		{
			log_warn("Option --endpos %X/%X is used, "
					 "even when the pgcopydb sentinel endpos was set to %X/%X",
					 LSN_FORMAT_ARGS(endpos),
					 LSN_FORMAT_ARGS(context->endpos));
		}
		context->endpos = endpos;
	}

	context->reachedStartPos = false;
	context->continuedTxn = false;
	context->reachedEOF = false;

	context->connStrings = connStrings;

	strlcpy(context->origin, origin, sizeof(context->origin));

	return true;
}


/*
 * computeSQLFileName updates the StreamApplyContext structure with the current
 * LSN applied to the target system, and computed
 */
bool
computeSQLFileName(StreamApplyContext *context)
{
	XLogSegNo segno;

	uint64_t switchLSN = context->switchLSN;

	/*
	 * If we haven't switched WAL yet, then we're still at the previousLSN
	 * position.
	 */
	if (switchLSN == InvalidXLogRecPtr)
	{
		switchLSN = context->previousLSN;
	}

	if (context->WalSegSz == 0)
	{
		log_error("Failed to compute the SQL filename for LSN %X/%X "
				  "without context->wal_segment_size",
				  LSN_FORMAT_ARGS(switchLSN));
		return false;
	}

	XLByteToSeg(switchLSN, segno, context->WalSegSz);
	XLogFileName(context->wal, context->system.timeline, segno, context->WalSegSz);

	sformat(context->sqlFileName, sizeof(context->sqlFileName),
			"%s/%s.sql",
			context->paths.dir,
			context->wal);

	log_debug("computeSQLFileName: %X/%X \"%s\"",
			  LSN_FORMAT_ARGS(switchLSN),
			  context->sqlFileName);

	return true;
}


/*
 * stream_apply_find_next_sql_file finds the next transformed SQL file after a
 * missing expected WAL-segment file. Some WAL segments legitimately contain no
 * SQL output for pgcopydb, so transform never creates those .sql files. Apply
 * must still consume later transformed files before switching to replay mode.
 */
static bool
stream_apply_find_next_sql_file(StreamApplyContext *context,
								const char *missingSQLFileName,
								bool *found)
{
	*found = false;

	const char *missingBaseName = strrchr(missingSQLFileName, '/');
	missingBaseName =
		missingBaseName == NULL ? missingSQLFileName : missingBaseName + 1;

	DIR *dir = opendir(context->paths.dir);

	if (dir == NULL)
	{
		log_error("Failed to open directory \"%s\": %m", context->paths.dir);
		return false;
	}

	char candidate[MAXPGPATH] = { 0 };
	struct dirent *entry = NULL;
	size_t expectedLength = strlen(missingBaseName);

	while ((entry = readdir(dir)) != NULL)
	{
		char *name = entry->d_name;
		size_t nameLength = strlen(name);

		if (nameLength != expectedLength ||
			nameLength < 5 ||
			!streq(name + nameLength - 4, ".sql") ||
			strcmp(name, missingBaseName) <= 0)
		{
			continue;
		}

		if (IS_EMPTY_STRING_BUFFER(candidate) || strcmp(name, candidate) < 0)
		{
			strlcpy(candidate, name, sizeof(candidate));
		}
	}

	if (closedir(dir) != 0)
	{
		log_error("Failed to close directory \"%s\": %m", context->paths.dir);
		return false;
	}

	if (IS_EMPTY_STRING_BUFFER(candidate))
	{
		return true;
	}

	sformat(context->sqlFileName, sizeof(context->sqlFileName),
			"%s/%s",
			context->paths.dir,
			candidate);

	*found = true;
	return true;
}


/*
 * skipSQLWhitespace advances over whitespace that can appear around a
 * schema/table separator in pgcopydb-generated SQL.
 */
static void
skipSQLWhitespace(const char **ptr)
{
	while (**ptr == ' ' || **ptr == '\t')
	{
		(*ptr)++;
	}
}


/*
 * identifierCanBeUnquoted implements the part of PostgreSQL quote_ident()
 * behavior that we can safely decide locally for catalog-name matching.
 * Keyword handling is intentionally left to the lookup fallback below.
 */
static bool
identifierCanBeUnquoted(const char *identifier)
{
	if (identifier == NULL || identifier[0] == '\0')
	{
		return false;
	}

	if (!((identifier[0] >= 'a' && identifier[0] <= 'z') ||
		  identifier[0] == '_'))
	{
		return false;
	}

	for (int index = 1; identifier[index] != '\0'; index++)
	{
		char c = identifier[index];

		if (!((c >= 'a' && c <= 'z') ||
			  (c >= '0' && c <= '9') ||
			  c == '_' ||
			  c == '$'))
		{
			return false;
		}
	}

	return true;
}


/*
 * quoteSQLIdentifierAlways quotes an already dequoted identifier using SQL's
 * doubled-quote escaping. It is used only as a catalog lookup fallback.
 */
static bool
quoteSQLIdentifierAlways(const char *identifier,
						 char *quotedIdentifier,
						 size_t quotedIdentifierSize)
{
	if (quotedIdentifierSize < 3)
	{
		return false;
	}

	size_t length = 0;

	quotedIdentifier[length++] = '"';

	for (int index = 0; identifier[index] != '\0'; index++)
	{
		if (identifier[index] == '"')
		{
			if (length + 2 >= quotedIdentifierSize)
			{
				return false;
			}

			quotedIdentifier[length++] = '"';
			quotedIdentifier[length++] = '"';
		}
		else
		{
			if (length + 1 >= quotedIdentifierSize)
			{
				return false;
			}

			quotedIdentifier[length++] = identifier[index];
		}
	}

	if (length + 1 >= quotedIdentifierSize)
	{
		return false;
	}

	quotedIdentifier[length++] = '"';
	quotedIdentifier[length] = '\0';

	return true;
}


/*
 * parseSQLIdentifier parses either an unquoted SQL identifier or a quoted
 * PostgreSQL identifier, including doubled quotes inside quoted identifiers.
 * It fails instead of truncating so threshold lookups never use a prefix that
 * might name a different table. The returned identifier uses pgcopydb's source
 * catalog spelling where possible: simple identifiers are unquoted, and names
 * that require quoting keep SQL doubled-quote escaping.
 */
static bool
parseSQLIdentifier(const char **ptr, char *identifier, size_t identifierSize)
{
	if (identifierSize == 0)
	{
		return false;
	}

	identifier[0] = '\0';

	size_t length = 0;

	if (**ptr == '"')
	{
		(*ptr)++;

		char rawIdentifier[PG_NAMEDATALEN] = { 0 };
		char quotedIdentifier[PG_NAMEDATALEN] = { 0 };
		size_t rawLength = 0;
		size_t quotedLength = 0;

		if (quotedLength + 1 >= sizeof(quotedIdentifier))
		{
			return false;
		}

		quotedIdentifier[quotedLength++] = '"';

		while (**ptr != '\0')
		{
			if (**ptr == '"')
			{
				if ((*ptr)[1] == '"')
				{
					if (rawLength + 1 >= sizeof(rawIdentifier) ||
						quotedLength + 2 >= sizeof(quotedIdentifier))
					{
						return false;
					}

					rawIdentifier[rawLength++] = '"';
					quotedIdentifier[quotedLength++] = '"';
					quotedIdentifier[quotedLength++] = '"';
					(*ptr) += 2;
					continue;
				}

				(*ptr)++;

				if (quotedLength + 2 >= sizeof(quotedIdentifier))
				{
					return false;
				}

				rawIdentifier[rawLength] = '\0';
				quotedIdentifier[quotedLength++] = '"';
				quotedIdentifier[quotedLength] = '\0';

				if (rawLength == 0)
				{
					return false;
				}

				if (identifierCanBeUnquoted(rawIdentifier))
				{
					strlcpy(identifier, rawIdentifier, identifierSize);
				}
				else
				{
					strlcpy(identifier, quotedIdentifier, identifierSize);
				}

				return true;
			}

			if (rawLength + 1 >= sizeof(rawIdentifier) ||
				quotedLength + 1 >= sizeof(quotedIdentifier))
			{
				return false;
			}

			rawIdentifier[rawLength++] = **ptr;
			quotedIdentifier[quotedLength++] = **ptr;
			(*ptr)++;
		}

		return false;
	}

	while (**ptr != '\0' &&
		   **ptr != '.' &&
		   **ptr != ' ' &&
		   **ptr != '\t' &&
		   **ptr != '(' &&
		   **ptr != ';' &&
		   **ptr != '\n' &&
		   **ptr != '\r')
	{
		if (length + 1 >= identifierSize)
		{
			return false;
		}

		identifier[length++] = **ptr;
		(*ptr)++;
	}

	identifier[length] = '\0';

	return length > 0;
}


/*
 * catalogLookupTableByParsedName looks up a table from names parsed out of SQL.
 * The main lookup uses the parser's catalog spelling. When that misses, try
 * quoted variants too: SQL generated from wal2json is often always-quoted,
 * while the source catalog stores format('%I') output, which is unquoted for
 * simple names and quoted for special or keyword names.
 */
static bool
catalogLookupTableByParsedName(StreamApplyContext *context,
							   const char *nspname,
							   const char *relname,
							   SourceTable *table)
{
	char quotedNspname[PG_NAMEDATALEN] = { 0 };
	char quotedRelname[PG_NAMEDATALEN] = { 0 };

	if (!quoteSQLIdentifierAlways(nspname,
								  quotedNspname,
								  sizeof(quotedNspname)) ||
		!quoteSQLIdentifierAlways(relname,
								  quotedRelname,
								  sizeof(quotedRelname)))
	{
		return false;
	}

	const char *candidateNspnames[] = { nspname, quotedNspname };
	const char *candidateRelnames[] = { relname, quotedRelname };

	for (int n = 0; n < 2; n++)
	{
		for (int r = 0; r < 2; r++)
		{
			if (n == 1 && streq(candidateNspnames[0], candidateNspnames[1]))
			{
				continue;
			}

			if (r == 1 && streq(candidateRelnames[0], candidateRelnames[1]))
			{
				continue;
			}

			SourceTable candidate = { 0 };

			if (!catalog_lookup_s_table_by_name(context->sourceDB,
												candidateNspnames[n],
												candidateRelnames[r],
												&candidate))
			{
				return false;
			}

			if (candidate.oid != 0)
			{
				*table = candidate;
				return true;
			}
		}
	}

	memset(table, 0, sizeof(SourceTable));
	return true;
}


/*
 * parseSQLQualifiedTableName parses [schema.]table from SQL generated by
 * pgcopydb. All wal2json relation names are written with PQescapeIdentifier,
 * so quoted identifiers are the common path.
 */
static bool
parseSQLQualifiedTableName(const char *tableStart,
						   char *nspname, size_t nspnameSize,
						   char *relname, size_t relnameSize)
{
	char first[PG_NAMEDATALEN] = { 0 };
	char second[PG_NAMEDATALEN] = { 0 };

	const char *ptr = tableStart;

	skipSQLWhitespace(&ptr);

	if (!parseSQLIdentifier(&ptr, first, sizeof(first)))
	{
		return false;
	}

	skipSQLWhitespace(&ptr);

	if (*ptr == '.')
	{
		ptr++;
		skipSQLWhitespace(&ptr);

		if (!parseSQLIdentifier(&ptr, second, sizeof(second)))
		{
			return false;
		}

		strlcpy(nspname, first, nspnameSize);
		strlcpy(relname, second, relnameSize);
	}
	else
	{
		strlcpy(nspname, "public", nspnameSize);
		strlcpy(relname, first, relnameSize);
	}

	return true;
}


/*
 * extractTableNameFromPrepare extracts the schema and table name from a
 * PREPARE statement like:
 *   PREPARE hash AS INSERT INTO "schema"."table" ...
 *   PREPARE hash AS UPDATE "schema"."table" SET ...
 *   PREPARE hash AS DELETE FROM "schema"."table" WHERE ...
 *
 * Returns true if extraction succeeded, false otherwise.
 */
static bool
extractTableNameFromPrepare(const char *stmt,
							char *nspname, size_t nspnameSize,
							char *relname, size_t relnameSize)
{
	/* Find the table name after INSERT INTO, UPDATE, or DELETE FROM */
	const char *tableStart = NULL;

	if ((tableStart = strstr(stmt, "INSERT INTO ")) != NULL)
	{
		tableStart += strlen("INSERT INTO ");
	}
	else if ((tableStart = strstr(stmt, "UPDATE ")) != NULL)
	{
		tableStart += strlen("UPDATE ");
	}
	else if ((tableStart = strstr(stmt, "DELETE FROM ")) != NULL)
	{
		tableStart += strlen("DELETE FROM ");
	}
	else
	{
		log_trace("Failed to find table name in PREPARE statement: %s", stmt);
		return false;
	}

	if (!parseSQLQualifiedTableName(tableStart,
									nspname, nspnameSize,
									relname, relnameSize))
	{
		log_error("Failed to parse table name in PREPARE statement: %s", stmt);
		return false;
	}

	return true;
}


/*
 * groupThresholdForTable resolves (and caches on the apply context) the copy
 * group and its threshold LSN_g for a table under --copy-groups: the consistent
 * point at which that group was COPYied. It maps name -> oid (s_table) -> group
 * number (s_table_group_assignment) -> LSN_g (s_group_lsn) once per table; the
 * result is cached on context->groupThresholdCache so it survives across
 * transactions (PreparedStmt entries are freed every COMMIT).
 *
 * Returns false on an unrecoverable inconsistency (catalog query error, or a
 * group > 0 with no recorded threshold LSN); the caller must then abort rather
 * than risk a duplicate/lost apply. A table with no s_table row is only safe
 * when the stream is not using grouped copy thresholds; in grouped mode, fail
 * closed because applying everything might replay a change already included in
 * the table's COPY.
 */
static bool
groupThresholdForTable(StreamApplyContext *context,
					   const char *nspname, const char *relname,
					   int *groupNumber, uint64_t *thresholdLSN)
{
	char key[2 * PG_NAMEDATALEN + 1] = { 0 };
	sformat(key, sizeof(key), "%s.%s", nspname, relname);

	GroupThresholdEntry *entry = NULL;
	HASH_FIND_STR(context->groupThresholdCache, key, entry);

	if (entry != NULL)
	{
		*groupNumber = entry->groupNumber;
		*thresholdLSN = entry->thresholdLSN;
		return true;
	}

	int g = 0;
	uint64_t lsn = InvalidXLogRecPtr;

	if (context->sourceDB != NULL)
	{
		SourceTable table = { 0 };

		if (!catalogLookupTableByParsedName(context,
											nspname, relname, &table))
		{
			/* errors have already been logged */
			return false;
		}

		if (table.oid != 0)
		{
			if (!catalog_lookup_s_table_group_number(context->sourceDB,
													 table.oid, &g))
			{
				return false;
			}

			if (g != 0)
			{
				if (!catalog_lookup_group_lsn(context->sourceDB, g, &lsn))
				{
					return false;
				}

				if (lsn == InvalidXLogRecPtr)
				{
					log_error("BUG: copy group %d has no recorded threshold LSN "
							  "for table \"%s\".\"%s\"", g, nspname, relname);
					return false;
				}
			}
		}
		else if (context->copyGroups > 1)
		{
			log_error("Cannot find table \"%s\".\"%s\" in the source catalog "
					  "while --copy-groups is enabled; aborting to avoid a "
					  "duplicate or lost apply", nspname, relname);
			return false;
		}
	}
	else if (context->copyGroups > 1)
	{
		log_error("Cannot resolve copy-group threshold without a source "
				  "catalog while --copy-groups is enabled");
		return false;
	}

	entry = (GroupThresholdEntry *) calloc(1, sizeof(GroupThresholdEntry));

	if (entry == NULL)
	{
		log_error(ALLOCATION_FAILED_ERROR);
		return false;
	}

	strlcpy(entry->key, key, sizeof(entry->key));
	entry->groupNumber = g;
	entry->thresholdLSN = lsn;
	HASH_ADD_STR(context->groupThresholdCache, key, entry);

	*groupNumber = g;
	*thresholdLSN = lsn;

	return true;
}


/*
 * shouldSkipChangeByThreshold implements the apply-side half of --copy-groups N.
 * The single stream decodes all WAL; a change to a group-g table must be applied
 * EXACTLY ONCE. Changes whose transaction committed at or before that group's
 * copy point LSN_g are already in the COPY, so they are skipped; changes
 * committed strictly after LSN_g are applied. This is the exact same cut a
 * dedicated slot created at LSN_g would make.
 *
 * Returns true when the change should be SKIPPED. Sets *ok = false when the
 * decision cannot be made safely for a group > 0 (catalog inconsistency, or an
 * unknown transaction commit LSN for a skippable group), in which case the
 * caller MUST abort the apply rather than risk duplicating or losing data.
 *
 * Inert at copyGroups <= 1 and for group 0 (threshold 0): everything applies.
 */
static bool
shouldSkipChangeByThreshold(StreamApplyContext *context,
							const char *nspname, const char *relname,
							bool *ok)
{
	*ok = true;

	if (context->copyGroups <= 1)
	{
		return false;
	}

	/*
	 * No table name means we cannot safely place this change against a
	 * per-group copy point. Fail closed rather than applying a change that
	 * might already be included in the group's COPY.
	 */
	if (nspname == NULL || nspname[0] == '\0' ||
		relname == NULL || relname[0] == '\0')
	{
		log_error("Cannot determine target table for a change while "
				  "--copy-groups is enabled; aborting to avoid a duplicate "
				  "or lost apply");
		*ok = false;
		return false;
	}

	int groupNumber = 0;
	uint64_t thresholdLSN = InvalidXLogRecPtr;

	if (!groupThresholdForTable(context, nspname, relname,
								&groupNumber, &thresholdLSN))
	{
		*ok = false;
		return false;
	}

	/* group 0 is anchored at the earliest consistent point: never skip */
	if (groupNumber == 0)
	{
		return false;
	}

	/*
	 * A group > 0 change we cannot place relative to LSN_g (unknown commit LSN,
	 * e.g. a continued multi-WAL-segment transaction whose commit metadata was
	 * not recorded) is unsafe to either apply (possible duplicate on top of the
	 * COPY) or skip (possible lost change). Fail closed so the operator sees a
	 * clear error instead of silent divergence.
	 */
	if (context->currentTxnCommitLSN == InvalidXLogRecPtr)
	{
		log_error("Cannot determine transaction commit LSN for a change to "
				  "\"%s\".\"%s\" in copy group %d; aborting to avoid a duplicate "
				  "or lost apply (continued transaction with no recorded commit "
				  "LSN)", nspname, relname, groupNumber);
		*ok = false;
		return false;
	}

	if (context->currentTxnCommitLSN <= thresholdLSN)
	{
		log_trace("Skipping change to \"%s\".\"%s\": txn commit %X/%X <= "
				  "group %d copy threshold %X/%X (already in COPY)",
				  nspname, relname,
				  LSN_FORMAT_ARGS(context->currentTxnCommitLSN),
				  groupNumber,
				  LSN_FORMAT_ARGS(thresholdLSN));
		return true;
	}

	return false;
}


/*
 * shouldFilterOutTable checks if a given table should be filtered out based
 * on the configured filters.
 *
 * Note: This function checks in-memory filter lists. For extension filtering
 * during CDC, we also need to check the catalog filter table (see usage in
 * parseSQLAction where we pass the catalog context).
 */
static bool
shouldFilterOutTable(const char *nspname, const char *relname,
					 SourceFilters *filters)
{
	if (filters == NULL)
	{
		return false;
	}

	/* Check exclude-schema filter */
	for (int i = 0; i < filters->excludeSchemaList.count; i++)
	{
		if (strcmp(filters->excludeSchemaList.array[i].nspname, nspname) == 0)
		{
			log_trace("Filtering out table \"%s\".\"%s\" (schema in exclude-schema list)",
					  nspname, relname);
			return true;
		}
	}

	/* Check include-only-schema filter */
	if (filters->includeOnlySchemaList.count > 0)
	{
		bool found = false;
		for (int i = 0; i < filters->includeOnlySchemaList.count; i++)
		{
			if (strcmp(filters->includeOnlySchemaList.array[i].nspname, nspname) == 0)
			{
				found = true;
				break;
			}
		}
		if (!found)
		{
			log_trace(
				"Filtering out table \"%s\".\"%s\" (schema not in include-only-schema list)",
				nspname, relname);
			return true;
		}
	}

	/* Check include-only-table filter */
	if (filters->includeOnlyTableList.count > 0)
	{
		bool found = false;
		for (int i = 0; i < filters->includeOnlyTableList.count; i++)
		{
			SourceFilterTable *table = &(filters->includeOnlyTableList.array[i]);
			if (strcmp(table->nspname, nspname) == 0 &&
				strcmp(table->relname, relname) == 0)
			{
				found = true;
				break;
			}
		}
		if (!found)
		{
			log_trace("Filtering out table \"%s\".\"%s\" (not in include-only list)",
					  nspname, relname);
			return true;
		}
	}

	/* Check exclude-table filter */
	for (int i = 0; i < filters->excludeTableList.count; i++)
	{
		SourceFilterTable *table = &(filters->excludeTableList.array[i]);
		if (strcmp(table->nspname, nspname) == 0 &&
			strcmp(table->relname, relname) == 0)
		{
			log_trace("Filtering out table \"%s\".\"%s\" (in exclude-table list)",
					  nspname, relname);
			return true;
		}
	}

	/* Check exclude-table-data filter */
	for (int i = 0; i < filters->excludeTableDataList.count; i++)
	{
		SourceFilterTable *table = &(filters->excludeTableDataList.array[i]);
		if (strcmp(table->nspname, nspname) == 0 &&
			strcmp(table->relname, relname) == 0)
		{
			log_trace("Filtering out table \"%s\".\"%s\" (in exclude-table-data list)",
					  nspname, relname);
			return true;
		}
	}

	return false;
}


/*
 * parseSQLAction returns the action that is implemented in the given SQL
 * query.
 */
bool
parseSQLAction(const char *query, LogicalMessageMetadata *metadata,
			   SourceFilters *filters)
{
	metadata->action = STREAM_ACTION_UNKNOWN;

	if (strcmp(query, "") == 0)
	{
		return true;
	}

	char *message = NULL;
	char *begin = strstr(query, OUTPUT_BEGIN);
	char *commit = strstr(query, OUTPUT_COMMIT);
	char *rollback = strstr(query, OUTPUT_ROLLBACK);
	char *switchwal = strstr(query, OUTPUT_SWITCHWAL);
	char *keepalive = strstr(query, OUTPUT_KEEPALIVE);
	char *endpos = strstr(query, OUTPUT_ENDPOS);

	/* do we have a BEGIN or a COMMIT message to parse metadata of? */
	if (query == begin)
	{
		metadata->action = STREAM_ACTION_BEGIN;
		message = begin + strlen(OUTPUT_BEGIN);
	}
	else if (query == commit)
	{
		metadata->action = STREAM_ACTION_COMMIT;
		message = commit + strlen(OUTPUT_COMMIT);
	}
	else if (query == rollback)
	{
		metadata->action = STREAM_ACTION_ROLLBACK;
		message = rollback + strlen(OUTPUT_ROLLBACK);
	}
	else if (query == switchwal)
	{
		metadata->action = STREAM_ACTION_SWITCH;
		message = switchwal + strlen(OUTPUT_SWITCHWAL);
	}
	else if (query == keepalive)
	{
		metadata->action = STREAM_ACTION_KEEPALIVE;
		message = keepalive + strlen(OUTPUT_KEEPALIVE);
	}
	else if (query == endpos)
	{
		metadata->action = STREAM_ACTION_ENDPOS;
		message = endpos + strlen(OUTPUT_ENDPOS);
	}

	if (message != NULL)
	{
		JSON_Value *json = json_parse_string(message);

		if (!parseMessageMetadata(metadata, message, json, true))
		{
			/* errors have already been logged */
			return false;
		}


		return true;
	}

	/*
	 * So the SQL Action is a DML (or a TRUNCATE).
	 */
	size_t tLen = sizeof(TRUNCATE) - 1;
	size_t pLen = sizeof(PREPARE) - 1;
	size_t eLen = sizeof(EXECUTE) - 1;

	if (strncmp(query, TRUNCATE, tLen) == 0)
	{
		metadata->action = STREAM_ACTION_TRUNCATE;

		/* Extract table name and check filters for TRUNCATE */
		const char *tableStart = query + tLen;
		while (*tableStart == ' ' || *tableStart == '\t')
		{
			tableStart++;
		}

		/*
		 * pgcopydb writes TRUNCATE as "TRUNCATE ONLY schema.table".
		 * The apply-side threshold must resolve the real relation name,
		 * not the optional ONLY keyword.
		 */
		if (strncmp(tableStart, "ONLY ", 5) == 0)
		{
			tableStart += 5;
			while (*tableStart == ' ' || *tableStart == '\t')
			{
				tableStart++;
			}
		}

		char nspname[PG_NAMEDATALEN] = { 0 };
		char relname[PG_NAMEDATALEN] = { 0 };

		if (!parseSQLQualifiedTableName(tableStart,
										nspname, sizeof(nspname),
										relname, sizeof(relname)))
		{
			log_error("Failed to parse table name in TRUNCATE statement: %s",
					  query);
			return false;
		}

		/*
		 * Remember the table so the apply can consult the --copy-groups
		 * threshold for this TRUNCATE (applied directly, not via EXECUTE).
		 */
		strlcpy(metadata->nspname, nspname, sizeof(metadata->nspname));
		strlcpy(metadata->relname, relname, sizeof(metadata->relname));

		/* Check if this table should be filtered out (user filters.ini) */
		if (shouldFilterOutTable(nspname, relname, filters))
		{
			metadata->filterOut = true;
			log_debug("Filtering out TRUNCATE for table \"%s\".\"%s\"",
					  nspname, relname);
		}
	}
	else if (strncmp(query, PREPARE, pLen) == 0)
	{
		char *spc = strchr(query + pLen, ' ');

		if (spc == NULL)
		{
			log_error("Failed to parse PREPARE statement: %s", query);
			return false;
		}

		/* make a copy of just the hexadecimal string */
		int len = spc - (query + pLen);
		char str[BUFSIZE] = { 0 };

		sformat(str, sizeof(str), "%.*s", len, query + pLen);

		uint32_t hash = 0;

		if (!hexStringToUInt32(str, &hash))
		{
			log_error("Failed to parse PREPARE statement name: %s", query);
			return false;
		}

		metadata->hash = hash;

		size_t iLen = sizeof(INSERT) - 1;
		size_t uLen = sizeof(UPDATE) - 1;
		size_t dLen = sizeof(DELETE) - 1;

		if (strncmp(spc + 1, INSERT, iLen) == 0)
		{
			/* skip ' AS ' and point to INSERT */
			metadata->stmt = spc + 1 + 3;
			metadata->action = STREAM_ACTION_INSERT;
		}
		else if (strncmp(spc + 1, UPDATE, uLen) == 0)
		{
			/* skip ' AS ' and point to UPDATE */
			metadata->stmt = spc + 1 + 3;
			metadata->action = STREAM_ACTION_UPDATE;
		}
		else if (strncmp(spc + 1, DELETE, dLen) == 0)
		{
			/* skip ' AS ' and point to DELETE */
			metadata->stmt = spc + 1 + 3;
			metadata->action = STREAM_ACTION_DELETE;
		}

		/* Extract table name and check filters for DML operations */
		if (metadata->stmt != NULL)
		{
			char nspname[PG_NAMEDATALEN] = { 0 };
			char relname[PG_NAMEDATALEN] = { 0 };

			if (extractTableNameFromPrepare(metadata->stmt,
											nspname, sizeof(nspname),
											relname, sizeof(relname)))
			{
				if (shouldFilterOutTable(nspname, relname, filters))
				{
					metadata->filterOut = true;
					log_debug("Filtering out %s for table \"%s\".\"%s\"",
							  metadata->action == STREAM_ACTION_INSERT ? "INSERT" :
							  metadata->action == STREAM_ACTION_UPDATE ? "UPDATE" :
							  "DELETE",
							  nspname, relname);
				}
			}
			else
			{
				return false;
			}
		}
	}
	else if (strncmp(query, EXECUTE, eLen) == 0)
	{
		metadata->action = STREAM_ACTION_EXECUTE;

		char *json = strchr(query + eLen, '[');

		if (json == NULL)
		{
			log_error("Failed to parse EXECUTE statement: %s", query);
			return false;
		}

		/* make a copy of just the hexadecimal string */
		int len = json - (query + eLen);
		char str[BUFSIZE] = { 0 };

		sformat(str, sizeof(str), "%.*s", len, query + pLen);

		uint32_t hash = 0;

		if (!hexStringToUInt32(str, &hash))
		{
			log_error("Failed to parse EXECUTE statement name: %s", query);
			return false;
		}

		metadata->hash = hash;

		/* chomp ; at the end of the query string */
		len = strlen(json) - 1;
		size_t bytes = len + 1;

		metadata->jsonBuffer = (char *) calloc(bytes, sizeof(char));

		if (metadata->jsonBuffer == NULL)
		{
			log_error(ALLOCATION_FAILED_ERROR);
			return false;
		}

		sformat(metadata->jsonBuffer, bytes, "%.*s", len, json);
	}

	if (metadata->action == STREAM_ACTION_UNKNOWN)
	{
		log_error("Failed to parse action from query: %s", query);
		return false;
	}

	return true;
}


/*
 * stream_apply_find_durable_lsn fetches the LSN for the current durable
 * location on the target system using pg_replication_origin_progress.
 */
bool
stream_apply_find_durable_lsn(StreamApplyContext *context, uint64_t *durableLSN)
{
	uint64_t flushLSN = InvalidXLogRecPtr;

	bool flush = true;

	if (!pgsql_replication_origin_progress(&(context->controlPgConn),
										   context->origin,
										   flush,
										   &flushLSN))
	{
		/* errors have already been logged */
		log_error("Failed to retrieve origin progress, "
				  "see above for details");
		return false;
	}

	*durableLSN = flushLSN;

	return true;
}


/*
 * readTxnCommitLSN ensures metadata has transaction COMMIT LSN by fetching it
 * from metadata file if it is not present
 */
static bool
readTxnCommitLSN(LogicalMessageMetadata *metadata,
				 const char *dir,
				 bool *txnCommitLSNFound)
{
	/* if txnCommitLSN is invalid, then fetch it from txn metadata file */
	if (metadata->txnCommitLSN != InvalidXLogRecPtr)
	{
		*txnCommitLSNFound = true;
		return true;
	}

	char txnfilename[MAXPGPATH] = { 0 };

	if (!computeTxnMetadataFilename(metadata->xid,
									dir,
									txnfilename))
	{
		/* errors have already been logged */
		return false;
	}

	if (!file_exists(txnfilename))
	{
		*txnCommitLSNFound = false;
		return true;
	}

	log_debug("stream_apply_sql: BEGIN message without a commit LSN, "
			  "fetching commit LSN from transaction metadata file \"%s\"",
			  txnfilename);

	LogicalMessageMetadata txnMetadata = { .xid = metadata->xid };

	if (!parseTxnMetadataFile(txnfilename, &txnMetadata))
	{
		/* errors have already been logged */
		return false;
	}

	*txnCommitLSNFound = true;
	metadata->txnCommitLSN = txnMetadata.txnCommitLSN;

	return true;
}


/*
 * parseTxnMetadataFile returns the transaction metadata content for the given
 * metadata filename.
 */
static bool
parseTxnMetadataFile(const char *filename, LogicalMessageMetadata *metadata)
{
	/* store xid as it will be overwritten while parsing metadata */
	uint32_t xid = metadata->xid;

	if (xid == 0)
	{
		log_error("BUG: parseTxnMetadataFile is called with "
				  "transaction xid: %lld", (long long) xid);
		return false;
	}

	char *txnMetadataContent = NULL;
	long size = 0L;

	if (!read_file(filename, &txnMetadataContent, &size))
	{
		/* errors have already been logged */
		return false;
	}

	JSON_Value *json = json_parse_string(txnMetadataContent);

	if (!parseMessageMetadata(metadata, txnMetadataContent, json, true))
	{
		/* errors have already been logged */
		return false;
	}


	if (metadata->txnCommitLSN == InvalidXLogRecPtr ||
		metadata->xid != xid ||
		IS_EMPTY_STRING_BUFFER(metadata->timestamp))
	{
		log_error("Failed to parse metadata for transaction metadata file "
				  "\"%s\": %s", filename, txnMetadataContent);
		return false;
	}

	return true;
}


/*
 *  computeTxnMetadataFilename computes the file path for transaction metadata
 *  based on its transaction id
 */
static bool
computeTxnMetadataFilename(uint32_t xid, const char *dir, char *filename)
{
	if (dir == NULL)
	{
		log_error("BUG: computeTxnMetadataFilename is called with "
				  "directory: NULL");
		return false;
	}

	if (xid == 0)
	{
		log_error("BUG: computeTxnMetadataFilename is called with "
				  "transaction xid: %lld", (long long) xid);
		return false;
	}

	sformat(filename, MAXPGPATH, "%s/%lld.json", dir, (long long) xid);

	return true;
}


/*
 * writeTxnCommitMetadata writes the transaction metadata to a file in the given
 * directory. Exposed so the transform can pre-write it for continued (multi-WAL
 * segment) transactions under --copy-groups, letting the apply resolve their
 * commit LSN at BEGIN for the per-group threshold filter.
 */
bool
writeTxnCommitMetadata(LogicalMessageMetadata *mesg, const char *dir)
{
	char txnfilename[MAXPGPATH] = { 0 };

	if (mesg->action != STREAM_ACTION_COMMIT)
	{
		log_error("BUG: writeTxnCommitMetadata is called with "
				  "action: %s", StreamActionToString(mesg->action));
		return false;
	}

	if (!computeTxnMetadataFilename(mesg->xid, dir, txnfilename))
	{
		/* errors have already been logged */
		return false;
	}

	log_debug("stream_write_commit_metadata_file: writing transaction "
			  "metadata file \"%s\" with commit lsn %X/%X",
			  txnfilename,
			  LSN_FORMAT_ARGS(mesg->lsn));

	char contents[BUFSIZE] = { 0 };

	sformat(contents, BUFSIZE,
			"{\"xid\":%lld,\"commit_lsn\":\"%X/%X\",\"timestamp\":\"%s\"}\n",
			(long long) mesg->xid,
			LSN_FORMAT_ARGS(mesg->lsn),
			mesg->timestamp);

	/* write the metadata to txnfilename */
	if (!write_file(contents, strlen(contents), txnfilename))
	{
		log_error("Failed to write file \"%s\"", txnfilename);
		return false;
	}

	return true;
}
