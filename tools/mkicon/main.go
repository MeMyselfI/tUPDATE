// Command mkicon renders the tUPDATE app icon — a white circular "refresh"
// double-arrow on a blue rounded-square gradient — and writes a Windows .ico
// (multi-size, 32-bit DIB entries) plus an optional PNG preview. Pure stdlib.
//
//	go run ./tools/mkicon -ico cmd/updater/icon.ico -png /tmp/icon-preview.png
package main

import (
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// work is the super-sampled canvas size; targets are box-downsampled from it
// for smooth (anti-aliased) edges.
const work = 1024

func main() {
	icoPath := flag.String("ico", "cmd/updater/icon.ico", "output .ico path")
	pngPath := flag.String("png", "", "optional PNG preview path (256px)")
	flag.Parse()

	big := render(work)

	if *pngPath != "" {
		prev := downsample(big, 256)
		f, err := os.Create(*pngPath)
		must(err)
		must(png.Encode(f, prev))
		must(f.Close())
	}

	sizes := []int{256, 64, 48, 32, 16}
	imgs := make([]*image.RGBA, 0, len(sizes))
	for _, s := range sizes {
		imgs = append(imgs, downsample(big, s))
	}
	must(writeICO(*icoPath, imgs))
}

// render draws the icon at size n×n with hard-edged masks; anti-aliasing comes
// from the later box downsample.
func render(n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	fn := float64(n)

	cx, cy := fn/2, fn/2
	margin := 0.015 * fn
	half := fn/2 - margin
	corner := 0.22 * fn

	// Refresh double-arrow geometry.
	rm := 0.300 * fn       // mid radius of the ring
	th := 0.115 * fn       // ring thickness
	ri := rm - th/2        // inner radius
	ro := rm + th/2        // outer radius
	ext := 0.050 * fn      // arrowhead overhang beyond ring
	const gapHalf = 26.0   // half-width of each gap (deg)
	const headSpan = 30.0  // angular length of arrowhead (deg)

	// Two arcs, gaps centred on the +x and -x axes.
	arc1lo, arc1hi := gapHalf, 180-gapHalf
	arc2lo, arc2hi := 180+gapHalf, 360-gapHalf

	white := color.RGBA{255, 255, 255, 255}

	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5

			if roundRectSDF(fx, fy, cx, cy, half, half, corner) > 0 {
				img.SetRGBA(x, y, color.RGBA{0, 0, 0, 0}) // transparent outside
				continue
			}

			// Blue vertical gradient background.
			t := fy / fn
			bg := color.RGBA{
				R: lerp(0x3b, 0x1d, t),
				G: lerp(0x82, 0x4e, t),
				B: lerp(0xf6, 0xd8, t),
				A: 255,
			}

			dx, dy := fx-cx, fy-cy
			r := math.Hypot(dx, dy)
			ang := math.Atan2(dy, dx) * 180 / math.Pi
			if ang < 0 {
				ang += 360
			}

			on := false
			if r >= ri && r <= ro {
				if (ang >= arc1lo && ang <= arc1hi) || (ang >= arc2lo && ang <= arc2hi) {
					on = true
				}
			}
			// Arrowheads at the clockwise (lower-angle) end of each arc.
			if !on {
				if inArrowhead(fx, fy, cx, cy, arc1lo, headSpan, ri-ext, ro+ext, rm) ||
					inArrowhead(fx, fy, cx, cy, arc2lo, headSpan, ri-ext, ro+ext, rm) {
					on = true
				}
			}

			if on {
				img.SetRGBA(x, y, white)
			} else {
				img.SetRGBA(x, y, bg)
			}
		}
	}
	return img
}

// inArrowhead reports whether (px,py) is inside the triangular arrowhead placed
// at the arc end angle aDeg. The base is radial (from rIn to rOut at aDeg); the
// tip sits span degrees clockwise (lower angle) on the mid radius rm.
func inArrowhead(px, py, cx, cy, aDeg, span, rIn, rOut, rm float64) bool {
	a := aDeg * math.Pi / 180
	tipA := (aDeg - span) * math.Pi / 180
	b1x, b1y := cx+rIn*math.Cos(a), cy+rIn*math.Sin(a)
	b2x, b2y := cx+rOut*math.Cos(a), cy+rOut*math.Sin(a)
	tx, ty := cx+rm*math.Cos(tipA), cy+rm*math.Sin(tipA)
	return pointInTriangle(px, py, b1x, b1y, b2x, b2y, tx, ty)
}

func pointInTriangle(px, py, ax, ay, bx, by, cx, cy float64) bool {
	d1 := cross(px, py, ax, ay, bx, by)
	d2 := cross(px, py, bx, by, cx, cy)
	d3 := cross(px, py, cx, cy, ax, ay)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

func cross(px, py, ax, ay, bx, by float64) float64 {
	return (px-bx)*(ay-by) - (ax-bx)*(py-by)
}

// roundRectSDF is the signed distance to a rounded rectangle centred at (cx,cy)
// with half-extents (hx,hy) and corner radius r. Negative inside.
func roundRectSDF(px, py, cx, cy, hx, hy, r float64) float64 {
	qx := math.Abs(px-cx) - (hx - r)
	qy := math.Abs(py-cy) - (hy - r)
	ox := math.Max(qx, 0)
	oy := math.Max(qy, 0)
	return math.Hypot(ox, oy) + math.Min(math.Max(qx, qy), 0) - r
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}

// downsample box-averages src down to size×size (src must be square).
func downsample(src *image.RGBA, size int) *image.RGBA {
	sw := src.Bounds().Dx()
	scale := sw / size
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var rr, gg, bb, aa int
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					c := src.RGBAAt(x*scale+sx, y*scale+sy)
					a := int(c.A)
					// premultiply so transparent pixels don't darken edges
					rr += int(c.R) * a
					gg += int(c.G) * a
					bb += int(c.B) * a
					aa += a
				}
			}
			n := scale * scale
			outA := aa / n
			var out color.RGBA
			if aa > 0 {
				out = color.RGBA{uint8(rr / aa), uint8(gg / aa), uint8(bb / aa), uint8(outA)}
			}
			dst.SetRGBA(x, y, out)
		}
	}
	return dst
}

// writeICO writes a multi-image .ico using 32-bit BGRA DIB entries (the format
// goversioninfo embeds reliably across all Windows versions).
func writeICO(path string, imgs []*image.RGBA) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// ICONDIR header.
	hdr := make([]byte, 6)
	binary.LittleEndian.PutUint16(hdr[0:], 0) // reserved
	binary.LittleEndian.PutUint16(hdr[2:], 1) // type: icon
	binary.LittleEndian.PutUint16(hdr[4:], uint16(len(imgs)))
	if _, err := f.Write(hdr); err != nil {
		return err
	}

	blobs := make([][]byte, len(imgs))
	for i, im := range imgs {
		blobs[i] = dibBytes(im)
	}

	offset := 6 + 16*len(imgs)
	for i, im := range imgs {
		w := im.Bounds().Dx()
		h := im.Bounds().Dy()
		entry := make([]byte, 16)
		entry[0] = byte(w & 0xff) // 256 -> 0
		entry[1] = byte(h & 0xff)
		entry[2] = 0 // palette
		entry[3] = 0 // reserved
		binary.LittleEndian.PutUint16(entry[4:], 1)  // planes
		binary.LittleEndian.PutUint16(entry[6:], 32) // bit count
		binary.LittleEndian.PutUint32(entry[8:], uint32(len(blobs[i])))
		binary.LittleEndian.PutUint32(entry[12:], uint32(offset))
		if _, err := f.Write(entry); err != nil {
			return err
		}
		offset += len(blobs[i])
	}
	for _, b := range blobs {
		if _, err := f.Write(b); err != nil {
			return err
		}
	}
	return nil
}

// dibBytes encodes a bottom-up 32-bit BGRA DIB (BITMAPINFOHEADER) plus a
// zeroed AND mask, as required inside an .ico entry.
func dibBytes(im *image.RGBA) []byte {
	w := im.Bounds().Dx()
	h := im.Bounds().Dy()

	header := make([]byte, 40)
	binary.LittleEndian.PutUint32(header[0:], 40)
	binary.LittleEndian.PutUint32(header[4:], uint32(w))
	binary.LittleEndian.PutUint32(header[8:], uint32(2*h)) // XOR + AND
	binary.LittleEndian.PutUint16(header[12:], 1)          // planes
	binary.LittleEndian.PutUint16(header[14:], 32)         // bpp

	xor := make([]byte, 0, w*h*4)
	for y := h - 1; y >= 0; y-- { // bottom-up
		for x := 0; x < w; x++ {
			c := im.RGBAAt(x, y)
			xor = append(xor, c.B, c.G, c.R, c.A)
		}
	}

	maskRow := ((w + 31) / 32) * 4 // 1bpp, 4-byte aligned
	and := make([]byte, maskRow*h) // all zero = use alpha

	out := append([]byte{}, header...)
	out = append(out, xor...)
	out = append(out, and...)
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
