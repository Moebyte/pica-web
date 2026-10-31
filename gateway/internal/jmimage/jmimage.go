// Package jmimage reverses the vertical block scramble JM (18comic) applies
// to its chapter images.
//
// Ported from the Dart client (app/lib/foundation/image_loader/image_recombine.dart,
// originally from JMComic-qt tools): the served image is cut into num equal
// vertical blocks (the remainder goes to the last block) and the blocks are
// stacked back in reverse order.
//
// JM now serves most pages as WebP. Those WebP files are scrambled exactly
// like the older JPEGs (verified against live chapters), so the gateway must
// recombine both formats. A pure-Go WebP encoder is not available, so WebP
// pages are decoded and re-encoded as JPEG.
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

	"golang.org/x/image/webp"
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

// Recombine reverses the scramble of a JM page image. num <= 1 returns the
// input unchanged. JPEG input is re-encoded as JPEG; WebP input is decoded
// and encoded as JPEG (quality EncodeQuality). Any other payload yields an
// error.
func Recombine(imgData []byte, num int) ([]byte, error) {
	if num <= 1 {
		return imgData, nil
	}
	src, err := decode(imgData)
	if err != nil {
		return nil, err
	}
	sub, ok := src.(interface {
		SubImage(r image.Rectangle) image.Image
	})
	if !ok {
		return nil, fmt.Errorf("decoder produced %T, cannot slice", src)
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

// decode sniffs the payload magic and decodes JM's two live formats.
func decode(imgData []byte) (image.Image, error) {
	switch {
	case IsWebPBytes(imgData):
		src, err := webp.Decode(bytes.NewReader(imgData))
		if err != nil {
			return nil, fmt.Errorf("decode webp: %w", err)
		}
		return src, nil
	case IsJPEGBytes(imgData):
		src, err := jpeg.Decode(bytes.NewReader(imgData))
		if err != nil {
			return nil, fmt.Errorf("decode jpeg: %w", err)
		}
		return src, nil
	default:
		return nil, fmt.Errorf("unsupported image format")
	}
}

// IsJPEG reports whether the content type / URL extension pair looks like a
// JPEG.
func IsJPEG(ext, contentType string) bool {
	switch ext {
	case "jpg", "jpeg":
		return true
	}
	return strings.Contains(contentType, "jpeg")
}

// IsJPEGBytes reports whether data starts with a JPEG SOI marker.
func IsJPEGBytes(data []byte) bool {
	return len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff
}

// IsWebP reports whether the content type / URL extension pair looks like a
// WebP image.
func IsWebP(ext, contentType string) bool {
	switch ext {
	case "webp":
		return true
	}
	return strings.Contains(contentType, "webp")
}

// IsWebPBytes reports whether data starts with a RIFF/WEBP header.
func IsWebPBytes(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}
