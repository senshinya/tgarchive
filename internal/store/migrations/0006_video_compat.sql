-- Browser-playable copies of archived videos whose codec no browser (or not every browser)
-- plays. compat_state: '' not checked yet, 'none' original plays everywhere, 'done' copy at
-- compat_path, 'failed' conversion failed (compat_error). compat_codec is the original's video
-- codec as ffprobe names it.
ALTER TABLE media ADD COLUMN compat_state TEXT NOT NULL DEFAULT '';
ALTER TABLE media ADD COLUMN compat_codec TEXT NOT NULL DEFAULT '';
ALTER TABLE media ADD COLUMN compat_path  TEXT NOT NULL DEFAULT '';
ALTER TABLE media ADD COLUMN compat_error TEXT NOT NULL DEFAULT '';
