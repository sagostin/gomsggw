-- ==============================================================================
-- GOMSGGW Schema Migration: Old → New
-- ==============================================================================
-- Run this AFTER running the Go migration tool (migrate_decrypt.go) which 
-- decrypts usernames and handles data migration.
--
-- This script adds new columns and creates new tables for the current schema.
-- ==============================================================================

-- ============================================
-- Step 1: Add missing columns to clients table
-- ============================================
ALTER TABLE public.clients 
    ADD COLUMN IF NOT EXISTS "type" text DEFAULT 'legacy',
    ADD COLUMN IF NOT EXISTS timezone text DEFAULT 'UTC';

-- ============================================
-- Step 2: Add missing columns to client_numbers table  
-- ============================================
ALTER TABLE public.client_numbers 
    ADD COLUMN IF NOT EXISTS tag text,
    ADD COLUMN IF NOT EXISTS "group" text;

-- ============================================
-- Step 3: Add missing columns to carriers table
-- ============================================
ALTER TABLE public.carriers 
    ADD COLUMN IF NOT EXISTS profile_id text;

-- ============================================
-- Step 4: Add missing columns to media_files table
-- ============================================
ALTER TABLE public.media_files 
    ADD COLUMN IF NOT EXISTS access_token text;

-- Create unique index for access_token
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_files_access_token 
    ON public.media_files(access_token) WHERE access_token IS NOT NULL;

-- ============================================
-- Step 5: Add missing columns to msg_record_db_items table
-- ============================================
ALTER TABLE public.msg_record_db_items 
    ADD COLUMN IF NOT EXISTS direction text,
    ADD COLUMN IF NOT EXISTS from_client_type text,
    ADD COLUMN IF NOT EXISTS to_client_type text,
    ADD COLUMN IF NOT EXISTS delivery_method text,
    ADD COLUMN IF NOT EXISTS encoding text,
    ADD COLUMN IF NOT EXISTS total_segments int DEFAULT 1,
    ADD COLUMN IF NOT EXISTS segment_index int DEFAULT 0,
    ADD COLUMN IF NOT EXISTS original_size_bytes int,
    ADD COLUMN IF NOT EXISTS transcoded_size_bytes int,
    ADD COLUMN IF NOT EXISTS media_count int,
    ADD COLUMN IF NOT EXISTS transcoding_performed bool DEFAULT false;

-- ============================================
-- Step 6: Create client_settings table (NEW)
-- ============================================
CREATE TABLE IF NOT EXISTS public.client_settings (
    id bigserial NOT NULL,
    client_id int8 UNIQUE NOT NULL,
    auth_method text DEFAULT 'basic',
    api_format text DEFAULT 'generic',
    disable_message_splitting bool DEFAULT false,
    webhook_retries int DEFAULT 3,
    webhook_timeout_secs int DEFAULT 10,
    include_raw_segments bool DEFAULT false,
    default_webhook text,
    sms_burst_limit int8 DEFAULT 0,
    sms_daily_limit int8 DEFAULT 0,
    sms_monthly_limit int8 DEFAULT 0,
    mms_burst_limit int8 DEFAULT 0,
    mms_daily_limit int8 DEFAULT 0,
    mms_monthly_limit int8 DEFAULT 0,
    limit_both bool DEFAULT false,
    CONSTRAINT client_settings_pkey PRIMARY KEY (id),
    CONSTRAINT fk_client_settings_client FOREIGN KEY (client_id) 
        REFERENCES public.clients(id) ON DELETE CASCADE
);

-- ============================================
-- Step 7: Create number_settings table (NEW)
-- ============================================
CREATE TABLE IF NOT EXISTS public.number_settings (
    id bigserial NOT NULL,
    number_id int8 UNIQUE NOT NULL,
    sms_burst_limit int8 DEFAULT 0,
    sms_daily_limit int8 DEFAULT 0,
    sms_monthly_limit int8 DEFAULT 0,
    mms_burst_limit int8 DEFAULT 0,
    mms_daily_limit int8 DEFAULT 0,
    mms_monthly_limit int8 DEFAULT 0,
    limit_both bool DEFAULT false,
    CONSTRAINT number_settings_pkey PRIMARY KEY (id),
    CONSTRAINT fk_number_settings_number FOREIGN KEY (number_id) 
        REFERENCES public.client_numbers(id) ON DELETE CASCADE
);

-- ============================================
-- Step 8: Set defaults for existing data
-- ============================================

-- Default type to 'legacy' for existing clients
UPDATE public.clients SET "type" = 'legacy' WHERE "type" IS NULL;

-- Default timezone to 'UTC' for existing clients  
UPDATE public.clients SET timezone = 'UTC' WHERE timezone IS NULL;

-- ============================================
-- Verification Queries (run after migration)
-- ============================================

-- Check new columns exist
-- SELECT column_name FROM information_schema.columns WHERE table_name = 'clients';
-- SELECT column_name FROM information_schema.columns WHERE table_name = 'client_numbers';
-- SELECT column_name FROM information_schema.columns WHERE table_name = 'media_files';

-- Check new tables exist
-- SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('client_settings', 'number_settings');
