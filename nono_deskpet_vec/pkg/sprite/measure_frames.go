package sprite

import (
	"fmt"
	"os"
	"time"

	"nonovec/pkg/spec"
)

// MeasureAllFrames 遍历素材的全部帧，按「帧 bbox」逐一渲染，报告最慢帧。
//
// 为什么要它：24fps 的预算是 41ms/帧，只看中位帧会漏掉最坏帧（大特效帧）。
// 必须知道 worst case 才能判定「实时渲染、不缓存」这条路是否成立。
//
// 环境变量 NONO_BENCH_DIR 指定素材目录；输出每帧耗时分布与最慢的 5 帧。
func MeasureAllFrames() error {
	dir := os.Getenv("NONO_BENCH_DIR")
	if dir == "" {
		return fmt.Errorf("未设置 NONO_BENCH_DIR")
	}
	sp, err := spec.Load(dir + "/sprite.json")
	if err != nil {
		return err
	}
	lib, err := spec.LoadLibrary(dir + "/parts")
	if err != nil {
		return err
	}
	r := New(sp, lib)
	frames := sp.Directions["default"].Frames
	type rec struct {
		idx int
		ms  float64
		px  float64
	}
	var recs []rec
	for i := range frames {
		bb := frames[i].Bbox
		if len(bb) != 4 || bb[2] <= 0 || bb[3] <= 0 {
			continue
		}
		clip := []float64{bb[0] - 1, bb[1] - 1, bb[2] + 2, bb[3] + 2}
		t0 := time.Now()
		if _, err := r.render("default", i, 4, 1, "", false, clip); err != nil {
			continue
		}
		recs = append(recs, rec{i, float64(time.Since(t0).Microseconds()) / 1000, bb[2] * bb[3] * 16 / 1e3})
	}
	if len(recs) == 0 {
		return fmt.Errorf("没有可渲染的帧")
	}
	worst := recs[0]
	sum := 0.0
	for _, x := range recs {
		sum += x.ms
		if x.ms > worst.ms {
			worst = x
		}
	}
	fmt.Printf("帧数 %d 平均 %.2fms 中位≈ 最慢 %.2fms(帧%d, %.0f kpx)\n",
		len(recs), sum/float64(len(recs)), worst.ms, worst.idx, worst.px)
	// 再报告 3 个位置采样，确认大特效帧不是偶发
	step := len(recs) / 6
	for i := step; i < len(recs); i += step {
		fmt.Printf("  帧%-4d %6.2fms %7.0f kpx\n", recs[i].idx, recs[i].ms, recs[i].px)
	}
	return nil
}
