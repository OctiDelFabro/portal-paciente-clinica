$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath (Join-Path $PSScriptRoot '..')
if (-not $env:MOCK_API_KEYS) { throw 'Defina MOCK_API_KEYS antes de iniciar el mock.' }
go run ./services/notifications/mock
if ($LASTEXITCODE -ne 0) { throw 'El mock finalizó con error.' }

