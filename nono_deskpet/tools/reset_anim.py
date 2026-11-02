"""临时调试脚本：把存档里的每形态动画速度重置为 0（=用出厂默认值）。

只用于本轮验证「点一次按钮改多少 ms」，不参与打包。
"""
import json
import os

P = "/tmp/nono-wine/drive_c/users/root/AppData/Roaming/NonoDeskpet/save.json"


def main():
    with open(P, encoding="utf-8") as f:
        d = json.load(f)
    d["idleMs"] = [0] * 6
    d["moveMs"] = [0] * 6
    d["superLevel"] = 0
    with open(P, "w", encoding="utf-8") as f:
        json.dump(d, f, indent=2)
    print("reset ok idleMs=%s moveMs=%s superLevel=%s"
          % (d["idleMs"], d["moveMs"], d["superLevel"]))


if __name__ == "__main__":
    main()
