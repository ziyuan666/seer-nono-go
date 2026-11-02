@echo off
cd /d %~dp0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags "-s -w -H windowsgui" -o nono_deskpet.exe .
if errorlevel 1 (
  echo build failed
  pause
  exit /b 1
)
echo built: nono_deskpet.exe
echo 发布时把 nono_deskpet.exe 和 assets 目录一起拷走
pause
