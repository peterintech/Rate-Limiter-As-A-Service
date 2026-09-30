\set ON_ERROR_STOP on

SELECT format('CREATE ROLE grafana_reader LOGIN PASSWORD %L', :'reader_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'grafana_reader')
\gexec

SELECT format('ALTER ROLE grafana_reader PASSWORD %L', :'reader_password')
\gexec

GRANT CONNECT ON DATABASE rate_limiter TO grafana_reader;
GRANT USAGE ON SCHEMA public TO grafana_reader;
GRANT SELECT ON TABLE approved_requests TO grafana_reader;
ALTER DEFAULT PRIVILEGES FOR ROLE rate_limiter IN SCHEMA public
    GRANT SELECT ON TABLES TO grafana_reader;
