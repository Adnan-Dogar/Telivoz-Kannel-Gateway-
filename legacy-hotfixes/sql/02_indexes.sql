-- Index changes. All are online in MariaDB (no table copy). Run off-peak anyway on large tables.

-- DLR pusher: both of its claim queries filter on runID = 0 and status, and it then reads by runID.
-- Without this index every 2-second poll scans delivery_reports.
ALTER TABLE delivery_reports ADD INDEX idx_claim (runID, status, nextSend), ALGORITHM = INPLACE, LOCK = NONE;
-- Used by the archiver event (03_dlr_archiver.sql).
ALTER TABLE delivery_reports ADD INDEX idx_date_created (date_created), ALGORITHM = INPLACE, LOCK = NONE;
-- Exact duplicate of `status`.
ALTER TABLE delivery_reports DROP INDEX status_2;

-- Route lookup: every message runs up to 8 queries on routes, all filtered by country and status.
ALTER TABLE routes ADD INDEX idx_lookup (country_id, status, mcc_mnc), ALGORITHM = INPLACE, LOCK = NONE;

-- outgoing_sms: drop indexes that slow every insert/update and that no query uses.
-- messageID and clientID_2 index the 700-character message text; the others duplicate existing indexes
-- (scheduleID_2 = scheduleID, date_created_2 = date_created).
ALTER TABLE outgoing_sms
    DROP INDEX messageID,
    DROP INDEX clientID_2,
    DROP INDEX scheduleID_2,
    DROP INDEX date_created_2;
