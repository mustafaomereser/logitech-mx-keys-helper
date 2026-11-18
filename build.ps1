# Windows'ta derleme: powershell -ExecutionPolicy Bypass -File build.ps1 [-Version v0.2.0]
param([string]$Version = "dev")
$env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
New-Item -ItemType Directory -Force dist | Out-Null
go vet ./...
if ($LASTEXITCODE -ne 0) { exit 1 }
go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$Version" -o dist/MXHelper.exe .
if ($LASTEXITCODE -eq 0) { Write-Host "dist/MXHelper.exe hazır" }
