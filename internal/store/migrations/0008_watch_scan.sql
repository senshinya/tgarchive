-- How many posts each watch has looked at, and how many of those it archived, from this version
-- on (hits counts everything since the watch was created): the hit rate is scan_hits / scanned.
ALTER TABLE channel_watches ADD COLUMN scanned INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channel_watches ADD COLUMN scan_hits INTEGER NOT NULL DEFAULT 0;
