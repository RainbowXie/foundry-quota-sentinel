@echo off
setlocal
set CGO_ENABLED=0
go build -ldflags="-s -w" -o ocgt-monitor.exe .
echo Build complete: ocgt-monitor.exe
