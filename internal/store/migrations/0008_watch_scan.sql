-- How many posts each watch has looked at, and how many of those it archived, from this version
-- on (hits counts everything since the watch was created): the hit rate is scan_hits / scanned.
ALTER TABLE channel_watches ADD COLUMN scanned INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channel_watches ADD COLUMN scan_hits INTEGER NOT NULL DEFAULT 0;
-- Posts already waiting to be judged were scanned too (an album counts once), so their hits
-- count towards the rate.
UPDATE channel_watches SET scanned = (SELECT COUNT(DISTINCT CASE WHEN p.grouped_id = 0 THEN 'm' || p.tg_message_id ELSE 'g' || p.grouped_id END)
  FROM watch_pending p WHERE p.watch_id = channel_watches.id);
