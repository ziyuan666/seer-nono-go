# NONO 桌宠（seer-nono-go）

赛尔号 NONO 机器人的 Windows 桌面宠物复刻。Go + Win32 原生实现，无 cgo、无第三方运行时依赖，编译产物为单个 exe。

## 功能

- 桌宠本体：待机动画、自由走动、拖拽、跟随鼠标（8 方向）
- 形态系统：普通形态 / 超能 1~12 级 / 至尊形态
- 飞行模式：4 种载具风格，程序化尾焰与方向帧
- 芯片系统：变色 / 能量 / 智慧 / 玩具 / 功能五类芯片
- 面板：主面板、基本资料、储藏空间、功能芯片环、芯片制造机
- 其他：托盘菜单、开机自启、JSON 存档、点击穿透、超能残影、快捷栏

## 构建

Windows（或 Linux 交叉编译 `GOOS=windows`，Go 1.22+）：

```sh
cd nono_deskpet
./build.sh        # 产出 nono_deskpet.exe，与 assets/ 同目录发布
```

## 目录结构

| 目录 | 说明 |
|---|---|
| `nono_deskpet/` | 主程序（Go + Win32 分层窗口） |
| `nono_deskpet_vec/` | 矢量渲染库（SWF → 光栅化；经 go.mod `replace` 引用，须与主程序保持同级目录） |
| `tools/`、`nono_deskpet/tools/` | 素材管线与辅助脚本（Python） |
| `swf_tools/` | SWF 查看器等辅助工具 |
| `assets_src/` | NONO 原始 SWF 素材（素材管线的源文件） |

## 素材管线（可选）

仓库内 `nono_deskpet/assets/` 已含全部运行时素材，直接构建即可。如需从 SWF 重新导出：

1. 下载 [FFDec（JPEXS Free Flash Decompiler）](https://github.com/jindrapetrik/jpexs-decompiler/releases)，解压到 `toolchain/ffdec/`；
2. 运行 `nono_deskpet/tools/` 下对应脚本（原始 SWF 素材已随仓库提供，见 `assets_src/`）。

## 说明

- NONO 及相关素材版权归赛尔号官方所有，本项目仅供学习研究。
- 存档为 exe 同目录 JSON 文件；调试开关总表见 `nono_deskpet/docs/ENV.md`。
