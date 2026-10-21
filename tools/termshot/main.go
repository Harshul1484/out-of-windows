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
//
// With -scene it writes the frames of the README's demo animation instead:
//
//	termshot -scene scene.json -frames DIR -title oow -replace 'D:\sb\C\=C:\'
//
// scene.json lists steps, [{"cmd": "oow clean --dry-run", "in": "clean.ans"}].
// Each step starts on a clear screen, types its command after a prompt, then
// shows the captured output a few lines at a time, scrolling like a terminal
// once it fills the window. The output is the real capture; only the typing
// is staged. DIR receives one HTML page per frame (f0001.html, ...) and
// frames.txt, an ffconcat list of the PNGs rendered from them with each
// frame's duration, for ffmpeg. All frames share one size, which is printed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
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
	scene := flag.String("scene", "", "animation steps (JSON); writes frames instead of one page")
	frames := flag.String("frames", "", "folder for the animation's frames (with -scene)")
	var reps replaces
	flag.Var(&reps, "replace", "literal OLD=NEW substitution (repeatable)")
	flag.Parse()
	if *scene != "" {
		if *frames == "" {
			flag.Usage()
			os.Exit(2)
		}
		animate(*scene, *frames, *title, *cols, reps)
		return
	}
	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	lines, err := readLines(*in, reps)
	if err != nil {
		fail(err)
	}

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

// readLines reads a capture, applies the substitutions and drops trailing
// blank lines.
func readLines(path string, reps replaces) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, r := range reps {
		old, nu, ok := strings.Cut(r, "=")
		if !ok {
			return nil, fmt.Errorf("-replace %q: want OLD=NEW", r)
		}
		text = strings.ReplaceAll(text, old, nu)
	}
	return strings.Split(strings.TrimRight(text, "\n "), "\n"), nil
}

// Animation timing. Output appears in about revealFrames steps whatever its
// length, then stays long enough to read.
const (
	sceneRows    = 42   // window height in lines; longer output scrolls
	typePerFrame = 2    // characters typed per frame
	typeDelay    = 0.05 // seconds per typing frame
	promptPause  = 0.6  // before typing, and before "Enter"
	revealFrames = 12
	revealDelay  = 0.08
	holdBase     = 2.0 // seconds the whole output stays, plus holdPerLine per line
	holdPerLine  = 0.04
	promptColor  = "#8b93a7"
)

type sceneStep struct {
	Cmd string `json:"cmd"`
	In  string `json:"in"`
}

// animate writes the frames of a scene and the ffconcat list, and prints
// their common size.
func animate(scenePath, dir, title string, cols int, reps replaces) {
	data, err := os.ReadFile(scenePath)
	if err != nil {
		fail(err)
	}
	var steps []sceneStep
	if err := json.Unmarshal(data, &steps); err != nil {
		fail(fmt.Errorf("%s: %w", scenePath, err))
	}
	if len(steps) == 0 {
		fail(fmt.Errorf("%s: no steps", scenePath))
	}
	outputs := make([][]string, len(steps))
	width := cols
	for i, s := range steps {
		if outputs[i], err = readLines(s.In, reps); err != nil {
			fail(err)
		}
		width = max(width, utf8.RuneCountInString(s.Cmd)+3)
		for _, line := range outputs[i] {
			var discard strings.Builder
			width = max(width, render(&discard, line))
		}
	}
	w := int(float64(width)*charWidth) + 2*padX
	h := sceneRows*lineHeight + 2*padY + titleBar
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}

	list := []string{"ffconcat version 1.0"}
	n := 0
	frame := func(cmd []rune, typed int, cursor bool, lines []string, secs float64) {
		n++
		name := fmt.Sprintf("f%04d", n)
		var body strings.Builder
		// The screen is the prompt line and the output; once it is taller than
		// the window, its top scrolls away.
		skip := max(0, 1+len(lines)-sceneRows)
		if skip == 0 {
			body.WriteString(`<span style="color:` + promptColor + `">&gt; </span>`)
			body.WriteString(html.EscapeString(string(cmd[:typed])))
			if cursor {
				body.WriteString(`<i class="c" style="background:` + defaultFG + `"></i>`)
			}
			body.WriteString("\n")
		}
		for _, line := range lines[max(0, skip-1):] {
			render(&body, line)
			body.WriteString("\n")
		}
		page := fmt.Sprintf(pageTemplate, w, h, background, titleBar,
			fontSize, lineHeight, defaultFG, padY, padX, lineHeight, html.EscapeString(title), body.String())
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(page), 0o644); err != nil {
			fail(err)
		}
		list = append(list, "file '"+name+".png'", fmt.Sprintf("duration %.2f", secs))
	}
	for i, s := range steps {
		cmd := []rune(s.Cmd)
		frame(cmd, 0, true, nil, promptPause)
		for t := typePerFrame; t < len(cmd); t += typePerFrame {
			frame(cmd, t, true, nil, typeDelay)
		}
		frame(cmd, len(cmd), true, nil, promptPause)
		lines := outputs[i]
		per := max(1, (len(lines)+revealFrames-1)/revealFrames)
		for k := per; k < len(lines); k += per {
			frame(cmd, len(cmd), false, lines[:k], revealDelay)
		}
		frame(cmd, len(cmd), false, lines, holdBase+holdPerLine*float64(len(lines)))
	}
	// ffconcat applies the last duration only when the last file is listed again.
	list = append(list, fmt.Sprintf("file 'f%04d.png'", n))
	if err := os.WriteFile(filepath.Join(dir, "frames.txt"), []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
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
