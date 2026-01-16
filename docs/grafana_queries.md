# GOMSGGW Grafana Analytics Queries

This document provides a comprehensive set of SQL queries for building Grafana dashboards to monitor GOMSGGW messaging gateway usage and performance.

> **Database**: These queries assume a PostgreSQL or MySQL backend (adjust syntax as needed for your database).
> **Table**: `msg_record_db_items`

---

## Table of Contents

1. [Overview Metrics](#overview-metrics)
2. [Client Usage Analytics](#client-usage-analytics)
3. [Message Type Distribution](#message-type-distribution)
4. [Direction & Routing Analytics](#direction--routing-analytics)
5. [MMS Media Analytics](#mms-media-analytics)
6. [SMS Encoding & Segmentation](#sms-encoding--segmentation)
7. [Carrier Analytics](#carrier-analytics)
8. [Time-Series Trends](#time-series-trends)
9. [Rate Limiting & Quota Monitoring](#rate-limiting--quota-monitoring)
10. [Server & Infrastructure](#server--infrastructure)

---

## Overview Metrics

### Total Messages (Stat Panel)
```sql
SELECT COUNT(*) AS total_messages
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
```

### Total SMS vs MMS (Pie Chart)
```sql
SELECT type, COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY type
```

### Active Clients Count (Stat Panel)
```sql
SELECT COUNT(DISTINCT client_id) AS active_clients
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
```

### Messages Today vs Yesterday (Stat Panel with Compare)
```sql
-- Today
SELECT COUNT(*) AS today_count
FROM msg_record_db_items
WHERE DATE(received_timestamp) = CURRENT_DATE

-- Yesterday (for comparison)
-- SELECT COUNT(*) FROM msg_record_db_items WHERE DATE(received_timestamp) = CURRENT_DATE - INTERVAL '1 day'
```

---

## Client Usage Analytics

### Top 10 Clients by Message Volume (Bar Chart - Horizontal)
```sql
SELECT 
    c.name AS client_name,
    COUNT(m.id) AS message_count
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY c.id, c.name
ORDER BY message_count DESC
LIMIT 10
```

### Client Message Volume Over Time (Time Series)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1h') AS time,
    c.name AS metric,
    COUNT(*) AS value
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE 
    m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
    AND c.id IN ($client_ids)  -- Dashboard variable
GROUP BY time, c.name
ORDER BY time
```

### Per-Client SMS/MMS Breakdown (Stacked Bar)
```sql
SELECT 
    c.name AS client_name,
    m.type,
    COUNT(*) AS count
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY c.name, m.type
ORDER BY COUNT(*) DESC
```

### Client Usage by Direction (Table)
```sql
SELECT 
    c.name AS client_name,
    SUM(CASE WHEN m.direction = 'outbound' THEN 1 ELSE 0 END) AS outbound,
    SUM(CASE WHEN m.direction = 'inbound' THEN 1 ELSE 0 END) AS inbound,
    COUNT(*) AS total
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY c.id, c.name
ORDER BY total DESC
```

### Clients with No Activity (Alert Query)
```sql
SELECT c.name AS dormant_client
FROM clients c
LEFT JOIN msg_record_db_items m 
    ON c.id = m.client_id 
    AND m.received_timestamp > NOW() - INTERVAL '7 day'
WHERE m.id IS NULL
```

---

## Message Type Distribution

### SMS vs MMS Ratio Over Time (Time Series - Stacked)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1h') AS time,
    type AS metric,
    COUNT(*) AS value
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY time, type
ORDER BY time
```

### Internal vs External Messages (Pie Chart)
```sql
SELECT 
    CASE WHEN internal THEN 'Internal (Client-to-Client)' ELSE 'External (Via Carrier)' END AS type,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY internal
```

### Message Routing Breakdown (Sankey/Flow - Table Data)
```sql
SELECT 
    from_client_type,
    to_client_type,
    delivery_method,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY from_client_type, to_client_type, delivery_method
ORDER BY count DESC
```

---

## Direction & Routing Analytics

### Inbound vs Outbound Volume (Time Series)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1h') AS time,
    direction AS metric,
    COUNT(*) AS value
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY time, direction
ORDER BY time
```

### Delivery Method Distribution (Pie Chart)
```sql
SELECT 
    delivery_method,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY delivery_method
ORDER BY count DESC
```

### Client Type Flow (Heatmap/Table)
```sql
SELECT 
    from_client_type AS source,
    to_client_type AS destination,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY from_client_type, to_client_type
ORDER BY count DESC
```

### Legacy vs Web Client Activity (Time Series)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1h') AS time,
    from_client_type AS metric,
    COUNT(*) AS value
FROM msg_record_db_items
WHERE 
    received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
    AND from_client_type IN ('legacy', 'web')
GROUP BY time, from_client_type
ORDER BY time
```

### Source IP Activity (Table - Web Clients Only)
```sql
SELECT 
    source_ip,
    c.name AS client_name,
    COUNT(*) AS message_count
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE 
    m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
    AND m.source_ip IS NOT NULL 
    AND m.source_ip != ''
GROUP BY source_ip, c.name
ORDER BY message_count DESC
LIMIT 20
```

---

## MMS Media Analytics

### Total MMS Media Volume (Stat Panel)
```sql
SELECT 
    SUM(media_count) AS total_media_files,
    SUM(original_size_bytes) / 1024 / 1024 AS original_mb,
    SUM(transcoded_size_bytes) / 1024 / 1024 AS transcoded_mb
FROM msg_record_db_items
WHERE 
    type = 'mms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
```

### Transcoding Efficiency (Gauge - Percentage Saved)
```sql
SELECT 
    ROUND(
        (1 - (SUM(transcoded_size_bytes)::float / NULLIF(SUM(original_size_bytes), 0))) * 100, 
        1
    ) AS percent_saved
FROM msg_record_db_items
WHERE 
    type = 'mms'
    AND transcoding_performed = true
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
```

### MMS Size Distribution Over Time (Time Series - Dual Axis)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1h') AS time,
    SUM(original_size_bytes) / 1024 AS original_kb,
    SUM(transcoded_size_bytes) / 1024 AS transcoded_kb
FROM msg_record_db_items
WHERE 
    type = 'mms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY time
ORDER BY time
```

### Average Media Count Per MMS (Stat)
```sql
SELECT ROUND(AVG(media_count), 2) AS avg_attachments
FROM msg_record_db_items
WHERE 
    type = 'mms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
```

### MMS Transcoding Rate (Gauge)
```sql
SELECT 
    ROUND(
        (SUM(CASE WHEN transcoding_performed THEN 1 ELSE 0 END)::float / COUNT(*)) * 100, 
        1
    ) AS transcoding_rate_percent
FROM msg_record_db_items
WHERE 
    type = 'mms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
```

### Largest MMS Messages (Table)
```sql
SELECT 
    c.name AS client_name,
    m.log_id,
    m.received_timestamp,
    m.original_size_bytes / 1024 AS original_kb,
    m.transcoded_size_bytes / 1024 AS transcoded_kb,
    m.media_count
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE 
    m.type = 'mms'
    AND m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
ORDER BY m.original_size_bytes DESC
LIMIT 10
```

### Client MMS Bandwidth Usage (Bar Chart)
```sql
SELECT 
    c.name AS client_name,
    SUM(m.original_size_bytes) / 1024 / 1024 AS original_mb,
    SUM(m.transcoded_size_bytes) / 1024 / 1024 AS transcoded_mb
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE 
    m.type = 'mms'
    AND m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY c.id, c.name
ORDER BY original_mb DESC
LIMIT 10
```

---

## SMS Encoding & Segmentation

### Encoding Distribution (Pie Chart)
```sql
SELECT 
    COALESCE(encoding, 'unknown') AS encoding,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE 
    type = 'sms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY encoding
```

### Multi-Segment SMS Rate (Gauge)
```sql
SELECT 
    ROUND(
        (SUM(CASE WHEN total_segments > 1 THEN 1 ELSE 0 END)::float / NULLIF(COUNT(*), 0)) * 100, 
        1
    ) AS multi_segment_rate
FROM msg_record_db_items
WHERE 
    type = 'sms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
```

### Segment Distribution (Histogram/Bar Chart)
```sql
SELECT 
    total_segments,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE 
    type = 'sms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY total_segments
ORDER BY total_segments
```

### Total Segments Processed (Stat)
```sql
SELECT SUM(total_segments) AS total_segments
FROM msg_record_db_items
WHERE 
    type = 'sms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
```

### Average SMS Size by Encoding (Table)
```sql
SELECT 
    encoding,
    COUNT(*) AS message_count,
    ROUND(AVG(original_bytes_length), 0) AS avg_bytes,
    ROUND(AVG(total_segments), 2) AS avg_segments
FROM msg_record_db_items
WHERE 
    type = 'sms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY encoding
ORDER BY message_count DESC
```

### UCS2 (Unicode) Usage Trend (Time Series)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1d') AS time,
    SUM(CASE WHEN encoding = 'ucs2' THEN 1 ELSE 0 END) AS ucs2_count,
    SUM(CASE WHEN encoding = 'gsm7' THEN 1 ELSE 0 END) AS gsm7_count
FROM msg_record_db_items
WHERE 
    type = 'sms'
    AND received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY time
ORDER BY time
```

---

## Carrier Analytics

### Messages by Carrier (Pie Chart)
```sql
SELECT 
    COALESCE(NULLIF(carrier, ''), 'Internal') AS carrier,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY carrier
ORDER BY count DESC
```

### Carrier Volume Over Time (Time Series)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1h') AS time,
    COALESCE(NULLIF(carrier, ''), 'internal') AS metric,
    COUNT(*) AS value
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY time, carrier
ORDER BY time
```

### Carrier Usage by Client (Table)
```sql
SELECT 
    c.name AS client_name,
    m.carrier,
    COUNT(*) AS message_count
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE 
    m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
    AND m.carrier IS NOT NULL 
    AND m.carrier != ''
GROUP BY c.name, m.carrier
ORDER BY message_count DESC
```

### Carrier Distribution by Message Type (Stacked Bar)
```sql
SELECT 
    COALESCE(NULLIF(carrier, ''), 'Internal') AS carrier,
    type,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY carrier, type
ORDER BY count DESC
```

---

## Time-Series Trends

### Message Volume Heatmap (Heatmap - Hour x Day of Week)
```sql
SELECT 
    EXTRACT(ISODOW FROM received_timestamp) AS day_of_week,
    EXTRACT(HOUR FROM received_timestamp) AS hour,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY day_of_week, hour
ORDER BY day_of_week, hour
```

### Hourly Message Rate (Time Series)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1h') AS time,
    COUNT(*) AS value
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY time
ORDER BY time
```

### Daily Message Volume (Bar Chart)
```sql
SELECT 
    DATE(received_timestamp) AS date,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY date
ORDER BY date
```

### Weekly Comparison (Time Series - Current vs Previous Week)
```sql
-- Current week
SELECT 
    $__timeGroup(received_timestamp, '1d') AS time,
    COUNT(*) AS current_week
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY time
ORDER BY time
```

### Peak Hours Analysis (Bar Chart)
```sql
SELECT 
    EXTRACT(HOUR FROM received_timestamp) AS hour,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY hour
ORDER BY hour
```

### Peak Days Analysis (Bar Chart)
```sql
SELECT 
    TO_CHAR(received_timestamp, 'Day') AS day_name,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY day_name, EXTRACT(ISODOW FROM received_timestamp)
ORDER BY EXTRACT(ISODOW FROM received_timestamp)
```

---

## Rate Limiting & Quota Monitoring

### Client Daily Usage vs Limits (Table)
```sql
SELECT 
    c.name AS client_name,
    cs.sms_daily_limit,
    COUNT(CASE WHEN m.type = 'sms' AND DATE(m.received_timestamp) = CURRENT_DATE THEN 1 END) AS sms_today,
    cs.mms_daily_limit,
    COUNT(CASE WHEN m.type = 'mms' AND DATE(m.received_timestamp) = CURRENT_DATE THEN 1 END) AS mms_today,
    ROUND(
        COUNT(CASE WHEN m.type = 'sms' AND DATE(m.received_timestamp) = CURRENT_DATE THEN 1 END)::float 
        / NULLIF(cs.sms_daily_limit, 0) * 100, 
        1
    ) AS sms_percent_used
FROM clients c
LEFT JOIN client_settings cs ON c.id = cs.client_id
LEFT JOIN msg_record_db_items m ON c.id = m.client_id
GROUP BY c.id, c.name, cs.sms_daily_limit, cs.mms_daily_limit
HAVING cs.sms_daily_limit > 0 OR cs.mms_daily_limit > 0
ORDER BY sms_percent_used DESC NULLS LAST
```

### Clients Near Daily Limit (Alert Query - >80% Used)
```sql
SELECT 
    c.name AS client_name,
    cs.sms_daily_limit AS limit,
    COUNT(*) AS used,
    ROUND(COUNT(*)::float / cs.sms_daily_limit * 100, 1) AS percent_used
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
JOIN client_settings cs ON c.id = cs.client_id
WHERE 
    m.type = 'sms'
    AND DATE(m.received_timestamp) = CURRENT_DATE
    AND cs.sms_daily_limit > 0
GROUP BY c.id, c.name, cs.sms_daily_limit
HAVING COUNT(*)::float / cs.sms_daily_limit > 0.8
ORDER BY percent_used DESC
```

### Monthly Usage Trend by Client (Time Series)
```sql
SELECT 
    DATE_TRUNC('month', received_timestamp) AS month,
    c.name AS metric,
    COUNT(*) AS value
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE 
    m.received_timestamp > NOW() - INTERVAL '6 month'
    AND c.id IN ($client_ids)
GROUP BY month, c.name
ORDER BY month
```

### Burst Activity Detection (Time Series - Per Minute Rate)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1m') AS time,
    c.name AS metric,
    COUNT(*) AS value
FROM msg_record_db_items m
JOIN clients c ON m.client_id = c.id
WHERE 
    m.received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
    AND c.id IN ($client_ids)
GROUP BY time, c.name
ORDER BY time
```

---

## Server & Infrastructure

### Messages by Server Instance (Pie Chart)
```sql
SELECT 
    server_id,
    COUNT(*) AS count
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY server_id
ORDER BY count DESC
```

### Server Load Over Time (Time Series)
```sql
SELECT 
    $__timeGroup(received_timestamp, '1h') AS time,
    server_id AS metric,
    COUNT(*) AS value
FROM msg_record_db_items
WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
GROUP BY time, server_id
ORDER BY time
```

### Processing Latency Distribution (Requires Additional Data)
```sql
-- Note: This requires a separate processing completion timestamp
-- If available, use this pattern:
-- SELECT 
--     $__timeGroup(received_timestamp, '1h') AS time,
--     AVG(EXTRACT(EPOCH FROM (processed_timestamp - received_timestamp))) AS avg_latency_sec
-- FROM msg_record_db_items
-- WHERE received_timestamp BETWEEN $__timeFrom() AND $__timeTo()
-- GROUP BY time
-- ORDER BY time
SELECT 'Requires processed_timestamp field' AS note
```

---

## Dashboard Variables

Add these as dashboard variables for interactivity:

### Client Selector
```sql
-- Variable: $client_ids (Multi-select)
SELECT id AS __value, name AS __text
FROM clients
ORDER BY name
```

### Carrier Selector
```sql
-- Variable: $carrier (Single/Multi-select)
SELECT DISTINCT carrier AS __value, carrier AS __text
FROM msg_record_db_items
WHERE carrier IS NOT NULL AND carrier != ''
ORDER BY carrier
```

### Message Type Selector
```sql
-- Variable: $msg_type (Single select)
SELECT 'sms' AS __value, 'SMS' AS __text
UNION ALL
SELECT 'mms', 'MMS'
UNION ALL
SELECT 'all', 'All Types'
```

### Direction Selector
```sql
-- Variable: $direction (Single select)  
SELECT 'inbound' AS __value, 'Inbound' AS __text
UNION ALL
SELECT 'outbound', 'Outbound'
UNION ALL
SELECT 'all', 'All Directions'
```

---

## Sample Dashboard Layout

### Row 1: Overview Stats
| Panel | Type | Query |
|-------|------|-------|
| Total Messages | Stat | [Total Messages](#total-messages-stat-panel) |
| Active Clients | Stat | [Active Clients](#active-clients-count-stat-panel) |
| SMS/MMS Ratio | Pie | [SMS vs MMS](#total-sms-vs-mms-pie-chart) |
| Inbound/Outbound | Pie | [Direction Distribution](#inbound-vs-outbound-volume-time-series) |

### Row 2: Traffic Trends
| Panel | Type | Query |
|-------|------|-------|
| Hourly Volume | Time Series | [Hourly Rate](#hourly-message-rate-time-series) |
| By Type | Time Series (Stacked) | [SMS vs MMS Over Time](#sms-vs-mms-ratio-over-time-time-series---stacked) |

### Row 3: Client Analytics
| Panel | Type | Query |
|-------|------|-------|
| Top Clients | Bar (Horizontal) | [Top 10 Clients](#top-10-clients-by-message-volume-bar-chart---horizontal) |
| Client Usage Table | Table | [Client Usage by Direction](#client-usage-by-direction-table) |

### Row 4: MMS/SMS Details
| Panel | Type | Query |
|-------|------|-------|
| Transcoding Efficiency | Gauge | [Transcoding Efficiency](#transcoding-efficiency-gauge---percentage-saved) |
| Encoding Distribution | Pie | [Encoding Distribution](#encoding-distribution-pie-chart) |
| Segment Distribution | Bar | [Segment Distribution](#segment-distribution-histogrambar-chart) |

### Row 5: Carrier & Infrastructure
| Panel | Type | Query |
|-------|------|-------|
| Carrier Distribution | Pie | [Messages by Carrier](#messages-by-carrier-pie-chart) |
| Server Load | Time Series | [Server Load](#server-load-over-time-time-series) |

---

## Notes

1. **Time Range**: All queries use Grafana's `$__timeFrom()` and `$__timeTo()` macros for time range filtering.

2. **PostgreSQL Syntax**: These queries use PostgreSQL syntax. For MySQL:
   - Replace `::float` with `/1.0` for float division
   - Replace `INTERVAL '1 day'` with `INTERVAL 1 DAY`
   - Replace `$__timeGroup()` with appropriate MySQL time bucketing

3. **Performance**: For high-volume tables, consider:
   - Adding indexes on `client_id`, `received_timestamp`, `type`, `direction`
   - Using materialized views for aggregations
   - Partitioning by `received_timestamp`

4. **Alerting**: Queries marked as "Alert Query" can be used with Grafana alerting to notify on thresholds.
