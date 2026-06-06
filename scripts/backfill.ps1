param(
    [string]$Config = "configs/config.example.yaml",
    [string]$Symbols = "BTCUSDT,ETHUSDT",
    [string]$Intervals = "1",
    [string]$Start = "",
    [string]$End = "",
    [int]$Limit = 1000
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$argsList = @(
    "run", "./cmd/backfill",
    "-config", $Config,
    "-symbols", $Symbols,
    "-intervals", $Intervals,
    "-limit", $Limit
)

if ($Start -ne "") {
    $argsList += @("-start", $Start)
}
if ($End -ne "") {
    $argsList += @("-end", $End)
}

go @argsList
