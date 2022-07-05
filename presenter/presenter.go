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

	"codeberg.org/t73fde/sxpf"
	"golang.org/x/term"

	"zettelstore.de/c/api"
	"zettelstore.de/c/client"
	"zettelstore.de/c/sexpr"
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
		fmt.Fprintln(os.Stderr, "Unknown zettelstore version. Use it at your own risk.")
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

const (
	zidConfig   = api.ZettelID("00009000001000")
	zidSlideCSS = api.ZettelID("00009000001005")
)

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
	m, err := c.GetMeta(ctx, zidConfig)
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
			case "reveal", "slide":
				processSlideSet(w, r, cfg, zid, &revealRenderer{})
			case "html":
				processSlideSet(w, r, cfg, zid, &handoutRenderer{})
			case "content":
				if content := retrieveContent(w, r, cfg.c, zid); len(content) > 0 {
					w.Write(content)
				}
			case "svg":
				if content := retrieveContent(w, r, cfg.c, zid); len(content) > 0 {
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

func retrieveContent(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID) []byte {
	content, err := c.GetZettel(r.Context(), zid, api.PartContent)
	if err != nil {
		reportRetrieveError(w, zid, err, "content")
		return nil
	}
	return content
}

func reportRetrieveError(w http.ResponseWriter, zid api.ZettelID, err error, objName string) {
	var cerr *client.Error
	if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
		http.Error(w, fmt.Sprintf("%s %s not found", objName, zid), http.StatusNotFound)
	} else {
		http.Error(w, fmt.Sprintf("Error retrieving %s %s: %s", zid, objName, err), http.StatusBadRequest)
	}
}

func processZettel(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID, slidesSetRole string) {
	ctx := r.Context()
	zjZettel, err := c.GetEvaluatedZJSON(ctx, zid, api.PartZettel)
	if err != nil {
		reportRetrieveError(w, zid, err, "zettel")
		return
	}
	zm, zContent := zjson.GetMetaContent(zjZettel)
	if zm == nil || zContent == nil {
		http.Error(w, fmt.Sprintf("Zettel %s has no meta/content", zid), http.StatusInternalServerError)
		return
	}

	sxZettel, err := c.GetEvaluatedSexpr(ctx, sexpr.Smk, zid, api.PartZettel)
	if err != nil {
		reportRetrieveError(w, zid, err, "zettel")
		return
	}
	sxMeta, _ := sexpr.GetMetaContent(sxZettel)

	role := sxMeta.GetString(api.KeyRole)
	if role == slidesSetRole {
		if slides := processSlideTOC(ctx, c, zid, zm, sxMeta); slides != nil {
			renderSlideTOC(w, slides)
			return
		}
	}

	zv := zettelVisitor{}
	zjson.WalkBlock(&zv, zContent, 0)

	title := getSlideTitleZid(sxMeta, zid)
	writeHTMLHeader(w, sxMeta.GetString(api.KeyLang), "")
	fmt.Fprintf(w, "<title>%s</title>\n", text.EvaluateInlineString(title))
	writeHTMLBody(w)
	he := htmlNew(w, nil, nil, 1, false, true)
	fmt.Fprintf(w, "<h1>%s</h1>\n", evaluateInline(he, title))
	hasHeader := false
	for k, v := range sxMeta {
		if v.Type != api.MetaURL {
			continue
		}
		strVal := sexpr.MakeString(v.Value)
		if strVal == "" {
			continue
		}
		if !hasHeader {
			io.WriteString(w, "<ul class=\"header\">\n")
			hasHeader = true
		}
		fmt.Fprintf(w, "<li>%s: <a href=\"%s\" target=\"_blank\">%s</a>&#10138;</li>", html.EscapeString(k), strVal, html.EscapeString(strVal))
	}
	if hasHeader {
		io.WriteString(w, "</ul>\n")
	}

	he.TraverseBlock(zContent)
	he.WriteEndnotes()
	fmt.Fprintf(w, "<p><a href=\"%sh/%s\">&#9838;</a></p>\n", c.Base(), zid)
	writeHTMLFooter(w, zv.hasMermaid)
}

func processSlideTOC(ctx context.Context, c *client.Client, zid api.ZettelID, zm zjson.Meta, sxMeta sexpr.Meta) *slideSet {
	o, err := c.GetZettelOrder(ctx, zid)
	if err != nil {
		return nil
	}
	slides := newSlideSetMeta(zid, zm, sxMeta)
	getZettel := func(zid api.ZettelID) ([]byte, error) { return c.GetZettel(ctx, zid, api.PartContent) }
	zGetZettel := func(zid api.ZettelID) (zjson.Value, error) {
		return c.GetEvaluatedZJSON(ctx, zid, api.PartZettel)
	}
	sGetZettel := func(zid api.ZettelID) (sxpf.Value, error) {
		return c.GetEvaluatedSexpr(ctx, sexpr.Smk, zid, api.PartZettel)
	}
	setupSlideSet(slides, o.List, getZettel, zGetZettel, sGetZettel)
	return slides
}

func renderSlideTOC(w http.ResponseWriter, slides *slideSet) {
	offset, zTitle, htmlTitle, zSubtitle := 1, slides.ZTitle(), "", slides.ZSubtitle()
	if len(zTitle) > 0 {
		offset++
		htmlTitle = zEncodeInline(nil, zTitle)
	}

	writeHTMLHeader(w, slides.Lang(), "")
	zWriteTitle(w, zTitle)
	writeHTMLBody(w)
	if len(zTitle) > 0 {
		fmt.Fprintf(w, "<h1>%s</h1>\n", htmlTitle)
		if len(zSubtitle) > 0 {
			fmt.Fprintf(w, "<h2>%s</h2>\n", zEncodeInline(nil, zSubtitle))
		}
	}
	io.WriteString(w, "<ol>\n")
	if len(zTitle) > 0 {
		fmt.Fprintf(w, "<li><a href=\"/%s.slide#(1)\">%s</a></li>\n", slides.zid, htmlTitle)
	}
	for si := slides.Slides(SlideRoleShow, offset); si != nil; si = si.Next() {
		var slideTitle string
		if zt := si.Slide.ZTitle(); len(zt) > 0 {
			slideTitle = zEncodeInline(nil, zt)
		} else {
			slideTitle = string(si.Slide.zid)
		}
		fmt.Fprintf(w, "<li><a href=\"/%s.slide#(%d)\">%s</a></li>\n", slides.zid, si.Number, slideTitle)
	}
	io.WriteString(w, "</ol>\n")
	fmt.Fprintf(w, "<p><a href=\"/%s.reveal\">Reveal</a>, <a href=\"/%s.html\">Handout</a>, <a href=\"\">Zettel</a></p>\n", slides.zid, slides.zid)
	writeHTMLFooter(w, false)
}

func processSlideSet(w http.ResponseWriter, r *http.Request, cfg *slidesConfig, zid api.ZettelID, ren renderer) {
	ctx := r.Context()
	o, err := cfg.c.GetZettelOrder(ctx, zid)
	if err != nil {
		reportRetrieveError(w, zid, err, "zettel")
		return
	}
	zjMeta, err := cfg.c.GetEvaluatedZJSON(ctx, zid, api.PartMeta)
	if err != nil {
		http.Error(w, fmt.Sprintf("Unable to read zettel %s: %v", zid, err), http.StatusBadRequest)
		return
	}
	sMeta, err := cfg.c.GetEvaluatedSexpr(ctx, sexpr.Smk, zid, api.PartMeta)
	if err != nil {
		http.Error(w, fmt.Sprintf("Unable to read zettel %s: %v", zid, err), http.StatusBadRequest)
		return
	}
	slides := newSlideSet(zid, zjMeta, sexpr.MakeMeta(sMeta))
	getZettel := func(zid api.ZettelID) ([]byte, error) { return cfg.c.GetZettel(ctx, zid, api.PartContent) }
	zGetZettel := func(zid api.ZettelID) (zjson.Value, error) {
		return cfg.c.GetEvaluatedZJSON(ctx, zid, api.PartZettel)
	}
	sGetZettel := func(zid api.ZettelID) (sxpf.Value, error) {
		return cfg.c.GetEvaluatedSexpr(ctx, sexpr.Smk, zid, api.PartZettel)
	}
	setupSlideSet(slides, o.List, getZettel, zGetZettel, sGetZettel)
	ren.Prepare(ctx, cfg)
	ren.Render(w, slides, slides.Author(cfg))
}

type renderer interface {
	Role() string
	Prepare(context.Context, *slidesConfig)
	Render(w http.ResponseWriter, slides *slideSet, author string)
}

type revealRenderer struct {
	userCSS []byte
}

func (*revealRenderer) Role() string { return SlideRoleShow }
func (rr *revealRenderer) Prepare(ctx context.Context, cfg *slidesConfig) {
	if data, err := cfg.c.GetZettel(ctx, zidSlideCSS, api.PartContent); err == nil {
		rr.userCSS = data
	}
}
func (rr *revealRenderer) Render(w http.ResponseWriter, slides *slideSet, author string) {
	lang := slides.Lang()
	writeHTMLHeader(w, lang, ".reveal ")
	if len(rr.userCSS) > 0 {
		io.WriteString(w, `<style type="text/css">`)
		w.Write(rr.userCSS)
		io.WriteString(w, "</style>\n")
	}

	zTitle := slides.ZTitle()
	zWriteTitle(w, zTitle)
	io.WriteString(w, `<link rel="stylesheet" href="revealjs/reveal.css">
<link rel="stylesheet" href="revealjs/theme/white.css">
<link rel="stylesheet" href="revealjs/plugin/highlight/default.css">
`)
	writeHTMLBody(w)

	io.WriteString(w, "<div class=\"reveal\">\n<div class=\"slides\">\n")
	offset := 1
	if len(zTitle) > 0 {
		offset++
		fmt.Fprintf(w, "<section>\n<h1 class=\"title\">%s</h1>", zEncodeInline(nil, zTitle))
		if zSubtitle := slides.ZSubtitle(); len(zSubtitle) > 0 {
			fmt.Fprintf(w, "\n<p class=\"subtitle\">%s</p>", zEncodeInline(nil, zSubtitle))
		}
		if author != "" {
			fmt.Fprintf(w, "\n<p class=\"author\">%s</p>", html.EscapeString(author))
		}
		io.WriteString(w, "\n</section>\n")
	}
	he := htmlNew(w, slides, rr, 1, false, true)
	for si := slides.Slides(SlideRoleShow, offset); si != nil; si = si.Next() {
		he.SetCurrentSlide(si)
		main := si.Child()
		sub := main.Next()
		if sub != nil {
			io.WriteString(w, "<section>\n")
		}
		fmt.Fprintf(w, `<section id="(%d)"`, main.SlideNo)
		if slLang := main.Slide.Lang(); slLang != "" && slLang != lang {
			fmt.Fprintf(w, ` lang="%s"`, slLang)
		}
		io.WriteString(w, ">\n")
		renderRevealSlide(w, he, main)
		io.WriteString(w, "</section>\n")

		if sub != nil {
			for {
				fmt.Fprintf(w, "<section id=\"(%d)\">\n", sub.SlideNo)
				renderRevealSlide(w, he, sub)
				io.WriteString(w, "</section>\n")
				sub = sub.Next()
				if sub == nil {
					break
				}
			}
			io.WriteString(w, "</section>\n")
		}
	}
	io.WriteString(w, `</div>
</div>
<script src="revealjs/plugin/highlight/highlight.js"></script>
<script src="revealjs/plugin/notes/notes.js"></script>
<script src="revealjs/reveal.js"></script>
<script>Reveal.initialize({width: 1920, height: 1024, center: true,
slideNumber: "c", hash: true,
plugins: [ RevealHighlight, RevealNotes ]});</script>
`)
	writeHTMLFooter(w, slides.hasMermaid)
}

func zWriteTitle(w http.ResponseWriter, title zjson.Array) {
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	}
}

func renderRevealSlide(w http.ResponseWriter, he *htmlV, si *slideInfo) {
	if title := si.Slide.ZTitle(); len(title) > 0 {
		fmt.Fprintf(w, "<h1>%s</h1>", zEncodeInline(he, title))
	}
	he.SetUnique(fmt.Sprintf("%d:", si.Number))
	he.TraverseBlock(si.Slide.ZContent())
	he.WriteEndnotes()
	fmt.Fprintf(w, "\n<p><a href=\"%s\" target=\"_blank\">&#9838;</a></p>\n", si.Slide.zid)
}

type handoutRenderer struct{}

func (*handoutRenderer) Role() string                           { return SlideRoleHandout }
func (*handoutRenderer) Prepare(context.Context, *slidesConfig) {}
func (hr *handoutRenderer) Render(w http.ResponseWriter, slides *slideSet, author string) {
	lang := slides.Lang()
	writeHTMLHeader(w, lang, "")
	io.WriteString(w, `<style type="text/css">
blockquote {
  border-left: 0.5rem solid lightgray;
  padding-left: 1rem;
  margin-left: 1rem;
  margin-right: 2rem;
  font-style: italic;
}
blockquote p { margin-bottom: .5rem }
blockquote cite { font-style: normal }
</style>
`)

	zTitle := slides.ZTitle()
	zWriteTitle(w, zTitle)
	writeMeta(w, "author", author)
	copyright := slides.Copyright()
	writeMeta(w, "copyright", copyright)
	license := slides.License()
	writeMeta(w, "license", license)
	writeHTMLBody(w)

	offset := 1
	if len(zTitle) > 0 {
		offset++
		fmt.Fprintf(w, "<h1 id=\"(1)\">%s</h1>\n", zEncodeInline(nil, zTitle))
		if zSubtitle := slides.ZSubtitle(); len(zSubtitle) > 0 {
			fmt.Fprintf(w, "<h2>%s</h2>\n", zEncodeInline(nil, zSubtitle))
		}
		writeEscapedString(w, author)
		writeEscapedString(w, copyright)
		writeEscapedString(w, license)
	}
	he := htmlNew(w, slides, hr, 1, true, false)
	for si := slides.Slides(SlideRoleHandout, offset); si != nil; si = si.Next() {
		he.SetCurrentSlide(si)
		sl := si.Slide
		if zTitle := sl.ZTitle(); len(zTitle) > 0 {
			fmt.Fprintf(w, "<h1 id=\"(%d)\"> %s%s</h1>\n", si.Number, zEncodeInline(he, zTitle), slideNoRange(si))
		} else {
			fmt.Fprintf(w, "<a id=\"(%d)\"></a>", si.Number)
		}
		slLang := sl.Lang()
		if slLang != "" && slLang != lang {
			fmt.Fprintf(w, `<div lang="%s">`, slLang)
		}

		he.SetUnique(fmt.Sprintf("%d:", si.Number))
		he.TraverseBlock(sl.ZContent())
		if slLang != "" && slLang != lang {
			io.WriteString(w, "</div>")
		}
	}
	he.WriteEndnotes()
	writeHTMLFooter(w, slides.hasMermaid)
}

func writeEscapedString(w http.ResponseWriter, s string) {
	if s != "" {
		fmt.Fprintf(w, "<p>%s</p>\n", html.EscapeString(s))
	}
}

func slideNoRange(si *slideInfo) string {
	if fromSlideNo := si.SlideNo; fromSlideNo > 0 {
		toSlideNo := si.LastChild().SlideNo
		if fromSlideNo >= toSlideNo {
			return fmt.Sprintf(" <small>(S.%d)</small>", fromSlideNo)
		}
		return fmt.Sprintf(" <small>(S.%d&ndash;%d)</small>", fromSlideNo, toSlideNo)
	}
	return ""
}

func setupSlideSet(slides *slideSet, l []api.ZidMetaJSON, getZettel getZettelContentFunc, zGetZettel zGetZettelFunc, sGetZettel sGetZettelFunc) {
	for _, sl := range l {
		slides.AddSlide(sl.ID, zGetZettel, sGetZettel)
	}
	slides.Completion(getZettel, zGetZettel)
}

func processList(w http.ResponseWriter, r *http.Request, c *client.Client) {
	ctx := r.Context()
	zQuery, zl, err := c.ListZettelJSON(ctx, r.URL.Query())
	if err != nil {
		http.Error(w, fmt.Sprintf("Error retrieving zettel list %s: %s\n", r.URL.Query(), err), http.StatusBadRequest)
		return
	}
	titles := make([]string, len(zl))
	for i, jm := range zl {
		if zjMeta, err := c.GetEvaluatedZJSON(ctx, jm.ID, api.PartMeta); err == nil {
			titles[i] = zEncodeInline(nil, zGetZettelTitleZid(zjson.MakeMeta(zjMeta), jm.ID))
		}
	}

	var title string
	if zQuery == "" {
		title = "All zettel"
		zQuery = title
	} else {
		title = "Selected zettel"
		zQuery = "Search: " + zQuery
	}
	writeHTMLHeader(w, "", "")
	fmt.Fprintf(w, "<title>%s</title>\n", title)
	writeHTMLBody(w)
	fmt.Fprintf(w, "<h1>%s</h1>\n", html.EscapeString(zQuery))
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

func writeHTMLHeader(w http.ResponseWriter, lang, prefix string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!DOCTYPE html>\n")
	if lang == "" {
		io.WriteString(w, "<html>\n")
	} else {
		fmt.Fprintf(w, "<html lang=\"%s\">\n", lang)
	}
	io.WriteString(w, `<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
<meta name="generator" content="Zettel Presenter">
`)
	writeDefaultCSS(w, prefix)
}

var defaultCSS = []string{
	"td.left,",
	"th.left { text-align: left }",
	"td.center,",
	"th.center { text-align: center }",
	"td.right,",
	"th.right { text-align: right }",
	"ol.zs-endnotes { padding-top: .5rem; border-top: 1px solid; font-size: smaller; margin-left: 2em; }",
	"a.broken { text-decoration: line-through }",
}

func writeDefaultCSS(w http.ResponseWriter, prefix string) {
	io.WriteString(w, "<style type=\"text/css\">\n")
	for _, line := range defaultCSS {
		io.WriteString(w, prefix)
		io.WriteString(w, line)
		io.WriteString(w, "\n")
	}
	io.WriteString(w, "</style>\n")
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

//go:embed mermaid/mermaid.min.js
var mermaid string

//go:embed revealjs
var revealjs embed.FS
