@echo off
setlocal

:: Enable CGO (needed for WebView2 sidebar)
set CGO_ENABLED=1

:: Set GCC path (MSYS2 MinGW64)
set "PATH=C:\msys64\mingw64\bin;%PATH%"

:: Build
go build -ldflags="-s -w" -o ocgt-monitor.exe .

echo Build complete: ocgt-monitor.exe
