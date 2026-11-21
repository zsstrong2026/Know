-- Migration: 000073_sop_cli_trigger (down)
DROP TABLE IF EXISTS sop_dispatch_reservations;
DROP TABLE IF EXISTS sop_trigger_logs;
DROP TABLE IF EXISTS sop_trigger_configs;
DROP TABLE IF EXISTS sop_cli_credentials;
