package main

import (
	"fmt"
	"image"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

func main() {
	sp, err := spec.Load("/tmp/shape46only/sprite.json")
	if err != nil { panic(err) }
	lib, err := spec.LoadLibrary("/tmp/shape46only/parts")
	if err != nil { panic(err) }
	r := sprite.New(sp, lib)
	img, err := r.RenderFullCanvas("default", 0, 6, 4)
	if err != nil { panic(err) }

	best := struct{ x, y, a int }{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := y*img.Stride + x*4
			if int(img.Pix[i+3]) > best.a {
				best.a = int(img.Pix[i+3]); best.x = x; best.y = y
			}
		}
	}
	i := best.y*img.Stride + best.x*4
	fmt.Printf("最高 alpha 像素 (%d,%d): R=%d G=%d B=%d A=%d\n",
		best.x, best.y, img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3])
	fmt.Printf("是否符合预乘 (R<=A): %v\n", int(img.Pix[i]) <= int(img.Pix[i+3]))
	_ = image.Rect
}
