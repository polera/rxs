ALTER TABLE entry_state
ADD COLUMN reading_progress REAL NOT NULL DEFAULT 0
CHECK (reading_progress >= 0 AND reading_progress <= 1);
