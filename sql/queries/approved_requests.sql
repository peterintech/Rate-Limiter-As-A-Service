-- name: CreateApprovedRequest :exec
INSERT INTO approved_requests (
    stream_id,
    client_id,
    resource,
    cost,
    approved_at,
    remaining
) VALUES (
    $1, $2, $3, $4, $5, $6
)
ON CONFLICT (stream_id) DO NOTHING;

-- name: GetApprovalSummary :one
SELECT
    COUNT(*)::BIGINT AS total_approvals,
    COALESCE(SUM(cost), 0)::BIGINT AS total_cost,
    COALESCE(EXTRACT(EPOCH FROM MIN(approved_at)) * 1000, 0)::BIGINT AS first_approval_at_ms,
    COALESCE(EXTRACT(EPOCH FROM MAX(approved_at)) * 1000, 0)::BIGINT AS latest_approval_at_ms
FROM approved_requests
WHERE approved_at >= NOW() - (sqlc.arg(days)::INTEGER * INTERVAL '1 day')
  AND (sqlc.arg(client_id)::TEXT = '' OR client_id = sqlc.arg(client_id))
  AND (sqlc.arg(resource)::TEXT = '' OR resource = sqlc.arg(resource));

-- name: ListApprovalSummaryByPolicy :many
SELECT
    client_id,
    resource,
    COUNT(*)::BIGINT AS total_approvals,
    COALESCE(SUM(cost), 0)::BIGINT AS total_cost
FROM approved_requests
WHERE approved_at >= NOW() - (sqlc.arg(days)::INTEGER * INTERVAL '1 day')
  AND (sqlc.arg(client_id)::TEXT = '' OR client_id = sqlc.arg(client_id))
  AND (sqlc.arg(resource)::TEXT = '' OR resource = sqlc.arg(resource))
GROUP BY client_id, resource
ORDER BY client_id, resource;

-- name: ListDailyApprovalTrends :many
SELECT
    (approved_at AT TIME ZONE 'UTC')::DATE AS day,
    client_id,
    resource,
    COUNT(*)::BIGINT AS total_approvals,
    COALESCE(SUM(cost), 0)::BIGINT AS total_cost
FROM approved_requests
WHERE approved_at >= NOW() - (sqlc.arg(days)::INTEGER * INTERVAL '1 day')
  AND (sqlc.arg(client_id)::TEXT = '' OR client_id = sqlc.arg(client_id))
  AND (sqlc.arg(resource)::TEXT = '' OR resource = sqlc.arg(resource))
GROUP BY (approved_at AT TIME ZONE 'UTC')::DATE, client_id, resource
ORDER BY day, client_id, resource;
