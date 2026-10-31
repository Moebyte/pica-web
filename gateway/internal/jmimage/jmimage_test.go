package jmimage

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// Reference values cross-checked against the Dart implementation
// (_getSegmentationNum) using Python's hashlib.md5.
func TestSegmentationNum(t *testing.T) {
	cases := []struct {
		epsID, scrambleID int
		book              string
		want              int
	}{
		{100, 220880, "00001", 0},    // epsID < scrambleID
		{220879, 220880, "00001", 0}, // boundary: still <
		{250000, 220880, "00001", 10},
		{268849, 1, "00001", 10},
		{268850, 1, "00001", 6},  // first hash branch, %10
		{300000, 1, "00001", 16}, // %10
		{421926, 1, "00001", 6},  // boundary: > is false, %10 branch
		{421927, 1, "00001", 8},  // first %8 branch
		{500000, 220880, "00042", 12},
	}
	for _, c := range cases {
		if got := SegmentationNum(c.epsID, c.scrambleID, c.book); got != c.want {
			t.Errorf("SegmentationNum(%d, %d, %q) = %d, want %d", c.epsID, c.scrambleID, c.book, got, c.want)
		}
	}
}

// stripes builds a width x height image with num solid horizontal gray
// stripes so every scramble block is exactly one solid, JPEG-friendly color
// (neutral gray keeps Cb/Cr at 128 and survives re-encoding almost losslessly).
func stripes(width, height, num int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	blockH := height / num
	for i := 0; i < num; i++ {
		start := i * blockH
		end := start + blockH
		if i == num-1 {
			end = height
		}
		v := uint8(30 + i*20)
		c := color.RGBA{R: v, G: v, B: v, A: 255}
		for y := start; y < end; y++ {
			for x := 0; x < width; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// scrambleBlocks produces the image JM serves for a given original: the
// original is cut into num vertical blocks with the REMAINDER in the FIRST
// block (mirroring how the Dart recombine slices the served file with the
// remainder in its last block), and the blocks are stacked in reverse order.
// Recombine(scrambleBlocks(img)) must reproduce img.
func scrambleBlocks(img image.Image, num int) *image.RGBA {
	b := img.Bounds()
	blockH := b.Dy() / num
	remainder := b.Dy() % num

	// Original block bounds: block 0 absorbs the remainder.
	type blk struct{ start, end int }
	blocks := make([]blk, num)
	for i := 0; i < num; i++ {
		start := i * blockH
		if i == 0 {
			start = 0
		} else {
			start = blockH + remainder + (i-1)*blockH
		}
		end := start + blockH
		if i == 0 {
			end = blockH + remainder
		}
		blocks[i] = blk{start, end}
	}

	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	y := 0
	for i := num - 1; i >= 0; i-- {
		drawCopy(dst, 0, y, img, blocks[i].start, blocks[i].end)
		y += blocks[i].end - blocks[i].start
	}
	return dst
}

func drawCopy(dst *image.RGBA, dx, dy int, src image.Image, start, end int) {
	for y := start; y < end; y++ {
		for x := 0; x < src.Bounds().Dx(); x++ {
			dst.Set(dx+x, dy+(y-start), src.At(x, y))
		}
	}
}

// TestRecombineRestoresStripes verifies the full round trip: a scrambled JPEG
// recombined with Recombine must decode to (nearly) the original pixels.
// JPEG chroma subsampling blurs the 1-2 rows at stripe boundaries, so colors
// are compared at stripe centers where the source is solid.
func TestRecombineRestoresStripes(t *testing.T) {
	const width, num = 40, 10
	for _, height := range []int{100, 103} { // 103: remainder goes to last block
		orig := stripes(width, height, num)
		scrambled := encodeJPEG(t, scrambleBlocks(orig, num), 95)

		out, err := Recombine(scrambled, num)
		if err != nil {
			t.Fatalf("height %d: Recombine: %v", height, err)
		}
		got, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("height %d: decode output: %v", height, err)
		}
		if got.Bounds() != orig.Bounds() {
			t.Fatalf("height %d: bounds = %v, want %v", height, got.Bounds(), orig.Bounds())
		}
		blockH := height / num
		for i := 0; i < num; i++ {
			start := i * blockH
			end := start + blockH
			if i == num-1 {
				end = height
			}
			centerY := (start + end) / 2
			r1, g1, b1 := colorAt(orig, width/2, centerY)
			r2, g2, b2 := colorAt(got, width/2, centerY)
			if abs(r1-r2) > 2 || abs(g1-g2) > 2 || abs(b1-b2) > 2 {
				t.Fatalf("height %d: stripe %d center color (%d,%d,%d), want (%d,%d,%d)",
					height, i, r2, g2, b2, r1, g1, b1)
			}
			// The stripe of the output at this position must also be uniform:
			// sampling near its top and bottom edge interior must stay close.
			for _, y := range []int{start + 2, end - 3} {
				if y < start || y >= end {
					continue
				}
				r3, g3, b3 := colorAt(got, width/2, y)
				if abs(r1-r3) > 6 || abs(g1-g3) > 6 || abs(b1-b3) > 6 {
					t.Fatalf("height %d: stripe %d near-edge color (%d,%d,%d), want (%d,%d,%d)",
						height, i, r3, g3, b3, r1, g1, b1)
				}
			}
		}
	}
}

// The stripe order of the recombined image must match the original order:
// the top stripe of the output must be the top stripe of the original, and
// the bottom stripe must be the bottom one.
func TestRecombineBlockOrder(t *testing.T) {
	const width, height, num = 8, 50, 10
	orig := stripes(width, height, num)
	scrambled := encodeJPEG(t, scrambleBlocks(orig, num), 95)

	out, err := Recombine(scrambled, num)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	pairs := [][2]int{{4, 0}, {4, height - 1}, {1, height / 2}}
	for _, p := range pairs {
		gotR, gotG, gotB := colorAt(got, p[0], p[1])
		wantR, wantG, wantB := colorAt(orig, p[0], p[1])
		if abs(gotR-wantR) > 6 || abs(gotG-wantG) > 6 || abs(gotB-wantB) > 6 {
			t.Fatalf("pixel (%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
				p[0], p[1], gotR, gotG, gotB, wantR, wantG, wantB)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func colorAt(img image.Image, x, y int) (int, int, int) {
	r, g, b, _ := img.At(x, y).RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}

func TestRecombinePassthrough(t *testing.T) {
	img := encodeJPEG(t, stripes(8, 50, 5), 90)
	for _, num := range []int{0, 1} {
		out, err := Recombine(img, num)
		if err != nil {
			t.Fatalf("num %d: %v", num, err)
		}
		if !bytes.Equal(out, img) {
			t.Fatalf("num %d: expected byte-identical passthrough", num)
		}
	}
}

func TestRecombineNumExceedsHeight(t *testing.T) {
	// height 8 < num 10: blockH = 0, everything lands in the last block.
	// Must not panic and must decode back to the same size.
	img := encodeJPEG(t, stripes(16, 8, 2), 90)
	out, err := Recombine(img, 10)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds().Dx() != 16 || got.Bounds().Dy() != 8 {
		t.Fatalf("bounds = %v", got.Bounds())
	}
}

func TestRecombineInvalidJPEG(t *testing.T) {
	if _, err := Recombine([]byte("not a jpeg"), 10); err == nil {
		t.Fatal("expected decode error")
	}
}

// TestRecombineWebP covers the live JM format: a lossless 8x24 WebP holding
// 12 solid gray stripes in scrambled (reverse) order. Recombine must restore
// the original stripe order and return JPEG.
func TestRecombineWebP(t *testing.T) {
	const scrambledWebP = "UklGRkYAAABXRUJQVlA4TDoAAAAvB8AFAF8gFkwkdudPGsMpPE7hMf/Z4YCQtp1MYQpTmEIKKaSQQgoppBDfdYjof34el8NmMRk0RRIA"
	raw, err := base64.StdEncoding.DecodeString(scrambledWebP)
	if err != nil {
		t.Fatal(err)
	}
	if !IsWebPBytes(raw) {
		t.Fatal("fixture is not webp")
	}
	out, err := Recombine(raw, 12)
	if err != nil {
		t.Fatalf("Recombine webp: %v", err)
	}
	if !IsJPEGBytes(out) {
		t.Fatal("output is not jpeg")
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got.Bounds().Dx() != 8 || got.Bounds().Dy() != 24 {
		t.Fatalf("bounds = %v", got.Bounds())
	}
	for i := 0; i < 12; i++ {
		want := 30 + i*20
		r, _, _, _ := got.At(4, i*2+1).RGBA()
		if diff := abs(int(r>>8) - want); diff > 6 {
			t.Fatalf("stripe %d = %d, want %d", i, r>>8, want)
		}
	}
}

func TestRecombineUnsupportedFormat(t *testing.T) {
	if _, err := Recombine([]byte("GIF89a not supported"), 4); err == nil {
		t.Fatal("expected unsupported format error")
	}
}

func TestImageFormatSniffing(t *testing.T) {
	if !IsWebP("webp", "") || !IsWebP("", "image/webp") || IsWebP("jpg", "image/jpeg") {
		t.Fatal("IsWebP")
	}
	if !IsJPEGBytes([]byte{0xff, 0xd8, 0xff, 0xe0}) || IsJPEGBytes([]byte{0xff, 0xd8}) {
		t.Fatal("IsJPEGBytes")
	}
	if !IsWebPBytes([]byte("RIFF....WEBPVP8 ")) || IsWebPBytes([]byte("RIFF....AXWV")) {
		t.Fatal("IsWebPBytes")
	}
}

func TestIsJPEG(t *testing.T) {
	cases := []struct {
		ext, ct string
		want    bool
	}{
		{"jpg", "", true},
		{"jpeg", "", true},
		{"", "image/jpeg", true},
		{"png", "image/png", false},
		{"webp", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := IsJPEG(c.ext, c.ct); got != c.want {
			t.Errorf("IsJPEG(%q, %q) = %v, want %v", c.ext, c.ct, got, c.want)
		}
	}
}
