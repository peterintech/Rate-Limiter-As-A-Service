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
