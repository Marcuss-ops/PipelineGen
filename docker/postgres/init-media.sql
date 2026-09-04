-- Keep the test database available without making PipelineGen depend on it.
-- This runs only when the named PostgreSQL volume is initialized.
SELECT 'CREATE DATABASE pipelinegen_media_test'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'pipelinegen_media_test')\gexec
