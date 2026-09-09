ALTER TABLE entries ADD COLUMN enrichment_input_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE entries ADD COLUMN enrichment_eligible INTEGER NOT NULL DEFAULT 0 CHECK (enrichment_eligible IN (0, 1));

CREATE TABLE store_metadata (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
INSERT INTO store_metadata(key, value) VALUES ('enrichment_policy', 0);

-- Include the hash so attempted-history filtering need not load feed bodies.
CREATE INDEX entries_enrichment_pending ON entries
    (feed_id, (CASE WHEN published_at='' THEN updated_at ELSE published_at END) DESC, id DESC, enrichment_input_hash)
    WHERE enrichment_eligible=1;
-- Open backfills the derived columns before exposing the store. A failed or
-- interrupted backfill leaves policy version 0, so the next Open retries it.
