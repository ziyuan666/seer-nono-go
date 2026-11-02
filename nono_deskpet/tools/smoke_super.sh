#!/bin/sh
# 逐级切换 superLevel 冒烟：验证 5 个超能形态 + 普通形态都能加载、无 panic。
# 等级→形态映射与服务端一致：1-3→super1、4-6→super2、7-8→super3、9-11→super4、12→super5。
set -u
cd nono_deskpet
for lv in 0 1 4 7 9 12; do
  python3 nono_deskpet/tools/set_super.py "$lv" > /dev/null
  rm -f nono_debug.log
  DISPLAY=:98 NONO_LOG=1 timeout 12 wine nono_deskpet.exe > /tmp/smoke_lv.log 2>&1
  echo "--- lv=$lv"
  grep -E "form loaded|resize w" nono_debug.log | head -2
  echo -n "panic="; grep -c panic /tmp/smoke_lv.log
done
python3 nono_deskpet/tools/set_super.py 0 > /dev/null
echo DONE
