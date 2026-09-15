package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
)

// The image and the document a recording sends are generated here rather than
// committed as files: a few pixels and a few hundred bytes of PDF are cheaper
// to read as code than as a blob, and a reader can see exactly what the model
// was shown. Both are shared by every provider's exchanges.

// imageColour fills the generated PNG: a saturated orange, far enough from
// red and yellow that "what colour is this?" has one right answer, which is
// what lets the offline test assert on the recorded reply.
var imageColour = color.NRGBA{R: 0xF2, G: 0x7A, B: 0x0C, A: 0xFF}

// pdfPhrase is the one line of text in the generated PDF. It is a phrase no
// model would produce unprompted, so a response quoting it proves the PDF was
// read rather than guessed at.
const pdfPhrase = "Marmalade on the tortoise."

// tinyPNG is a small solid-colour image, a few pixels square. It is the
// smallest input that makes "what colour is this?" a question with one right
// answer.
func tinyPNG() ([]byte, error) {
	const side = 8
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			img.SetNRGBA(x, y, imageColour)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encoding the test image: %w", err)
	}
	return buf.Bytes(), nil
}

// tinyPDF builds a minimal valid PDF: one page, one line of text in a
// standard font, an accurate cross-reference table and a trailer. It is about
// six hundred bytes, which is small enough to send on every recording and
// large enough to be a real PDF rather than something a parser has to
// forgive.
//
// The cross-reference offsets are computed from the bytes as they are written,
// because a PDF with a wrong xref is the kind of file that works in one reader
// and is rejected by the next.
func tinyPDF() []byte {
	// Object 4 is the content stream; its /Length must match the stream's
	// bytes exactly, so the stream is built before the object that
	// declares it.
	stream := "BT /F1 18 Tf 24 48 Td (" + pdfEscape(pdfPhrase) + ") Tj ET\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 120] /Contents 4 0 R " +
			"/Resources << /Font << /F1 5 0 R >> >> >>",
		"<< /Length " + strconv.Itoa(len(stream)) + " >>\nstream\n" + stream + "endstream",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objects)+1)
	// Every xref entry is exactly twenty bytes, free list entry included;
	// a reader seeks by multiplying, so a byte out is a broken file.
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, xref)
	return buf.Bytes()
}

// pdfEscape escapes the three characters a PDF literal string cannot carry
// raw. The phrase above needs none of them; the function is here so changing
// the phrase cannot quietly produce a malformed file.
func pdfEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(s)
}
