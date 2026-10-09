-- Database safety net against storing (and therefore sending and charging) the same message twice.
-- A message is a duplicate when the same client sends the same client message ID to the same destination.
-- Rows without a real client message ID (NULL, '' or '0') are never blocked.
--
-- Works for every insert path, including components outside the portal code. A duplicate insert fails with
-- error 1062; the patched ApiController catches it and answers with the original message's reference.
-- Apply AFTER the PHP patches (see README). outgoing_sms itself is not altered, so archiving keeps working.

CREATE TABLE IF NOT EXISTS outgoing_sms_dedup (
    dedup_key  VARCHAR(255) NOT NULL,
    created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (dedup_key),
    KEY idx_created_at (created_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4;

DROP TRIGGER IF EXISTS outgoing_sms_dedup_guard;

DELIMITER $$
CREATE TRIGGER outgoing_sms_dedup_guard BEFORE INSERT ON outgoing_sms FOR EACH ROW
BEGIN
    IF NEW.external_message_id IS NOT NULL AND NEW.external_message_id NOT IN ('', '0') THEN
        INSERT INTO outgoing_sms_dedup (dedup_key)
        VALUES (CONCAT_WS(':', NEW.client_id, NEW.destination, NEW.external_message_id));
    END IF;
END$$
DELIMITER ;

-- Retries arrive within minutes; keep keys for 3 days. Needs event_scheduler = ON.
DROP EVENT IF EXISTS outgoing_sms_dedup_purge;
CREATE EVENT outgoing_sms_dedup_purge ON SCHEDULE EVERY 1 HOUR
    COMMENT 'Remove duplicate-guard keys older than 3 days'
    DO DELETE FROM outgoing_sms_dedup WHERE created_at < NOW() - INTERVAL 3 DAY;
