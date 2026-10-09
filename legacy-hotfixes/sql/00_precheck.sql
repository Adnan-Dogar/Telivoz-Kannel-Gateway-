-- Read-only checks to run before applying the hotfixes. Safe on production.
-- Usage: mysql <db> < 00_precheck.sql

-- 1. Duplicate submits in the last 7 days: same client, destination and client message ID stored more than once.
SELECT COUNT(*) AS duplicate_groups, COALESCE(SUM(copies - 1), 0) AS extra_rows,
       ROUND(AVG(gap_seconds)) AS avg_seconds_between_copies
FROM (
    SELECT client_id, destination, external_message_id, COUNT(*) AS copies,
           TIMESTAMPDIFF(SECOND, MIN(date_created), MAX(date_created)) AS gap_seconds
    FROM outgoing_sms
    WHERE date_created >= NOW() - INTERVAL 7 DAY
      AND external_message_id IS NOT NULL AND external_message_id NOT IN ('', '0')
    GROUP BY client_id, destination, external_message_id
    HAVING COUNT(*) > 1
) d;

-- 2. Vendor connections and their ack-expiry setting (0x01 re-queues and can resend the same message).
SELECT id, smsc_name, status, wait_ack, wait_ack_expire, max_pending_submits, field1 AS throughput
FROM sms_gateways WHERE gateway_type = 2;

-- 3. Table sizes, to plan the index changes.
SELECT table_name, table_rows, ROUND((data_length + index_length) / 1024 / 1024) AS size_mb
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name IN ('outgoing_sms', 'delivery_reports', 'sqlbox_send_sms', 'sqlbox_sent_sms', 'routes');

-- 4. DLRs waiting to be pushed to clients, and the oldest one.
SELECT status, COUNT(*) AS dlrs, MIN(date_created) AS oldest FROM delivery_reports GROUP BY status;

-- 5. The event scheduler must be ON for the purge and archiver events.
SHOW VARIABLES LIKE 'event_scheduler';
