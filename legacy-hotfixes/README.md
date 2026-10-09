# Temporary fixes for the current (old) system

These keep the current Kannel/PHP system stable **only until traffic moves to the new platform**. No new
features are built on the old code. Apply off-peak, one step at a time, after taking a database backup.

| Step | File | Fixes | Needs restart |
|---|---|---|---|
| 0 | `sql/00_precheck.sql` | Read-only report: duplicates, vendor ack settings, table sizes, stuck DLRs | No |
| 1 | `patches/portal-hotfixes.patch` | Duplicate submits stored/charged once; store + charge in one transaction; no overdraw; timeouts on Kannel calls; SMSC status cached; smsbox-only restart on routing change; vendor ack default 0x02 | No (PHP) |
| 2 | `sql/01_duplicate_guard.sql` | Database blocks a second copy of the same client message ID to the same destination (any insert path) | No |
| 3 | `sql/02_indexes.sql` | Faster DLR polling and routing; faster inserts | No (online) |
| 4 | `sql/03_dlr_archiver.sql` | DLR archiving in small batches (no more stalls) | No |
| 5 | `sql/04_kannel_dlr_table.sql` + `kannel/kannel-dlr-storage.conf` | DLRs survive restarts; resends capped at 3 | **One** bearerbox restart, at the quietest time |
| 6 | `sql/05_vendor_ack_expiry.sql` | Vendors no longer get the same message again and again | Reload each vendor connection, one at a time |

Apply the patch from the portal root: `patch -p1 < portal-hotfixes.patch` (try `--dry-run` first).
Step 1 must go before step 2. Enable the event scheduler (`SET GLOBAL event_scheduler = ON`) for steps 2 and 4.
Rollback: `sql/99_rollback.sql` and `patch -R -p1 < portal-hotfixes.patch`.

**Tested** on a copy of the provided database dump: the original code stores and charges a retried message
twice (and 5 concurrent retries 5 times); with the patch each is stored and charged once, a message with the
same ID to another number is still sent, and insufficient balance is rejected without storing or charging.
All SQL scripts install cleanly. Not yet exercised: the batch logic of the step 4 archiver event — watch the
first run on production (`SELECT COUNT(*) FROM delivery_reports_archive` before and after).

**Findings from the dump:** 2.4% of archived messages are duplicates of the same message ID, arriving ~60 s
apart (Kannel's resend interval); 14,740 DLRs are stuck in "processing" since 2023; balances show float
rounding errors. Messages reach Kannel through a database trigger (`insert_sqlbox` → `sqlbox_send_sms`), and
DLRs are written to `delivery_reports` by the `emad_dlr_updator` trigger.
