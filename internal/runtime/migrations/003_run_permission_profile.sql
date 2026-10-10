ALTER TABLE runtime.runs
    ADD COLUMN IF NOT EXISTS permission_profile_id text;
