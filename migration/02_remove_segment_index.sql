-- ==============================================================================
-- GOMSGGW Schema Migration: Remove SegmentIndex, Add OriginalBytesLength
-- ==============================================================================
-- This migration removes the unused segment_index column and adds
-- original_bytes_length for SMS message byte tracking.
-- ==============================================================================

-- ============================================
-- Step 1: Drop unused segment_index column
-- ============================================
ALTER TABLE public.msg_record_db_items 
    DROP COLUMN IF EXISTS segment_index;

-- ============================================
-- Step 2: Add original_bytes_length for SMS
-- ============================================
ALTER TABLE public.msg_record_db_items 
    ADD COLUMN IF NOT EXISTS original_bytes_length int;

-- ============================================
-- Verification Query
-- ============================================
-- SELECT column_name FROM information_schema.columns 
-- WHERE table_name = 'msg_record_db_items' 
-- ORDER BY column_name;
