// Command termshot turns captured terminal output (with ANSI colour codes)
// into an HTML page that looks like a terminal window, for the README
// screenshots. Headless Chrome then renders the page to PNG
// (.github/workflows/screenshots.yml).
//
// Block and shade elements (▀ ▄ █ ░ ▒ ▓) are drawn as CSS cells rather than font glyphs, so
// half-block art (the logo, bars) has no gaps between lines whatever the
// font. Text is otherwise reproduced exactly; -replace only rewrites literal
// substrings (used to show the sandbox's simulated drive as C:\).
//
//	termshot -in clean.ans -out clean.html -title "oow clean --dry-run" -replace 'D:\sb\C\=C:\'
//
// It prints the window size in CSS pixels ("WIDTHxHEIGHT") for the
// screenshot.
package main

import (
	"flag"
	"fmt"
	"html"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

type replaces []string

func (r *replaces) String() string     { return strings.Join(*r, ",") }
func (r *replaces) Set(v string) error { *r = append(*r, v); return nil }

const (
	fontSize   = 15  // px
	charWidth  = 9.0 // px: Cascadia Mono advances 0.6 em (Consolas, the fallback, is narrower)
	lineHeight = 20  // px
	padX, padY = 24, 18
	titleBar   = 36
	defaultFG  = "#d6dae4"
	background = "#0d1117"
)

func main() {
	in := flag.String("in", "", "captured output (UTF-8, ANSI SGR codes)")
	out := flag.String("out", "", "HTML file to write")
	title := flag.String("title", "", "window title, e.g. the command line")
	cols := flag.Int("cols", 80, "terminal width in columns")
	var reps replaces
	flag.Var(&reps, "replace", "literal OLD=NEW substitution (repeatable)")
	flag.Parse()
	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fail(err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, r := range reps {
		old, nu, ok := strings.Cut(r, "=")
		if !ok {
			fail(fmt.Errorf("-replace %q: want OLD=NEW", r))
		}
		text = strings.ReplaceAll(text, old, nu)
	}
	text = strings.TrimRight(text, "\n ")
	lines := strings.Split(text, "\n")

	var body strings.Builder
	maxCols := *cols
	for _, line := range lines {
		n := render(&body, line)
		maxCols = max(maxCols, n)
		body.WriteString("\n")
	}
	w := int(float64(maxCols)*charWidth) + 2*padX
	h := len(lines)*lineHeight + 2*padY + titleBar
	page := fmt.Sprintf(pageTemplate, w, h, background, titleBar,
		fontSize, lineHeight, defaultFG, padY, padX, lineHeight, html.EscapeString(*title), body.String())
	if err := os.WriteFile(*out, []byte(page), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("%dx%d\n", w, h)
}

type style struct {
	fg   string
	bold bool
	dim  bool
}

// render writes one line as HTML and returns its width in columns.
func render(b *strings.Builder, line string) int {
	var st style
	cols := 0
	open := false
	start := func() {
		if open {
			return
		}
		var css []string
		if st.fg != "" {
			css = append(css, "color:"+st.fg)
		}
		if st.bold {
			css = append(css, "font-weight:700")
		}
		if st.dim {
			css = append(css, "opacity:.65")
		}
		b.WriteString(`<span style="` + strings.Join(css, ";") + `">`)
		open = true
	}
	end := func() {
		if open {
			b.WriteString("</span>")
			open = false
		}
	}
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && (line[j] < 0x40 || line[j] > 0x7e) {
				j++
			}
			if j < len(line) && line[j] == 'm' {
				end()
				st = apply(st, line[i+2:j])
			}
			i = j + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		i += size
		start()
		fg := st.fg
		if fg == "" {
			fg = defaultFG
		}
		switch r {
		case '█':
			b.WriteString(`<i class="c" style="background:` + fg + `"></i>`)
		case '▀':
			b.WriteString(`<i class="c" style="background:linear-gradient(` + fg + ` 50%,transparent 50%)"></i>`)
		case '▄':
			b.WriteString(`<i class="c" style="background:linear-gradient(transparent 50%,` + fg + ` 50%)"></i>`)
		case '░', '▒', '▓': // shades: the cell in the text colour at 25/50/75 %
			op := map[rune]string{'░': ".25", '▒': ".5", '▓': ".75"}[r]
			b.WriteString(`<i class="c" style="background:` + fg + `;opacity:` + op + `"></i>`)
		default:
			b.WriteString(html.EscapeString(string(r)))
		}
		cols++
	}
	end()
	return cols
}

// apply updates a style with one SGR parameter list.
func apply(st style, params string) style {
	if params == "" {
		return style{}
	}
	ps := strings.Split(params, ";")
	for k := 0; k < len(ps); k++ {
		n, _ := strconv.Atoi(ps[k])
		switch {
		case n == 0:
			st = style{}
		case n == 1:
			st.bold = true
		case n == 2:
			st.dim = true
		case n == 22:
			st.bold, st.dim = false, false
		case n == 39:
			st.fg = ""
		case n >= 30 && n <= 37:
			st.fg = ansi16[n-30]
		case n >= 90 && n <= 97:
			st.fg = ansi16[n-90+8]
		case n == 38 && k+4 < len(ps) && ps[k+1] == "2":
			r, _ := strconv.Atoi(ps[k+2])
			g, _ := strconv.Atoi(ps[k+3])
			bl, _ := strconv.Atoi(ps[k+4])
			st.fg = fmt.Sprintf("#%02x%02x%02x", r, g, bl)
			k += 4
		case n == 38 && k+2 < len(ps) && ps[k+1] == "5":
			idx, _ := strconv.Atoi(ps[k+2])
			if idx < 16 {
				st.fg = ansi16[idx]
			}
			k += 2
		}
	}
	return st
}

var ansi16 = []string{"#0d1117", "#f87171", "#4ade80", "#fbbf24", "#60a5fa", "#a78bfa", "#22d3ee", "#d6dae4",
	"#8b93a7", "#fca5a5", "#86efac", "#fde68a", "#93c5fd", "#c4b5fd", "#67e8f9", "#ffffff"}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "termshot:", err)
	os.Exit(1)
}

const pageTemplate = `<!doctype html>
<html><head><meta charset="utf-8"><style>
html,body{margin:0;width:%dpx;height:%dpx;background:%s;overflow:hidden}
.bar{height:%dpx;display:flex;align-items:center;padding:0 16px;gap:8px;background:#161b22;border-bottom:1px solid #21262d}
.dot{width:12px;height:12px;border-radius:50%%;background:#30363d}
.t{margin-left:10px;color:#8b93a7;font:13px "Segoe UI",Arial,sans-serif;white-space:nowrap}
pre{margin:0;font-family:"Cascadia Mono","Cascadia Code",Consolas,monospace;font-size:%dpx;line-height:%dpx;color:%s;padding:%dpx %dpx;white-space:pre}
.c{display:inline-block;width:1ch;height:%dpx;vertical-align:top;font-style:normal}
</style></head><body><div class="bar"><span class="dot"></span><span class="dot"></span><span class="dot"></span><span class="t">%s</span></div><pre>%s</pre></body></html>
`
