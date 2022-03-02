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
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
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

func main() {
	withAuth := flag.Bool("a", false, "Zettelstore needs authentication")
	listenAddress := flag.String("l", ":23120", "Listen address")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		io.WriteString(out, "  [URL] URL of Zettelstore (default: \"http://127.0.0.1:23123\")\n")
	}
	flag.Parse()
	ctx := context.Background()
	c, err := getClient(ctx, flag.Arg(0), *withAuth)
	if err != nil {
		panic(err)
	}
	cfg, err := getConfig(ctx, c)
	if err != nil {
		panic(err)
	}

	// Fix an error in slidy.js
	slidy2js = strings.ReplaceAll(slidy2js, "</script>", "<\\/script>")

	http.HandleFunc("/", makeHandler(&cfg))
	fmt.Println("Listening:", *listenAddress)
	http.ListenAndServe(*listenAddress, nil)
}

func getClient(ctx context.Context, base string, withAuth bool) (*client.Client, error) {
	if base == "" {
		base = "http://127.0.0.1:23123"
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	username, password := "", ""
	if uinfo := u.User; uinfo != nil {
		username = uinfo.Username()
		if pw, ok := uinfo.Password(); ok {
			password = pw
		}
		withAuth = true
	}
	c := client.NewClient(base)
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
	copyright    string
}

func getConfig(ctx context.Context, c *client.Client) (slidesConfig, error) {
	result := slidesConfig{
		c:            c,
		slideSetRole: "slideset",
	}
	m, err := c.GetMeta(ctx, configZettel)
	if err != nil {
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
			return result, nil
		}
		panic(err)
	}
	if ssr, ok := m["slideset-role"]; ok {
		result.slideSetRole = ssr
	}
	if author, ok := m["author"]; ok {
		result.author = author
	}
	if copyright, ok := m["copyright"]; ok {
		result.copyright = copyright
	}
	return result, nil
}

func makeHandler(cfg *slidesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			processZettel(w, r, cfg.c, api.ZidDefaultHome, cfg.slideSetRole)
			return
		}
		if zid := api.ZettelID(path[1:]); zid.IsValid() {
			processZettel(w, r, cfg.c, zid, cfg.slideSetRole)
			return
		}
		if strings.HasPrefix(path, "/sl/") {
			if zid := api.ZettelID(path[4:]); zid.IsValid() {
				processSlideSet(w, r, cfg, zid, renderSlideSet)
				return
			}
		}
		if strings.HasPrefix(path, "/ho/") {
			if zid := api.ZettelID(path[4:]); zid.IsValid() {
				processSlideSet(w, r, cfg, zid, renderHandout)
				return
			}
		}
		if strings.HasPrefix(path, "/z/") {
			if zid := api.ZettelID(path[3:]); zid.IsValid() {
				if content := processContent(w, r, cfg.c, zid); len(content) > 0 {
					w.Write(content)
				}
				return
			}
		}
		if strings.HasPrefix(path, "/svg/") {
			if zid := api.ZettelID(path[5:]); zid.IsValid() {
				if content := processContent(w, r, cfg.c, zid); len(content) > 0 {
					io.WriteString(w, `<?xml version='1.0' encoding='utf-8'?>`)
					w.Write(content)
				}
				return
			}
		}
		if len(path) == 2 && ' ' < path[1] && path[1] <= 'z' {
			processList(w, r, cfg.c)
			return
		}
		http.Error(w, fmt.Sprintf("Unhandled request %q", r.URL), http.StatusNotFound)
	}
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

func processZettel(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID, slidesRole string) {
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
	if role == slidesRole {
		if slides := processSlideTOC(ctx, c, zid, m); slides != nil {
			renderSlideTOC(w, slides)
			return
		}
	}

	writeHTMLZettel(ctx, w, c, zid, m, content)
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
		htmlTitle = htmlEncodeInline(title)
	}

	writeHTMLHeader(w, slides.Lang())
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	}
	writeHTMLBody(w)
	if len(title) > 0 {
		fmt.Fprintf(w, "<h1>%s</h1>\n", htmlTitle)
		if len(subtitle) > 0 {
			fmt.Fprintf(w, "<h2>%s</h2>\n", htmlEncodeInline(subtitle))
		}
	}
	io.WriteString(w, "<ol>\n")
	if len(title) > 0 {
		fmt.Fprintf(w, "<li><a href=\"/sl/%s#(1)\">%s</a></li>\n", slides.zid, htmlTitle)
	}
	for i, sl := range slides.Slides() {
		var slideTitle string
		if t := sl.Title(); len(t) > 0 {
			slideTitle = htmlEncodeInline(t)
		} else {
			slideTitle = string(sl.zid)
		}
		fmt.Fprintf(w, "<li><a href=\"/sl/%s#(%d)\">%s</a></li>\n", slides.zid, i+offset, slideTitle)
	}
	io.WriteString(w, "</ol>\n")
	fmt.Fprintf(w, "<p><a href=\"/ho/%s\">Handout</a>, <a href=\"\">Zettel</a></p>\n", slides.zid)
	writeHTMLFooter(w)
}

func writeHTMLZettel(ctx context.Context, w http.ResponseWriter, c *client.Client, zid api.ZettelID, m zjson.Meta, content zjson.Array) {
	title := getSlideTitleZid(m, zid)
	writeHTMLHeader(w, m.GetString(api.KeyLang))
	fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	writeHTMLBody(w)
	fmt.Fprintf(w, "<h1>%s</h1>\n", htmlEncodeInline(title))
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
			io.WriteString(w, "<ul class=\"zp-header\">\n")
			hasHeader = true
		}
		fmt.Fprintf(w, "<li>%s: <a href=\"%s\" target=\"_blank\">%s</a>&#10138;</li>", html.EscapeString(k), u, html.EscapeString(u))
	}
	if hasHeader {
		io.WriteString(w, "</ul>\n")
	}

	he := htmlNew(w, nil, 1, false, true)
	zjson.WalkBlock(he, content, 0)
	he.visitEndnotes()

	writeHTMLFooter(w)
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
	render(w, cfg, slides)
}

type renderSlidesFunc func(http.ResponseWriter, *slidesConfig, *slideSet)

func renderSlideSet(w http.ResponseWriter, cfg *slidesConfig, slides *slideSet) {
	lang := slides.Lang()
	writeHTMLHeader(w, lang)
	title := slides.Title()
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	}
	if copyright := slides.Copyright(cfg); copyright != "" {
		fmt.Fprintf(w, "<meta name=\"copyright\" content=\"%s\" />\n", html.EscapeString(copyright))
	}
	fmt.Fprintf(w, "<style type=\"text/css\" media=\"screen, projection, print\">\n%s</style>\n", slidy2css)
	writeHTMLBody(w)

	offset := 1
	if len(title) > 0 {
		offset++
		io.WriteString(w, "<div class=\"slide titlepage\">\n")
		fmt.Fprintf(w, "<h1 class=\"title\">%s</h1>\n", htmlEncodeInline(title))
		if subtitle := slides.Subtitle(); len(subtitle) > 0 {
			fmt.Fprintf(w, "<p class=\"subtitle\">%s</p>\n", htmlEncodeInline(subtitle))
		}
		if author := slides.Author(cfg); author != "" {
			fmt.Fprintf(w, "<p class=\"author\">%s</p>\n", html.EscapeString(author))
		}
		io.WriteString(w, "\n</div>\n")
	}
	for slideNo, sl := range slides.Slides() {
		io.WriteString(w, `<div class="slide"`)
		if slLang := sl.Lang(); slLang != "" && slLang != lang {
			fmt.Fprintf(w, ` lang="%s"`, slLang)
		}
		io.WriteString(w, ">\n")
		if title := sl.Title(); len(title) > 0 {
			fmt.Fprintf(w, "<h1>%s</h1>\n", htmlEncodeInline(title))
		}

		he := htmlNew(w, slides, 1, false, true)
		he.SetCurrentSlide(slideNo, offset)
		he.SetUnique(fmt.Sprintf("%d:", slideNo))
		zjson.WalkBlock(he, sl.Content(), 0)
		he.visitEndnotes()
		io.WriteString(w, "</div>\n")
	}
	fmt.Fprintf(w, "<script type=\"text/javascript\">\n//<![CDATA[\n%s//]]>\n</script>\n", slidy2js)
	writeHTMLFooter(w)
}

func renderHandout(w http.ResponseWriter, cfg *slidesConfig, slides *slideSet) {
	lang := slides.Lang()
	writeHTMLHeader(w, lang)
	title := slides.Title()
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", text.EncodeInlineString(title))
	}
	copyright := slides.Copyright(cfg)
	if copyright != "" {
		fmt.Fprintf(w, "<meta name=\"copyright\" content=\"%s\" />\n", html.EscapeString(copyright))
	}
	writeHTMLBody(w)

	offset := 1
	if len(title) > 0 {
		offset++
		// TODO: add <a if="(slideno)"...> to allow internal references.
		fmt.Fprintf(w, "<h1 id=\"(1)\">%s</h1>\n", htmlEncodeInline(title))
		if subtitle := slides.Subtitle(); len(subtitle) > 0 {
			fmt.Fprintf(w, "<h2>%s</h2>\n", htmlEncodeInline(subtitle))
		}
		if author := slides.Author(cfg); author != "" {
			fmt.Fprintf(w, "<p>%s</p>\n", html.EscapeString(author))
		}
		if copyright != "" {
			fmt.Fprintf(w, "<p>%s</p>\n", html.EscapeString(copyright))
		}
		io.WriteString(w, "<hr>\n")
	}
	he := htmlNew(w, slides, 1, true, false)
	for slideNo, sl := range slides.Slides() {
		if slideNo > 0 {
			io.WriteString(w, "<hr>\n")
		}
		he.SetCurrentSlide(slideNo, offset)
		htmlSlideNo := slideNo + offset
		if title := sl.Title(); len(title) > 0 {
			fmt.Fprintf(w, "<h1 id=\"(%d)\">%d. %s</h1>\n", htmlSlideNo, htmlSlideNo, htmlEncodeInline(title))
		} else {
			fmt.Fprintf(w, "<a id=\"(%d)\"></a>", htmlSlideNo)
		}
		slLang := sl.Lang()
		if slLang != "" && slLang != lang {
			fmt.Fprintf(w, `<div lang="%s">`, slLang)
		}

		he.SetUnique(fmt.Sprintf("%d:", slideNo))
		zjson.WalkBlock(he, sl.Content(), 0)
		if slLang != "" && slLang != lang {
			io.WriteString(w, "</div>")
		}
	}
	he.visitEndnotes()
	writeHTMLFooter(w)
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
			titles[i] = htmlEncodeInline(getZettelTitleZid(zjson.MakeMeta(zjMeta), jm.ID))
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
	writeHTMLFooter(w)
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
	fmt.Fprintf(w, "<style type=\"text/css\" media=\"screen, projection, print\">\n%s</style>\n", mycss)
}

func writeHTMLBody(w http.ResponseWriter)   { io.WriteString(w, "</head>\n<body>\n") }
func writeHTMLFooter(w http.ResponseWriter) { io.WriteString(w, "</body>\n</html>\n") }

//go:embed slidy2/slidy.css
var slidy2css string

//go:embed slidy2/slidy.js
var slidy2js string

var mycss = `/* Additional CSS to make it a little more beautiful */
.zp-left { text-align: left }
.zp-center { text-align: center }
.zp-right { text-align: right }
.zp-endnotes { padding-top: .5rem; border-top: 1px solid }
.zp-external {}
.zp-broken { text-decoration: line-through }
.zp-header { list-style-type: none; margin: 0; padding: 0;}
`

// Everything below this line should move into client/zjson
