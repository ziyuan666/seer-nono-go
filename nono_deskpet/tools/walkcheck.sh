#!/bin/bash
# walkcheck.sh —— 自动走动朝向核对：开 autoWalk，跑 60 秒，抓 dir 变化与撞边反弹。
#   WINEPREFIX=/tmp/nono-wine DISPLAY=:1 NONO_LOG=1 bash tools/walkcheck.sh
set -u
cd "$(dirname "$0")/.." || exit 1
SAVE=/tmp/nono-wine/drive_c/users/root/AppData/Roaming/NonoDeskpet/save.json
sed -i 's/"autoWalk": false/"autoWalk": true/' "$SAVE"
sed -i 's/"follow": true/"follow": false/' "$SAVE"
rm -f nono_debug.log
timeout 65 wine nono_deskpet.exe > /tmp/walk.log 2>&1
echo "=== 撞边反弹 ==="
grep "walk bounce" nono_debug.log | head -10
echo "=== dir 变化序列 ==="
grep -o "idle dir=[a-z]* base=[0-9]*,[0-9]*" nono_debug.log | head -20
echo "=== panic ==="
grep -c panic /tmp/walk.log
