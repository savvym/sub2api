-- Mixed-version cleanup may select an expired idempotency row immediately
-- before a newer binary extends its upgrade-fence lifetime. Recheck the
-- current row version at deletion time so that stale cleanup candidates cannot
-- remove a renewed fence and let an older binary execute the raw key again.

CREATE OR REPLACE FUNCTION guard_idempotency_record_expiry_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.expires_at > CURRENT_TIMESTAMP THEN
        RETURN NULL;
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS idempotency_records_expiry_delete_guard ON idempotency_records;

CREATE TRIGGER idempotency_records_expiry_delete_guard
BEFORE DELETE ON idempotency_records
FOR EACH ROW
EXECUTE FUNCTION guard_idempotency_record_expiry_delete();
