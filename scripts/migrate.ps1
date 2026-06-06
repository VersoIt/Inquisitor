param(
    [string]$Config = "configs/config.example.yaml",
    [string]$Migrations = "migrations"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

go run ./cmd/migrate -config $Config -migrations $Migrations
