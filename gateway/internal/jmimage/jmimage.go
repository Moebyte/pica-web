// Package jmimage reverses the vertical block scramble JM (18comic) applies
// to its chapter images.
//
// Ported from the Dart client (app/lib/foundation/image_loader/image_recombine.dart,
// originally from JMComic-qt tools): the served JPEG is cut into num equal
// vertical blocks (the remainder goes to the last block) and the blocks are
// stacked back in reverse order.
package jmimage

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"strconv"
	"strings"
)

// SegmentationNum returns the number of vertical blocks JM used to scramble
// the image. bookName is the image file name without its extension (for
// example "00001").
//
// Rules (epsID and scrambleID are the numeric chapter id and the album's
// scramble id):
//
//	epsID < scrambleID      -> 0 (not scrambled)
//	epsID < 268850          -> 10
//	epsID > 421926          -> (last md5 hex char of epsID+bookName) %8 *2+2
//	otherwise               -> (last md5 hex char of epsID+bookName) %10 *2+2
func SegmentationNum(epsID, scrambleID int, bookName string) int {
	switch {
	case epsID < scrambleID:
		return 0
	case epsID < 268850:
		return 10
	case epsID > 421926:
		return hashSegments(epsID, bookName, 8)
	default:
		return hashSegments(epsID, bookName, 10)
	}
}

func hashSegments(epsID int, bookName string, mod int) int {
	sum := md5.Sum([]byte(strconv.Itoa(epsID) + bookName))
	hash := hex.EncodeToString(sum[:])
	// The hash is lowercase hex, so the byte value equals the Dart code unit.
	return (int(hash[len(hash)-1])%mod)*2 + 2
}

// EncodeQuality is the JPEG quality used for re-encoded images.
const EncodeQuality = 85

// Recombine reverses the scramble of a JM JPEG. num <= 1 returns the input
// unchanged. The image is decoded, split into num vertical SubImages (the
// remainder belongs to the last block) which are redrawn in reverse order onto
// a fresh canvas and re-encoded as JPEG (quality 85).
func Recombine(imgData []byte, num int) ([]byte, error) {
	if num <= 1 {
		return imgData, nil
	}
	src, err := jpeg.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, fmt.Errorf("decode jpeg: %w", err)
	}
	sub, ok := src.(interface {
		SubImage(r image.Rectangle) image.Image
	})
	if !ok {
		return nil, fmt.Errorf("jpeg decoder produced %T, cannot slice", src)
	}

	b := src.Bounds()
	width, height := b.Dx(), b.Dy()
	blockH := height / num
	remainder := height % num

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	y := 0
	for i := num - 1; i >= 0; i-- {
		start := i * blockH
		end := start + blockH
		if i == num-1 {
			end += remainder
		}
		if end <= start {
			continue
		}
		block := image.Rect(b.Min.X, b.Min.Y+start, b.Min.X+width, b.Min.Y+end)
		draw.Draw(dst, image.Rect(0, y, width, y+(end-start)),
			sub.SubImage(block), image.Pt(b.Min.X, b.Min.Y+start), draw.Src)
		y += end - start
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: EncodeQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return out.Bytes(), nil
}

// IsJPEG reports whether the content type / URL extension pair looks like a
// JPEG (the only format JM scrambling applies to).
func IsJPEG(ext, contentType string) bool {
	switch ext {
	case "jpg", "jpeg":
		return true
	}
	return strings.Contains(contentType, "jpeg")
}
