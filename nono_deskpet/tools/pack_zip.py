#!/usr/bin/env python3
"""打包桌宠发布 zip（只放「解压即用」的东西：主程序 + 运行时素材）。

为什么要单独成脚本：内联 heredoc 打包命令跑过 25s 会被代码改动闸门按
「无法静态判定写入目标」拒绝；固定脚本 + 字面输出路径可以稳定通过。
用法：python3 tools/pack_zip.py <输出zip路径>

20261029-02 改成白名单：以前是黑名单排除（历史包/备份/日志），结果每新增一类
文件都会悄悄漏进发布包 —— 20261029-01 那个包带了 48 个 .go、52 个 .py、
37 个 docs、12 张 out 调试截图，还有一份开发机的调试存档 save.json
（superLevel=3 / fly=true / scale=1.5，解压即覆盖用户设置）。
现在只认 ALLOW_ROOT_FILES + ALLOW_DIRS，其余一律不进包，并打印排除摘要。
"""
import os
import sys
import zipfile

SRC = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SKIP_DIRS = {".git", "__pycache__", "dist"}
SKIP_SUFFIX = (".zip", ".log", ".tmp", ".prof", ".raw.png")
SKIP_NAMES = {"canvas_debug.png"}
# dump_*.png 是飞行器分层排查时导出的中间图，运行时不用。
SKIP_PREFIX_NAME = ("dump_",)
# 根目录曾经混进过 nono_t21.exe、nono_smoke.exe 这类临时构建产物（2026-10-27 实测
# 多带 5MB 进包）。除了主程序，其余 nono_*.exe 一律不进包。
KEEP_EXE = "nono_deskpet.exe"

# 历史产物，包内白占 100MB+）。只跳这些目录下的 .png，assets/sprites（矢量
# sprite.json + parts）、assets/ui、assets/sfx 一律保留——它们是运行时真源。
# 源文件不删除，本地留档在 变更工程/当前改动/20261026-07/旧PNG留档/。
SKIP_PNG_PREFIX = (
    "assets/plain/",
    "assets/super1/",
    "assets/super2/",
    "assets/super3/",
    "assets/super4/",
    "assets/super5/",
    "assets/fly/",
)

# ---- 白名单（20261029-02）----
# 发布包里只允许这两类东西：根目录的主程序、assets/ 子树。
# 源码、tools/、docs/、out/、internal/、cmd/、README 一律不进包：
# 运行时一个都不读，只会让收包的人以为发的是整个工程。
ALLOW_ROOT_FILES = {"nono_deskpet.exe"}
ALLOW_DIRS = ("assets/",)

# 共存布局（20261030-11）：zip 内把 assets/ 改名 assets_nono/，与赛尔发布包
# 解压到同一文件夹时不互相覆盖。源码树里仍叫 assets/，程序侧按 exe 名前缀
# 自动选目录（见 tagpath.go / assets.go 的 assetsRoot）。
TAG = "nono"


def tagged(rel):
    """把源码相对路径映射成包内路径。"""
    if rel.startswith("assets/"):
        return "assets_" + TAG + rel[len("assets"):]
    return rel

# 存档永不带（哪怕将来放宽白名单）：包里带 save.json，解压到空目录就等于把
# 打包机上的调试设置当成用户第一次运行的状态读进去。
ALWAYS_SKIP_NAMES = {"save.json"}

# 代码/文档类扩展名一律不带，不管它们落在哪 —— assets/ui/chips/ 底下历史上就
# 躺着 gen_custom_mask.py 这种"放在素材目录里的生成脚本"。
FORBIDDEN_SUFFIX = (".py", ".go", ".md", ".sh", ".bat", ".mod", ".sum", ".txt~")


def allowed(rel: str) -> bool:
    relp = rel.replace(os.sep, "/")
    name = relp.rsplit("/", 1)[-1]
    if name in ALWAYS_SKIP_NAMES:
        return False
    if name.lower().endswith(FORBIDDEN_SUFFIX):
        return False
    if "/" not in relp:
        return relp in ALLOW_ROOT_FILES
    return any(relp.startswith(p) for p in ALLOW_DIRS)


def should_skip(name: str) -> bool:
    # 备份文件（*.before_xxx）与历史发布包一律不进发布包
    if name.endswith(".exe") and name != KEEP_EXE:
        return True
    if name in SKIP_NAMES or name.startswith(SKIP_PREFIX_NAME):
        return True
    return name.endswith(SKIP_SUFFIX) or ".before_" in name


def skip_rel(rel: str) -> bool:
    if not rel.endswith(".png"):
        return False
    relp = rel.replace(os.sep, "/")
    # assets/fly/ 下的一级 PNG 是 1x 旧版飞行器整帧回退素材（矢量为主，包内剔除）；
    # 但 assets/fly/flame/ 是**运行时必读**的官方尾焰帧（2026-10-28 起尾焰来源），
    # 无条件跳过会让发布包缺素材、火焰整个消失。
    if relp.startswith("assets/fly/flame/"):
        return False
    return any(relp.startswith(p) for p in SKIP_PNG_PREFIX)


def main() -> int:
    out = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(SRC), "nono_deskpet_release.zip")
    if os.path.exists(out):
        os.remove(out)
    n = raw = 0
    skipped = {}  # 顶层名 -> [条目数, 原始字节]，只用于结尾打印
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as z:
        for root, dirs, files in os.walk(SRC):
            dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
            for f in files:
                p = os.path.join(root, f)
                rel = os.path.relpath(p, SRC)
                if should_skip(f) or not allowed(rel) or skip_rel(rel):
                    top = rel.replace(os.sep, "/").split("/")[0]
                    st = skipped.setdefault(top, [0, 0])
                    st[0] += 1
                    st[1] += os.path.getsize(p)
                    continue
                raw += os.path.getsize(p)
                z.write(p, tagged(rel))
                n += 1
    size = os.path.getsize(out)
    print("entries %d raw %.1fMB zip %.1fMB" % (n, raw / 1e6, size / 1e6))
    print("--- 未进包（按顶层目录，最大的在前）---")
    for k, (c, b) in sorted(skipped.items(), key=lambda x: -x[1][1]):
        print("  %-14s %5d files %8.2f MB" % (k, c, b / 1e6))
    with zipfile.ZipFile(out) as z:
        print("testzip", "OK" if z.testzip() is None else "BAD")
        exe = "nono_deskpet.exe"
        if exe in z.namelist():
            print("exe in zip", z.getinfo(exe).file_size, "disk", os.path.getsize(os.path.join(SRC, exe)))
        else:
            print("exe MISSING")
        # 白名单自检：这三类只要出现一个就说明规则被绕过了
        bad = [nm for nm in z.namelist() if nm.endswith((".go", ".py", ".md", ".mod", ".sum")) or nm == "save.json"]
        print("forbidden entries", len(bad), bad[:5])
        # 共存前缀自检：不该再有裸 assets/ 条目
        bare = [nm for nm in z.namelist() if nm.startswith("assets/")]
        print("bare assets/ entries", len(bare), bare[:3])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
