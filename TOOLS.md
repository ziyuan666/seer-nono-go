# 工具链说明

## 运行时

- Go 1.22+（交叉编译 `GOOS=windows`；脚本默认从仓库根目录运行）
- Python 3（素材管线脚本）

## 素材管线

- [FFDec（JPEXS Free Flash Decompiler）](https://github.com/jindrapetrik/jpexs-decompiler)：SWF 导出与反编译。下载后解压至 `toolchain/ffdec/`，脚本默认引用 `toolchain/ffdec/ffdec-cli.jar`。
- `nono_deskpet/tools/`：帧烘焙、图标生成、发布打包（`pack_zip.py`）等脚本。脚本内引用的 SWF 源文件不在本仓库内，路径按需修改。
- `swf_tools/`：SWF 查看器与播放器生成脚本。
- `tools/`：气泡预览脚本。

## 发布

- `nono_deskpet/tools/pack_zip.py`：按发布清单打包（排除 dist/、日志、存档）。
