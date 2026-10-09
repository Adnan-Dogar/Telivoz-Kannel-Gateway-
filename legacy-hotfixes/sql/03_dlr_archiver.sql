-- Replaces the delivery_reports_archiver event. The old version filtered on DATE(date_created), which cannot
-- use an index, and moved every old row in one large transaction, stalling the DLR pusher while it ran.
-- This version keeps the same 21-day retention but works through the table in batches of 5,000 rows.
-- Requires idx_date_created from 02_indexes.sql and event_scheduler = ON.

DROP EVENT IF EXISTS delivery_reports_archiver;

DELIMITER $$
CREATE EVENT delivery_reports_archiver ON SCHEDULE EVERY 15 MINUTE
    ON COMPLETION PRESERVE ENABLE
    COMMENT 'Archive delivery reports older than 21 days, in batches'
DO BEGIN
    DECLARE cutoff DATETIME DEFAULT CURDATE() - INTERVAL 21 DAY;
    DECLARE lo INT;
    DECLARE hi INT;
    DECLARE last_id INT;

    SELECT MIN(id), MAX(id) INTO lo, last_id FROM delivery_reports WHERE date_created < cutoff;

    WHILE lo IS NOT NULL AND lo <= last_id DO
        SET hi = lo + 5000;
        INSERT IGNORE INTO delivery_reports_archive
            SELECT * FROM delivery_reports WHERE id >= lo AND id < hi AND date_created < cutoff;
        DELETE FROM delivery_reports WHERE id >= lo AND id < hi AND date_created < cutoff;
        SET lo = hi;
    END WHILE;
END$$
DELIMITER ;
