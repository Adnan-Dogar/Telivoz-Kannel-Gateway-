-- Stop re-queuing unacknowledged messages (0x01), which resends the same message to the vendor.
-- 0x02 keeps waiting for the vendor's ack. After running this, re-save each vendor in the portal (or edit its
-- SMSC file) and reload that connection, one vendor at a time, off-peak. See README step 5.
SELECT id, smsc_name, wait_ack_expire FROM sms_gateways WHERE gateway_type = 2;
UPDATE sms_gateways SET wait_ack_expire = '0x02'
WHERE gateway_type = 2 AND (wait_ack_expire IS NULL OR wait_ack_expire IN ('', '0x01', '1', '0x1'));
