#!/bin/bash
# smoke6.sh —— 6 形态冒烟：逐个设超能等级启动 20 秒，记录 panic / 方向 / 窗口尺寸。
# 用法（必须显式带 Wine 前缀，否则读到 ~/.wine 旧存档）：
#   WINEPREFIX=/tmp/nono-wine DISPLAY=:1 NONO_LOG=1 bash tools/smoke6.sh
set -u
cd "$(dirname "$0")/.." || exit 1
python3 tools/reset_anim.py >/dev/null 2>&1
out=/tmp/smoke6
mkdir -p "$out"
for lv in 0 1 4 7 9 12; do
  python3 tools/set_super.py "$lv" >/dev/null 2>&1
  rm -f nono_debug.log
  timeout 20 wine nono_deskpet.exe > "$out/lv$lv.log" 2>&1
  panic=$(grep -c panic "$out/lv$lv.log")
  dir=$(grep -o 'idle dir=[a-z]*' nono_debug.log | tail -1)
  win=$(grep -o 'win=[0-9]*x[0-9]*' nono_debug.log | tail -1)
  form=$(grep -o 'form=[a-z0-9]*' nono_debug.log | tail -1)
  echo "lv=$lv panic=$panic $form $dir $win"
done
