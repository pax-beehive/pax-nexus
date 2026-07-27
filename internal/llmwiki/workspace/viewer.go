package workspace

import (
	"bufio"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var sourceAnchorLinePattern = regexp.MustCompile(
	`^<a id="(msg-[a-f0-9]{16})"></a>$`,
)

type viewer struct {
	root string
}

type viewerPage struct {
	Title      string
	Kind       string
	Sidebar    template.HTML
	Article    template.HTML
	SourcePath string
}

func NewViewer(root string) http.Handler {
	return &viewer{root: root}
}

func (v *viewer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	relative, kind, err := viewerPath(request.URL)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	target := filepath.Join(v.root, filepath.FromSlash(relative))
	if err := ensureViewerTarget(v.root, target); err != nil {
		http.Error(writer, "invalid Wiki path", http.StatusBadRequest)
		return
	}
	content, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(writer, request)
			return
		}
		http.Error(writer, "read Wiki page", http.StatusInternalServerError)
		return
	}
	indexPath := filepath.Join(v.root, "wiki", "index.md")
	indexContent, err := os.ReadFile(indexPath)
	if err != nil {
		http.Error(writer, "read topic tree", http.StatusInternalServerError)
		return
	}
	page := viewerPage{
		Title:      markdownTitle(content, filepath.Base(relative)),
		Kind:       kind,
		Sidebar:    renderMarkdown(v.root, "wiki/index.md", indexContent),
		Article:    renderMarkdown(v.root, relative, content),
		SourcePath: relative,
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set(
		"Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'",
	)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method == http.MethodHead {
		writer.WriteHeader(http.StatusOK)
		return
	}
	if err := viewerTemplate.Execute(writer, page); err != nil {
		http.Error(writer, "render Wiki page", http.StatusInternalServerError)
	}
}

func viewerPath(requestURL *url.URL) (string, string, error) {
	escaped := requestURL.EscapedPath()
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return "", "", fmt.Errorf("decode request path: %w", err)
	}
	if decoded == "/" || decoded == "" {
		return "wiki/index.md", "Wiki Page", nil
	}
	var relative string
	var kind string
	switch {
	case strings.HasPrefix(decoded, "/wiki/"):
		relative = strings.TrimPrefix(decoded, "/")
		kind = "Wiki Page"
	case strings.HasPrefix(decoded, "/sources/"):
		relative = strings.TrimPrefix(decoded, "/")
		kind = "Immutable Source"
	default:
		return "", "", fmt.Errorf("unknown viewer path")
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", "", fmt.Errorf("path traversal is forbidden")
		}
	}
	if !strings.EqualFold(path.Ext(relative), ".md") {
		return "", "", fmt.Errorf("viewer serves Markdown files only")
	}
	return relative, kind, nil
}

func ensureViewerTarget(root, target string) error {
	if err := ensureWithinRoot(root, target); err != nil {
		return err
	}
	evaluated, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return ensureWithinRoot(root, evaluated)
}

func markdownTitle(content []byte, fallback string) string {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return fallback
}

func renderMarkdown(root, current string, content []byte) template.HTML {
	var rendered strings.Builder
	inCode := false
	inList := false
	for _, line := range logicalMarkdownLines(content) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inList {
				rendered.WriteString("</ul>")
				inList = false
			}
			if inCode {
				rendered.WriteString("</code></pre>")
			} else {
				rendered.WriteString("<pre><code>")
			}
			inCode = !inCode
			continue
		}
		if inCode {
			rendered.WriteString(html.EscapeString(line))
			rendered.WriteByte('\n')
			continue
		}
		renderMarkdownLine(root, current, trimmed, &rendered, &inList)
	}
	closeList(&rendered, &inList)
	if inCode {
		rendered.WriteString("</code></pre>")
	}
	return template.HTML(rendered.String()) //nolint:gosec // All source text is escaped above.
}

func logicalMarkdownLines(content []byte) []string {
	content = stripMarkdownFrontmatter(content)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	var result []string
	var pending string
	inCode := false
	flush := func() {
		if pending == "" {
			return
		}
		result = append(result, pending)
		pending = ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			flush()
			result = append(result, trimmed)
			inCode = !inCode
			continue
		}
		if inCode {
			result = append(result, line)
			continue
		}
		if trimmed == "" {
			flush()
			result = append(result, "")
			continue
		}
		if isStandaloneMarkdownLine(trimmed) {
			flush()
			pending = trimmed
			continue
		}
		if pending == "" {
			pending = trimmed
		} else {
			pending += " " + trimmed
		}
	}
	flush()
	return result
}

func stripMarkdownFrontmatter(content []byte) []byte {
	const open = "---\n"
	if !strings.HasPrefix(string(content), open) {
		return content
	}
	remainder := content[len(open):]
	end := strings.Index(string(remainder), "\n---\n")
	if end < 0 {
		return content
	}
	return remainder[end+len("\n---\n"):]
}

func isStandaloneMarkdownLine(line string) bool {
	return strings.HasPrefix(line, "# ") ||
		strings.HasPrefix(line, "## ") ||
		strings.HasPrefix(line, "### ") ||
		strings.HasPrefix(line, "- ") ||
		sourceAnchorLinePattern.MatchString(line)
}

func renderMarkdownLine(
	root,
	current,
	line string,
	rendered *strings.Builder,
	inList *bool,
) {
	if match := sourceAnchorLinePattern.FindStringSubmatch(line); len(match) == 2 {
		rendered.WriteString(`<a id="`)
		rendered.WriteString(match[1])
		rendered.WriteString(`"></a>`)
		return
	}
	for _, heading := range []struct {
		Prefix string
		Open   string
		Close  string
	}{
		{Prefix: "### ", Open: "<h3>", Close: "</h3>"},
		{Prefix: "## ", Open: "<h2>", Close: "</h2>"},
		{Prefix: "# ", Open: "<h1>", Close: "</h1>"},
	} {
		if strings.HasPrefix(line, heading.Prefix) {
			closeList(rendered, inList)
			rendered.WriteString(heading.Open)
			rendered.WriteString(renderInline(
				root, current, strings.TrimPrefix(line, heading.Prefix),
			))
			rendered.WriteString(heading.Close)
			return
		}
	}
	if strings.HasPrefix(line, "- ") {
		if !*inList {
			rendered.WriteString("<ul>")
			*inList = true
		}
		rendered.WriteString("<li>")
		rendered.WriteString(renderInline(root, current, strings.TrimPrefix(line, "- ")))
		rendered.WriteString("</li>")
		return
	}
	closeList(rendered, inList)
	if line == "" {
		return
	}
	rendered.WriteString("<p>")
	rendered.WriteString(renderInline(root, current, line))
	rendered.WriteString("</p>")
}

func closeList(rendered *strings.Builder, inList *bool) {
	if *inList {
		rendered.WriteString("</ul>")
		*inList = false
	}
}

func renderInline(root, current, line string) string {
	var rendered strings.Builder
	offset := 0
	for _, indexes := range markdownLinkPattern.FindAllStringSubmatchIndex(line, -1) {
		rendered.WriteString(html.EscapeString(line[offset:indexes[0]]))
		labelStart := indexes[0] + strings.Index(line[indexes[0]:indexes[1]], "[") + 1
		labelEnd := indexes[0] + strings.Index(line[indexes[0]:indexes[1]], "]")
		target := line[indexes[2]:indexes[3]]
		href := viewerHref(root, current, target)
		if href == "" || labelEnd < labelStart {
			rendered.WriteString(html.EscapeString(line[indexes[0]:indexes[1]]))
		} else {
			rendered.WriteString(`<a href="`)
			rendered.WriteString(html.EscapeString(href))
			rendered.WriteString(`">`)
			rendered.WriteString(html.EscapeString(line[labelStart:labelEnd]))
			rendered.WriteString("</a>")
		}
		offset = indexes[1]
	}
	rendered.WriteString(html.EscapeString(line[offset:]))
	return rendered.String()
}

func viewerHref(root, current, raw string) string {
	if isExternalLink(raw) {
		return raw
	}
	pathPart, fragment, _ := strings.Cut(raw, "#")
	resolved, err := resolveLink(
		root,
		filepath.Join(root, filepath.FromSlash(current)),
		pathPart,
	)
	if err != nil ||
		(!strings.HasPrefix(resolved, "wiki/") &&
			!strings.HasPrefix(resolved, "sources/")) {
		return ""
	}
	result := "/" + escapeURLPath(resolved)
	if fragment != "" {
		result += "#" + url.QueryEscape(fragment)
	}
	return result
}

func escapeURLPath(value string) string {
	parts := strings.Split(filepath.ToSlash(value), "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

var viewerTemplate = template.Must(template.New("viewer").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} · PAX Wiki</title>
  <style>
    :root { color-scheme: light; font-family: ui-sans-serif, system-ui, sans-serif; }
    body { margin: 0; color: #17211b; background: #f5f7f2; }
    header { padding: 1rem 2rem; background: #173f35; color: #fff; }
    header a { color: #fff; text-decoration: none; font-weight: 700; }
    .layout { display: grid; grid-template-columns: minmax(16rem, 24rem) 1fr; min-height: calc(100vh - 4rem); }
    aside { padding: 1.5rem; border-right: 1px solid #ccd7cf; background: #edf2ec; }
    main { padding: 2rem clamp(1.5rem, 5vw, 5rem); max-width: 70rem; }
    article { background: #fff; padding: clamp(1.5rem, 4vw, 3rem); box-shadow: 0 1px 8px #173f3517; }
    a { color: #08664f; text-underline-offset: .18em; }
    h1, h2, h3 { line-height: 1.2; }
    pre { overflow: auto; padding: 1rem; background: #10201a; color: #d9eee4; }
    code { font-family: ui-monospace, monospace; }
    .kind { text-transform: uppercase; letter-spacing: .08em; font-size: .75rem; color: #527064; }
    .path { color: #6b756f; font-family: ui-monospace, monospace; font-size: .8rem; }
    @media (max-width: 760px) { .layout { grid-template-columns: 1fr; } aside { border-right: 0; border-bottom: 1px solid #ccd7cf; } }
  </style>
</head>
<body>
  <header><a href="/">PAX Wiki</a></header>
  <div class="layout">
    <aside><div class="kind">Topic Tree</div>{{.Sidebar}}</aside>
    <main>
      <div class="kind">{{.Kind}}</div>
      <div class="path">{{.SourcePath}}</div>
      <article>{{.Article}}</article>
    </main>
  </div>
</body>
</html>`))
