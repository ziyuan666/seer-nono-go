#!/bin/sh
# 交叉编译 Windows exe
set -e
cd "$(dirname "$0")"
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H windowsgui" -o nono_deskpet.exe .
echo "built: $(ls -lh nono_deskpet.exe | awk '{print $5}')"
echo "发布时把 nono_deskpet.exe 和 assets/ 一起拷走"
