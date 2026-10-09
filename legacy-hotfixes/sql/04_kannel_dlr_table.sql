-- Table for Kannel's DLR store (see kannel/kannel-dlr-storage.conf). Today bearerbox keeps this mapping in
-- memory, so every bearerbox restart loses the DLRs of messages still waiting for one.
CREATE TABLE IF NOT EXISTS kannel_dlr (
    smsc        VARCHAR(48)  NOT NULL,
    ts          VARCHAR(65)  NOT NULL,
    destination VARCHAR(40)  DEFAULT NULL,
    source      VARCHAR(40)  DEFAULT NULL,
    service     VARCHAR(255) DEFAULT NULL,
    url         VARCHAR(255) DEFAULT NULL,
    mask        INT          DEFAULT NULL,
    status      INT          DEFAULT NULL,
    boxc        VARCHAR(40)  DEFAULT NULL,
    KEY idx_smsc_ts (smsc, ts)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4;

-- Least-privilege user for Kannel. Replace the password before running.
-- CREATE USER 'kannel_dlr'@'localhost' IDENTIFIED BY 'CHANGE_ME';
-- GRANT SELECT, INSERT, UPDATE, DELETE ON <database>.kannel_dlr TO 'kannel_dlr'@'localhost';
