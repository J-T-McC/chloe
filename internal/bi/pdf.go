package bi

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

// PDF lays rendered pages (PNG) out one per PDF page, losslessly, at 96 dpi of
// the page's logical width: a 1280px page captured at any pixel ratio is 960pt
// wide.
func PDF(pages [][]byte, logicalWidth int) ([]byte, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("no pages to put in the PDF")
	}
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string, stream []byte) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s", len(offsets), body)
		if stream != nil {
			buf.WriteString("\nstream\n")
			buf.Write(stream)
			buf.WriteString("\nendstream")
		}
		buf.WriteString("\nendobj\n")
	}
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	n := len(pages)
	// 1 catalog, 2 pages; then per page: page, image, content.
	kids := ""
	for i := 0; i < n; i++ {
		kids += fmt.Sprintf("%d 0 R ", 3+i*3)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, n), nil)
	for i, data := range pages {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", i+1, err)
		}
		b := img.Bounds()
		rgba := image.NewRGBA(b)
		draw.Draw(rgba, b, image.White, image.Point{}, draw.Src)
		draw.Draw(rgba, b, img, b.Min, draw.Over)
		rgb := make([]byte, 0, b.Dx()*b.Dy()*3)
		for p := 0; p < len(rgba.Pix); p += 4 {
			rgb = append(rgb, rgba.Pix[p], rgba.Pix[p+1], rgba.Pix[p+2])
		}
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		zw.Write(rgb)
		zw.Close()
		ptPerPx := 0.75 * float64(logicalWidth) / float64(b.Dx())
		w, h := float64(b.Dx())*ptPerPx, float64(b.Dy())*ptPerPx
		page, im := 3+i*3, 4+i*3
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /XObject << /Im%d %d 0 R >> >> /Contents %d 0 R >>", w, h, i, im, page+2), nil)
		obj(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>", b.Dx(), b.Dy(), z.Len()), z.Bytes())
		content := fmt.Sprintf("q %.2f 0 0 %.2f 0 0 cm /Im%d Do Q", w, h, i)
		obj(fmt.Sprintf("<< /Length %d >>", len(content)), []byte(content))
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return buf.Bytes(), nil
}
