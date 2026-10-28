# Installs or updates the invite service (one-time support links) on the server that runs
# rustdesk-api and Caddy. Run in an elevated PowerShell from the folder containing
# invite-server.exe and invite-helper.exe (the "invite-windows" build artifact).
#
#   .\install-invite-server.ps1 -Portable C:\Temp\ManashaktiSupport-1.5.1-x86_64.exe -Msi C:\Temp\ManashaktiSupport-1.5.1-x86_64.msi
#
# Re-run with new -Portable/-Msi files after each client build; the service keeps its invites.
param(
    [Parameter(Mandatory)] [string]$Portable,
    [Parameter(Mandatory)] [string]$Msi,
    [string]$Source = $PSScriptRoot,
    [string]$Dir = "C:\RustDesk-Invite",
    [string]$CaddyDir = "C:\Caddy",
    [string]$ServerDir = "C:\RustDesk-Server",
    [string]$Domain = "support.manashakti.org",
    [int]$HttpsPort = 8443
)
$ErrorActionPreference = "Stop"
$nssm = Join-Path $ServerDir "nssm.exe"
$caddy = Join-Path $CaddyDir "caddy.exe"
$caddyfile = Join-Path $CaddyDir "Caddyfile"

foreach ($f in (Join-Path $Source "invite-server.exe"), (Join-Path $Source "invite-helper.exe"), $Portable, $Msi, $nssm, $caddy, $caddyfile) {
    if (-not (Test-Path $f)) { throw "Missing $f" }
}

# --- files ------------------------------------------------------------------------------
$svc = Get-Service invite-server -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -eq "Running") { Stop-Service invite-server }
foreach ($d in $Dir, "$Dir\files", "$Dir\data", "$Dir\logs") { New-Item -ItemType Directory -Force $d | Out-Null }
icacls $Dir /inheritance:r /grant:r "SYSTEM:(OI)(CI)F" "Administrators:(OI)(CI)F" | Out-Null
Copy-Item (Join-Path $Source "invite-server.exe") $Dir -Force
Copy-Item (Join-Path $Source "invite-helper.exe") "$Dir\files\invite-helper.exe" -Force
Copy-Item $Portable "$Dir\files\ManashaktiSupport.exe" -Force
Copy-Item $Msi "$Dir\files\ManashaktiSupport.msi" -Force
Get-ChildItem "$Dir\files" | Get-FileHash -Algorithm SHA256 | Format-Table @{n='File'; e={Split-Path $_.Path -Leaf}}, Hash -AutoSize

# --- service ----------------------------------------------------------------------------
if (-not $svc) {
    & $nssm install invite-server (Join-Path $Dir "invite-server.exe") -dir $Dir -public-url "https://${Domain}:$HttpsPort" | Out-Null
    & $nssm set invite-server AppDirectory $Dir | Out-Null
    & $nssm set invite-server DisplayName "Manashakti Support invites" | Out-Null
    & $nssm set invite-server Start SERVICE_AUTO_START | Out-Null
    & $nssm set invite-server DependOnService rustdesk-api | Out-Null
    & $nssm set invite-server AppStdout "$Dir\logs\invite-server.log" | Out-Null
    & $nssm set invite-server AppStderr "$Dir\logs\invite-server.log" | Out-Null
    & $nssm set invite-server AppRotateFiles 1 | Out-Null
    & $nssm set invite-server AppRotateBytes 10485760 | Out-Null
}
Start-Service invite-server

# --- Caddy routes -----------------------------------------------------------------------
$marker = "# Invite service"
$conf = Get-Content $caddyfile -Raw
if ($conf -notmatch [regex]::Escape($marker)) {
    $anchor = "	# Web admin and its API: office network only."
    if ($conf -notmatch [regex]::Escape($anchor)) { throw "Caddyfile layout not recognised; add the invite routes by hand (see docs/invites.md)." }
    $routes = @"
	$marker
	@invites path /invites /invites/*
	handle @invites {
		@outside not remote_ip private_ranges
		respond @outside "Forbidden" 403
		reverse_proxy 127.0.0.1:21120
	}
	handle /i/* {
		reverse_proxy 127.0.0.1:21120
	}
	handle /api/invite/* {
		reverse_proxy 127.0.0.1:21120
	}

"@
    Copy-Item $caddyfile "$caddyfile.bak-$(Get-Date -Format yyyyMMdd-HHmm)"
    $conf.Replace($anchor, $routes + $anchor) | Set-Content -Encoding UTF8 $caddyfile
    # Caddy logs to stderr; under "Stop", Windows PowerShell would turn that into a terminating error.
    $ErrorActionPreference = "Continue"
    $out = & $caddy validate --config $caddyfile --adapter caddyfile 2>&1 | Out-String
    $code = $LASTEXITCODE
    $ErrorActionPreference = "Stop"
    ($out.Trim() -split "`n")[-1]
    if ($code -ne 0) { throw "Caddyfile validation failed; the previous version is in $caddyfile.bak-*" }
    Restart-Service caddy
}

Start-Sleep 3
Get-Service rustdesk-api, caddy, invite-server | Format-Table Name, Status, StartType
try {
    $r = Invoke-WebRequest "https://${Domain}:$HttpsPort/invites" -UseBasicParsing -TimeoutSec 15
    Write-Host "Invite page OK ($($r.StatusCode)): https://${Domain}:$HttpsPort/invites (office network only)" -ForegroundColor Green
} catch {
    Write-Host "Invite page not answering: $($_.Exception.Message)" -ForegroundColor Yellow
    Get-Content "$Dir\logs\invite-server.log" -Tail 10 -ErrorAction SilentlyContinue
}
