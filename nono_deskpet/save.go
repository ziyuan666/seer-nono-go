//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Save struct {
	Nick string `json:"nick"`
	// Owner / Birth：基本资料面板「主人」「生日」两行（原版 NonoInfo 的玩家名与
	// 赛尔历生日）。桌宠没有账号体系，缺省给通用值，用户可直接改 save.json。
	Owner      string `json:"owner"`
	Birth      string `json:"birth"`
	Level      int    `json:"level"`
	Power      int    `json:"power"`
	Mate       int    `json:"mate"`
	AI         int    `json:"ai"`
	Exp        int    `json:"exp"`
	Color      uint32 `json:"color"`
	ColorChip  int    `json:"colorChip"`
	// 本地新增的两枚变色芯片（原版 items.xml 没有，见 chips.json 700313/700314）：
	//   700313 自定义颜色 → ColorCustom
	//   700314 自定义渐变 → GradA（上/左）→ GradB（下/右），GradMode 0 竖直 1 水平 2 对角
	// 老存档读出来是 0，由 defaultColorExtras 兜底，不会变成黑色。
	ColorCustom uint32 `json:"colorCustom"`
	GradA       uint32 `json:"gradA"`
	GradB       uint32 `json:"gradB"`
	GradMode    int    `json:"gradMode"`
	// ExtraRev 是上面四个字段的初始化版本号，见 loadSave。
	ExtraRev int `json:"extraRev"`
	Open       bool   `json:"open"`
	Super      bool   `json:"super"`      // 超能形态（对应 actorInfo.superNono），由 SuperLevel>0 派生
	SuperLevel int    `json:"superLevel"` // 超能等级 1-13，0=非超能；13=至尊 N(annual)，形态由 superLevelToForm 换算
	// SuperEnergy 超能值：原版 NonoInfo.superEnergy，资料面板 mc0 显示
	// superEnergy / _powerA[min(superLevel,10)]（NewNonoInfoPanel.as:104/317）。
	// 桌宠没有服务器按天累加，初值由 load 迁移给（等级上限的一半）。
	SuperEnergy int         `json:"superEnergy"`
	Chips       []int       `json:"chips"`
	Counts      map[int]int `json:"counts"` // 储藏格内每颗芯片的剩余数量，缺省视为 1
	// Materials 材料道具库存：道具 ID(4000xx) -> 数量。合成芯片用官方配方消耗，
	// 数据来源 /app/working/workspaces/a2ZDuB/kaiyuanhao/data/items.xml，见 docs/官方芯片配方_20261023.md。
	Materials map[int]int `json:"materials"`
	Follow    bool        `json:"follow"`
	Fly       bool        `json:"fly"`
	FlyStyle  int         `json:"flyStyle"` // 飞行载具风格 1-4（原版 nonoFlyModel）
	Shield    bool        `json:"shield"`
	Glow      bool        `json:"glow"`
	ExpBonus  bool        `json:"expBonus"`

	Scale float64 `json:"scale"`
	// RenderRev 记录「渲染口径版本」，用于一次性迁移 Scale。
	// 缺失（0）= 这存档来自 1x 时代。
	RenderRev int  `json:"renderRev"`
	Topmost   bool `json:"topmost"`
	AutoWalk  bool `json:"autoWalk"`
	IdleSpeed int  `json:"idleSpeed"` // 待机姿态是否播放帧动画：0 静止(默认，靠整体上下浮动给悬浮感) 4 慢 3 中 2 快
	// IdleMs / MoveMs：每个形态的待机 / 移动「每帧时长（毫秒）」，越小越快。
	// 下标 0=plain、1..5 对应 super1..super5、6=annual（至尊）；0 = 未设置，回落到默认 100ms。
	//
	// 2026-10-26 子渊：各形态待机帧数不同（plain 17、super1 13、super5 20），
	// 同一帧时长下循环周期从 1.3 秒到 2.1 秒不等，观感「有的快有的慢」。
	// 与其替他猜一个统一值，不如把数值开放到设置面板自己调。
	IdleMs  [7]int `json:"idleMs"`
	MoveMs  [7]int `json:"moveMs"`
	Bubbles bool   `json:"bubbles"`
	Sound   bool   `json:"sound"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
}

func defaultSave() Save {
	return Save{
		Nick: "NONO", Level: 0, Power: 60, Mate: 40, AI: 3, Exp: 0,
		Owner: "赛尔", Birth: "赛尔历1年1月1日",
		Color: 0xFFFFFF, Open: true,
		// 新增芯片的默认色：自定义颜色取极光蓝，渐变取「极光蓝 → 紫」，
		// 竖直方向（球体上亮下暗最自然，也是渐变最普遍的用法）。
		ColorCustom: 0x33CCFF, GradA: 0x33CCFF, GradB: 0x8F31E1, GradMode: 0,
		ExtraRev: 1,
		// 默认 4 = 1:1 显示矢量素材（球径 99px）。素材画布是 4x 口径（269x286），
		// Scale=1 会显示成 67x72（球径 25px），1px 边缘在 25px 的球上就是肉眼可见
		// 的台阶——这就是「矢量图怎么这么糊」的直接原因（子渊 2026-10-25 反馈）。
		// 用户仍可用面板 scale +/- 在 1~4 之间调。
		// 子渊 2026-10-26：4 倍太大，1.5 倍刚好（面板 +/- 仍可调 1~4）。
		Scale: 1.5, Topmost: true, AutoWalk: true, Bubbles: true, Sound: true, IdleSpeed: 0,
		X: 200, Y: 200,
		Materials: defaultMaterials(),
	}
}

// savePath 返回存档路径：**exe 同级目录**下的 save.json。
//
// 子渊 2026-10-26：「存档在哪里？不在 exe 同级目录吗，把存档位置改到这边」。
// 原来是 os.UserConfigDir()（Windows = C:\Users\<用户>\AppData\Roaming\NonoDeskpet），
// 桌宠这种绿色版程序换个目录/换台机器拷过去，存档就跟不上了。改成 exe 同级后
// 直接连 exe 一起搬运即可。
//
// 例外：exe 放在 C:\Program Files 之类不可写目录时不能硬写（写失败 = 每次设置
// 都白丢），探测不可写就退回旧目录，保证功能不残废。
func savePath() string {
	if p := exeSavePath(); p != "" {
		return p
	}
	return legacySavePath()
}

// exeSavePath 返回 exe 同级存档的路径；目录不可写时返回空串。
//
// 有 appTag 时文件名用 save_<tag>.json（同目录与其它桌宠共存），否则 save.json。
// NONO_SAVE_DIR 覆盖存档目录（2026-11-02 对齐 seer 的 SEER_SAVE_DIR：
// 便携部署 / 单测隔离用；未设置 = 默认 exe 同级）。
func exeSavePath() string {
	if dir := os.Getenv("NONO_SAVE_DIR"); dir != "" {
		name := "save.json"
		if t := appTag(); t != "" {
			name = "save_" + t + ".json"
		}
		return filepath.Join(dir, name)
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	name := "save.json"
	if t := appTag(); t != "" {
		name = "save_" + t + ".json"
	}
	p := filepath.Join(dir, name)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	// 目录可写探测：不预建空 save.json（空文件会让 loadSave 报解析失败），
	// 用一次性临时文件探测后删掉。
	tmp := filepath.Join(dir, ".nono_write_test")
	f, err := os.Create(tmp)
	if err != nil {
		return ""
	}
	f.Close()
	os.Remove(tmp)
	return p
}

// sharedLegacySavePath 同目录旧布局的 save.json（两个项目历史上都用这个名字）。
// 只在当前进程自己的存档不存在时读取，并且要靠 looksLikeNonoSave 校验归属。
func sharedLegacySavePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(exe), "save.json")
	if p == savePath() {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// legacySavePath 旧存档位置（AppData/Roaming/NonoDeskpet/save.json），
// 只用于首次切到 exe 同级时的迁移来源。
func legacySavePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	d := filepath.Join(dir, "NonoDeskpet")
	os.MkdirAll(d, 0755)
	return filepath.Join(d, "save.json")
}

func loadSave() Save {
	s := defaultSave()
	p := savePath()
	b, err := os.ReadFile(p)
	migrated := false
	if err != nil {
		// ① 同目录共存：自己还没有 save_<tag>.json 时，把历史 save.json 迁过来。
		//    必须先校验归属 —— 同目录里那份可能属于另一个桌宠（赛尔）。
		if lp := sharedLegacySavePath(); lp != "" {
			if lb, lerr := os.ReadFile(lp); lerr == nil && looksLikeNonoSave(lb) {
				logf("save 迁移(同目录): %s → %s", lp, p)
				b, err = lb, nil
				migrated = true
			}
		}
	}
	if err != nil {
		// ② 首次切到 exe 同级：把旧 AppData 存档搬过来，别让用户白丢设置。
		// 只搬一次（下面写盘一次到新路径，下次就直接读新的）。
		if lp := legacySavePath(); lp != p {
			if lb, lerr := os.ReadFile(lp); lerr == nil {
				logf("save 迁移: %s → %s", lp, p)
				b, err = lb, nil
				migrated = true
			}
		}
	}
if err == nil {
		// Unmarshal 出错时 Go 只保留已成功解析的字段，后面的字段会退回默认值，
		// 表现成「存档里写着 false、程序里却是 true」。这里必须把错误打出来，
		// 否则这类半解析失败完全静默（2026-10-25 排查自动走动开关时踩到）。
		if e := json.Unmarshal(b, &s); e != nil {
			// 2026-11-02 对照 seer-20261102-05 D-03 修复：损坏存档先备份成
			// <save>.bad 再回默认，避免用户装扮/坐标数据直接蒸发且无法找回。
			if bad := p + ".bad"; os.Rename(p, bad) == nil {
				logf("save 解析失败，已备份为 %s: %v", bad, e)
			} else {
				logf("save 解析失败: %v path=%s（备份失败 %v）", e, p, bad)
			}
			s = defaultSave()
		}
} else {
		logf("save 读取失败（用默认值）: %v path=%s", err, savePath())
	}
	// 旧存档只有 super 布尔：升级到超能 1 级，避免出现 Super=true 但 SuperLevel=0 的半状态
	if s.Super && s.SuperLevel < 1 {
		s.SuperLevel = 1
	}
	if s.SuperLevel > 0 {
		s.Super = true
	}
	// SuperEnergy 是 2026-10-26 新增字段（资料面板 mc0「超能值」需要真实数值）。
	// 老存档为 0 会显示 0/N，且 tooltip 算出离谱的天数；按等级上限的一半给初值，
	// 用户仍可直接改 save.json。上限表与原版 _powerA 一致。
	if s.SuperLevel > 0 && s.SuperEnergy <= 0 {
		lv := s.SuperLevel
		if lv > 10 {
			lv = 10
		}
		s.SuperEnergy = infoPowerA[lv] / 2
		migrated = true
	}
	// 本地新增的自定义色/渐变色：ExtraRev 只做一次性兜底。
	// 不能简单按「==0 就填默认」，否则用户真把自定义色选成纯黑，重启就变回蓝。
	if s.ExtraRev < 1 {
		s.ColorCustom = 0x33CCFF
		s.GradA = 0x33CCFF
		s.GradB = 0x8F31E1
		s.GradMode = 0
		s.ExtraRev = 1
		migrated = true
	}
	// flyStyle 是后加字段，老存档里是零值 0；不归一化会导致
	// flyBodyInsetV2(0) 查不到 metrics 直接返回 0，飞行时整只浮在 NONO 原位置上方
	// （子渊 2026-10-24 反馈"改用飞行模式后会在原本 NONO 的位置上方很高"）。
	if s.FlyStyle < 1 || s.FlyStyle > 4 {
		s.FlyStyle = 1
	}
	// 4x 原生渲染（2026-10-25）后，素材画布是 269x286、zoom=4；老存档里的
	// Scale 还是 1x 时代的（默认 1 → 显示 67x72、球径 25px）。不迁移的话，
	// 用户覆盖升级后看到的还是那个"糊"的小 NONO，会以为改动根本没生效。
	// 一次性迁移：老存档 Scale<=1（即从没调过）提到 4；调过的（2/3）保留，
	// 因为 scale 语义没变（显示倍率 = Scale/zoom），老 2 和新 2 视觉一致。
	// 子渊 2026-10-25 明确「存档全部按最新的来」：不再保留用户旧值，
	// 一律落到新口径 Scale=4（4x 素材 1:1 显示，球径 99px）。
	if s.RenderRev < 3 {
		s.Scale = 4
		s.RenderRev = 3
		s.write()
		logf("save 迁移: Scale=%g RenderRev=%d（按最新口径全量重置）", s.Scale, s.RenderRev)
	}
	// 子渊 2026-10-26：4 倍在真机上太大，1.5 倍刚好。老存档一律落到 1.5，
	// 之后用户用面板 +/- 自己调（0.5 步长，1~4 范围）。renderRev 提到 4，
	// 保证这次迁移只发生一次，不会覆盖用户后续手动调的值。
	if s.RenderRev < 4 {
		s.Scale = 1.5
		s.RenderRev = 4
		s.write()
		logf("save 迁移: Scale=1.5 RenderRev=4（默认倍率下调）")
	}
	if s.Scale < 1 {
		s.Scale = 1
	}
	if s.Scale > 4 {
		s.Scale = 4
	}
	s.ensureMaterials()
	// 桌宠没有发明室/任务链，芯片全部直接可用；旧存档在这里补齐
	if s.ensureAllChips() {
		s.write()
	}
	// 从旧目录迁过来的存档立刻落到 exe 同级，避免下次启动又去读旧文件
	if migrated {
		s.write()
		logf("save 迁移完成，已写入 %s", p)
	}
	return s
}

// ensureAllChips 把全部芯片塞进已拥有列表并补足数量。
// 返回是否有变化（有则调用方落盘一次）。
func (s *Save) ensureAllChips() bool {
	if len(chipByID) == 0 {
		return false
	}
	changed := false
	if s.Counts == nil {
		s.Counts = map[int]int{}
		changed = true
	}
	for id := range chipByID {
		if !containsInt(s.Chips, id) {
			s.Chips = append(s.Chips, id)
			changed = true
		}
		if s.Counts[id] < 1 {
			s.Counts[id] = 1
			changed = true
		}
	}
	return changed
}

func (s *Save) write() {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		logf("save 序列化失败: %v", err)
		return
	}
	// 原子写：先写 .tmp 再 Rename，避免写到一半崩溃/断电留下截断存档
	//（2026-11-02 对照 seer-20261102-05 D-02 修复）。
	// Rename 在 Windows 上是同卷原子替换；.tmp 残留（进程被杀在两步之间）
	// 也只占一个文件，下次写会再覆盖。
	p := savePath()
	t := p + ".tmp"
	if err := os.WriteFile(t, b, 0644); err != nil {
		logf("save 写入失败: %v path=%s", err, p)
		return
	}
	if err := os.Rename(t, p); err != nil {
		logf("save 重命名失败: %v path=%s", err, p)
		return
	}
}

// hasChip 判定某芯片是否已安装。对应原版 NONO 的 func 位（位 i ⇔ 700001+i 已解锁），
// 桌宠用 Save.Chips（曾经使用过的芯片列表）代替 func 位图。
func (s *Save) hasChip(id int) bool {
	return containsInt(s.Chips, id)
}

// canUseChip 只看「装没装」。
// 2026-10-27 子渊要求去掉智能门槛：原版快捷栏追加位要求 ai >= UseAI，
// 桌宠里这会把已经买下的芯片锁在快捷栏外，体验上说不通。
func (s *Save) canUseChip(id int) bool {
	return s.hasChip(id)
}

// chipCount 返回储藏格内某芯片的剩余数量；未记录时按 1 颗算。
func (s *Save) chipCount(id int) int {
	if s.Counts == nil {
		return 1
	}
	if n, ok := s.Counts[id]; ok {
		return n
	}
	return 1
}

// consumeChip 扣一颗。消耗型芯片（isConsume==0）用完后从储藏里移除。
// 返回剩余数量，0 表示已用光。
func (s *Save) consumeChip(id int) int {
	if s.Counts == nil {
		s.Counts = map[int]int{}
	}
	n := s.chipCount(id) - 1
	s.Counts[id] = n
	if n <= 0 {
		delete(s.Counts, id)
		kept := s.Chips[:0]
		for _, c := range s.Chips {
			if c != id {
				kept = append(kept, c)
			}
		}
		s.Chips = kept
	}
	return n
}
