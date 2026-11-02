//go:build windows

package main

// 通用「贴图面板窗口」：分层窗口 + PNG 合成 + 按钮命中。
// 与旧 panels.go 的区别：不再用 GDI 画形状，面板外观全部来自 assets/ui 下的
// Game 导出图，因此视觉与游戏一致；GDI 只负责文字。

import (
	"image"
	"image/png"
	"os"
	"syscall"
	"unsafe"
)

type uiBtn struct {
	Action string
	X, Y   int
	W, H   int
	// HitX/HitY/HitW/HitH 是可选的可点击子矩形（相对按钮左上，逻辑像素）。
	// HitW>0 时命中判定只用这块，X/Y/W/H 仍决定图片绘制位置。
	// 用途：飞行选择面板的「取消飞行模式」按钮，over 态图里带一条比图标宽 4 倍的
	// 黑底提示条，命中区必须只盖图标本体（官方 hittest 就是 26×28），否则鼠标停在
	// 提示条上也算命中，整块区域会吃点击。
	HitX, HitY  int
	HitW, HitH  int
	Up          *image.RGBA
	Over        *image.RGBA
	Down        *image.RGBA
	Disabled    bool
}

type uiWin struct {
	Hwnd     HWND
	S        float64 // DPI 缩放：面板图与坐标都按逻辑像素设计，显示时统一乘 S
	W, H     int
	X, Y     int
	Bg       *image.RGBA
	// BgPick 可选：每次重绘时挑底图（同一尺寸的不同状态图，例如普通/超能形态）。
	// 底图在两种形态间切换（资料面板），Bg 单值不够用；返回 nil 时用 Bg。
	BgPick   func() *image.RGBA
	Canvas   *image.RGBA
	R        *Renderer
	Btns     []uiBtn
	Labels   []uiLabel
	Hover    int
	DownIdx  int
	Visible  bool
	OnAction func(action string)
	OnHover  func(idx int)
	Draw     func(img *image.RGBA, s float64) // 自定义绘制钩子（进度条等），s 为 DPI 缩放
	// 拖动：按在非按钮的不透明区域上可拖动整个面板；按在按钮上则要位移超过
	// uiDragThreshold 才转成拖动（否则单击按钮就被吃掉）。
	Dragging    bool
	DragPending bool // 按钮上按下、尚未超过拖动阈值
	DragMouse   POINT
	DragWinX    int
	DragWinY    int
	// ReleaseTicks 记录「定时器发现左键已松开」后经过的拍数。
	// Wine 下 WM_LBUTTONUP 常比 15ms 定时器晚到几十毫秒，若一发现松开就清
	// DownIdx，单击会被吃成无效点击（实测：点储藏格 down=5 / up=-1）。
	// 宽限 6 拍（≈90ms）等 UP 消息，超时才认定 UP 丢失并兜底清理。
	ReleaseTicks int
	// DragLogN 拖动位移日志节流计数（2026-11-02 E-08，每 10 位移打一条）。
	DragLogN int
	// OnMove 在拖动过程中每发生一次位移时调用，dx/dy 是本次的增量（物理像素）。
	// 用途：主面板拖动时带着功能芯片环一起走（环是以主面板可见圆为圆心定位的）。
	OnMove func(dx, dy int)
}

// uiDragThreshold 按钮上按下后转成「拖动」所需的位移（物理像素）。
// 6px 是手感与误触的折中：手指/鼠标自然抖动 2~3px 不算拖，超过 6px 明显是
// 想搬面板。配合 WM_TIMER 15ms 轮询，快速拖拽也能在下一拍生效。
const uiDragThreshold = 6

var hitLog int

// Wine 下 SetCapture 对分层窗口不生效，拖动改用定时器轮询光标 + 左键状态。
var pGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")

const uiDragTimerID = 7

var (
	uiWins       = map[HWND]*uiWin{}
	uiClassReady bool
	pTrackMouse   = user32.NewProc("TrackMouseEvent")
)

type trackMouseEvent struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   HWND
	DwHoverTime uint32
}

func uiWndProc(hwnd HWND, msg uint32, wp, lp uintptr) uintptr {
	w := uiWins[hwnd]
	switch msg {
	case WM_MOUSEMOVE:
		if w != nil && w.Dragging {
			var pt POINT
			pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			nx := w.DragWinX + int(pt.X-w.DragMouse.X)
			ny := w.DragWinY + int(pt.Y-w.DragMouse.Y)
			if nx != w.X || ny != w.Y {
				oldX, oldY := w.X, w.Y
				w.X, w.Y = nx, ny
				// 只移动窗口，内容不重绘（分层位图跟着窗口走）
				r, _, e := pSetWindowPos.Call(uintptr(w.Hwnd), ^uintptr(0), uintptr(nx), uintptr(ny),
					uintptr(w.W), uintptr(w.H), 0x0010|SWP_SHOWWINDOW)
				// 改名输入框是独立顶层窗口，必须跟着面板一起挪，否则脱位
				if w == infoPanelWin && nickHwnd != 0 {
					moveNickInput()
				}
				w.notifyMoved(nx-oldX, ny-oldY)
				// 2026-11-02 E-08：拖动位移日志降到 1/10（面板拖动期间
				// WM_MOUSEMOVE 每帧一条，长拖一次就几十上百条）。
				if w.DragLogN%10 == 0 {
					logf("panel drag move %d,%d -> %d,%d ret=%d err=%v", w.DragWinX, w.DragWinY, nx, ny, r, e)
				}
				w.DragLogN++
			}
			return 0
		}
		if w != nil {
			x, y := GET_X_LPARAM(lp), GET_Y_LPARAM(lp)
			// 这里原本有一段「查 GetKeyState 补抬起」的兜底。实测它会误触发：
			// DownIdx 只要有一次没清（DOWN 到了、UP 丢了），之后鼠标单纯滑过
			// 窗口就会走这里把 action 执行掉，表现成"鼠标滑过去就打开了一些功能"
			// （子渊 2026-10-24 反馈）。抬起完全交给 WM_LBUTTONUP + tick 轮询，
			// 两处都带"按下/抬起同一按钮"校验，不再靠这里猜。
			h := w.hitAt(x, y)
			if h != w.Hover {
				w.Hover = h
				w.paint()
				if w.OnHover != nil {
					w.OnHover(h)
				}
			}
		}
		return 0
	case WM_MOUSEACTIVATE:
		// 配合 WS_EX_NOACTIVATE：点击既不激活窗口也不丢弃消息，
		// 本次 WM_LBUTTONDOWN 直接进按钮（否则要第一下激活、第二下才生效）。
		return MA_NOACTIVATE
	case WM_NCHITTEST:
		// 透明区域穿透：只让按钮矩形接收点击，其余交给下层窗口（本体拖动不受影响）
		if w != nil {
			x, y := GET_X_LPARAM(lp), GET_Y_LPARAM(lp)
			cx, cy := x-w.X, y-w.Y
			hh := w.hitAt(cx, cy)
			if (w == shortcutWin || w == shellWin) && hitLog < 10 {
				hitLog++
				logf("hittest raw=%d,%d win=%d,%d local=%d,%d hit=%d alpha=%d", x, y, w.X, w.Y, cx, cy, hh, w.bgAlphaAt(cx, cy))
			}
			if hh < 0 && w.bgAlphaAt(cx, cy) < 8 {
				return ^uintptr(0) // HTTRANSPARENT = -1
			}
			return HTCLIENT
		}
		return HTCLIENT
	case WM_LBUTTONDOWN:
		if w != nil {
			x, y := GET_X_LPARAM(lp), GET_Y_LPARAM(lp)
			w.DownIdx = w.hitAt(x, y)
			logf("ui down idx=%d at %d,%d win=%d,%d", w.DownIdx, x, y, w.X, w.Y)
			// 抓住鼠标：分层窗口在 Wine 下若不吃 SetCapture，LBUTTONUP 常常
			// 发给别的窗口，按钮就变成"按下有反应、抬起没反应"=点了没效果
			// （子渊 2026-10-23：部分按钮点了没反应、面板关不掉）。
			pSetCapture.Call(uintptr(w.Hwnd))
			var pt POINT
			pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			w.DragPending = false
			w.ReleaseTicks = 0
			if w.DownIdx < 0 && w.bgAlphaAt(x, y) >= 8 {
				// 空白但不透明处：立刻开始拖动（系统 HTCAPTION 在分层窗口下不可靠）
				w.Dragging = true
				logf("panel drag start at %d,%d local=%d,%d alpha=%d", w.X, w.Y, x, y, w.bgAlphaAt(x, y))
				w.DragMouse = pt
				w.DragWinX, w.DragWinY = w.X, w.Y
				pSetTimer.Call(uintptr(w.Hwnd), uiDragTimerID, 15, 0)
			} else if w.DownIdx >= 0 {
				// 按钮上按下：先按正常点击处理（按钮进按下态），位移超过
				// uiDragThreshold 才在 WM_TIMER 里转成拖动。
				// 储藏面板 9 个 60×60 格 + 5 个功能键几乎铺满 242×300，非按钮的
				// 不透明区只剩 25.6% 且是 6px 窄条，原先完全拖不动（子渊 2026-10-27）。
				w.DragPending = true
				w.DragMouse = pt
				w.DragWinX, w.DragWinY = w.X, w.Y
				pSetTimer.Call(uintptr(w.Hwnd), uiDragTimerID, 15, 0)
			}
			w.paint()
		}
		return 0
	case WM_LBUTTONUP:
		if w != nil {
			pReleaseCapture.Call()
			w.ReleaseTicks = 0
			if w.Dragging {
				w.Dragging = false
				w.DragPending = false
				pKillTimer.Call(uintptr(w.Hwnd), uiDragTimerID)
				notePanelMoved(w)
				w.paint()
				return 0
			}
			// 按钮上按下、但没超过拖动阈值：就是一次正常单击，继续走下面的 action 判定
			w.DragPending = false
			pKillTimer.Call(uintptr(w.Hwnd), uiDragTimerID)
			idx := w.hitAt(GET_X_LPARAM(lp), GET_Y_LPARAM(lp))
			logf("ui up idx=%d down=%d btns=%d", idx, w.DownIdx, len(w.Btns))
			if idx >= 0 && idx == w.DownIdx && idx < len(w.Btns) {
				a := w.Btns[idx].Action
				w.DownIdx = -1
				w.paint()
				if w.OnAction != nil {
					w.OnAction(a)
				}
			} else {
				w.DownIdx = -1
				w.paint()
			}
		}
		return 0
	case WM_TIMER:
		if w != nil && int(wp) == uiDragTimerID {
			// 左键已松开（高位为 0）
			if st, _, _ := pGetAsyncKeyState.Call(1); int32(uint16(st))&0x8000 == 0 {
				if w.Dragging {
					w.Dragging = false
					w.DragPending = false
					w.ReleaseTicks = 0
					pKillTimer.Call(uintptr(w.Hwnd), uiDragTimerID)
					notePanelMoved(w)
					w.paint()
					return 0
				}
				// 只是「点了一下」：给 WM_LBUTTONUP 留 6 拍宽限再清状态，
				// 否则单击在 Wine 下会被吃成无效点击（UP 比定时器晚到）。
				w.ReleaseTicks++
				if w.ReleaseTicks >= 6 {
					w.DragPending = false
					if w.DownIdx >= 0 {
						// UP 消息真丢了：只清按下态，绝不补执行 action
						// （否则鼠标扫过按钮就会把功能打开，2026-10-24 的老坑）
						w.DownIdx = -1
						logf("panel press released without UP, clear down")
					}
					pKillTimer.Call(uintptr(w.Hwnd), uiDragTimerID)
					w.paint()
				}
				return 0
			}
			w.ReleaseTicks = 0
			if w.Dragging {
				var pt POINT
				pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
				nx := w.DragWinX + int(pt.X-w.DragMouse.X)
				ny := w.DragWinY + int(pt.Y-w.DragMouse.Y)
				if nx != w.X || ny != w.Y {
					oldX, oldY := w.X, w.Y
					w.X, w.Y = nx, ny
					// 分层窗口靠 UpdateLayeredWindow 的 dstPoint 定位，单用 SetWindowPos
					// 会留下旧位置的像素残影，所以重新 paint 一次（16ms 内只画一帧，开销可接受）。
					w.paint()
					w.notifyMoved(nx-oldX, ny-oldY)
					logf("panel drag timer move %d,%d -> %d,%d", oldX, oldY, nx, ny)
				}
				return 0
			}
			if w.DragPending {
				// 在按钮上按住并移动超过阈值 -> 转成拖动面板，同时取消按钮按下态
				var pt POINT
				pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
				dx := int(pt.X - w.DragMouse.X)
				dy := int(pt.Y - w.DragMouse.Y)
				if dx < 0 {
					dx = -dx
				}
				if dy < 0 {
					dy = -dy
				}
				if dx >= uiDragThreshold || dy >= uiDragThreshold {
					w.DragPending = false
					w.Dragging = true
					w.DownIdx = -1
					w.Hover = -1
					logf("panel drag armed by threshold at %d,%d", w.X, w.Y)
					w.paint()
				}
				return 0
			}
			return 0
		}
	case WM_CLOSE:
		if w != nil {
			w.hide()
		}
		return 0
	case WM_DESTROY:
		delete(uiWins, hwnd)
		if w != nil {
			w.Hwnd = 0
		}
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wp, lp)
	return ret
}

func uiEnsureClass() {
	if uiClassReady {
		return
	}
	hInst, _, _ := pGetModuleHandleW.Call(0)
	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(uiWndProc),
		HInstance:     HINSTANCE(hInst),
		LpszClassName: utf16ptr("NonoUIWnd"),
	}
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	uiClassReady = true
}

// newUIWin 创建一个透明分层窗口，尺寸为逻辑像素 w×h。
// uiScale 读系统 DPI 比例（96dpi = 1.0）。Per-Monitor V2 下窗口坐标是物理像素，
// 而面板图按 1x 设计，不缩放会在大屏上偏小。
func uiScale() float64 {
	hdc, _, _ := pGetDC.Call(0)
	if hdc == 0 {
		return 1
	}
	defer pReleaseDC.Call(0, hdc)
	dpi, _, _ := gdi32.NewProc("GetDeviceCaps").Call(hdc, 88) // LOGPIXELSX
	if dpi < 96 {
		return 1
	}
	s := float64(dpi) / 96
	if s > 3 {
		s = 3
	}
	return s
}

func newUIWin(w, h int) *uiWin {
	uiEnsureClass()
	sc := uiScale()
	dw, dh := int(float64(w)*sc+0.5), int(float64(h)*sc+0.5)
	hInst, _, _ := pGetModuleHandleW.Call(0)
	hwnd, _, _ := pCreateWindowExW.Call(
		// WS_EX_NOACTIVATE(0x08000000)：面板不抢前台的焦点。否则窗口创建后处于
		// 未激活态，第一次点击会被系统拿去激活窗口、不落成 WM_LBUTTONDOWN，
		// 表现为按钮「要点两下才生效」（子渊 2026-10-26 报的扳手换√要两下）。
		// 改名输入框是 panel_input.go 里的独立顶层窗口，自取焦点，不受影响。
		WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_TOPMOST|0x08000000,
		uintptr(unsafe.Pointer(utf16ptr("NonoUIWnd"))),
		uintptr(unsafe.Pointer(utf16ptr("NONO"))),
		WS_POPUP,
		0, 0, uintptr(dw), uintptr(dh), 0, 0, hInst, 0)
	if hwnd == 0 {
		logf("newUIWin failed")
		return nil
	}
	win := &uiWin{
		Hwnd:    HWND(hwnd),
		S:       sc,
		W:       dw,
		H:       dh,
		Canvas:  image.NewRGBA(image.Rect(0, 0, dw, dh)),
		R:       newRenderer(dw, dh),
		Hover:   -1,
		DownIdx: -1,
	}
	uiWins[win.Hwnd] = win
	logf("uiwin created hwnd=%d size=%dx%d", hwnd, w, h)
	return win
}

func (w *uiWin) hitAt(x, y int) int {
	s := w.S
	if s <= 0 {
		s = 1
	}
	x = int(float64(x)/s + 0.5)
	y = int(float64(y)/s + 0.5)
	for i, b := range w.Btns {
		if b.Disabled {
			continue
		}
		if b.HitW > 0 {
			if x >= b.X+b.HitX && x < b.X+b.HitX+b.HitW && y >= b.Y+b.HitY && y < b.Y+b.HitY+b.HitH {
				return i
			}
			continue
		}
		if x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H {
			return i
		}
	}
	return -1
}

// paint 重画整块画布并推送到窗口。
func (w *uiWin) paint() {
	if w == nil || w.Hwnd == 0 || w.Bg == nil {
		return
	}
	bg := w.Bg
	if w.BgPick != nil {
		if b := w.BgPick(); b != nil {
			bg = b
		}
	}
	if bg == nil {
		return
	}
	// 底图通常与画布同尺寸，直接整块复制。只有飞行选择面板例外：它的画布比底图高
	// 30px，「取消飞行模式」按钮 over 态的黑色提示条要画到面板底边之外（官方 SWF
	// 里这个按钮的 over 态就比面板本体低 29px，Flash 的 sprite 不裁剪）。
	// 不同尺寸时必须先清空再逐行贴，直接 copy 会把底图字节铺满画布、下半截留残影。
	if bgw, bgh := bg.Bounds().Dx(), bg.Bounds().Dy(); bgw == w.Canvas.Bounds().Dx() && bgh == w.Canvas.Bounds().Dy() {
		copy(w.Canvas.Pix, bg.Pix)
	} else {
		for i := range w.Canvas.Pix {
			w.Canvas.Pix[i] = 0
		}
		chs := w.Canvas.Stride
		for y := 0; y < bgh && y < w.Canvas.Bounds().Dy(); y++ {
			copy(w.Canvas.Pix[y*chs:y*chs+bgw*4], bg.Pix[y*bg.Stride:y*bg.Stride+bgw*4])
		}
	}
	s := w.S
	if s <= 0 {
		s = 1
	}
	for i, b := range w.Btns {
		img := b.Up
		if b.Disabled {
			img = b.Down // 禁用态用预烘焙的灰图
			if img == nil {
				img = b.Up
			}
		} else if w.DownIdx == i && b.Down != nil {
			img = b.Down
		} else if w.Hover == i && b.Over != nil {
			img = b.Over
		}
		if img != nil && len(img.Pix) > 0 && (img.Bounds().Dx() != int(float64(b.W)*s+0.5) || img.Bounds().Dy() != int(float64(b.H)*s+0.5)) {
			img = uiFit(img, int(float64(b.W)*s+0.5), int(float64(b.H)*s+0.5))
		}
		uiBlend(w.Canvas, img, int(float64(b.X)*s+0.5), int(float64(b.Y)*s+0.5))
	}
	if w.Draw != nil {
		w.Draw(w.Canvas, s)
	}
	uiDrawLabels(w.Canvas, scaleLabels(w.Labels, s))
	w.R.blit(w.Canvas)
	updateLayered(w.Hwnd, w.R.Hdc, w.X, w.Y, w.W, w.H)
	// 调试钩子：NONO_DUMP_PANEL=<png 路径> 把面板画布落盘。
	// Wine 下截图抓不到分层窗口（xwd 全黑），验证芯片栏排版/翻页只能靠离屏画布。
	// 排除 tooltip：它也是 uiWin，会把这个路径抢过去写成 140×24 的小图。
	if dp := os.Getenv("NONO_DUMP_PANEL"); dp != "" && w != tipWin {
		if f, err := os.Create(dp); err == nil {
			_ = png.Encode(f, w.Canvas)
			f.Close()
		}
	}
}

// notifyMoved 把本次拖动的位移通知给跟随窗口（主面板 → 功能芯片环）。
// dx/dy 是物理像素增量，可能是负数；Helpers 只做转发，不关心移动方式
// （WM_MOUSEMOVE 走 SetWindowPos、WM_TIMER 走 paint，两条路径都必须通知）。
func (w *uiWin) notifyMoved(dx, dy int) {
	if w == nil || w.OnMove == nil || (dx == 0 && dy == 0) {
		return
	}
	w.OnMove(dx, dy)
}

// Contains 判断屏幕坐标是否落在窗口矩形内（含边界）。
func (w *uiWin) Contains(x, y int) bool {
	if w == nil || w.Hwnd == 0 {
		return false
	}
	return x >= w.X && x < w.X+w.W && y >= w.Y && y < w.Y+w.H
}

func (w *uiWin) showAt(x, y int) {
	if w == nil || w.Hwnd == 0 {
		return
	}
	w.X, w.Y = x, y
	w.Visible = true
	pShowWindow.Call(uintptr(w.Hwnd), 8)
	// 面板与本体都是 TOPMOST，谁后置顶谁在上。本体每帧都在 UpdateLayeredWindow，
	// 不显式把面板提到最前的话，本体窗口会压在面板标题上（实测 2026-10-23）。
	pSetWindowPos.Call(uintptr(w.Hwnd), uintptr(^uintptr(0)), 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	// 本体窗口同为 TOPMOST 且每帧重绘，实测会在某些时序下重新盖到面板标题上。
	// 面板显示期间把本体显式插到面板之后（Z 序下方），关闭时再恢复本体置顶。
	if g != nil && g.Hwnd != 0 {
		pSetWindowPos.Call(uintptr(g.Hwnd), uintptr(w.Hwnd), 0, 0, 0, 0,
			SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	}
	// 位置只靠 UpdateLayeredWindow 的 dstPoint 生效；额外 SetWindowPos 在 Wine 下
	// 会把旧位置的像素留在桌面上（残影），所以位置参数传 SWP_NOMOVE。
	w.paint()
}

func (w *uiWin) hide() {
	if w == nil || w.Hwnd == 0 {
		return
	}
	w.Visible = false
	w.Hover = -1
	w.DownIdx = -1
	pShowWindow.Call(uintptr(w.Hwnd), SW_HIDE)
	// 面板收起后把本体放回置顶，避免被普通窗口压住
	if g != nil && g.Hwnd != 0 {
		pSetWindowPos.Call(uintptr(g.Hwnd), uintptr(^uintptr(0)), 0, 0, 0, 0,
			SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	}
}

// destroy 真正销毁窗口并从 uiWins 注销，释放 GDI 渲染资源。
//
// 2026-11-02 E-01 修复：showShell 每次打开都 newUIWin，旧的只 hide 不销毁，
// 窗口句柄 + GDI 对象 + uiWins map 只增不减，长时间使用会耗尽 GDI 对象
// （Windows 每进程默认上限 10000）。需要「每次新建」的窗口（shell 面板、
// 快捷栏重建）关闭时必须走 destroy 而不是 hide。
func (w *uiWin) destroy() {
	if w == nil || w.Hwnd == 0 {
		return
	}
	w.Visible = false
	if w.R != nil {
		w.R.release()
		w.R = nil
	}
	delete(uiWins, w.Hwnd)
	pDestroyWindow.Call(uintptr(w.Hwnd))
	w.Hwnd = 0
}

func (w *uiWin) containsScreen(x, y int) bool {
	return w != nil && w.Visible && x >= w.X && x < w.X+w.W && y >= w.Y && y < w.Y+w.H
}

// scaleLabels 把逻辑坐标的标签换算到实际像素。
func scaleLabels(ls []uiLabel, s float64) []uiLabel {
	if s == 1 || len(ls) == 0 {
		return ls
	}
	out := make([]uiLabel, len(ls))
	for i, l := range ls {
		l.X = int(float64(l.X)*s + 0.5)
		l.Y = int(float64(l.Y)*s + 0.5)
		l.Size = int(float64(l.Size)*s + 0.5)
		out[i] = l
	}
	return out
}

// bgAlphaAt 读背景图某点 alpha（物理像素），用于分层窗口的透明区穿透判定。
func (w *uiWin) bgAlphaAt(x, y int) uint8 {
	if w.Bg == nil || x < 0 || y < 0 || x >= w.Bg.Bounds().Dx() || y >= w.Bg.Bounds().Dy() {
		return 0
	}
	return w.Bg.Pix[w.Bg.PixOffset(x, y)+3]
}

// keepPanelsOnTop 让所有可见的面板保持在最前，并把本体窗口压到面板之下。
// 本体每帧 UpdateLayeredWindow，Wine 下会把本体重新浮到同组顶部，所以需要每帧校正；
// 面板本身只在 showAt 时置顶一次，这里只对「有面板可见」的情况补正，开销可忽略。
func keepPanelsOnTop() {
	if g == nil || g.Hwnd == 0 {
		return
	}
	top := (*uiWin)(nil)
	// flyChoiceWin 也必须在列表里：本体每帧 UpdateLayeredWindow 会浮回顶层，
	// 只有飞行模式面板可见时没人补正，本体就会压在它上面（2026-10-27 一并修）。
	for _, w := range []*uiWin{mainPanelWin, chipPanelWin, storagePanelWin, labWin,
		shellWin, infoPanelWin, flyChoiceWin} {
		if w != nil && w.Visible && w.Hwnd != 0 {
			top = w
		}
	}
	if top == nil {
		return
	}
	pSetWindowPos.Call(uintptr(top.Hwnd), uintptr(^uintptr(0)), 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	pSetWindowPos.Call(uintptr(g.Hwnd), uintptr(top.Hwnd), 0, 0, 0, 0,
		SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	// 改名输入框是独立 TOPMOST 弹窗：面板被抬到最上之后会把它整个盖住，
	// 表现就是「点了扳手只看到蓝条高亮，看不到输入框」。这里跟一次抬起。
	if nickHwnd != 0 {
		pSetWindowPos.Call(uintptr(nickHwnd), uintptr(^uintptr(0)), 0, 0, 0, 0,
			SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
	}
	// 面板抬起时直接丢弃残影（本来也只在移动瞬间出现）。残影已画进主窗口，
	// 不再有独立 TOPMOST 窗口需要避让，清掉池即可。
	if len(ghostPool) > 0 {
		ghostPool = nil
	}
}
