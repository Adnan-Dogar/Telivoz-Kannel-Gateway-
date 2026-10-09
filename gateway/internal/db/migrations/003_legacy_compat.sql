-- DLR webhook payload format: 'v1' (new JSON) or 'legacy' (the old DLR pusher's JSON), so migrated clients
-- keep working without code changes.
ALTER TABLE clients ADD COLUMN dlr_format TEXT NOT NULL DEFAULT 'v1' CHECK (dlr_format IN ('v1', 'legacy'));
