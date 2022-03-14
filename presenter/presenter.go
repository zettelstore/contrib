//-----------------------------------------------------------------------------
// Copyright (c) 2021-2022 Detlef Stern
//
// This file is part of zettelstore slides application.
//
// Zettelstore slides application is licensed under the latest version of the
// EUPL (European Union Public License). Please see file LICENSE.txt for your
// rights and obligations under this license.
//-----------------------------------------------------------------------------

// Package main is the starting point for the slides command.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/term"

	"zettelstore.de/c/api"
	"zettelstore.de/c/client"
	"zettelstore.de/c/text"
	"zettelstore.de/c/zjson"
)

// Constants for minimum required version.
const (
	minMajor = 0
	minMinor = 5
)

func hasVersion(major, minor int) bool {
	if major < minMajor {
		return false
	}
	return minor >= minMinor
}

func main() {
	listenAddress := flag.String("l", ":23120", "Listen address")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		io.WriteString(out, "  [URL] URL of Zettelstore (default: \"http://127.0.0.1:23123\")\n")
	}
	flag.Parse()
	ctx := context.Background()
	c, err := getClient(ctx, flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to zettelstore: %v\n", err)
		os.Exit(2)
	}
	cfg, err := getConfig(ctx, c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to retrieve presenter config: %v\n", err)
		os.Exit(2)
	}

	// Fix an error in slidy.js
	slidy2js = strings.ReplaceAll(slidy2js, "</script>", "<\\/script>")

	http.HandleFunc("/", makeHandler(&cfg))
	http.Handle("/revealjs/", http.FileServer(http.FS(revealjs)))
	fmt.Println("Listening:", *listenAddress)
	http.ListenAndServe(*listenAddress, nil)
}

func getClient(ctx context.Context, base string) (*client.Client, error) {
	if base == "" {
		base = "http://127.0.0.1:23123"
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	withAuth, username, password := false, "", ""
	if uinfo := u.User; uinfo != nil {
		username = uinfo.Username()
		if pw, ok := uinfo.Password(); ok {
			password = pw
		}
		withAuth = true
		u.User = nil
	}
	c := client.NewClient(u)
	ver, err := c.GetVersionJSON(ctx)
	if err != nil {
		return nil, err
	}
	if ver.Major == -1 {
		fmt.Fprintln(os.Stderr, "Unknown zettelstore version. Use it at your own risk,")
	} else if !hasVersion(ver.Major, ver.Minor) {
		return nil, fmt.Errorf("need at least zettelstore version %d.%d", minMajor, minMinor)
	}

	if !withAuth {
		err = c.ExecuteCommand(ctx, api.CommandAuthenticated)
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusUnauthorized {
			withAuth = true
		}
	}

	if withAuth {
		if username == "" {
			io.WriteString(os.Stderr, "Username: ")
			_, err := fmt.Fscanln(os.Stdin, &username)
			if err != nil {
				return nil, err
			}
		}
		if password == "" {
			io.WriteString(os.Stderr, "Password: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			io.WriteString(os.Stderr, "\n")
			if err != nil {
				return nil, err
			}
			password = string(pw)
		}
		c.SetAuth(username, password)
		err := c.Authenticate(ctx)
		if err != nil {
			return nil, err
		}
	}

	return c, nil
}

const configZettel = api.ZettelID("00009000001000")

type slidesConfig struct {
	c            *client.Client
	slideSetRole string
	author       string
}

func getConfig(ctx context.Context, c *client.Client) (slidesConfig, error) {
	result := slidesConfig{
		c:            c,
		slideSetRole: DefaultSlideSetRole,
	}
	m, err := c.GetMeta(ctx, configZettel)
	if err != nil {
		return slidesConfig{}, err
	}
	if ssr, ok := m[KeySlideSetRole]; ok {
		result.slideSetRole = ssr
	}
	if author, ok := m[KeyAuthor]; ok {
		result.author = author
	}
	return result, nil
}

func makeHandler(cfg *slidesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if zid, suffix := retrieveZidAndSuffix(path); zid != api.InvalidZID {
			switch suffix {
			case "slidy", "slide":
				processSlideSet(w, r, cfg, zid, renderSlidyShow)
			case "reveal":
				processSlideSet(w, r, cfg, zid, renderRevealShow)
			case "html":
				processSlideSet(w, r, cfg, zid, renderHandout)
			case "content":
				if content := processContent(w, r, cfg.c, zid); len(content) > 0 {
					w.Write(content)
				}
			case "svg":
				if content := processContent(w, r, cfg.c, zid); len(content) > 0 {
					io.WriteString(w, `<?xml version='1.0' encoding='utf-8'?>`)
					w.Write(content)
				}
			default:
				processZettel(w, r, cfg.c, zid, cfg.slideSetRole)
			}
			return
		}
		if len(path) == 2 && ' ' < path[1] && path[1] <= 'z' {
			processList(w, r, cfg.c)
			return
		}
		log.Println("NOTF", path)
		http.Error(w, fmt.Sprintf("Unhandled request %q", r.URL), http.StatusNotFound)
	}
}

func retrieveZidAndSuffix(path string) (api.ZettelID, string) {
	if path == "" {
		return api.InvalidZID, ""
	}
	if path == "/" {
		return api.ZidDefaultHome, ""
	}
	if path[0] == '/' {
		path = path[1:]
	}
	if len(path) < api.LengthZid {
		return api.InvalidZID, ""
	}
	zid := api.ZettelID(path[:api.LengthZid])
	if !zid.IsValid() {
		return api.InvalidZID, ""
	}
	if len(path) == api.LengthZid {
		return zid, ""
	}
	if path[api.LengthZid] != '.' {
		return api.InvalidZID, ""
	}
	if suffix := path[api.LengthZid+1:]; suffix != "" {
		return zid, suffix
	}
	return api.InvalidZID, ""
}

func processContent(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID) []byte {
	content, err := c.GetZettel(r.Context(), zid, api.PartContent)
	if err != nil {
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
			http.Error(w, fmt.Sprintf("Content %s not found", zid), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Error retrieving content %s: %s", zid, err), http.StatusBadRequest)
		}
	}
	return content
}

func processZettel(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID, slidesSetRole string) {
	ctx := r.Context()
	zjZettel, err := c.GetEvaluatedZJSON(ctx, zid, api.PartZettel, false)
	if err != nil {
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
			http.Error(w, fmt.Sprintf("Zettel %s not found", zid), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Error retrieving zettel %s: %s", zid, err), http.StatusBadRequest)
		}
		return
	}
	m, content := zjson.GetMetaContent(zjZettel)
	if m == nil || content == nil {
		http.Error(w, fmt.Sprintf("Zettel %s has no meta/content", zid), http.StatusInternalServerError)
		return
	}
	role := m.GetString(api.KeyRole)
	if role == slidesSetRole {
		if slides := processSlideTOC(ctx, c, zid, m); slides != nil {
			renderSlideTOC(w, slides)
			return
		}
	}

	zv := zettelVisitor{}
	zjson.WalkBlock(&zv, content, 0)

	title := getSlideTitleZid(m, zid)
	writeHTMLHeader(w, m.GetString(api.KeyLang))
	fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	writeHTMLBody(w)
	he := htmlNew(w, nil, 1, false, true, true)
	fmt.Fprintf(w, "<h1>%s</h1>\n", htmlEncodeInline(he, title))
	hasHeader := false
	for k, v := range m {
		if v.Type != zjson.MetaURL {
			continue
		}
		u := zjson.MakeString(v.Value)
		if u == "" {
			continue
		}
		if !hasHeader {
			io.WriteString(w, "<ul class=\"header\">\n")
			hasHeader = true
		}
		fmt.Fprintf(w, "<li>%s: <a href=\"%s\" target=\"_blank\">%s</a>&#10138;</li>", html.EscapeString(k), u, html.EscapeString(u))
	}
	if hasHeader {
		io.WriteString(w, "</ul>\n")
	}

	zjson.WalkBlock(he, content, 0)
	he.visitEndnotes()
	fmt.Fprintf(w, "<p><a href=\"%sh/%s\">&#9838;</a></p>\n", c.Base(), zid)
	writeHTMLFooter(w, zv.hasMermaid)
}

func processSlideTOC(ctx context.Context, c *client.Client, zid api.ZettelID, m zjson.Meta) *slideSet {
	o, err := c.GetZettelOrder(ctx, zid)
	if err != nil {
		return nil
	}
	slides := newSlideSetMeta(zid, m)
	getZettel := func(zid api.ZettelID) ([]byte, error) { return c.GetZettel(ctx, zid, api.PartContent) }
	getZettelZJSON := func(zid api.ZettelID) (zjson.Value, error) {
		return c.GetEvaluatedZJSON(ctx, zid, api.PartZettel, true)
	}
	setupSlideSet(slides, o.List, getZettel, getZettelZJSON)
	return slides
}

func renderSlideTOC(w http.ResponseWriter, slides *slideSet) {
	offset, title, htmlTitle, subtitle := 1, slides.Title(), "", slides.Subtitle()
	if len(title) > 0 {
		offset++
		htmlTitle = htmlEncodeInline(nil, title)
	}

	writeHTMLHeader(w, slides.Lang())
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	}
	writeHTMLBody(w)
	if len(title) > 0 {
		fmt.Fprintf(w, "<h1>%s</h1>\n", htmlTitle)
		if len(subtitle) > 0 {
			fmt.Fprintf(w, "<h2>%s</h2>\n", htmlEncodeInline(nil, subtitle))
		}
	}
	io.WriteString(w, "<ol>\n")
	if len(title) > 0 {
		fmt.Fprintf(w, "<li><a href=\"/%s.slide#(1)\">%s</a></li>\n", slides.zid, htmlTitle)
	}
	for si := slides.Slides(SlideRoleShow, offset); si != nil; si = si.Next() {
		var slideTitle string
		if t := si.Slide.Title(); len(t) > 0 {
			slideTitle = htmlEncodeInline(nil, t)
		} else {
			slideTitle = string(si.Slide.zid)
		}
		fmt.Fprintf(w, "<li><a href=\"/%s.slide#(%d)\">%s</a></li>\n", slides.zid, si.Number, slideTitle)
	}
	io.WriteString(w, "</ol>\n")
	fmt.Fprintf(w, "<p><a href=\"/%s.reveal\">Reveal</a>, <a href=\"/%s.html\">Handout</a>, <a href=\"\">Zettel</a></p>\n", slides.zid, slides.zid)
	writeHTMLFooter(w, false)
}

func processSlideSet(w http.ResponseWriter, r *http.Request, cfg *slidesConfig, zid api.ZettelID, render renderSlidesFunc) {
	ctx := r.Context()
	o, err := cfg.c.GetZettelOrder(ctx, zid)
	if err != nil {
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
			http.Error(w, fmt.Sprintf("Zettel %s not found", zid), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Unable to read zettel %s: %v", zid, err), http.StatusBadRequest)
		}
		return
	}
	zjMeta, err := cfg.c.GetEvaluatedZJSON(ctx, zid, api.PartMeta, false)
	if err != nil {
		http.Error(w, fmt.Sprintf("Unable to read zettel %s: %v", zid, err), http.StatusBadRequest)
	}
	slides := newSlideSet(zid, zjMeta)
	getZettel := func(zid api.ZettelID) ([]byte, error) { return cfg.c.GetZettel(ctx, zid, api.PartContent) }
	getZettelZJSON := func(zid api.ZettelID) (zjson.Value, error) {
		return cfg.c.GetEvaluatedZJSON(ctx, zid, api.PartZettel, false)
	}
	setupSlideSet(slides, o.List, getZettel, getZettelZJSON)
	render(w, slides, slides.Author(cfg))
}

type renderSlidesFunc func(http.ResponseWriter, *slideSet, string)

func renderSlidyShow(w http.ResponseWriter, slides *slideSet, author string) {
	lang := slides.Lang()
	writeHTMLHeader(w, lang)
	title := slides.Title()
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	}
	writeMeta(w, "author", author)
	writeMeta(w, "copyright", slides.Copyright())
	writeMeta(w, "license", slides.License())
	// writeMeta(w, "font-size-adjustment", "+1")
	fmt.Fprintf(w, "<style type=\"text/css\" media=\"screen, projection, print\">\n%s</style>\n", slidy2css)
	writeHTMLBody(w)

	offset := 1
	if len(title) > 0 {
		offset++
		io.WriteString(w, "<div class=\"slide titlepage\">\n")
		fmt.Fprintf(w, "<h1 class=\"title\">%s</h1>\n", htmlEncodeInline(nil, title))
		if subtitle := slides.Subtitle(); len(subtitle) > 0 {
			fmt.Fprintf(w, "<p class=\"subtitle\">%s</p>\n", htmlEncodeInline(nil, subtitle))
		}
		if author != "" {
			fmt.Fprintf(w, "<p class=\"author\">%s</p>\n", html.EscapeString(author))
		}
		io.WriteString(w, "\n</div>\n")
	}
	he := htmlNew(w, slides, 1, false, true, true)
	for si := slides.Slides(SlideRoleShow, offset); si != nil; si = si.Next() {
		sl := si.Slide
		io.WriteString(w, `<div class="slide"`)
		if slLang := sl.Lang(); slLang != "" && slLang != lang {
			fmt.Fprintf(w, ` lang="%s"`, slLang)
		}
		io.WriteString(w, ">\n")
		if title := sl.Title(); len(title) > 0 {
			fmt.Fprintf(w, "<h1>%s</h1>\n", htmlEncodeInline(he, title))
		}

		he.SetCurrentSlide(si)
		he.SetUnique(fmt.Sprintf("%d:", si.Number))
		zjson.WalkBlock(he, sl.Content(), 0)
		he.visitEndnotes()
		fmt.Fprintf(w, "<p><a href=\"%s\" target=\"_blank\">&#9838;</a></p>\n", sl.zid)
		io.WriteString(w, "</div>\n")
	}
	fmt.Fprintf(w, "<script type=\"text/javascript\">\n//<![CDATA[\n%s//]]>\n</script>\n", slidy2js)
	writeHTMLFooter(w, slides.hasMermaid)
}

func renderRevealShow(w http.ResponseWriter, slides *slideSet, author string) {
	lang := slides.Lang()
	writeHTMLHeader(w, lang)
	title := slides.Title()
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	}

	io.WriteString(w, "<link rel=\"stylesheet\" href=\"revealjs/reveal.css\">\n")
	io.WriteString(w, "<link rel=\"stylesheet\" href=\"revealjs/theme/white.css\">\n")
	writeHTMLBody(w)

	io.WriteString(w, "<div class=\"reveal\">\n")
	io.WriteString(w, "<div class=\"slides\">\n")
	offset := 1
	if len(title) > 0 {
		offset++
		io.WriteString(w, "<section>\n")
		fmt.Fprintf(w, "<h1 class=\"title\">%s</h1>\n", htmlEncodeInline(nil, title))
		if subtitle := slides.Subtitle(); len(subtitle) > 0 {
			fmt.Fprintf(w, "<p class=\"subtitle\">%s</p>\n", htmlEncodeInline(nil, subtitle))
		}
		if author != "" {
			fmt.Fprintf(w, "<p class=\"author\">%s</p>\n", html.EscapeString(author))
		}
		io.WriteString(w, "\n</section>\n")
	}
	he := htmlNew(w, slides, 1, false, true, true)
	for si := slides.Slides(SlideRoleShow, offset); si != nil; si = si.Next() {
		sl := si.Slide
		fmt.Fprintf(w, `<section id="(%d)"`, si.SlideNo)
		if slLang := sl.Lang(); slLang != "" && slLang != lang {
			fmt.Fprintf(w, ` lang="%s"`, slLang)
		}
		io.WriteString(w, ">\n")
		if title := sl.Title(); len(title) > 0 {
			fmt.Fprintf(w, "<h1>%s</h1>\n", htmlEncodeInline(he, title))
		}

		he.SetCurrentSlide(si)
		he.SetUnique(fmt.Sprintf("%d:", si.Number))
		zjson.WalkBlock(he, sl.Content(), 0)
		he.visitEndnotes()
		fmt.Fprintf(w, "<p><a href=\"%s\" target=\"_blank\">&#9838;</a></p>\n", sl.zid)
		io.WriteString(w, "</section>\n")
	}
	io.WriteString(w, "</div>\n</div>\n")
	io.WriteString(w, `<script src="revealjs/reveal.js"></script>
<script>Reveal.initialize({
width: 1920,
height: 1024,
center: true,
slideNumber: "c",
hash: true,
showNotes: true
});</script>
`)
	writeHTMLFooter(w, slides.hasMermaid)
}

func renderHandout(w http.ResponseWriter, slides *slideSet, author string) {
	lang := slides.Lang()
	writeHTMLHeader(w, lang)
	title := slides.Title()
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	}
	writeMeta(w, "author", author)
	copyright := slides.Copyright()
	writeMeta(w, "copyright", copyright)
	license := slides.License()
	writeMeta(w, "license", license)
	writeHTMLBody(w)

	offset := 1
	if len(title) > 0 {
		offset++
		fmt.Fprintf(w, "<h1 id=\"(1)\">%s</h1>\n", htmlEncodeInline(nil, title))
		if subtitle := slides.Subtitle(); len(subtitle) > 0 {
			fmt.Fprintf(w, "<h2>%s</h2>\n", htmlEncodeInline(nil, subtitle))
		}
		if author != "" {
			fmt.Fprintf(w, "<p>%s</p>\n", html.EscapeString(author))
		}
		if copyright != "" {
			fmt.Fprintf(w, "<p>%s</p>\n", html.EscapeString(copyright))
		}
		if license != "" {
			fmt.Fprintf(w, "<p>%s</p>\n", html.EscapeString(license))
		}
	}
	he := htmlNew(w, slides, 1, true, false, false)
	for si := slides.Slides(SlideRoleHandout, offset); si != nil; si = si.Next() {
		he.SetCurrentSlide(si)
		sl := si.Slide
		if title := sl.Title(); len(title) > 0 {
			if si.SlideNo > 0 {
				fmt.Fprintf(w, "<h1 id=\"(%d)\">%s <small>(S.%d)</small></h1>\n", si.Number, htmlEncodeInline(he, title), si.SlideNo)
			} else {
				fmt.Fprintf(w, "<h1 id=\"(%d)\"> %s</h1>\n", si.Number, htmlEncodeInline(he, title))
			}
		} else {
			fmt.Fprintf(w, "<a id=\"(%d)\"></a>", si.Number)
		}
		slLang := sl.Lang()
		if slLang != "" && slLang != lang {
			fmt.Fprintf(w, `<div lang="%s">`, slLang)
		}

		he.SetUnique(fmt.Sprintf("%d:", si.Number))
		zjson.WalkBlock(he, sl.Content(), 0)
		if slLang != "" && slLang != lang {
			io.WriteString(w, "</div>")
		}
	}
	he.visitEndnotes()
	writeHTMLFooter(w, slides.hasMermaid)
}

func setupSlideSet(slides *slideSet, l []api.ZidMetaJSON, getZettel getZettelContentFunc, getZettelZJSON getZettelZSONFunc) {
	for _, sl := range l {
		slides.AddSlide(sl.ID, getZettelZJSON)
	}
	slides.Completion(getZettel, getZettelZJSON)
}

func processList(w http.ResponseWriter, r *http.Request, c *client.Client) {
	ctx := r.Context()
	query, zl, err := c.ListZettelJSON(ctx, r.URL.Query())
	if err != nil {
		http.Error(w, fmt.Sprintf("Error retrieving zettel list %s: %s\n", r.URL.Query(), err), http.StatusBadRequest)
		return
	}
	titles := make([]string, len(zl))
	for i, jm := range zl {
		if zjMeta, err := c.GetEvaluatedZJSON(ctx, jm.ID, api.PartMeta, false); err == nil {
			titles[i] = htmlEncodeInline(nil, getZettelTitleZid(zjson.MakeMeta(zjMeta), jm.ID))
		}
	}

	var title string
	if query == "" {
		title = "All zettel"
		query = title
	} else {
		title = "Selected zettel"
		query = "Search: " + query
	}
	writeHTMLHeader(w, "")
	fmt.Fprintf(w, "<title>%s</title>\n", title)
	writeHTMLBody(w)
	fmt.Fprintf(w, "<h1>%s</h1>\n", html.EscapeString(query))
	io.WriteString(w, "<ul>\n")
	for i, jm := range zl {
		fmt.Fprintf(
			w,
			"<li><a href=\"%s\">%s</a></li>\n",
			jm.ID,
			titles[i],
		)
	}
	io.WriteString(w, "</ul>\n")
	writeHTMLFooter(w, false)
}

func writeHTMLHeader(w http.ResponseWriter, lang string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!DOCTYPE html>\n")
	if lang == "" {
		io.WriteString(w, "<html>\n")
	} else {
		fmt.Fprintf(w, "<html lang=\"%s\">\n", lang)
	}
	io.WriteString(w, "<head>\n")
	io.WriteString(w, "<meta charset=\"utf-8\">\n")
	io.WriteString(w, "<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no\">\n")
	io.WriteString(w, "<meta name=\"generator\" content=\"Zettel Presenter\">\n")
	fmt.Fprintf(w, "<style type=\"text/css\" media=\"screen, projection, print\">\n%s</style>\n", mycss)
}

func writeHTMLBody(w http.ResponseWriter) { io.WriteString(w, "</head>\n<body>\n") }
func writeHTMLFooter(w http.ResponseWriter, hasMermaid bool) {
	if hasMermaid {
		fmt.Fprintf(w, "<script type=\"text/javascript\">\n//<![CDATA[\n%s//]]>\n</script>\n", mermaid)
		io.WriteString(w, "<script>mermaid.initialize({startOnLoad:true});</script>\n")
	}
	io.WriteString(w, "</body>\n</html>\n")
}
func writeMeta(w http.ResponseWriter, key, val string) {
	if val != "" {
		fmt.Fprintf(w, "<meta name=\"%s\" content=\"%s\" />\n", key, html.EscapeString(val))
	}
}

//go:embed slidy2/slidy.css
var slidy2css string

//go:embed slidy2/slidy.js
var slidy2js string

//go:embed mermaid/mermaid.min.js
var mermaid string

//go:embed revealjs
var revealjs embed.FS

var mycss = `/* Additional CSS to make it a little more beautiful */
td.left, th.left { text-align: left }
td.center, th.center { text-align: center }
td.right, th.right { text-align: right }
img.right { float:right }
ol.endnotes { padding-top: .5rem; border-top: 1px solid; font-size: smaller; margin-left: 2em; }
a.external {}
a.broken { text-decoration: line-through }
ul.header { list-style-type: none; margin: 0; padding: 0;}
blockquote {
  border-left: 0.5rem solid lightgray;
  padding-left: 1rem;
  margin-left: 1rem;
  margin-right: 2rem;
  font-style: italic;
}
blockquote p { margin-bottom: .5rem }
blockquote cite { font-style: normal }
`
