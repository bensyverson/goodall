package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// tinyPDFBody is a minimal one-page PDF. A viewer that repairs cross-references
// reads it; the phrase is one no model would produce unprompted.
const tinyPDFBody = "%PDF-1.4\n" +
	"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
	"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
	"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 300 100]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj\n" +
	"4 0 obj<</Length 52>>stream\nBT /F1 12 Tf 20 50 Td (Marmalade on the tortoise.) Tj ET\nendstream endobj\n" +
	"5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n" +
	"trailer<</Root 1 0 R>>\n%%EOF\n"

// mediaURLs returns an orange PNG and the PDF above as data URLs.
func mediaURLs() (pngURL, pdfURL string, err error) {
	const side = 8
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			img.SetNRGBA(x, y, color.NRGBA{R: 0xF2, G: 0x7A, B: 0x0C, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", fmt.Errorf("encoding the test image: %w", err)
	}
	pngURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	pdfURL = "data:application/pdf;base64," + base64.StdEncoding.EncodeToString([]byte(tinyPDFBody))
	return pngURL, pdfURL, nil
}
