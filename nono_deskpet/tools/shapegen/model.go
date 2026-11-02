package main

import (
	"git.gammaspectra.live/WeebDataHoarder/swf-go/types"
)

// Mat 是 2D 仿射矩阵：x' = A*x + C*y + Tx ; y' = B*x + D*y + Ty
type Mat struct{ A, B, C, D, Tx, Ty float64 }

func Ident() Mat { return Mat{1, 0, 0, 1, 0, 0} }

// Mul 返回 m ∘ n（先 n 后 m）。
func (m Mat) Mul(n Mat) Mat {
	return Mat{
		A:  m.A*n.A + m.C*n.B,
		B:  m.B*n.A + m.D*n.B,
		C:  m.A*n.C + m.C*n.D,
		D:  m.B*n.C + m.D*n.D,
		Tx: m.A*n.Tx + m.C*n.Ty + m.Tx,
		Ty: m.B*n.Tx + m.D*n.Ty + m.Ty,
	}
}

func (m Mat) Pt(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.Tx, m.B*x + m.D*y + m.Ty
}

// FromSWFMatrix 把 SWF 的 MATRIX（scale/rotate/skew + twips 平移）转成 px 矩阵。
// SWF 中 Scale/Rotate 是 16.16 定点，Translate 单位是 twip(1/20 px)。
func FromSWFMatrix(m types.MATRIX) Mat {
	return Mat{
		A:  float64(m.ScaleX) / 65536,
		B:  float64(m.RotateSkew0) / 65536,
		C:  float64(m.RotateSkew1) / 65536,
		D:  float64(m.ScaleY) / 65536,
		Tx: float64(m.TranslateX) / 20,
		Ty: float64(m.TranslateY) / 20,
	}
}

// Translate 返回纯平移矩阵（px）。
func Translate(x, y float64) Mat { return Mat{1, 0, 0, 1, x, y} }
