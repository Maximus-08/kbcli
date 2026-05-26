# Copy workspace files to local C: drive scratch to bypass Samba locks
$ScratchDir = "C:\Users\avnis\.gemini\antigravity\scratch\kb-system"
New-Item -ItemType Directory -Force -Path $ScratchDir

Write-Host "Syncing files to local scratch folder..." -ForegroundColor Cyan
Copy-Item -Recurse -Force -Path .\* -Destination $ScratchDir -Exclude "bin", ".git", ".idea", "build.ps1"

# Compile inside scratch
Write-Host "Building Go binary locally on C: drive..." -ForegroundColor Cyan
Push-Location $ScratchDir
go build -o kb.exe .\cmd\kb\
if ($LASTEXITCODE -ne 0) {
    Write-Error "Go build failed!"
    Pop-Location
    exit $LASTEXITCODE
}
Pop-Location

# Copy back the binary and the updated go.mod/go.sum
Write-Host "Syncing binaries and mod files back..." -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path .\bin
Copy-Item -Force -Path "$ScratchDir\kb.exe" -Destination .\bin\kb.exe
Copy-Item -Force -Path "$ScratchDir\go.mod" -Destination .\go.mod
if (Test-Path "$ScratchDir\go.sum") {
    Copy-Item -Force -Path "$ScratchDir\go.sum" -Destination .\go.sum
}

Write-Host "Success! Binary is at .\bin\kb.exe" -ForegroundColor Green
