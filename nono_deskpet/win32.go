//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// ---- 基础句柄与常量 ----
type HANDLE uintptr
type HWND HANDLE
type HDC HANDLE
type HBITMAP HANDLE
type HBRUSH HANDLE
type HMENU HANDLE
type HFONT HANDLE
type HINSTANCE HANDLE
type HMODULE HANDLE

const (
	WS_POPUP         = 0x80000000
	WS_OVERLAPPED    = 0x00000000
	WS_CAPTION       = 0x00C00000
	WS_SYSMENU       = 0x00080000
	WS_MINIMIZEBOX   = 0x00020000
	WS_VISIBLE       = 0x10000000
	WS_CHILD         = 0x40000000
	WS_BORDER        = 0x00800000
	WS_EX_LAYERED    = 0x00080000
	WS_EX_TOPMOST    = 0x00000008
	WS_EX_TOOLWINDOW = 0x00000080
	WS_EX_APPWINDOW  = 0x00040000

	SW_SHOW       = 5
	SW_HIDE       = 0
	SW_SHOWNORMAL = 1
	SW_RESTORE    = 9

	WM_DESTROY       = 0x0002
	WM_CLOSE         = 0x0010
	WM_PAINT         = 0x000F
	WM_ERASEBKGND    = 0x0014
	WM_TIMER         = 0x0113
	WM_MOUSEMOVE     = 0x0200
	WM_LBUTTONDOWN   = 0x0201
	WM_LBUTTONUP     = 0x0202
	WM_RBUTTONUP     = 0x0205
	WM_MOUSEACTIVATE = 0x0021
	MA_NOACTIVATE    = 3
	WM_NCHITTEST     = 0x0084
	WM_COMMAND       = 0x0111
	WM_APP           = 0x8000
	WM_NULL          = 0x0000
	WM_SETICON       = 0x0080

	// 虚拟键码：抬起兜底用（Wine 下分层窗口偶尔丢 WM_LBUTTONUP）
	VK_LBUTTON = 0x01

	HTTRANSPARENT = -1
	HTCLIENT      = 1

	ULW_ALPHA      = 0x00000002
	AC_SRC_OVER    = 0
	AC_SRC_ALPHA   = 1
	DIB_RGB_COLORS = 0

	MF_STRING       = 0x00000000
	MF_POPUP        = 0x00000010
	MF_SEPARATOR    = 0x00000800
	MF_CHECKED      = 0x00000008
	TPM_LEFTALIGN   = 0x0000
	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100

	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_NOZORDER   = 0x0004
	SWP_SHOWWINDOW = 0x0040
	SWP_NOACTIVATE = 0x0010

	HWND_TOPMOST = -1

	MB_OK = 0x0

	FW_NORMAL       = 400
	FW_BOLD         = 700
	DEFAULT_CHARSET = 1
	TRANSPARENT     = 1
)

type POINT struct{ X, Y int32 }
type SIZE struct{ CX, CY int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }
type RGBQUAD struct{ R, G, B, A byte }
type BLENDFUNCTION struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}
type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}
type BITMAPINFO struct {
	BmiHeader BITMAPINFOHEADER
	BmiColors [1]RGBQUAD
}
type MSG struct {
	Hwnd    HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}
type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     HINSTANCE
	HIcon         HANDLE
	HCursor       HANDLE
	HbrBackground HBRUSH
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       HANDLE
}
type PAINTSTRUCT struct {
	Hdc         HDC
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	pRegisterClassExW    = user32.NewProc("RegisterClassExW")
	pCreateWindowExW     = user32.NewProc("CreateWindowExW")
	pDefWindowProcW      = user32.NewProc("DefWindowProcW")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pGetMessageW         = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessageW    = user32.NewProc("DispatchMessageW")
	pPostMessageW        = user32.NewProc("PostMessageW")
	pSetTimer            = user32.NewProc("SetTimer")
	pKillTimer           = user32.NewProc("KillTimer")
	pSendMessageW        = user32.NewProc("SendMessageW")
	pUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	pClientToScreen       = user32.NewProc("ClientToScreen")
	pGetDC               = user32.NewProc("GetDC")
	pReleaseDC           = user32.NewProc("ReleaseDC")
	pBeginPaint          = user32.NewProc("BeginPaint")
	pEndPaint            = user32.NewProc("EndPaint")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenuW         = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pShowWindow          = user32.NewProc("ShowWindow")
	pSetWindowPos        = user32.NewProc("SetWindowPos")
	pGetClientRect       = user32.NewProc("GetClientRect")
	pFillRect            = user32.NewProc("FillRect")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pSetWindowTextW      = user32.NewProc("SetWindowTextW")
	pGetWindowTextW      = user32.NewProc("GetWindowTextW")
	pMsgBox              = user32.NewProc("MessageBoxW")
	pInvalidateRect      = user32.NewProc("InvalidateRect")
	pLoadIconW           = user32.NewProc("LoadIconW")
	pSetFocus             = user32.NewProc("SetFocus")
	pSetCapture          = user32.NewProc("SetCapture")
	pReleaseCapture      = user32.NewProc("ReleaseCapture")

	pCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	pSelectObject          = gdi32.NewProc("SelectObject")
	pDeleteObject          = gdi32.NewProc("DeleteObject")
	pDeleteDC              = gdi32.NewProc("DeleteDC")
	pSetBkMode             = gdi32.NewProc("SetBkMode")
	pSetBkColor            = gdi32.NewProc("SetBkColor")
	pSetTextColor          = gdi32.NewProc("SetTextColor")
	pTextOutW              = gdi32.NewProc("TextOutW")
	pCreateFontW           = gdi32.NewProc("CreateFontW")
	pCreateSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	pGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")

	// 单实例接管（single.go）用到的 API。
	pFindWindowW                = user32.NewProc("FindWindowW")
	pGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	pCreateMutexW               = kernel32.NewProc("CreateMutexW")
	pOpenProcess                = kernel32.NewProc("OpenProcess")
	pTerminateProcess           = kernel32.NewProc("TerminateProcess")
	pQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	pWaitForSingleObject        = kernel32.NewProc("WaitForSingleObject")
	pCloseHandle                = kernel32.NewProc("CloseHandle")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

func utf16ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func loword(v uintptr) int        { return int(v & 0xffff) }
func hiword(v uintptr) int        { return int((v >> 16) & 0xffff) }
func GET_X_LPARAM(lp uintptr) int { return int(int16(lp & 0xffff)) }
func GET_Y_LPARAM(lp uintptr) int { return int(int16((lp >> 16) & 0xffff)) }
func makeLParam(x, y int) uintptr {
	return uintptr(uint16(x)) | uintptr(uint16(y))<<16
}

func updateLayered(hwnd HWND, hdcSrc HDC, x, y, w, h int) {
	ptDst := POINT{int32(x), int32(y)}
	ptSrc := POINT{0, 0}
	sz := SIZE{int32(w), int32(h)}
	bf := BLENDFUNCTION{BlendOp: AC_SRC_OVER, SourceConstantAlpha: 255, AlphaFormat: AC_SRC_ALPHA}
	pUpdateLayeredWindow.Call(uintptr(hwnd), 0,
		uintptr(unsafe.Pointer(&ptDst)), uintptr(unsafe.Pointer(&sz)),
		uintptr(hdcSrc), uintptr(unsafe.Pointer(&ptSrc)), 0,
		uintptr(unsafe.Pointer(&bf)), ULW_ALPHA)
}
