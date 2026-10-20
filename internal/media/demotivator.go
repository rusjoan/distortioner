package media

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Demotivator is the text of a demotivator poster.
type Demotivator struct {
	Title    string // large serif line
	Subtitle string // smaller line below, optional
}

// Fonts with Cyrillic; the first one found is used. In the image they come
// from the fonts-liberation package.
var (
	titleFonts = []string{
		"/usr/share/fonts/truetype/liberation/LiberationSerif-Regular.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf",
		"/System/Library/Fonts/Supplemental/Times New Roman.ttf",
	}
	textFonts = []string{
		"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/System/Library/Fonts/Supplemental/Arial.ttf",
	}
)

func firstFont(paths []string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return paths[0] // let ImageMagick report the missing font
}

// imText escapes text for ImageMagick's caption: '@' would read a file,
// '%' and '\' are escapes.
func imText(s string) string {
	s = strings.NewReplacer(`\`, `\\`, "%", "%%").Replace(s)
	if strings.HasPrefix(s, "@") {
		s = `\` + s
	}
	return s
}

// demotivate frames the picture in: a black poster, a thin white frame
// around the image and the caption below.
func (e *Engine) demotivate(ctx context.Context, in, out string, d Demotivator, quality int) error {
	size, err := run(ctx, e.magickEn, e.Magick, "identify", "-format", "%w %h", in+"[0]")
	if err != nil {
		return err
	}
	var w, h int
	if _, err := fmt.Sscan(string(size), &w, &h); err != nil || w <= 0 || h <= 0 {
		return fmt.Errorf("demotivator: bad size %q", size)
	}
	// ImageMagick cannot draw an empty caption.
	if strings.TrimSpace(d.Title) == "" {
		d.Title, d.Subtitle = d.Subtitle, ""
	}
	if strings.TrimSpace(d.Title) == "" {
		d.Title = "…"
	}
	pad := max(w/10, 24)
	width := w + 14 + 2*pad // 4 px black + 3 px white frame on both sides
	textWidth := width - 2*pad
	titleSize := max(width/12, 22)

	args := []string{
		"-background", "black", "-fill", "white",
		"(", in + "[0]", "-bordercolor", "black", "-border", "4", "-bordercolor", "white", "-border", "3",
		"-bordercolor", "black", "-border", fmt.Sprintf("%dx%d", pad, pad), ")",
		"(", "-size", fmt.Sprintf("%dx", textWidth), "-font", firstFont(titleFonts), "-pointsize", strconv.Itoa(titleSize),
		"-gravity", "center", "caption:" + imText(d.Title), "-bordercolor", "black", "-border", fmt.Sprintf("%dx0", pad), ")",
	}
	if strings.TrimSpace(d.Subtitle) != "" {
		args = append(args,
			"(", "-size", fmt.Sprintf("%dx", textWidth), "-font", firstFont(textFonts), "-pointsize", strconv.Itoa(titleSize*45/100),
			"-gravity", "center", "caption:"+imText(d.Subtitle), "-bordercolor", "black", "-border", fmt.Sprintf("%dx%d", pad, pad/4), ")")
	}
	args = append(args,
		"(", "-size", fmt.Sprintf("%dx%d", width, pad/2), "xc:black", ")",
		"-gravity", "center", "-append", "-quality", strconv.Itoa(quality), out)
	_, err = run(ctx, e.magickEn, e.Magick, args...)
	return err
}
