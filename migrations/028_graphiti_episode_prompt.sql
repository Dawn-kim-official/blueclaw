ALTER TABLE graphiti_episode
  ADD COLUMN IF NOT EXISTS prompt text NOT NULL DEFAULT '';
