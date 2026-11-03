@echo off
rem Builds Counterbuild.exe (requires Go 1.24+).
cd /d "%~dp0"
set CGO_ENABLED=0
go build -trimpath -ldflags "-H=windowsgui -s -w" -o Counterbuild.exe .
if errorlevel 1 exit /b 1
echo Built Counterbuild.exe
