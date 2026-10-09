-- Undo the SQL hotfixes. Run only the sections you need.

-- 01_duplicate_guard.sql
DROP TRIGGER IF EXISTS outgoing_sms_dedup_guard;
DROP EVENT IF EXISTS outgoing_sms_dedup_purge;
-- DROP TABLE outgoing_sms_dedup;   -- optional, harmless to keep

-- 02_indexes.sql
ALTER TABLE delivery_reports DROP INDEX idx_claim;
ALTER TABLE delivery_reports DROP INDEX idx_date_created;
ALTER TABLE delivery_reports ADD INDEX status_2 (status);
ALTER TABLE routes DROP INDEX idx_lookup;
ALTER TABLE outgoing_sms
    ADD INDEX messageID (message),
    ADD INDEX clientID_2 (client_id, message),
    ADD INDEX scheduleID_2 (schedule_id),
    ADD INDEX date_created_2 (date_created);

-- 03_dlr_archiver.sql: restore the original event from portal/events/delivery_reports_archiver.sql

-- 05_vendor_ack_expiry.sql: set wait_ack_expire back to the values printed by the script's first SELECT.
